package tests

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enoquesousa/gowasmrunner/internal/engine"
	"github.com/enoquesousa/gowasmrunner/internal/gateway"
)

// This token is public, synthetic test data, never an operator credential.
const gatewayTestToken = "synthetic-public-test-token-0000000001"

type gatewayRunnerFunc func(context.Context, string, string, string) (string, error)

func (run gatewayRunnerFunc) RunPluginString(ctx context.Context, plugin, function, input string) (string, error) {
	return run(ctx, plugin, function, input)
}

type gatewayBody struct {
	io.Reader
	reads int
}

func (body *gatewayBody) Read(buffer []byte) (int, error) {
	body.reads++
	return body.Reader.Read(buffer)
}

func (body *gatewayBody) Close() error { return nil }

func gatewayToken(value string) *string { return &value }

func gatewayServer(t *testing.T, token *string, runner gatewayRunnerFunc) *http.Server {
	t.Helper()
	server, err := gateway.NewServer(gateway.Config{Host: "127.0.0.1", Port: "0", APIToken: token}, runner)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func checkGatewayResponse(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; response = %q", response.Code, status, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Error("gateway response is not marked no-store")
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("gateway response permits content type sniffing")
	}
}

func TestGatewayRequiresHeaderBeforeReadingOrExecuting(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		headers http.Header
		remote  string
		status  int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "empty", headers: http.Header{"Authorization": {""}}, status: http.StatusUnauthorized},
		{name: "wrong token", headers: http.Header{"Authorization": {"Bearer " + strings.Repeat("b", 32)}}, status: http.StatusUnauthorized},
		{name: "wrong scheme", headers: http.Header{"Authorization": {"Basic " + gatewayTestToken}}, status: http.StatusUnauthorized},
		{name: "extra space", headers: http.Header{"Authorization": {"Bearer  " + gatewayTestToken}}, status: http.StatusUnauthorized},
		{name: "duplicate", headers: http.Header{"Authorization": {"Bearer " + gatewayTestToken, "Bearer " + gatewayTestToken}}, status: http.StatusUnauthorized},
		{name: "combined headers", headers: http.Header{"Authorization": {"Bearer " + gatewayTestToken + ", Bearer " + gatewayTestToken}}, status: http.StatusUnauthorized},
		{name: "cookie only", headers: http.Header{"Cookie": {"token=" + gatewayTestToken}}, status: http.StatusUnauthorized},
		{name: "query token", path: "/execute/private.wasm?token=" + gatewayTestToken, status: http.StatusBadRequest},
		{name: "empty query token", path: "/execute/private.wasm?token=", status: http.StatusBadRequest},
		{name: "duplicate query token", path: "/execute/private.wasm?token=&token=", status: http.StatusBadRequest},
		{name: "encoded query token", path: "/execute/private.wasm?%74oken=", status: http.StatusBadRequest},
		{name: "query with valid header", path: "/execute/private.wasm?access_token=" + gatewayTestToken, headers: http.Header{"Authorization": {"Bearer " + gatewayTestToken}}, status: http.StatusBadRequest},
		{name: "duplicate function", path: "/execute/private.wasm?func=a&func=b", status: http.StatusBadRequest},
		{name: "malformed query", path: "/execute/private.wasm?func=%zz", status: http.StatusBadRequest},
		{name: "authentication precedes routing", path: "/not-an-endpoint", status: http.StatusUnauthorized},
		{name: "remote plaintext", remote: "192.0.2.2:1234", headers: http.Header{"Authorization": {"Bearer " + gatewayTestToken}}, status: http.StatusForbidden},
		{name: "forwarded headers cannot authorize plaintext", remote: "192.0.2.2:1234", headers: http.Header{"Authorization": {"Bearer " + gatewayTestToken}, "X-Forwarded-Proto": {"https"}, "X-Forwarded-For": {"127.0.0.1"}}, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			server := gatewayServer(t, gatewayToken(gatewayTestToken), func(context.Context, string, string, string) (string, error) {
				calls++
				return "", nil
			})
			path := test.path
			if path == "" {
				path = "/execute/private.wasm"
			}
			body := &gatewayBody{Reader: strings.NewReader("private request body")}
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, body)
			request.RemoteAddr = "127.0.0.1:1234"
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			if test.headers != nil {
				request.Header = test.headers.Clone()
			}
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			checkGatewayResponse(t, response, test.status)
			if calls != 0 || body.reads != 0 {
				t.Fatalf("rejected request reached runner (%d calls) or body (%d reads)", calls, body.reads)
			}
			if test.status == http.StatusUnauthorized && response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Error("missing Bearer authentication challenge")
			}
			for _, sensitive := range []string{gatewayTestToken, "private.wasm", "private request body"} {
				if strings.Contains(response.Body.String(), sensitive) {
					t.Error("rejection reflected private request data")
				}
			}
		})
	}
}

