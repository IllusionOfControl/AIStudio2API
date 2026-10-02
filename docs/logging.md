# Operational Logging

The management dashboard and system console share the same set of structured events. The web UI aggregates logs per request, while the console outputs line-by-line JSON to standard error. Log sources are identified as `service`, `request`, or the Google email address of the executing account.

The management API pushes historical snapshots and real-time increments via `GET /api/events`. Authentication operations that are not yet bound to a formal account (such as account creation) use the `auth` source.

## Log Structure

| Column | Meaning | Example |
| --- | --- | --- |
| Time | Event timestamp | `22:26:46` |
| Level | Event severity | `INFO`, `WARN`, `ERROR` |
| Source | Service, pending request, or executing account | `service`, `request`, `account@example.com` |
| Message | Phase, real-time metrics, and error descriptions | `generation service ready`, `stream stalled` |

Management event DTOs (event objects returned by the management API) use the following fields:

| Field | Meaning | Example |
| --- | --- | --- |
| `time` | RFC 3339 timestamp; displayed in local time in UI | `2026-08-30T02:10:00+08:00` |
| `level` | Event severity | `WARN` |
| `source` | `service`, `request`, `auth`, or account label | `request` |
| `message` | Phase, metrics, and raw error | `stream stalled \| ...` |
| `event` | Stable event identifier | `request.started`, `request.progress`, `request.finished`, `runtime.message` |
| `request` | Request identifier, status, usage, and diagnostic fields | Present on request-related events |

`INFO` records state progress and completion results; `WARN` records waits, fallbacks, and client cancellations; `ERROR` records failures. The management UI supports filtering by level, source, and message text, and log messages support selection and horizontal scrolling.

Request errors are stored in `request.error` and displayed directly below the request row. Operational event error details are preserved in `message`.

## Startup Sequence

The management process loads accounts and starts the control plane first. Once ready, the management dashboard allows viewing accounts, configurations, and logs while the generation service remains `STOPPED`.

```text
INFO  service  runtime assembly | 1/3 | loading accounts
INFO  service  runtime assembly | 2/3 | verifying Camoufox | accounts=28
INFO  service  runtime assembly | 3/3 | creating protocol client
INFO  service  protocol runtime ready | accounts=28 | elapsed=31ms
INFO  service  management listener started | addr=127.0.0.1:2048
INFO  service  management service ready | addr=http://127.0.0.1:2048
INFO  service  management UI opened | addr=http://127.0.0.1:2048
```

Clicking "Start Service" transitions the state to `LAUNCHING`. The service first reads `CachedModels` from the current `generation` (the generation service instance created by a start cycle) and executes `ListModels` concurrently across all `enabled`, `ready`, or `busy` accounts. When the cache is non-empty, workers are pre-warmed immediately; generations without cached models wait for the first non-empty verified catalog, while remaining accounts continue background synchronization. Once the first worker is ready, the state transitions to `RUNNING` and begins accepting requests, while remaining target workers continue warming up. Clicking "Stop Service" during `LAUNCHING` cancels the current phase and returns to `STOPPED`.

```text
INFO  service              generation service start | 1/2 | preparing model catalog | cached=0
INFO  service              generation service start | 2/2 | warming WAA workers | models=39 | target=5
INFO  service              model catalog background sync finished | synced=28 | non_empty=28 | models=39 | retry_pending=0 | elapsed=2.792s
INFO  account@example.com  WAA worker ready | page_model=gemini-flash-latest | PID=18240 | elapsed=8.172s
INFO  service              generation service ready | models=39 | workers=1/5 | elapsed=10.686s
INFO  service              WAA worker pre-warming complete | workers=5/5 | elapsed=34.903s
```

In `preparing model catalog`, `cached` indicates the consolidated verified model count at launch. In `model catalog background sync finished`, `synced` represents accounts returning without error (including empty catalogs); `non_empty` is the number of accounts returning non-empty verified catalogs; `models` is the merged public model count; `retry_pending` is the size of the pending retry set. Each non-empty result updates the shared catalog, publishes management events, and warms more workers upon arrival, meaning background sync completion logs may appear after `generation service ready`.

Accounts with empty catalogs, single-account sync failures, or empty responses enter the pending ID set. After the initial fan-out completes, a `RUNNING` generation service uses a single 30-second ticker to re-execute `ListModels` concurrently for all pending IDs; non-empty successes immediately update accounts, models, and worker selection, while failures and empty results are retained:

