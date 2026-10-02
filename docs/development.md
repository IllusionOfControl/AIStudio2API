# Development & Contributing

AIStudio2API uses Go to directly invoke Google AI Studio's internal MakerSuite private protocol, presenting accounts, models, cooldowns, and request statuses via an embedded Vue 3 management dashboard. Request processing, streaming decoding, account scheduling, and public protocol adaptation are all performed in-process within Go; Camoufox is retained solely for official WAA initialization and fresh proof generation workflows (when `WAA_BACKEND=go`, this is also handled in-process in Go; see [WAA Implementation](waa.md)).

The management listener, public APIs, account pool, protocol runtime, and embedded Vue management UI reside within the same process. The generation service is a stoppable, recreatable public API service instance; for raw JSON+protobuf, WebChannel, and WAA formats, see [protocol.md](protocol.md).

## 1. Environment, Initial Configuration, and Startup

| Scenario | Required Components | Description |
| --- | --- | --- |
| Release execution | `aistudio2api` | Automatically prepares Camoufox on first run; does not require Python, Node.js, or Playwright |
| Source execution | Go 1.25.0+, Node.js 22.13+ or 24+, accompanying npm | Node.js is only used to build the Vue management frontend |
| Windows Chrome import | Windows amd64, stable Chrome release | The Go application directly reads OAuth/DBSC materials from local Chrome profiles |

Windows users can directly run `start.bat` in the repository root. The script prioritizes launching an existing `aistudio2api.exe`; only when the executable is missing will it run `npm ci`, build the frontend, and compile the Go binary. Once started, the program automatically opens the management dashboard while the generation service remains stopped initially; account login, log inspection, and starting/stopping the generation service are all performed in this interface.

Execution order of `start.bat`:

```text
aistudio2api.exe exists
  -> Directly run existing binary

aistudio2api.exe does not exist
  -> Check node, npm, go, and web/package-lock.json
  -> web/npm ci
  -> web/npm run build
  -> go build -o aistudio2api.exe ./cmd/aistudio2api
  -> Run newly built binary
```

When frontend or Go source code changes require a rebuild, terminate the current management process and remove the old binary before running `start.bat`. On Windows, replacing an active binary may leave behind `aistudio2api.exe~`, which is a build artifact copy produced while the target file was locked by the old process.

Initial startup from source:

```powershell
cd web
npm ci
npm run build
cd ..
go run ./cmd/aistudio2api
```

The "Accounts" page in the management dashboard provides bulk Chrome import and browser login, as well as re-login, verification, editing, enabling/disabling, and deletion of accounts. Browser login is completed in an isolated Camoufox instance and automatically captures the email address. The `setup` subcommand retains four CLI import entry points:

| Entry Point | Command | Use Case |
| --- | --- | --- |
| Scan local Chrome | `aistudio2api setup` | Interactively select importable profiles |
| Specific Chrome account | `aistudio2api setup --profile <PROFILE>` or repeated `--email` | Deterministic batch imports |
| Isolated login | `aistudio2api setup --login` | Manually complete Google login in a visible Camoufox window |
| File import | `aistudio2api setup --storage-state <file>` | Import Playwright storage state structure |

`setup` also accepts the following flags:

| Flag | Purpose |
| --- | --- |
| `--chrome-root <DIR>` | Specify Chrome User Data root directory |
| `--proxy <URL>` | Pin to account initialization, WAA, and operational requests |
| `--locale <LOCALE>` | Configure account language |
| `--timezone <IANA_ZONE>` | Configure account timezone |

`--storage-state`, `--login`, and Chrome import parameters constitute file import, isolated login, and browser import modes respectively. Isolated login uses `--login`. `setup` requires `AISTUDIO_AUTH_STATES` to point to an account directory.

