package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP Metrics
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "http",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests processed, partitioned by method, path template, and status code.",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "http",
			Name:      "request_duration_seconds",
			Help:      "Duration of HTTP requests in seconds.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		},
		[]string{"method", "path"},
	)

	HTTPRequestsInFlight = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "http",
			Name:      "requests_in_flight",
			Help:      "Current number of in-flight HTTP requests.",
		},
	)

	HTTPResponseSizeBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "http",
			Name:      "response_size_bytes",
			Help:      "Size of HTTP responses in bytes.",
			Buckets:   []float64{100, 1000, 5000, 10000, 50000, 100000, 500000, 1000000, 5000000},
		},
		[]string{"method", "path"},
	)

	// AIStudio2API Generation Metrics
	GenerationRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "requests_total",
			Help:      "Total number of AI model generation requests.",
		},
		[]string{"model", "protocol", "stream", "status"},
	)

	GenerationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "duration_seconds",
			Help:      "Total end-to-end duration of generation requests in seconds.",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300},
		},
		[]string{"model", "protocol", "stream"},
	)

	TimeToFirstTokenSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "time_to_first_token_seconds",
			Help:      "Time from request start until the first content token was emitted (TTFT).",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
		},
		[]string{"model", "account"},
	)

	TimeToFirstEventSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "time_to_first_event_seconds",
			Help:      "Time from request start until the first upstream event was received.",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
		},
		[]string{"model", "account"},
	)

	ActiveGenerations = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "active_requests",
			Help:      "Current number of active generation requests in flight.",
		},
		[]string{"model"},
	)

	TokensTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "tokens_total",
			Help:      "Total number of tokens processed (prompt, completion, reasoning).",
		},
		[]string{"type", "model", "account"},
	)

	GeneratedCharsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "chars_total",
			Help:      "Total output characters generated.",
		},
		[]string{"model", "account"},
	)

	FinishReasonsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "generation",
			Name:      "finish_reasons_total",
			Help:      "Count of generation stream termination reasons.",
		},
		[]string{"reason", "model"},
	)

	// Orchestration & Engine Metrics
	PreparationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "aistudio2api",
			Subsystem: "orchestrator",
			Name:      "preparation_duration_seconds",
			Help:      "Duration of request preparation phases in seconds.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		},
		[]string{"phase"},
	)

	UpstreamNetworkBytesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "orchestrator",
			Name:      "upstream_bytes_total",
			Help:      "Total upstream network bytes transferred.",
		},
		[]string{"direction"},
	)

	RetriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "orchestrator",
			Name:      "retries_total",
			Help:      "Total number of request retry and failover attempts across accounts.",
		},
		[]string{"model", "reason"},
	)

	StreamStallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "orchestrator",
			Name:      "stream_stalls_total",
			Help:      "Total number of stream stall events detected during generation.",
		},
		[]string{"model", "account"},
	)

	LatestPerformanceSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "aistudio2api",
			Subsystem: "orchestrator",
			Name:      "latest_latency_seconds",
			Help:      "Latest observed first event latency in seconds for account and model.",
		},
		[]string{"account", "model"},
	)

	// Account Pool Metrics
	AccountsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "aistudio2api",
			Subsystem: "accounts",
			Name:      "total",
			Help:      "Number of accounts in the pool grouped by state.",
		},
		[]string{"state"},
	)

	AccountCooldownsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "accounts",
			Name:      "cooldowns_total",
			Help:      "Total count of account cooldown occurrences.",
		},
		[]string{"account", "model", "reason"},
	)

	// Worker Metrics
	WorkersTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "aistudio2api",
			Subsystem: "workers",
			Name:      "total",
			Help:      "Number of WAA workers grouped by state.",
		},
		[]string{"state"},
	)

	WorkerLaunchesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "workers",
			Name:      "launches_total",
			Help:      "Total worker startup launches.",
		},
		[]string{"account", "status"},
	)

	WorkerLaunchDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "aistudio2api",
			Subsystem: "workers",
			Name:      "launch_duration_seconds",
			Help:      "Worker launch duration in seconds.",
			Buckets:   []float64{0.5, 1, 2, 5, 10, 20, 30, 60},
		},
	)

	// Token Count & Video Operations
	TokenCountRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "tokens",
			Name:      "count_requests_total",
			Help:      "Total token counting requests.",
		},
		[]string{"model", "status"},
	)

	VideoRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "aistudio2api",
			Subsystem: "video",
			Name:      "requests_total",
			Help:      "Total video generation requests.",
		},
		[]string{"status"},
	)
)

