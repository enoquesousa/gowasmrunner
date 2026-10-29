// Package gateway protects the HTTP interface to cached WebAssembly plugins.
package gateway

import (
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// MaxBodyBytes is the maximum request body accepted by the execution endpoint.
const MaxBodyBytes int64 = 1 << 20

// Config contains HTTP listener settings and credentials supplied by the operator.
type Config struct {
	// Host is the bind IP or hostname; an empty value means all interfaces.
	Host string
	// Port is the numeric TCP port, including zero for an ephemeral listener.
	Port string
	// APIToken is nil only when the environment variable is absent; an empty value is invalid.
	APIToken *string
	// TLSCert and TLSKey are paths to the PEM server certificate chain and private key.
	TLSCert string
	TLSKey  string
}

// NewServer validates configuration and loads TLS credentials without opening a listener.
// Call it before loading plugins. Remote listeners require both a token and TLS.
func NewServer(cfg Config, runner Runner) (*http.Server, error) {
	address, loopback, err := listenAddress(cfg.Host, cfg.Port)
	if err != nil {
		return nil, err
	}
	auth, err := newAuthorization(cfg.APIToken)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := loadTLS(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return nil, err
	}
	if !loopback && (!auth.configured || tlsConfig == nil) {
		return nil, errors.New("remote listeners require GOWASMRUNNER_API_TOKEN and both TLS files")
	}
	if runner == nil {
		return nil, errors.New("gateway requires a plugin runner")
	}
	return &http.Server{
		Addr:              address,
		Handler:           &handler{runner: runner, auth: auth, origins: http.NewCrossOriginProtection()},
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}, nil
}

func newAuthorization(token *string) (authorization, error) {
	if token == nil {
		return authorization{}, nil
	}
	if !validToken(*token) {
		return authorization{}, errors.New("GOWASMRUNNER_API_TOKEN must contain 32 to 4096 safe ASCII Bearer characters without whitespace")
	}
	return authorization{configured: true, digest: sha256.Sum256([]byte(*token))}, nil
}

func listenAddress(host, port string) (string, bool, error) {
	if err := validatePort(port); err != nil {
		return "", false, err
	}
	ip, ipErr := netip.ParseAddr(host)
	if ipErr == nil {
		if ip.Zone() != "" {
			return "", false, errors.New("bind host must not include an IP zone")
		}
		return net.JoinHostPort(host, port), ip.IsLoopback(), nil
	}
	if host != "" && !validHostname(host) {
		return "", false, errors.New("bind host must be an IP literal or DNS hostname without a port or scheme")
	}
	return net.JoinHostPort(host, port), false, nil
}

func validatePort(port string) error {
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return errors.New("port must be a number from 0 to 65535")
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return errors.New("port must be a number from 0 to 65535")
	}
	return nil
}

func validHostname(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !validHostLabel(label) {
			return false
		}
	}
	return true
}

func validHostLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for index := range len(label) {
		if !asciiAlphanumeric(label[index]) && label[index] != '-' {
			return false
		}
	}
	return true
}

func loadTLS(certPath, keyPath string) (*tls.Config, error) {
	if certPath == "" && keyPath == "" {
		return nil, nil
	}
	if certPath == "" || keyPath == "" {
		return nil, errors.New("TLS requires both -tls-cert and -tls-key")
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, errors.New("cannot load the TLS certificate and matching private key from PEM files")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, nil
}