`--proxy` pins the proxy URL for initialization, WAA, and operational requests of new accounts, accepting an unauthenticated HTTP, HTTPS, or SOCKS5 URL. `--locale` and `--timezone` configure the account environment; when Chrome import does not explicitly specify a language, it reads the profile's preferred language. Camoufox is located in the following order: process environment variable `CAMOUFOX_PATH`, `runtime/camoufox/`, a directory with the same name beside the executable, and local Windows Camoufox cache. If none exists, it automatically downloads the pinned version for the current platform.

Routine startup only requires running the binary or Go entry point, then starting the generation service from the management dashboard:

```powershell
./aistudio2api.exe
go run ./cmd/aistudio2api --listen 127.0.0.1:2048 --open-ui
```

Routine service execution accepts the following flags:

| Flag | Purpose | Default Source |
| --- | --- | --- |
| `--auth <PATHS>` | Override account files, directories, or comma-separated paths for this process | `AISTUDIO_AUTH_STATES` |
| `--listen <HOST:PORT>` | Override listen address for management dashboard and API | `LISTEN_ADDR` |
| `--proxy <URL>` | Override global proxy used when creating generation service instances | `PROXY` |
| `--open-ui` | Open management dashboard in browser upon launch | `true` when launched without arguments |

The management dashboard and `/api` control plane continue running even when the generation service is stopped. Stopping the generation service cancels active generation requests and closes WAA workers; restarting it re-reads account model catalogs. Closing the console window or pressing `Ctrl+C` exits the entire management process.

## 2. Directories, Components, and Runtime Dependencies

```text
cmd/aistudio2api/        Thin entry point, calls app.Run in internal/app
internal/app/            Configuration, account assembly, auth refresh, signals, generation instances, scheduling, lifecycle
internal/setup/          Chrome import, storage-state import, and isolated login commands
internal/aistudio/       Accounts, MakerSuite, WAA, models, tools, uploads, media, and canonical events
internal/api/            OpenAI, Responses, Anthropic, Gemini, and management HTTP routes
internal/camoufoxnative/ Native WebDriver BiDi, WAA bootstrap, and isolated login
internal/waa/            Pure-Go WAA: BotGuard VM, Firefox-shaped host, and goja fork
internal/chromeauth/     Windows Chrome OAuth/DBSC discovery, import, and renewal
internal/config/         Global configuration reading, validation, and atomic persistence
internal/webui/          Embeds and serves Vue production assets
web/                     Vue 3, TypeScript, Vite, and Tailwind CSS source code
docs/                    Development workflows and private protocol specifications
auth/                    Per-account configuration, authentication state, and recoverable runtime state
runtime/camoufox/        Camoufox browser runtime used in releases
```

Primary dependency direction:

```text
cmd/aistudio2api
  -> internal/app
       -> internal/api
       -> internal/aistudio
       -> internal/setup
       -> internal/camoufoxnative
       -> internal/config
       -> internal/webui

internal/setup
  -> internal/aistudio
  -> internal/camoufoxnative
  -> internal/chromeauth
  -> internal/config
```

The request processing pipeline maintains a strict unidirectional flow:

```text
HTTP route
  -> client protocol decoder
  -> canonical request
  -> capability-aware account lease
  -> AI Studio array encoder
  -> per-account WAA proof
  -> fingerprinted Camoufox GenerateContent transport
  -> incremental response decoder
  -> canonical events
  -> client protocol response
```

WebSocket endpoints follow the same layering: `internal/api` decodes the public protocol, `internal/app` binds accounts and runtime states, and `internal/aistudio` performs WebChannel and canonical event conversions. Public adapters consume only canonical requests and events; account files, WAA objects, raw arrays, and resource stickiness are managed by `internal/aistudio` and `internal/app`.

