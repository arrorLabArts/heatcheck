package httpapi

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	apidocs "github.com/arrorLabArts/heatcheck/api"
	"github.com/go-chi/chi/v5"
)

func TestHealth(t *testing.T) {
	api := New(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		AllowedOrigins: []string{"https://frontend.example"},
	})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("Origin", "https://frontend.example")
	response := httptest.NewRecorder()

	api.Router().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://frontend.example" {
		t.Fatal("expected configured CORS origin")
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body %s", response.Body.String())
	}
}

func TestClientIPUsesOnlyTrustedProxyHeaders(t *testing.T) {
	api := New(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		TrustedProxyCIDRs: []string{"127.0.0.1/32"},
	})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "127.0.0.1:43210"
	request.Header.Set("X-Forwarded-For", "198.51.100.9, 203.0.113.4")
	if actual := api.clientIP(request); actual != "203.0.113.4" {
		t.Fatalf("clientIP() = %q, want rightmost untrusted address", actual)
	}

	request.RemoteAddr = "203.0.113.10:43210"
	request.Header.Set("X-Forwarded-For", "198.51.100.9")
	if actual := api.clientIP(request); actual != "203.0.113.10" {
		t.Fatalf("clientIP() = %q, want direct untrusted peer", actual)
	}
}

func TestOpenAPISpecIsServed(t *testing.T) {
	api := New(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	request := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	response := httptest.NewRecorder()

	api.Router().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), "openapi: 3.1.0") {
		t.Fatal("embedded OpenAPI document is missing")
	}
}

func TestRouterOperationsMatchOpenAPI(t *testing.T) {
	api := New(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	mux, ok := api.Router().(*chi.Mux)
	if !ok {
		t.Fatal("Router() did not return a chi mux")
	}
	routes := make(map[string]struct{})
	if err := chi.Walk(mux, func(
		method string,
		route string,
		_ http.Handler,
		_ ...func(http.Handler) http.Handler,
	) error {
		routes[method+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	pathPattern := regexp.MustCompile(`^  (/.+):$`)
	methodPattern := regexp.MustCompile(`^    (get|post|put|patch|delete):$`)
	documented := make(map[string]struct{})
	var currentPath string
	scanner := bufio.NewScanner(bytes.NewReader(apidocs.Spec))
	for scanner.Scan() {
		line := scanner.Text()
		if match := pathPattern.FindStringSubmatch(line); match != nil {
			currentPath = match[1]
			continue
		}
		if match := methodPattern.FindStringSubmatch(line); match != nil && currentPath != "" {
			documented[strings.ToUpper(match[1])+" "+currentPath] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	var missing, undocumented []string
	for operation := range documented {
		if _, ok := routes[operation]; !ok {
			missing = append(missing, operation)
		}
	}
	for operation := range routes {
		if _, ok := documented[operation]; !ok {
			undocumented = append(undocumented, operation)
		}
	}
	sort.Strings(missing)
	sort.Strings(undocumented)
	if len(missing) > 0 || len(undocumented) > 0 {
		t.Fatalf("missing routes=%v undocumented routes=%v", missing, undocumented)
	}
}