func TestGatewayLocalModeRejectsBrowserAndRebindingRequests(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		remote  string
		headers http.Header
		status  int
	}{
		{name: "CLI IPv4", host: "127.0.0.1:8080", status: http.StatusOK},
		{name: "CLI IPv6", host: "[::1]:8080", remote: "[::1]:1234", status: http.StatusOK},
		{name: "IPv6 default port", host: "[::1]", status: http.StatusOK},
		{name: "localhost", host: "localhost:8080", status: http.StatusOK},
		{name: "same origin", host: "127.0.0.1:8080", headers: http.Header{"Origin": {"http://127.0.0.1:8080"}}, status: http.StatusOK},
		{name: "same origin metadata", host: "127.0.0.1:8080", headers: http.Header{"Sec-Fetch-Site": {"same-origin"}}, status: http.StatusOK},
		{name: "direct navigation metadata", host: "127.0.0.1:8080", headers: http.Header{"Sec-Fetch-Site": {"none"}}, status: http.StatusOK},
		{name: "DNS rebinding", host: "attacker.example:8080", status: http.StatusForbidden},
		{name: "localhost suffix", host: "localhost.attacker.example", status: http.StatusForbidden},
		{name: "IPv4 suffix", host: "127.0.0.1.attacker.example", status: http.StatusForbidden},
		{name: "unspecified host", host: "0.0.0.0:8080", status: http.StatusForbidden},
		{name: "cross origin", host: "127.0.0.1:8080", headers: http.Header{"Origin": {"https://attacker.example"}}, status: http.StatusForbidden},
		{name: "opaque origin", host: "127.0.0.1:8080", headers: http.Header{"Origin": {"null"}}, status: http.StatusForbidden},
		{name: "cross site metadata", host: "127.0.0.1:8080", headers: http.Header{"Sec-Fetch-Site": {"cross-site"}}, status: http.StatusForbidden},
		{name: "same site is insufficient", host: "127.0.0.1:8080", headers: http.Header{"Sec-Fetch-Site": {"same-site"}}, status: http.StatusForbidden},
		{name: "duplicate origin", host: "127.0.0.1:8080", headers: http.Header{"Origin": {"http://127.0.0.1:8080", "https://attacker.example"}}, status: http.StatusForbidden},
		{name: "duplicate metadata", host: "127.0.0.1:8080", headers: http.Header{"Sec-Fetch-Site": {"same-origin", "cross-site"}}, status: http.StatusForbidden},
		{name: "remote peer", host: "127.0.0.1:8080", remote: "192.0.2.2:1234", status: http.StatusForbidden},
		{name: "token without configured authentication", host: "127.0.0.1:8080", headers: http.Header{"Authorization": {"Bearer " + gatewayTestToken}}, status: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			server := gatewayServer(t, nil, func(context.Context, string, string, string) (string, error) {
				calls++
				return "ok", nil
			})
			body := &gatewayBody{Reader: strings.NewReader("input")}
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/execute/plugin.wasm", body)
			request.Host, request.RemoteAddr = test.host, "127.0.0.1:1234"
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			if test.headers != nil {
				request.Header = test.headers.Clone()
			}
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			checkGatewayResponse(t, response, test.status)
			if test.status != http.StatusOK && (calls != 0 || body.reads != 0) {
				t.Fatal("unsafe local request reached its body or runner")
			}
			if test.status == http.StatusOK && calls != 1 {
				t.Fatal("safe local request did not reach its runner")
			}
		})
	}
}