Camoufox is managed directly by Go via WebDriver BiDi. When starting the data plane, the service prepares isolated, headless, resident account runtimes according to `WARM_WORKER_LIMIT` and `WARM_STARTUP_CONCURRENCY`, replacing the least recently used idle runtime when other account capabilities are needed. Headless runtimes throttle page rendering to 1 frame per second. Each runtime uses a temporary profile that is deleted upon shutdown; the HTTP disk cache is written to `camoufox-cache/` in the account directory, allowing the same account to reuse official static assets across restarts. A second concurrent runtime for the same account uses the cache inside its temporary profile. The process creating the temporary profile holds a lock file inside the profile, and runtime assembly deletes orphaned profiles whose locks have been released. On Windows, each Camoufox process tree is assigned to a Job Object held by the service process, ensuring all child processes terminate when the parent exits. Each runtime triggers GenerateContent on the official page and intercepts the request before network transmission to acquire the official WAA service and dynamic headers; subsequent operational payloads are encoded by Go, and after synchronizing prompt state and generating a fresh proof, sent via native `fetch` within the same fingerprinted page. The response stream is returned in chunks to Go over WebDriver BiDi. Other MakerSuite, Drive, and media control plane requests use the Go HTTP transport pinned to the account's egress proxy.

When `WAA_BACKEND=go`, Camoufox is not located, downloaded, or launched. Each account runtime requests the official web page and `GetLoggingContext` within the service process to obtain public request headers, calls `Waa/Create` to obtain a challenge, downloads and caches the interpreter to `auth/.waa-interpreters/` by hash, and executes the program within the `goja` fork and Firefox-shaped host in `internal/waa`. Protected requests carry Firefox request headers and account cookies, sent by Go HTTP transport via the account's pinned egress, and response cookies are written back to account state. Browser login on the Accounts page prepares Camoufox on demand upon first use. For complete details on the architecture, host environment, lifecycle, data files, and upstream tracking methods, see [WAA Implementation](waa.md).

## 3. Configuration, Accounts, and Persistent State

The application reads optional `.env` configuration from the current working directory; process environment variables override corresponding settings:

| Variable | Description | Default |
| --- | --- | --- |
| `AISTUDIO_AUTH_STATES` | Account file, directory, or comma-separated multiple paths | `auth` |
| `LISTEN_ADDR` | HTTP service listen address | `127.0.0.1:2048` |
| `PROXY_API_KEY` | Public API access key | Empty |
| `ADMIN_AUTH_ENABLED` | Toggle for admin username/password login | `false` |
| `ADMIN_USERNAME` | Admin login username | `admin` |
| `ADMIN_PASSWORD` | Admin password, required when login is enabled | Empty |
| `PROXY` | Fixed egress proxy used by setup and accounts without a custom proxy | Empty |
| `INIT_TIMEOUT` | Single account initialization timeout | `2m` |
| `REQUEST_TIMEOUT` | Maximum execution time for a single request | `5m` |
| `WARM_WORKER_LIMIT` | Target resident pre-warmed account pool size | `5` |
| `MAX_ACTIVE_WORKERS` | Maximum active worker capacity limit, must be >= warm pool target | `10` |
| `WARM_STARTUP_CONCURRENCY` | Number of accounts initialized concurrently during startup | `2` |
| `PER_ACCOUNT_CONCURRENCY` | Concurrency limit per account | `2` |
| `ROUTING_STRATEGY` | Account routing strategy: `round-robin` or sticky `fill-first` | `round-robin` |
| `UPSTREAM_CHANNELS` | Upstream generation channels: `playground`, `build` (comma-separated) | `playground,build` |
| `BUILD_NATIVE_NONSTREAM` | Prefer Build native unary calls for non-streaming requests | `true` |
| `WAA_BACKEND` | WAA backend: `camoufox` or `go` | `camoufox` |
| `TEMPORARY_CHAT` | Whether WAA warm-up pages use temporary chat mode | `false` |
| `HEADLESS` | Whether Camoufox runs in headless mode (`true` silent background; `false` visible window) | `true` |
| `CAMOUFOX_PATH` | Custom Camoufox browser executable path (optional) | Empty |

