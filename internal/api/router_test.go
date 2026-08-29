package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsEndpoint(t *testing.T) {
	handler := NewHandler(nil, Config{})
	server := httptest.NewServer(handler)
	defer server.Close()

	// Perform a health request to record HTTP traffic
	healthResp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("failed to GET /health: %v", err)
	}
	_ = healthResp.Body.Close()

	resp, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed to GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read /metrics body: %v", err)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "http_requests_total") {
		t.Errorf("expected /metrics output to contain 'http_requests_total', got:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "http_requests_in_flight") {
		t.Errorf("expected /metrics output to contain 'http_requests_in_flight', got:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "go_goroutines") {
		t.Errorf("expected /metrics output to contain 'go_goroutines', got:\n%s", bodyStr)
	}
}
