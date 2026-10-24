#!/bin/sh
# Generate a fresh, short-lived CA and server identity for the local TLS example.
set -eu
umask 077

cert_dir=${1:-certs/dev}
if [ "$#" -gt 1 ]; then
    printf 'Usage: %s [new-output-directory]\n' "$0" >&2
    exit 1
fi
if [ -e "$cert_dir" ] || [ -L "$cert_dir" ]; then
    printf 'Refusing to overwrite existing certificates: %s\n' "$cert_dir" >&2
    exit 1
fi
command -v openssl >/dev/null 2>&1 || {
    printf 'OpenSSL is required.\n' >&2
    exit 1
}
mkdir -p -- "$cert_dir"

openssl req -x509 -newkey rsa:2048 -sha256 -days 7 -nodes \
    -keyout "$cert_dir/ca.key" -out "$cert_dir/ca.crt" \
    -subj '/CN=gowasmrunner local development CA' \
    -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign'

openssl req -new -newkey rsa:2048 -sha256 -nodes \
    -keyout "$cert_dir/server.key" -out "$cert_dir/server.csr" \
    -subj '/CN=localhost'
cat > "$cert_dir/server.ext" <<'CERT_EXTENSIONS'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1
CERT_EXTENSIONS
openssl x509 -req -in "$cert_dir/server.csr" -sha256 -days 7 \
    -CA "$cert_dir/ca.crt" -CAkey "$cert_dir/ca.key" -CAcreateserial \
    -extfile "$cert_dir/server.ext" -out "$cert_dir/server.crt"
rm -- "$cert_dir/server.csr" "$cert_dir/server.ext"

printf 'Development TLS files created in %s (valid for 7 days).\n' "$cert_dir"
printf 'Mount only server.crt and server.key; clients trust ca.crt. Keep ca.key offline.\n'
