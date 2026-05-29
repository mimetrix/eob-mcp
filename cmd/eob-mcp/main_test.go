package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthzHandler(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	healthzHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	if body := rr.Body.String(); body != "ok\n" {
		t.Fatalf("body: got %q, want %q", body, "ok\n")
	}
}

func TestReadyzHandler(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	readyzHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestVersionHandler(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/version", nil)
	versionHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var got struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if got.Version == "" {
		t.Fatal("version field is empty")
	}
}

func TestWithRequestLimitsPanicRecovery(t *testing.T) {
	t.Parallel()
	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("deliberate test panic")
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil)
	withRequestLimits(panicking).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestWithRequestLimitsBodyCap(t *testing.T) {
	t.Parallel()
	sink := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	})
	oversized := strings.Repeat("x", maxRequestBodyBytes+1024)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sink", strings.NewReader(oversized))
	withRequestLimits(sink).ServeHTTP(rr, req)
	// The handler will get an EOF after maxRequestBodyBytes; the wrapper itself
	// doesn't return an error code here — that's the caller's responsibility.
	// We just verify it doesn't panic and the response is intact.
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}