var (
	activeGenerationsMu sync.Mutex
	activeGenerations   = make(map[string]int)
)

func sanitize(val string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return "unknown"
	}
	return val
}

func sanitizeModel(model string) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return "unknown"
	}
	return model
}

func sanitizeAccount(account string) string {
	account = strings.TrimSpace(account)
	if account == "" {
		return "unknown"
	}
	return account
}

// ObserveHTTPRequest records standard HTTP request metrics.
func ObserveHTTPRequest(method, path string, status int, duration time.Duration, responseBytes int) {
	method = sanitize(method)
	path = NormalizePath(path)
	statusStr := strconv.Itoa(status)

	HTTPRequestsTotal.WithLabelValues(method, path, statusStr).Inc()
	HTTPRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
	if responseBytes > 0 {
		HTTPResponseSizeBytes.WithLabelValues(method, path).Observe(float64(responseBytes))
	}
}

// NormalizePath collapses dynamic path parameters into normalized templates.
func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if strings.HasPrefix(path, "/v1/videos/") {
		if strings.HasSuffix(path, "/content") {
			return "/v1/videos/{video}/content"
		}
		return "/v1/videos/{video}"
	}
	if strings.HasPrefix(path, "/v1beta/models/") {
		return "/v1beta/models/{action}"
	}
	if strings.HasPrefix(path, "/v1beta/operations/") {
		return "/v1beta/operations/{operation}"
	}
	return path
}

// ObserveGenerationRequest records generation request counts and latency.
func ObserveGenerationRequest(model, protocol string, stream bool, status string, duration time.Duration) {
	m := sanitizeModel(model)
	p := sanitize(protocol)
	s := strconv.FormatBool(stream)
	st := sanitize(status)

	GenerationRequestsTotal.WithLabelValues(m, p, s, st).Inc()
	GenerationDurationSeconds.WithLabelValues(m, p, s).Observe(duration.Seconds())
}

// ObserveTimeToFirstToken records TTFT duration.
func ObserveTimeToFirstToken(model, account string, duration time.Duration) {
	if duration <= 0 {
		return
	}
	m := sanitizeModel(model)
	a := sanitizeAccount(account)
	TimeToFirstTokenSeconds.WithLabelValues(m, a).Observe(duration.Seconds())
}

// ObserveTimeToFirstEvent records upstream first event duration.
func ObserveTimeToFirstEvent(model, account string, duration time.Duration) {
	if duration <= 0 {
		return
	}
	m := sanitizeModel(model)
	a := sanitizeAccount(account)
	TimeToFirstEventSeconds.WithLabelValues(m, a).Observe(duration.Seconds())
}

// IncActiveGeneration increments active generations for a model.
func IncActiveGeneration(model string) {
	m := sanitizeModel(model)
	activeGenerationsMu.Lock()
	activeGenerations[m]++
	ActiveGenerations.WithLabelValues(m).Set(float64(activeGenerations[m]))
	activeGenerationsMu.Unlock()
}

// DecActiveGeneration decrements active generations for a model.
func DecActiveGeneration(model string) {
	m := sanitizeModel(model)
	activeGenerationsMu.Lock()
	if activeGenerations[m] > 0 {
		activeGenerations[m]--
	}
	ActiveGenerations.WithLabelValues(m).Set(float64(activeGenerations[m]))
	activeGenerationsMu.Unlock()
}

// ObserveTokens increments token counters.
func ObserveTokens(tokenType, model, account string, count int64) {
	if count <= 0 {
		return
	}
	t := sanitize(tokenType)
	m := sanitizeModel(model)
	a := sanitizeAccount(account)
	TokensTotal.WithLabelValues(t, m, a).Add(float64(count))
}

// ObserveGeneratedChars increments output characters generated.
func ObserveGeneratedChars(model, account string, count int) {
	if count <= 0 {
		return
	}
	m := sanitizeModel(model)
	a := sanitizeAccount(account)
	GeneratedCharsTotal.WithLabelValues(m, a).Add(float64(count))
}

