package gateway

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// Runner is the string execution operation exposed by the WebAssembly engine.
type Runner interface {
	RunPluginString(context.Context, string, string, string) (string, error)
}

type handler struct {
	runner  Runner
	auth    authorization
	origins *http.CrossOriginProtection
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if status := h.authorize(r); status != 0 {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	h.execute(w, r)
}

func (h *handler) authorize(r *http.Request) int {
	if !validQuery(r.URL.RawQuery) {
		return http.StatusBadRequest
	}
	if status := h.auth.check(r); status != 0 {
		return status
	}
	if h.auth.configured {
		return 0
	}
	if !localHost(r.Host) || len(r.Header.Values("Origin")) > 1 || len(r.Header.Values("Sec-Fetch-Site")) > 1 {
		return http.StatusForbidden
	}
	if err := h.origins.Check(r); err != nil {
		return http.StatusForbidden
	}
	return 0
}

// Only the documented function argument is accepted; credentials in any query
// parameter (including empty or repeated token parameters) are never consumed.
func validQuery(raw string) bool {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return false
	}
	for name, values := range query {
		if name != "func" || len(values) != 1 {
			return false
		}
	}
	return true
}

func (h *handler) execute(w http.ResponseWriter, r *http.Request) {
	plugin := strings.TrimPrefix(r.URL.Path, "/execute/")
	if plugin == r.URL.Path {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	if !validPluginName(plugin) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	function := r.URL.Query().Get("func")
	if function == "" {
		function = "greet"
	}
	body, status := readBody(w, r)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	result, err := h.runner.RunPluginString(r.Context(), plugin, function, string(body))
	if err != nil {
		http.Error(w, "Plugin execution failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, writeErr := io.WriteString(w, result); writeErr != nil {
		log.Print("Error writing gateway response")
	}
}

func validPluginName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\")
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, int) {
	if r.ContentLength > MaxBodyBytes {
		return nil, http.StatusRequestEntityTooLarge
	}
	body := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	defer func() {
		if err := body.Close(); err != nil {
			log.Print("Error closing gateway request body")
		}
	}()
	data, err := io.ReadAll(body)
	if err == nil {
		return data, 0
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, http.StatusRequestEntityTooLarge
	}
	return nil, http.StatusBadRequest
}