func TestGatewayConfigurationFailsClosed(t *testing.T) {
	certPath, keyPath, _ := gatewayTLSFiles(t)
	tests := []struct {
		name    string
		host    string
		port    string
		token   *string
		cert    string
		key     string
		wantErr bool
	}{
		{name: "local IPv4", host: "127.0.0.1"},
		{name: "local IPv6", host: "::1"},
		{name: "local token", host: "127.0.0.1", token: gatewayToken(gatewayTestToken)},
		{name: "local TLS is retained", host: "127.0.0.1", cert: certPath, key: keyPath},
		{name: "empty environment is invalid", host: "127.0.0.1", token: gatewayToken(""), wantErr: true},
		{name: "short token", host: "127.0.0.1", token: gatewayToken(strings.Repeat("a", 31)), wantErr: true},
		{name: "long token", host: "127.0.0.1", token: gatewayToken(strings.Repeat("a", 4097)), wantErr: true},
		{name: "space in token", host: "127.0.0.1", token: gatewayToken(gatewayTestToken + " "), wantErr: true},
		{name: "newline in token", host: "127.0.0.1", token: gatewayToken(gatewayTestToken + "\n"), wantErr: true},
		{name: "non ASCII token", host: "127.0.0.1", token: gatewayToken(gatewayTestToken + "é"), wantErr: true},
		{name: "unsafe token character", host: "127.0.0.1", token: gatewayToken(gatewayTestToken + ":"), wantErr: true},
		{name: "remote without credentials", host: "192.0.2.1", wantErr: true},
		{name: "wildcard without credentials", host: "0.0.0.0", wantErr: true},
		{name: "empty host without credentials", wantErr: true},
		{name: "IPv6 wildcard without credentials", host: "::", wantErr: true},
		{name: "localhost is not a literal bind", host: "localhost", wantErr: true},
		{name: "remote token without TLS", host: "0.0.0.0", token: gatewayToken(gatewayTestToken), wantErr: true},
		{name: "remote TLS without token", host: "0.0.0.0", cert: certPath, key: keyPath, wantErr: true},
		{name: "remote token and TLS", host: "0.0.0.0", token: gatewayToken(gatewayTestToken), cert: certPath, key: keyPath},
		{name: "DNS bind token and TLS", host: "gateway.example", token: gatewayToken(gatewayTestToken), cert: certPath, key: keyPath},
		{name: "missing key", host: "127.0.0.1", cert: certPath, wantErr: true},
		{name: "missing certificate", host: "127.0.0.1", key: keyPath, wantErr: true},
		{name: "invalid PEM", host: "127.0.0.1", cert: keyPath, key: certPath, wantErr: true},
		{name: "missing PEM", host: "127.0.0.1", cert: filepath.Join(t.TempDir(), "missing"), key: keyPath, wantErr: true},
		{name: "host with port", host: "127.0.0.1:8080", wantErr: true},
		{name: "host with scheme", host: "https://127.0.0.1", wantErr: true},
		{name: "host zone", host: "::1%lo", wantErr: true},
		{name: "signed port", host: "127.0.0.1", port: "+80", wantErr: true},
		{name: "port overflow", host: "127.0.0.1", port: "65536", wantErr: true},
	}
	runner := gatewayRunnerFunc(func(context.Context, string, string, string) (string, error) {
		t.Fatal("configuration validation must not execute a plugin")
		return "", nil
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			port := test.port
			if port == "" {
				port = "0"
			}
			server, err := gateway.NewServer(gateway.Config{Host: test.host, Port: port, APIToken: test.token, TLSCert: test.cert, TLSKey: test.key}, runner)
			if test.wantErr {
				if err == nil {
					t.Fatal("unsafe configuration was accepted")
				}
				if strings.Contains(err.Error(), gatewayTestToken) || strings.Contains(err.Error(), keyPath) {
					t.Fatal("configuration error exposed credential data or a private key path")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
				t.Error("gateway is missing bounded HTTP timeouts or headers")
			}
			if test.cert != "" && (server.TLSConfig == nil || server.TLSConfig.MinVersion < tls.VersionTLS12 || len(server.TLSConfig.Certificates) != 1) {
				t.Fatal("configured TLS was downgraded or left unloaded")
			}
		})
	}
}