`LISTEN_ADDR` uses `host:port` syntax with a port range of `1..65535`. Duration and capacity fields must be positive values; the valid range for `WARM_STARTUP_CONCURRENCY` is `1..WARM_WORKER_LIMIT`. Global proxy URLs use `http`, `https`, or `socks5` pure origin shapes. Command-line flags `--auth` and `--proxy` override saved values read when launching generation service instances.

`GET /api/config` exposes the active runtime configuration (read-only). Configuration is loaded once at startup from environment variables or `.env` and does not support runtime mutation via the API:

| Field | Meaning |
| --- | --- |
| `auth_states`, `proxy`, `init_timeout`, `request_timeout` | Saved values used upon next generation service startup |
| `warm_worker_limit`, `max_active_workers`, `warm_startup_concurrency`, `per_account_concurrency` | Capacity parameters used upon next generation service startup |
| `temporary_chat`, `waa_backend`, `upstream_channels`, `build_native_nonstream` | WAA and upstream channel configuration used upon next generation service startup |
| `admin_auth_enabled`, `admin_username`, `admin_password` | Saved admin authentication settings; omitting password retains current value, password is write-only |
| `admin_password_set` | Whether an admin password has been configured |
| `listen_addr`, `proxy_api_key` | Saved management listener configuration |
| `active_listen_addr`, `active_proxy_api_key` | Immutable values bound to current management process |
| `management_restart_required` | Saved listen address, API key, or admin auth differs from current management process |
| `service_restart_required` | Saved generation service configuration differs from active generation service instance |

Configuration persistence uses temporary files, `Sync`, and atomic replacement. Listen address, local API key, and admin authentication settings are held by the management process and applied after process restart; other configurations take effect after stopping and restarting the generation service.

When admin authentication is enabled, `/api` uses an independent `HttpOnly`, `SameSite=Strict` session cookie valid for 12 hours. Logging out revokes the session and terminates its management SSE subscription. When admin authentication is disabled, the management API enforces loopback origin and host validation. Remote management should be proxied via HTTPS reverse proxy, preserving `Host` and setting `X-Forwarded-Proto: https`. Access keys for the generation API are configured separately.

Configuration is loaded once from environment variables and `.env` upon startup and remains strictly read-only during execution. The Web UI Settings panel functions as a read-only configuration inspector.
Generation service startup follows this sequence (in code, `generation` denotes a single generation service instance created by a Stop/Start cycle):

```text
POST /api/control/stop
  -> Cancel LAUNCHING, active requests, and background scaling worker starts
  -> Wait for model catalog refresh to exit and shut down active workers
  -> Management listener continues serving /api and Web UI

POST /api/control/start
  -> Complete cleanup of previously stopped instance
  -> Re-read .env and apply command-line --auth/--proxy overrides
  -> Create and activate new generation service instance
  -> Initialize in-memory catalog from current generation's CachedModels
  -> Concurrently refresh all enabled ready/busy accounts
  -> Cold generation waits for first non-empty verified catalog
  -> Start first WAA worker and transition to RUNNING; if no warmable account is ready before first sync completes, re-warm after sync completes
  -> Remaining catalog sync and warm pool pre-warming continue in the background
```

When configuration reading, validation, instance creation fails, or cancellation occurs before activation, the stopped instance remains unchanged. If startup fails after switching to a new instance, the instance enters `STOPPED` and the management UI returns a structured error. The model catalog is kept in the memory of the current generation instance; new instances created by standard Stop/Start perform a cold start from empty `CachedModels`. When the current instance already has a verified cache, `trackedService.Start` directly uses that cache to warm workers while continuing to refresh all accounts in the background.

Catalog synchronization shares a context (Go cancellation signal) with the active generation service. Startup failures, cancellations, or Stop operations wait up to 2 seconds for sync goroutines to exit; incomplete lifecycle transitions (start or stop operations) wait up to 12 seconds. Timeouts and worker cleanup errors are retained in the error chain via `errors.Join`.

