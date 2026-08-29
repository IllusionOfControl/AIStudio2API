# AIStudio2API Monitoring (Prometheus + Grafana)

AIStudio2API comes with built-in comprehensive metrics collection, a Prometheus-compatible `/metrics` exporter, and a turnkey Grafana dashboard.

---

## 🚀 Quick Start

### 1. Start AIStudio2API
Launch the server as usual:
```bash
./start.bat
# or
go run ./cmd/aistudio2api
```
Metrics are exposed at: **`http://localhost:8080/metrics`**

### 2. Launch Prometheus and Grafana with Docker
From the project root:
```bash
# Windows
start-monitoring.bat

# Linux / macOS
./start-monitoring.sh
# or
docker compose -f docker-compose.metrics.yml up -d
```

### 3. Access Dashboards & Endpoints
- **Grafana**: [http://localhost:3000](http://localhost:3000)
  - Username: `admin`
  - Password: `admin`
  - The **"AIStudio2API Overview & Metrics"** dashboard is pre-provisioned and loads automatically.
- **Prometheus**: [http://localhost:9090](http://localhost:9090)

---

## 📊 Available Metrics

### 1. HTTP & API Requests
| Metric | Type | Description |
|---|---|---|
| `http_requests_total` | Counter | Total count of HTTP requests by `method`, `path` template, and status code `status`. |
| `http_request_duration_seconds` | Histogram | Duration of HTTP requests in seconds. |
| `http_requests_in_flight` | Gauge | Number of active HTTP connections currently being processed. |
| `http_response_size_bytes` | Histogram | Size of HTTP response payloads in bytes. |

### 2. Generation & LLM Metrics
| Metric | Type | Description |
|---|---|---|
| `aistudio2api_generation_requests_total` | Counter | Number of generation requests by `model`, `protocol` (`openai`, `anthropic`, `gemini`), `stream` mode, and `status`. |
| `aistudio2api_generation_duration_seconds` | Histogram | End-to-end request duration / latency in seconds. |
| `aistudio2api_generation_time_to_first_token_seconds` | Histogram | Time to first content token emission (**TTFT**). |
| `aistudio2api_generation_time_to_first_event_seconds` | Histogram | Time until the first event is received from upstream Google AI Studio. |
| `aistudio2api_generation_active_requests` | Gauge | Real-time count of active generation requests in flight. |
| `aistudio2api_generation_tokens_total` | Counter | Token usage partitioned by `type` (`prompt`, `completion`, `reasoning`), `model`, and `account`. |
| `aistudio2api_generation_chars_total` | Counter | Total count of output characters generated. |
| `aistudio2api_generation_finish_reasons_total` | Counter | Stream termination finish reasons (`STOP`, `MAX_TOKENS`, `SAFETY`, `TOOL_CALLS`, etc.). |

### 3. Orchestration & Upstream Engine
| Metric | Type | Description |
|---|---|---|
| `aistudio2api_orchestrator_preparation_duration_seconds` | Histogram | Duration of preparation phases (WAA proof generation, response headers). |
| `aistudio2api_orchestrator_upstream_bytes_total` | Counter | Upstream network bandwidth (bytes received / sent). |
| `aistudio2api_orchestrator_retries_total` | Counter | Account failovers, retries, and worker recovery events. |
| `aistudio2api_orchestrator_stream_stalls_total` | Counter | Count of detected stream stall / freeze events. |
| `aistudio2api_orchestrator_latest_latency_seconds` | Gauge | Latest observed first-event latency for account-model pairs. |

### 4. Account Pool & Workers (WAA)
| Metric | Type | Description |
|---|---|---|
| `aistudio2api_accounts_total` | Gauge | Account counts grouped by state (`ready`, `busy`, `cooldown`, `auth_required`, `unavailable`). |
| `aistudio2api_accounts_cooldowns_total` | Counter | Account cooldown occurrences by model and reason (Rate Limit, Quota, etc.). |
| `aistudio2api_workers_total` | Gauge | WAA browser worker counts by state (`running`, `prewarmed`, `failed`). |
| `aistudio2api_workers_launches_total` | Counter | Camoufox browser startup events and outcomes (`success` / `failure`). |
| `aistudio2api_workers_launch_duration_seconds` | Histogram | Cold-start launch duration of browser workers in seconds. |

### 5. Tokenization & Media Operations
| Metric | Type | Description |
|---|---|---|
| `aistudio2api_tokens_count_requests_total` | Counter | Token count requests (`/count_tokens`). |
| `aistudio2api_video_requests_total` | Counter | Video generation requests. |

### 6. Go Runtime & System Metrics
| Metric | Type | Description |
|---|---|---|
| `go_goroutines` | Gauge | Number of active goroutines. |
| `go_memstats_alloc_bytes` | Gauge | Heap memory currently in use. |
| `process_cpu_seconds_total` | Counter | Total process CPU usage. |
| `go_gc_duration_seconds` | Summary | Garbage collector STW pause durations. |

---

## 📈 Grafana Dashboard Layout

The pre-configured **"AIStudio2API Overview & Metrics"** dashboard includes 6 core sections:
1. **Overview & Key Stats**: Active in-flight requests, total generations, current RPS, error rate %, total token count, and median TTFT (P50).
2. **Request Rates & Traffic Analysis**: Real-time request rate graphs segmented by model (Gemini, Claude, GPT), protocol (OpenAI / Anthropic / Gemini), and HTTP status codes.
3. **Latency, TTFT & Performance**: Latency percentiles (P50, P90, P95, P99), TTFT per model, and granular preparation phase durations (WAA proof generation).
4. **Token Usage & Generation Analytics**: Token generation rate (Prompt, Completion, Reasoning) and pie charts for token distribution across models and accounts.
5. **Account Pool, Workers & Failovers**: Live account pool health, cooldown occurrences, failovers, stream stalls, and stream termination reasons.
6. **System, Go Runtime & Network Resources**: Memory allocation (Heap / RSS), goroutines, OS threads, upstream bandwidth, and CPU utilization.