func TestGatewayBearerAndExecutionContract(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		remote   string
		tls      bool
		runError bool
		status   int
	}{
		{name: "loopback proxy hop", method: http.MethodPost, path: "/execute/greet.wasm", status: http.StatusOK},
		{name: "remote TLS", method: http.MethodPost, path: "/execute/greet.wasm?func=custom", remote: "192.0.2.2:1234", tls: true, status: http.StatusOK},
		{name: "wrong method", method: http.MethodGet, path: "/execute/greet.wasm", status: http.StatusMethodNotAllowed},
		{name: "missing route", method: http.MethodPost, path: "/other", status: http.StatusNotFound},
		{name: "missing plugin", method: http.MethodPost, path: "/execute/", status: http.StatusBadRequest},
		{name: "nested plugin", method: http.MethodPost, path: "/execute/parent/plugin.wasm", status: http.StatusBadRequest},
		{name: "engine failure is generic", method: http.MethodPost, path: "/execute/greet.wasm", runError: true, status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			server := gatewayServer(t, gatewayToken(gatewayTestToken), func(ctx context.Context, plugin, function, input string) (string, error) {
				calls++
				wantFunction := "greet"
				if strings.Contains(test.path, "func=") {
					wantFunction = "custom"
				}
				if plugin != "greet.wasm" || function != wantFunction || input != "request-secret" || ctx.Err() != nil {
					t.Error("gateway changed the execution arguments or request context")
				}
				if test.runError {
					return "", errors.New("private engine diagnostic: " + gatewayTestToken + " request-secret")
				}
				return "plugin output", nil
			})
			request := httptest.NewRequest(test.method, "http://127.0.0.1"+test.path, strings.NewReader("request-secret"))
			request.RemoteAddr = "127.0.0.1:1234"
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			if test.tls {
				request.TLS = &tls.ConnectionState{}
			}
			request.Header.Set("Authorization", "bEaReR "+gatewayTestToken)
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			checkGatewayResponse(t, response, test.status)
			if test.status == http.StatusOK && (calls != 1 || response.Body.String() != "plugin output") {
				t.Fatal("authorized execution did not return the plugin output")
			}
			if test.status == http.StatusInternalServerError && response.Body.String() != "Plugin execution failed\n" {
				t.Fatal("engine failure exposed a detailed diagnostic")
			}
			if test.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodPost {
				t.Error("method rejection omitted the POST contract")
			}
		})
	}
}

func TestGatewayBoundsBodyAndPassesCancellation(t *testing.T) {
	for _, test := range []struct {
		name          string
		size          int
		contentLength int64
		status        int
	}{
		{name: "exact limit", size: int(gateway.MaxBodyBytes), contentLength: gateway.MaxBodyBytes, status: http.StatusOK},
		{name: "declared excess before read", contentLength: gateway.MaxBodyBytes + 1, status: http.StatusRequestEntityTooLarge},
		{name: "streamed excess", size: int(gateway.MaxBodyBytes) + 1, contentLength: -1, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			server := gatewayServer(t, gatewayToken(gatewayTestToken), func(_ context.Context, _, _, input string) (string, error) {
				calls++
				if len(input) != test.size {
					t.Error("accepted body was truncated")
				}
				return "ok", nil
			})
			body := &gatewayBody{Reader: strings.NewReader(strings.Repeat("x", test.size))}
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/execute/greet.wasm", body)
			request.RemoteAddr, request.ContentLength = "127.0.0.1:1234", test.contentLength
			request.Header.Set("Authorization", "Bearer "+gatewayTestToken)
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			checkGatewayResponse(t, response, test.status)
			if test.status != http.StatusOK && calls != 0 {
				t.Error("oversized body reached the engine")
			}
			if test.contentLength > gateway.MaxBodyBytes && body.reads != 0 {
				t.Error("known oversized body was read before rejection")
			}
		})
	}

	t.Run("request context reaches the engine", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var called bool
		server := gatewayServer(t, nil, func(received context.Context, _, _, _ string) (string, error) {
			called = true
			if received != ctx || !errors.Is(received.Err(), context.Canceled) {
				t.Error("gateway detached execution from the canceled request")
			}
			return "", received.Err()
		})
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/execute/greet.wasm", nil)
		request.RemoteAddr = "127.0.0.1:1234"
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		checkGatewayResponse(t, response, http.StatusInternalServerError)
		if !called {
			t.Fatal("context test did not reach its runner")
		}
	})
}