Each account directory contains:

| File | Content | Lifecycle |
| --- | --- | --- |
| `account.json` | label, enabled, proxy, locale, timezone | Written when creating or editing an account |
| `storage-state.json` | Cookies, localStorage, and optional Chrome OAuth/DBSC refresh materials | Atomically written back after merging `Set-Cookie` or auth refresh |
| `camoufox-fingerprint.json` | Account's fixed browser fingerprint, language, and timezone | Generated on first run; empty values, window geometry, fonts, voices, and media devices normalized per Camoufox launcher rules; reused across re-logins and WAA runtimes |
| `runtime-state.json` | Benefit tier, model access status, cooldowns, and resource-account bindings | Atomically written back after tier sync, first model result, or resource changes |
| `camoufox-cache/` | Camoufox HTTP disk cache for this account, capped at 256 MB | Exclusively written while WAA runtime is active; deleted along with account directory |

In `runtime-state.json`, `model_access` values are `{state, checked_at, reason?}` with `verified` representing success; `cooldowns` values are `{until, reason?}`; `resources` values store kind, name, mime, size, purpose, created_at, and optional video metadata. Drive files, Veo operations, video artifacts, and Bidi resumption tokens maintain sticky affinity to their creating account. Veo operations additionally persist public video object metadata (model, duration in seconds, file size, UTC creation timestamp), allowing subsequent polling after service or process restarts to project identical fields.

Active request locks protect cross-process account leases, while short transaction locks guard `runtime-state.json` merge writes. Short transaction locks reside at `auth/.leases/<account>.runtime.lock`, retrying at 25ms intervals up to 2 seconds; once acquired, disk state is re-read, target fields modified, files atomically replaced, and in-memory caches and resource indices synchronized. Resource transactions taking a context return early if cancelled or upon deadline expiration.

Account updates treat the atomic write of `account.json` as the persistent commit point. `internal/app` first prepares a `pending` (uncommitted) fixed egress, shuts down the old worker, locks the account's worker configuration, and calls `AccountLease.SaveConfig`; once saved, worker configuration and fixed egress are committed in sequence. If preparation, shutdown, or saving fails, pending updates are discarded; lease release errors after a successful save are returned as-is, and published configurations remain active.

Account scheduling filters candidates by models and methods returned from real-time per-account `ListModels`, then selects workers that are ready with available concurrency slots. `ROUTING_STRATEGY=round-robin` alternates across candidate accounts for each model, resuming from the last selected account ID; `fill-first` persistently uses the first available account ordered by ID, switching only when concurrency slots are full, in cooldown, or unavailable. Generation request candidates are combinations of accounts and enabled `UPSTREAM_CHANNELS`: Playground evaluates candidates via `ListModels` and benefit tiers, while Build evaluates via the Gemini API model catalog returned by the Build proxy and the same benefit tiers; round-robin and sticky selection progress across combinations ordered by account ID and channel sequence, with cooldowns tracked per channel (`build:<model>` for Build). When one channel enters cooldown, the same account can continue on another channel; when all enabled channels enter cooldown, requests queue or return 429 according to cooldown rules (see [Build Channel](build.md)). Concurrency slots, workers, and WAA are shared per account; token counting, Live, Veo, transcription, and Drive file references use Playground RPCs. Each account leases at most `PER_ACCOUNT_CONCURRENCY` request slots concurrently; the first request acquires the cross-process file lock, and the last request releases it. WAA proofs are generated serially by account workers, while `GenerateContent` is transmitted concurrently and read as a stream over the same Camoufox page (or via account runtime Go HTTP when `WAA_BACKEND=go`); requests generate Authorization using the worker's current cookies before dispatch, and once response headers arrive, worker cookies are atomically synchronized to persistent account storage. Cookies from other MakerSuite HTTP responses are merged with latest account state upon header arrival. Unpinned requests encountering retriable 401, 403, 404, 429, 5xx, or single-account initialization timeouts can switch to untried accounts with identical capabilities before the first upstream semantic event is emitted; Drive references are temporarily copied to the executing account on demand, while explicit accounts and Veo operations maintain sticky bindings. Chrome-imported states retain refresh materials: on HTTP `401`, a single refresh is attempted via the same fixed egress, the account's WAA runtime is rebuilt, and the request is replayed.

