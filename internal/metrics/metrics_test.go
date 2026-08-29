package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMetricsObservations(t *testing.T) {
	ObserveHTTPRequest("POST", "/v1/chat/completions", 200, 150*time.Millisecond, 1024)
	ObserveGenerationRequest("gemini-2.5-flash", "openai", true, "success", 1200*time.Millisecond)
	ObserveTimeToFirstToken("gemini-2.5-flash", "account-1", 450*time.Millisecond)
	ObserveTimeToFirstEvent("gemini-2.5-flash", "account-1", 300*time.Millisecond)
	IncActiveGeneration("gemini-2.5-flash")
	DecActiveGeneration("gemini-2.5-flash")
	ObserveTokens("prompt", "gemini-2.5-flash", "account-1", 120)
	ObserveTokens("completion", "gemini-2.5-flash", "account-1", 85)
	ObserveTokens("reasoning", "gemini-2.5-flash", "account-1", 20)
	ObserveGeneratedChars("gemini-2.5-flash", "account-1", 340)
	ObserveFinishReason("STOP", "gemini-2.5-flash")
	ObservePreparationDuration("waa", 45*time.Millisecond)
	AddUpstreamNetworkBytes("received", 2048)
	ObserveRetry("gemini-2.5-flash", "cooldown")
	ObserveStreamStall("gemini-2.5-flash", "account-1")
	SetLatestPerformance("account-1", "gemini-2.5-flash", 250*time.Millisecond)
	ObserveAccountCooldown("account-1", "gemini-2.5-flash", "rate_limit")
	UpdateAccountStates(map[string]int{"ready": 2, "busy": 1, "cooldown": 0})
	UpdateWorkerStates(map[string]int{"running": 2, "prewarmed": 1})
	ObserveWorkerLaunch("account-1", true, 2500*time.Millisecond)
	ObserveTokenCount("gemini-2.5-flash", true)
	ObserveVideoRequest("success")
}

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"/v1/chat/completions", "/v1/chat/completions"},
		{"/v1/videos/vid_12345/content", "/v1/videos/{video}/content"},
		{"/v1/videos/vid_12345", "/v1/videos/{video}"},
		{"/v1beta/models/gemini-2.5-flash:generateContent", "/v1beta/models/{action}"},
		{"/v1beta/operations/op_123", "/v1beta/operations/{operation}"},
		{"/health", "/health"},
		{"/metrics", "/metrics"},
	}

	for _, c := range cases {
		actual := NormalizePath(c.input)
		if actual != c.expected {
			t.Errorf("NormalizePath(%q) = %q, expected %q", c.input, actual, c.expected)
		}
	}
}

func TestHTTPMiddleware(t *testing.T) {
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