func gatewayTLSFiles(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-gateway-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("could not trust synthetic gateway certificate")
	}
	return certPath, keyPath, roots
}

func loadGatewayGreetingRunner(t *testing.T, ctx context.Context) *engine.Runner {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("..", "examples", "greet.wasm"))
	if err != nil {
		t.Fatalf("generate the greeting fixture with make gen-all before testing: %v", err)
	}
	plugins := t.TempDir()
	if err := os.WriteFile(filepath.Join(plugins, "greet.wasm"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	runner, err := engine.NewRunner(ctx, engine.RunnerConfig{MaxMemoryPages: 20, Timeout: time.Second, Stdout: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := runner.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err := runner.LoadPlugins(ctx, plugins); err != nil {
		t.Fatal(err)
	}
	return runner
}

func startGatewayTLSTestServer(t *testing.T, runner gateway.Runner) (string, *http.Client) {
	t.Helper()
	certPath, keyPath, roots := gatewayTLSFiles(t)
	server, err := gateway.NewServer(gateway.Config{Host: "127.0.0.1", Port: "0", APIToken: gatewayToken(gatewayTestToken), TLSCert: certPath, TLSKey: keyPath}, runner)
	if err != nil {
		t.Fatal(err)
	}
	server.ErrorLog = log.New(io.Discard, "", 0)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		if serveErr := <-done; !errors.Is(serveErr, http.ErrServerClosed) {
			t.Errorf("TLS server stopped unexpectedly: %v", serveErr)
		}
	})
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	endpoint := "https://" + listener.Addr().String() + "/execute/greet.wasm"
	return endpoint, client
}

func readGatewayTLSResponse(t *testing.T, client *http.Client, request *http.Request) (*http.Response, []byte) {
	t.Helper()
	response, responseErr := client.Do(request)
	if responseErr != nil {
		t.Fatal(responseErr)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("failed to consume HTTP response: read=%v, close=%v", readErr, closeErr)
	}
	return response, body
}

func TestGatewayTLSExecutesRealPlugin(t *testing.T) {
	ctx := context.Background()
	runner := loadGatewayGreetingRunner(t, ctx)
	var calls atomic.Int32
	countedRunner := gatewayRunnerFunc(func(ctx context.Context, plugin, function, input string) (string, error) {
		calls.Add(1)
		return runner.RunPluginString(ctx, plugin, function, input)
	})
	endpoint, client := startGatewayTLSTestServer(t, countedRunner)
	for _, test := range []struct {
		name   string
		token  string
		status int
	}{
		{name: "missing token", status: http.StatusUnauthorized},
		{name: "wrong token", token: strings.Repeat("z", 32), status: http.StatusUnauthorized},
		{name: "authenticated Wasm execution", token: gatewayTestToken, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("Gateway"))
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response, body := readGatewayTLSResponse(t, client, request)
			if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected real HTTPS response: status=%d, body=%q", response.StatusCode, body)
			}
			if test.status == http.StatusOK && string(body) != "Hello, Gateway" {
				t.Fatalf("actual Wasm output = %q", body)
			}
		})
	}
	if calls.Load() != 1 {
		t.Fatalf("unauthorized HTTPS requests reached Wasm; total calls=%d", calls.Load())
	}
}
