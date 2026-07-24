package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	api := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
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

func TestRateLimiter(t *testing.T) {
	limiter := newFixedWindowLimiter()
	if !limiter.allow("key", 1, 1, testTime()) {
		t.Fatal("expected first request to be allowed")
	}
	if limiter.allow("key", 1, 1, testTime()) {
		t.Fatal("expected second request to be blocked")
	}
}

func testTime() (value time.Time) {
	return time.Unix(1_000, 0)
}