// ObserveFinishReason records stream termination reason.
func ObserveFinishReason(reason, model string) {
	r := sanitize(reason)
	m := sanitizeModel(model)
	FinishReasonsTotal.WithLabelValues(r, m).Inc()
}

// ObservePreparationDuration records duration of preparation phases.
func ObservePreparationDuration(phase string, duration time.Duration) {
	if duration <= 0 {
		return
	}
	p := sanitize(phase)
	PreparationDurationSeconds.WithLabelValues(p).Observe(duration.Seconds())
}

// AddUpstreamNetworkBytes records upstream network throughput.
func AddUpstreamNetworkBytes(direction string, count int64) {
	if count <= 0 {
		return
	}
	d := sanitize(direction)
	UpstreamNetworkBytesTotal.WithLabelValues(d).Add(float64(count))
}

// ObserveRetry records a failover / retry event.
func ObserveRetry(model, reason string) {
	m := sanitizeModel(model)
	r := sanitize(reason)
	RetriesTotal.WithLabelValues(m, r).Inc()
}

// ObserveStreamStall records a detected stream stall.
func ObserveStreamStall(model, account string) {
	m := sanitizeModel(model)
	a := sanitizeAccount(account)
	StreamStallsTotal.WithLabelValues(m, a).Inc()
}

// SetLatestPerformance updates the latest first event latency gauge.
func SetLatestPerformance(account, model string, duration time.Duration) {
	if duration <= 0 {
		return
	}
	a := sanitizeAccount(account)
	m := sanitizeModel(model)
	LatestPerformanceSeconds.WithLabelValues(a, m).Set(duration.Seconds())
}

// ObserveAccountCooldown records an account being put on cooldown.
func ObserveAccountCooldown(account, model, reason string) {
	a := sanitizeAccount(account)
	m := sanitizeModel(model)
	r := sanitize(reason)
	AccountCooldownsTotal.WithLabelValues(a, m, r).Inc()
}

// UpdateAccountStates sets gauge counts for all account states.
func UpdateAccountStates(counts map[string]int) {
	knownStates := []string{"ready", "busy", "cooldown", "auth_required", "unavailable"}
	for _, state := range knownStates {
		count := counts[state]
		AccountsTotal.WithLabelValues(state).Set(float64(count))
	}
}

// UpdateWorkerStates sets gauge counts for all worker states.
func UpdateWorkerStates(counts map[string]int) {
	knownStates := []string{"running", "prewarmed", "failed", "launching"}
	for _, state := range knownStates {
		count := counts[state]
		WorkersTotal.WithLabelValues(state).Set(float64(count))
	}
}

// ObserveWorkerLaunch records a worker launch outcome and duration.
func ObserveWorkerLaunch(account string, success bool, duration time.Duration) {
	a := sanitizeAccount(account)
	status := "success"
	if !success {
		status = "failure"
	}
	WorkerLaunchesTotal.WithLabelValues(a, status).Inc()
	if duration > 0 {
		WorkerLaunchDurationSeconds.Observe(duration.Seconds())
	}
}

// ObserveTokenCount records token count requests.
func ObserveTokenCount(model string, success bool) {
	m := sanitizeModel(model)
	status := "success"
	if !success {
		status = "failure"
	}
	TokenCountRequestsTotal.WithLabelValues(m, status).Inc()
}

// ObserveVideoRequest records video generation requests.
func ObserveVideoRequest(status string) {
	st := sanitize(status)
	VideoRequestsTotal.WithLabelValues(st).Inc()
}

// HTTPMiddleware wraps an http.Handler to automatically track HTTP metrics.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HTTPRequestsInFlight.Inc()
		defer HTTPRequestsInFlight.Dec()

		start := time.Now()
		wrapped := &responseWriterTracker{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		duration := time.Since(start)

		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}

		ObserveHTTPRequest(r.Method, r.URL.Path, status, duration, wrapped.written)
	})
}

type responseWriterTracker struct {
	http.ResponseWriter
	status  int
	written int
}

func (w *responseWriterTracker) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriterTracker) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.written += n
	return n, err
}

func (w *responseWriterTracker) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseWriterTracker) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