Worker capacity is constrained by warm pool target, active worker ceiling, and per-account concurrency. When active worker count is below `MAX_ACTIVE_WORKERS`, new workers are launched and published directly. When capacity is full and idle legacy instances exist, a pending replacement worker is started before shutting down the old instance and publishing the replacement; idle instances whose requested model is in cooldown (global or model quota limit) are prioritized, with the least recently used among them selected first, followed by the oldest idle instances overall. Multiple cooling instances can be replaced in parallel, each reserving an independent old instance, resulting in a brief coexistence of old and new processes during startup. Existing workers continue serving if replacement startup fails or is cancelled. Requests only claim cold accounts when capacity slots are available; when no slot is immediately available, the account is released and reclassified to be serviced by an idle warm worker or a newly vacated slot. Requests with schedulable accounts but no free slots queue in first-come, first-served order under matching selection criteria, waking on lease releases or worker state changes until request timeout. When all candidate accounts are in cooldown, requests whose earliest recovery time is within 1 minute queue waiting for recovery, while requests with later recovery times immediately return 429 with the earliest recovery time in the error payload. Workers exceeding `WARM_WORKER_LIMIT` that remain idle for 5 minutes are closed, maintaining the warm pool target. When concurrent recycling of an old worker and pending worker fails, both process and lease are retained as cleanup pending and consume a capacity slot; subsequent Stop operations will retry cleanup. When an account's WAA runtime lease is held by another process, it is excluded from warming and scheduling candidates, re-probing after 5 seconds initially with doubled backoff up to 1 minute, logging once per contention period; requests requesting that account specifically or left with only contended accounts return an account-in-use error.

Failure recovery waits for active requests on the same account to release their leases; during this waiting period, new requests are suspended for that account. Client cancellations only terminate their own request. Warm-up runs are submitted once the official Run button is activated, and requests re-check account cooldown status immediately before transmission.

Account states:

| State | Meaning |
| --- | --- |
| `ready` | Authentication valid and schedulable capacity available |
| `busy` | Account undergoing exclusive operation, auth refresh, or active request; scheduling evaluates remaining slots via `PER_ACCOUNT_CONCURRENCY` |
| `cooldown` | Account's global `*` cooldown is active; model `scope` cooldowns only affect candidate classification for matching requests |
| `auth_required` | Account-level authentication failure; re-login or credential renewal required |
| `unavailable` | Account cannot be used by current runtime |
| `disabled` | Account configuration disabled |

Authentication results carry `authGeneration` and `checkedAt`, applied only when account object, authGeneration, and chronological sequence match; successful results at the same timestamp take precedence over failures. Model success and cooldown persistence use independent `modelAccessGeneration` and `checked_at`; modelAccessGeneration increments on catalog changes, retaining verified state when success already exists at the same timestamp.

Model access scopes:

| Operation | Scope | Success and Failure Semantics |
| --- | --- | --- |
| Standard GenerateContent | `<modelID>` | Writes `verified` upon arrival of canonical `EventFinish`; Code 7 retains existing record |
| CountTokens | `count-tokens:<modelID>` | Success clears cooldown for this scope; model `verified` remains unchanged |
| Transcribe | `<modelID>` | Writes `verified` on non-empty text or segments; Code 7 retains existing record |
| Live text-only | `<modelID>` | Writes `verified` on setup success; each `SendText` initiates a qualification check, updated upon `turn_complete` |
| Live audio or image | `bidi-media:<modelID>` | `SendMedia` initiates a qualification check and updates media scope |
| Robotics | `bidi-media:<modelID>` | `SendText` initiates a qualification check, updated upon `turn_complete` |