```text
INFO  service  model catalog retry completed | synced=2 | non_empty=2 | retry_pending=0
```

Cancellation and failure logs:

```text
INFO   service  generation service startup cancelled | elapsed=2.132s
ERROR  service  generation service startup failed | elapsed=2.132s | err=<ERROR>
```

Startup failures, cancellations, or Stop commands stop model catalog fan-out via Go `context` cancellation, waiting up to 2 seconds (`model catalog refresh stop timeout`). Control requests wait up to 12 seconds for in-flight start or stop operations, corresponding to `start timeout`, `stop timeout`, or `switch timeout`; these errors join worker cleanup errors in the error chain.

Stopping the generation service retains the management listener and logs worker counts and elapsed durations:

```text
INFO   service  stopping generation service | workers=5
INFO   service  generation service stopped | elapsed=3.204s
INFO   service  generation service is already stopped
ERROR  service  generation service stop failed | elapsed=10.006s | err=<JOINED_ERROR>
```

### Worker Lifecycle

Each account selects its WAA bootstrap model from its real-time catalog. When `gemini-flash-latest` is available and explicitly supports `generateContent` and `chat` capabilities, it is prioritized; otherwise, eligible models are tried in catalog order. Logs record the actual page model used.

WAA Bootstrap (page initialization) intercepts and terminates the initialization `GenerateContent` via WebDriver BiDi once proof generation capabilities and dynamic request headers are captured, producing zero model output tokens. The candidate loop terminates upon the first successful bootstrap.

```text
INFO  account@example.com  WAA worker starting | 1/7 | initializing page | page_model=gemini-flash-latest
INFO  account@example.com  WAA worker starting | 2/7 | preparing browser profile
INFO  account@example.com  WAA worker starting | 3/7 | launching Camoufox
INFO  account@example.com  WAA worker starting | 4/7 | connecting WebDriver BiDi
INFO  account@example.com  WAA worker starting | 5/7 | loading AI Studio
INFO  account@example.com  WAA worker starting | 6/7 | locating WAA service
INFO  account@example.com  WAA worker starting | 7/7 | executing WAA bootstrap
INFO  account@example.com  WAA worker ready | page_model=gemini-flash-latest | PID=18240 | elapsed=10.842s
```

Startup failures record the page model, elapsed duration, and root error. On-demand scaling events describe warm pool operations:

| Event | Meaning |
| --- | --- |
| `WAA worker scaled on-demand \| workers=N/M` | Active workers below `MAX_ACTIVE_WORKERS`; new worker successfully published |
| `WAA worker replaced on-demand \| workers=N/M` | Pending replacement worker successfully launched and replaced an idle worker |
| `WAA worker idle recycling \| workers=N/M` | Workers exceeding warm pool target closed after 5 minutes of inactivity |
| `WAA worker legacy instance stop failed` | Old instance failed to close; publishing aborted and pending worker teardown initiated |
| `WAA worker rebuild \| model=... \| replaying request` | Worker invalidated; operational request replayed on new instance (next line shows trigger) |
| `WAA worker updated \| model=... \| replaying request` | Concurrent path already replaced worker; current request uses new instance |

Individual worker stop events:

```text
INFO   account@example.com  stopping WAA worker | PID=18240
INFO   account@example.com  WAA worker stopped | PID=18240 | elapsed=3.014s
ERROR  account@example.com  WAA worker stop failed | PID=18240 | elapsed=10.002s | err=<JOINED_ERROR>
```

Shutdown deadlines consist of three sequential stages:

| Stage | Upper Bound | Error Semantics |
| --- | ---: | --- |
| BiDi `session.end` | 3 seconds | On timeout, closes BiDi connection and proceeds to process termination |
| Process termination & `command.Wait` | 5 seconds | Windows executes `taskkill /T /F` followed by `Process.Kill`; retains failure cause |
| Browser profile directory removal retry | 2 seconds | Retries every 100ms; returns final `RemoveAll` error on failure |

`Worker.Close` has a typical upper bound of ~10 seconds plus scheduling overhead. On failure, worker handles, process handles, runtime leases, warm pool records, and generation indices remain retriable; subsequent Stop operations re-execute the cleanup chain. If both legacy and pending workers fail to close during hot replacement, both instances occupy capacity slots.