`ModelAccessKey(scope, model)` removes the `models/` prefix; empty scope returns the canonical model ID, and non-empty scope returns `<scope>:<canonicalModelID>`. Bidi setup uses the account lease timestamp, while each qualifying turn within a session is assigned a strictly increasing attempt timestamp consumed by `turn_complete`. Standard streaming generation writes `verified` upon arrival of canonical `EventFinish`; prior text, reasoning, tool, usage, and initial events serve output and latency metrics, while mid-stream disconnections, cancellations, or errors preserve existing verification states.

Model catalog refresh runs concurrently across all enabled ready/busy accounts. Accounts returning errors or empty catalogs enter a pending ID set within the generation instance; each non-empty result immediately updates the shared catalog, account state, and warms additional workers during `RUNNING`. After the initial fan-out completes, a single 30-second ticker concurrently re-refreshes eligible pending accounts. Tasks in cooldown are retained until expiration; accounts requiring login, disabled, unavailable, or deleted exit the retry loop, re-syncing via account updates once re-authenticated or re-enabled. Failure logs record account and reason; successful recovery logs model count. `modelRevision` tracks account and configuration changes, ensuring the generation service validates application of the current revision before accepting traffic.

Authentication state contains persistent credentials and device-bound materials stored in a secure local directory. Commits, issues, CI logs, and standard logs use sanitized payloads, preserving field structures while replacing cookies, tokens, proofs, emails, account IDs, prompts, response bodies, and raw wire frames.

## 4. Go Protocol Layer, Public Endpoints, and Vue Management

Public endpoints uniformly consume the same real-time model catalog and canonical events:

| Protocol | Endpoints |
| --- | --- |
| OpenAI Chat | `GET /v1/models`, `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Gemini Interactions | `POST /v1beta/interactions`, `POST /v1/interactions` |
| OpenAI Files | `POST /v1/files`, `GET/DELETE /v1/files/{file}`, `GET /v1/files/{file}/content` |
| OpenAI Media | `POST /v1/images/generations`, `POST /v1/audio/speech`, `POST /v1/videos`, `GET /v1/videos/{id}`, `GET /v1/videos/{id}/content` |
| OpenAI Transcribe | `POST /v1/audio/transcriptions` |
| Anthropic | `POST /v1/messages`, `POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`, `GET /v1beta/models/{model}`, `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens`, `:predictLongRunning`, `GET /v1beta/operations/{id}` |
| Realtime | `GET /v1/live`, `GET /v1/robotics/stream` |

Management dashboard routes:

| Capability | Route |
| --- | --- |
| Health & Status | `GET /health`, `GET /api/status` |
| Models & Accounts | `GET /api/models`, `GET/POST /api/accounts`, `GET/POST /api/accounts/import/chrome`, `PUT/DELETE /api/accounts/{id}` |
| Login & Verification | `POST /api/accounts/{id}/login`, `POST /api/accounts/{id}/verify` |
| Generation Service | `POST /api/control/start`, `POST /api/control/stop` |
| Configuration | `GET /api/config` |
| Cooldowns & Requests | `GET /api/cooldowns`, `GET /api/requests`, `POST /api/requests/{id}/cancel` |
| Logs & Events | `DELETE /api/logs`, `GET /api/events` |

`/api` accepts loopback requests and performs same-origin validation when requests include an `Origin` header. `/v1` and `/v1beta` use public API key authorization and CORS headers.

`previous_response_id` in OpenAI Responses and `previous_interaction_id` in Gemini Interactions share an in-memory cache of up to 256 response nodes within the active service instance, used to reconstruct full context for subsequent turns; clients should re-submit complete context after service restarts. Account bindings for Drive files, Veo operations, and artifact files are persisted to `runtime-state.json`, allowing continued polling and downloads across restarts.

Adding upstream capabilities starts in `internal/aistudio`: encoding actual array slots, decoding server events, and projecting them to public protocols in `internal/api`. Model methods, contexts, output limits, tools, voices, image specifications, and video formats all derive from real-time `ListModels`.

`runtimeManager` implements the foundational `Service` interface as well as Video, File, Bidi, and Transcription extension interfaces. `internal/app` handles resource-account bindings, cross-account file replication, runtime state, and retry workflows; `internal/api` manages HTTP, SSE, and WebSocket DTOs (the request, response, and event objects used by public protocols). For full request fields, response DTOs, SSE, and WebSocket events, see [protocol.md](protocol.md).

Frontend development commands:

```powershell
cd web
npm run dev
npm run typecheck
npm run lint
npm run format:check
npm run build
```

Vite writes production assets to `internal/webui/dist`. The management dashboard communicates via local `/api` routes to manage generation services, logs, accounts, configuration, model cooldowns, active requests, and SSE status events; authentication state and WAA objects are never stored in browser storage.

`internal/webui/embed.go` uses `//go:embed dist`, requiring built frontend assets before building Go binaries. The dashboard receives `status`, `models`, `accounts`, `log`, `cooldowns`, and `request` events from `/api/events`.

The API playground consumes SSE via `eventsource-parser`, completing requests on respective protocol finish events; mid-stream errors and premature disconnections are displayed as failures. Flushed errors on headers, bodies, and heartbeats propagate along the HTTP write path, and event forwarding releases account leases upon cancellation. The playground executes one request at a time, allowing subsequent submissions once stopped.

## 5. Protocol Implementation Workflow

New upstream capabilities are implemented in the following order:

1. Define canonical request, response types, and model capabilities in `internal/aistudio`
2. Encode corresponding JSON+protobuf arrays documented in [protocol.md](protocol.md)
3. Decode network increments into canonical events
4. Project into OpenAI, Responses, Anthropic, and Gemini protocols in `internal/api`
5. Bind structured state and management controls to the dashboard

`internal/api` consumes canonical requests and events, while `internal/aistudio` manages account files, WAA runtimes, raw upstream arrays, and real-time model capabilities. Resource-based operations record the creating account ID, routing subsequent polling, downloads, and prompt references to that account.

Recognized fields validate types and oneof constraints. Unknown non-empty slots are preserved as provider events or raw extension fields; uninterpretable consumed fields, missing finish frames, and invalid media payloads return structured protocol errors.

## 6. Build and Contribution

Build frontend assets before compiling the release binary:

```powershell
cd web
npm ci
npm run build
cd ..
go build -trimpath -o aistudio2api.exe ./cmd/aistudio2api
```

Run functional acceptance checks, frontend verification, and Go static analysis prior to submission:

```powershell
go vet ./...
```

Windows release archives include `aistudio2api.exe` and `start.bat`; other platforms use the same Go binary. Camoufox is prepared automatically on first startup; when `WAA_BACKEND=go`, it is prepared on first browser login. Contributions should focus on single features or protocol updates, using sanitized request and response examples.

GitHub Actions runs frontend linting, typechecking, building, and minimum-Go-version checks on `main` commits and Pull Requests. Release packages are compiled with current stable Go across Windows amd64, Linux amd64/arm64, and macOS amd64/arm64. Pushing a `v*` tag triggers automated Release creation with attached binaries, startup scripts, example configs, and documentation; tags containing `-` are published as pre-releases. Regular build artifacts are retained for 7 days in Actions, while Release assets are preserved indefinitely.

Source contributions include protocol implementations, frontend source code, and public documentation. Local account states, cookies, tokens, proofs, prompt bodies, response bodies, and runtime artifacts must remain local.