## API Requests

Request events are correlated via `request.id`. The UI merges start, progress, and completion into a single row displaying the executing account, HTTP status, model, latency, and token statistics. Expanding details reveals endpoint, input usage, raw termination reason, full event timeline, and JSON payloads.

```text
22:11:47  200  gemini-3.8-flash  4.76 s  tool_calls 1
thinking 32 · reply 38 · total output 70 tokens · avg speed 14.7 tokens/s
```

| Field | Meaning |
| --- | --- |
| HTTP Status | Request outcome; errors occurring after stream commencement still record actual failure status |
| Latency | Total duration from request ingress to termination, in seconds |
| Thinking | `request.usage.reasoning_tokens` |
| Reply | `request.usage.reply_tokens`, including text, tool calls, and non-thinking output |
| Total Output | `request.usage.output_tokens`, equal to thinking plus reply tokens |
| Avg Speed | Total output divided by total latency (tokens/s), including preparation and wait time |
| Tool Calls | Count of function call events produced in this generation |
| Input Usage | `request.usage.input_tokens`, including input messages and tool definitions |
| Total Usage | `request.usage.total_tokens`, sum of input and output tokens |

Usage metrics are displayed once returned by the generation service; `request.usage` is omitted when usage is unavailable. Average speed measures end-to-end throughput across the same duration interval, even for buffered or chunk-burst network delivery.

`request.state` distinguishes `running`, `completed`, `tool_calls`, `limited`, `blocked`, `failed`, and `cancelled`. Policy terminations retain HTTP `200` and display raw `finish_reason`; reaching output token limits displays `max_tokens`. Unknown termination reasons are preserved as `provider_<code>`.

In the console, each line is an independent JSON event:

```json
{"time":"2026-09-06T14:11:47Z","level":"INFO","msg":"tool calls completed","event":"request.finished","source":"account@example.com","request":{"id":"chatcmpl_example","state":"tool_calls","model":"gemini-3.8-flash","status":200,"duration_ms":4758,"tool_calls":1,"finish_reason":"stop","usage":{"input_tokens":100,"reasoning_tokens":32,"reply_tokens":38,"output_tokens":70,"total_tokens":170,"average_tokens_per_second":14.712064}}}
```

Sampling parameters are recorded in `request.parameters`. `first_event_ms` and `upstream_bytes` are preserved as JSON diagnostic fields. The dashboard supports searching by severity, account, model, request ID, and HTTP status code.

Client cancellations are recorded as `499`; authentication, quota, and upstream failures use respective HTTP status codes and `request.error`.

When the management UI cancels active requests or stops the generation service, connected clients receive `503 request_canceled` or corresponding streaming error events per public protocol standards.

## Streaming Latency and Stalls

When no progress is detected across 15 consecutive seconds, diagnostic logs are generated. Requests terminate upon receiving upstream completion, client cancellation, or reaching `REQUEST_TIMEOUT`.

| Event | Current Phase | Key Fields |
| --- | --- | --- |
| `request preparation waiting` | Account selected; preparing WAA proof or waiting for GenerateContent headers | `current`, `model` |
| `request preparation finished` | Response body receiving began after >15s in preparation | `waiting`, `waa`, `headers`, `model` |
| `upstream first event waiting` | Upstream response established; no semantic events decoded yet | `upstream_bytes`, `last_network` |
| `upstream first event arrived` | First semantic event decoded after waiting | `waiting`, `event`, `model` |
| `stream stalled` | Semantic events previously received; no new events for 15s | `last_event`, `reasoning`, `content`, `upstream_bytes`, `last_network` |
| `stream resumed` | Next semantic event decoded following stall | `stalled`, `current_event` |
| `account fallback` | Current account failed prior to first upstream semantic event | Account source, model, and underlying reason |

`upstream_bytes` measures cumulative upstream body bytes read for the account, and `last_network` records the duration since the most recent read. `upstream_bytes=0` indicates no response body data has arrived; growing byte counts with short `last_network` values indicate incoming network data that the decoder has not yet formed into complete semantic events.

```text
WARN  account@example.com  request preparation waiting | elapsed=15s | current=waiting for upstream headers | model=gemini-3.7-flash
INFO  account@example.com  request preparation finished | waiting=47.755s | waa=1.204s | headers=46.551s | model=gemini-3.7-flash
```

```text
WARN  account@example.com  upstream first event waiting | elapsed=15s | model=gemini-3.7-flash | upstream_bytes=0
INFO  account@example.com  upstream first event arrived | waiting=28.447s | event=reasoning | model=gemini-3.7-flash
```

```text
WARN  account@example.com  stream stalled | model=gemini-3.7-flash | elapsed=15s | last_event=reasoning | reasoning=4 | content=0 | upstream_bytes=18240 | last_network=15.001s
INFO  account@example.com  stream resumed | model=gemini-3.7-flash | stalled=1m31.208s | current_event=reasoning
```

All streaming public protocols emit an SSE comment frame after 10 consecutive seconds without semantic events:

```text
: ping

```

SSE clients treat this frame as a keep-alive signal, while message content, reasoning, and usage continue over protocol-specific `data` or named events. Anthropic sends initial events before invoking the generation service; OpenAI Chat and Responses send initial events once the generation stream is acquired.

## Account Events

HTTP `401` triggers an automatic cookie renewal using the OAuth/DBSC materials saved for Chrome-imported accounts, resets the WAA worker, and replays the request once.

```text
INFO  account@example.com  account auth renewal | 1/2 | refreshing cookies
INFO  account@example.com  account auth renewal | 2/2 | resetting protocol runtime
INFO  account@example.com  account auth renewal completed | elapsed=1.116s
ERROR account@example.com  account auth renewal failed | elapsed=1.116s | err=<ERROR>
```

HTTP `403` and protocol Code 7 retain the account and model's `verified` record without marking permanent permission failure. Protocol Code 5, worker process crashes, or worker replacements trigger worker recreation and a single replay; other retriable errors enter temporary cooldown.

```text
WARN  account@example.com  account fallback | model=gemini-3.7-flash
                           reason: AI Studio GenerateContent returned HTTP 403, protocol code 7: The caller does not have permission
```

Account tier classifications (`Free`, `Pro`, `Ultra`, `Plus`) originate from `GetAiStudioBenefitTier`. Paid models filter accounts by benefit tier and access mode; accounts with prior successful calls to the target model are prioritized over untried accounts.

Management dashboard account operation events:

| Event | Outcome |
| --- | --- |
| `account addition \| 1/2`, `2/2`, `account addition complete` | Isolated login, auth state saved, model catalog synchronized |
| `account login \| 1/2`, `2/2`, `account login complete` | Updated credentials and worker instances for current account |
| `account verification`, `account verification complete` | Verified AI Studio access, benefit tiers, and live catalog |
| `account config updated`, `account deleted` | Account configuration and runtime objects updated |
| `account catalog sync complete` | New catalog written to shared model catalog and scheduling states |
| `account catalog sync retry pending` | Current sync failed; runtime retries will continue processing |

Resource and scope tracking events:

| Event | Meaning |
| --- | --- |
| `file reference copied` | Drive file temporarily replicated to target account for this generation |
| `inline attachment processing complete` | Count, raw byte size, and processing duration of attachments in request |
| `temporary file cleanup failed` | Cleanup of temporary Drive file failed; raw error retained |
| `transcription account fallback` | Transcription generation phase switched candidate prior to first result |
| `bidi account fallback` | Live/Robotics setup phase switched candidate |

Delayed authentication results apply only when `authGeneration` and `checkedAt` match the current account; delayed model successes or cooldown results apply only when `modelAccessGeneration` and `checked_at` match the current model catalog. Logs record changes actually committed to runtime state and any persistence failures.

## Management Event Stream

When the management dashboard connects to `GET /api/events`, it initially receives current service status, models, accounts, the last 2000 log events, cooldowns, and active requests, followed by real-time incremental events. Request events aggregate by ID in the UI, and the console outputs line-by-line JSON via Go's `slog.JSONHandler`.

Initial event sequence:

1. `status`
2. `models`
3. `accounts`
4. Up to 2000 `log` entries
5. `cooldowns`
6. Active `request` objects sorted by start timestamp

Subsequent incremental event types include `status`, `models`, `accounts`, `log`, `cooldowns`, and `request`. Each SSE `data` line adheres to `{"type":"<TYPE>","data":<DTO>}` where `<DTO>` is the event object for that type; field definitions are specified in [protocol.md](protocol.md).
