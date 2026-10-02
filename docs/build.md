# Build Channel

AI Studio's Build application invokes MakerSuite proxy RPCs via the official host page to access the Gemini API, utilizing a quota pool calculated independently from Playground. AIStudio2API treats Build as a generation channel equivalent to Playground: the same account holds two distinct quota pools, and generation requests are scheduled across combinations of accounts and channels. This document defines proxy RPC wire formats, request headers and WAA, model catalogs and eligibility, scheduling and cooldowns, field-by-field mapping of Gemini API JSON, response decoding, error and quota handling, as well as channel configuration and observability. For WAA proof generation, see [WAA Implementation](waa.md); for Playground `GenerateContent`, see [Protocol Specification](protocol.md).

## 1. Official Mechanics & Channel Configuration

The Build application runs within a blob sandbox iframe under `*.scf.usercontent.goog`. The host page sends a `bootstrap` message and a `MessagePort` to the iframe. A shim inside the iframe intercepts `fetch` calls matching `https://generativelanguage.googleapis.com/.*`, `https://ai.studio/.*`, and `applet:.*`, transmitting `fetch`, `websocket_open`, `get_host_url`, `get_model_quota`, and other messages across the port. When users choose a paid API key, the host page connects directly to the Gemini API carrying that key; AIStudio2API implements the keyless MakerSuite proxy branch.

Before forwarding, the host page requires `navigator.userActivation.hasBeenActive` to be true, along with at least one of: 30 cumulative trusted `mousemove` events, 5 trusted `keydown` events, or running on a mobile device. When unfulfilled, the iframe's `fetch` hangs in a pending state; this gate exists solely within the page bridge layer, and proxy RPC fields and headers carry no activation tokens. The service sends proxy RPCs directly via account workers, treating the RPC wire format as its implementation boundary without replicating the iframe's user interaction gating.

| Gemini API Request | Proxy RPC |
| --- | --- |
| `:streamGenerateContent` | `ProxyStreamedCall` |
| Other methods | `ProxyUnaryCall` |
| Files & cache uploads | `ProxyUnaryFileApiCall` |
| Live WebSocket | Host page WebChannel forwarding |

The service implements generation and model catalogs over `ProxyStreamedCall` and `ProxyUnaryCall`. Handling of `ProxyUnaryFileApiCall` and Live bridging is described in "Capability Routing & Boundaries".

### Configuration & Display

`UPSTREAM_CHANNELS` lists the enabled generation channels:

| Value | Channel |
| --- | --- |
| `playground` | Official Playground `GenerateContent` |
| `build` | Gemini API calls proxied via Build application |

- Default value: `playground,build`; comma-separated, trimmed, and lowercased; at least one channel required, duplicates forbidden; invalid values fail configuration validation.
- List order defines channel priority within the same account.
- Configured via `.env`, the "Upstream Channels" setting in the management dashboard (must retain at least one, saved in `playground,build` order), or `PUT /api/config` with `upstream_channels`; saved values take effect on next generation service startup.

`BUILD_NATIVE_NONSTREAM=true` is the default: non-streaming requests across Gemini, Chat, Responses, Anthropic, Interactions, image, and audio endpoints prioritize eligible Build channels executing `ProxyUnaryCall` with `:generateContent`. When that channel is disabled, the model is unsupported, or quota is in cooldown, requests schedule to remaining available channels; file references and dedicated capabilities route to Playground. Disabling this option schedules by original channel order, though non-streaming requests selecting Build still execute unary calls.

Playground `GenerateContent` responses consist of repeated streaming frames converted into canonical non-streaming responses after complete accumulation. Upstream call logs record actual channel, `native` / `stream` mode, and RPC; when non-streaming requests fall back to streaming, a WARN log records "fallback streaming" and the reason. When Build explicitly returns that unary calls are unsupported, it falls back to `ProxyStreamedCall` on that channel; parameter errors return the original error.

Channel visibility across the service:

| Location | Field |
| --- | --- |
| `GET /v1/models` (OpenAI format), `GET /v1beta/models` (Gemini format), admin `GET /api/models`, and management UI model list | Model object `channels`, listing channels where at least one enabled account can invoke the model |
| `GET /api/requests` and management UI request list | Current attempted `channel` |
| Request log `request.channel` and management UI logs | Channel actually utilized |
| `GET /api/cooldowns` and management UI cooldown list | `channel` and `model_id` without the `build:` prefix |

## 2. Proxy RPC & WAA

Both RPCs reside on MakerSuiteService:

```text
https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/ProxyStreamedCall
https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/ProxyUnaryCall
```

### Request Structure

| Protobuf Field | Content |
| ---: | --- |
| 1 | Gemini API path |
| 2 | Request body JSON string; for GET, JSON object of query parameters |
| 3 | WAA proof; `null` for model catalog requests |
| 4 | HTTP method, only used in `ProxyUnaryCall` |

```json
["/v1beta/models/<MODEL_ID>:streamGenerateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>"]
["/v1beta/models/<MODEL_ID>:generateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>", "POST"]
```

Non-streaming requests, as well as models where AccessModes is non-empty and Free tier is excluded, route through `ProxyUnaryCall` with `:generateContent`; remaining streaming requests route through `ProxyStreamedCall` with `:streamGenerateContent`.

### Request Headers

Proxy RPC headers match the set, order, and values of Playground text `GenerateContent`: `content-type`, `x-goog-api-key`, `x-goog-authuser`, `x-user-agent`, `x-aistudio-visit-id`, `x-goog-ext-519733851-bin`, `authorization`, and account cookies. Playground image requests omit `x-goog-ext-519733851-bin`, whereas Build proxy image generation requests still carry it. Benefit tier header `X-AIStudio-G1-Tier` is only sent with `ProxyUnaryCall` (`TIER1` for Pro, `TIER2` for Ultra, `TIER0` for Plus, omitted for Free); `ProxyStreamedCall` omits this header. Models requiring subscription tiers return HTTP 403 Code 7 when invoked via `ProxyStreamedCall`, so such models are permanently routed to `ProxyUnaryCall`.

### WAA Proof

The proof resides in field 3, generated by the account's WAA Worker sharing the same VM as Playground. The binding string joins field 1 and field 2 with a single space:

```text
/v1beta/models/<MODEL_ID>:streamGenerateContent {"contents":[...],"generationConfig":{...}}
```

The digest is the lowercase hexadecimal SHA-256 hash of the binding string. Protected requests are dispatched by the Worker: via native page `fetch` for Camoufox backend, or via account-pinned Go HTTP transport for pure-Go backend.

### Response Structure

A successful `ProxyUnaryCall` response is a ProxyResponse:

```json
["<GenerateContentResponse JSON>"]
```

`ProxyStreamedCall` response field 1 contains repeated ProxyResponses; mid-stream errors append a google.rpc status `[code, message]` after the response list:

```json
[[["<GenerateContentResponse JSON>"], ["<GenerateContentResponse JSON>"]]]
[[["<GenerateContentResponse JSON>"]], [8, "<MESSAGE>"]]
```

In a ProxyResponse, index `0` contains the JSON string body, and index `2` contains Base64 encoded byte body (one of the two). Each body represents a complete Gemini API `GenerateContentResponse`. Streaming decoders parse each element as soon as a complete element appears in the response array.

## 3. Models, Eligibility, and Scheduling

### Build Catalog

During account catalog synchronization, after Playground `ListModels` succeeds, Build-enabled accounts query the Gemini API model list via `ProxyUnaryCall`:

```json
["/v1beta/models", "{\"pageSize\":\"200\"}", null, "GET"]
```

When `nextPageToken` is present, it is added to `pageToken` in the query object to fetch subsequent pages (up to 10 pages). Each model entry maps to:

| Gemini API Field | Catalog Field |
| --- | --- |
| `name` | Model ID without `models/` prefix |
| `displayName`, `description` | Display name and description |
| `inputTokenLimit`, `outputTokenLimit` | Input and output token limits |
| `supportedGenerationMethods` | Generation methods; presence of `generateContent` sets chat capability |
| `thinking` | Thinking capability |

The Build catalog is kept in account memory and is not persisted to `runtime-state.json`. If fetching fails, that account's catalog sync round is marked as failed, entering the retry set on a 30-second cycle.

### Channel Eligibility

An account's Build channel can serve requests for model M when all of the following conditions are met:

1. Standard generation: method is `generateContent`, without capability constraints, resource bindings, dedicated scopes, or Playground-specific requirements.
2. `UPSTREAM_CHANNELS` includes `build`.
3. Account's Build catalog contains M with `generateContent` in `supportedGenerationMethods`.
4. When M appears in any account's Playground catalog: M is neither an Interactions nor a transcription model, and the account's tier satisfies M's AccessModes.
5. When M appears exclusively in the Build catalog: M is not a Computer Use-only model.

The following requests exclusively route through Playground: CountTokens, Live & Robotics, Veo, transcription, Interactions models, generations referencing Drive files, and TTS with multi-speaker `mode`. Embedding, `aqa`, and models supporting only real-time methods do not support `generateContent` and are excluded from Build.

### Public Catalog

The public catalog represents the union of enabled channels. Models unique to Build that can be generated by at least one enabled account are added to the public catalog. When a model appears in Playground's catalog, Build requests use Playground's default parameters and capabilities; models unique to Build default output limits to `outputTokenLimit` and thinking capabilities to `thinking`. Each model's `channels` field lists available invocation channels.

### Scheduling & Cooldowns

Candidate scheduling units are combinations of account and channel, ordered by ascending account ID and channel order in `UPSTREAM_CHANNELS`. Scheduling prioritizes accounts with existing warm workers before starting standby workers on-demand (see [Development & Contributing](development.md)).

| Strategy | Combination Traversal |
| --- | --- |
| `round-robin` | Tracks last selected account and channel per model, resuming from subsequent combination |
| `fill-first` | Always starts from first available combination |

- Concurrency slots, workers, and WAA are shared per account; both channels consume the same pool of request slots.
- Cooldowns are tracked per channel: `<model>` for Playground, `build:<model>` for Build; global `*` cooldowns apply to both channels.
- An account is only considered in cooldown for a model once all supported channels for that model enter cooldown.
- When a channel returns a quota error, cooldown is recorded for that specific channel; if another channel on the same account is available, the request retries on that channel without incrementing the tried-accounts count.
- When all candidate combinations enter cooldown, requests with recovery times within 1 minute queue waiting; requests with later recovery return HTTP 429 `rate_limit_exceeded` indicating the earliest recovery time.
- Successful generation writes `verified` to that channel's scope (`<model>` or `build:<model>`).

### Attachments & File References

| Input | Build | Playground |
| --- | --- | --- |
| Inline attachment | Written directly to `inlineData` | Uploaded to generating account's Drive, referenced by file ID |
| YouTube link | `fileData` | External media Part |
| Drive file reference | Not accepted | Routes to owning account's Playground |

Generations with Drive file references strictly use Playground. If the owning account lacks schedulable Playground candidates or cooldown expires >1 minute away, the file is temporarily copied to another account for Playground generation and cleaned up afterward.

## 4. Request Mapping

Build requests share the same preprocessing pipeline as Playground: tool validation, model media defaults, TTS script parsing, parameter validation, and catalog defaults. Stop sequences are not sent upstream but truncated locally (see "Stop Sequences, Usage, and Termination"). Top-level request fields:

| Field | Value |
| --- | --- |
| `contents` | Canonical contents array |
| `systemInstruction` | `{"parts":[{"text":"<SYSTEM>"}]}` when system prompt is non-empty |
| `tools` | Tool declarations |
| `toolConfig` | `{"includeServerSideToolInvocations":true}` when declaring both functions and Google tools |
| `generationConfig` | Generation configuration parameters |
| `safetySettings` | `OFF` for harassment, hate speech, sexual content, and dangerous content; omitted for image models |

```json
{
  "contents": [{"role": "user", "parts": [{"text": "Reply OK"}]}],
  "systemInstruction": {"parts": [{"text": "You are a diagnostic assistant"}]},
  "generationConfig": {
    "maxOutputTokens": 512,
    "temperature": 0.2,
    "thinkingConfig": {"includeThoughts": true, "thinkingLevel": "LOW"}
  },
  "safetySettings": [
    {"category": "HARM_CATEGORY_HARASSMENT", "threshold": "OFF"},
    {"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "OFF"},
    {"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "OFF"},
    {"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "OFF"}
  ]
}
```

### contents

Roles `user` and `tool` map to `user`; `assistant` maps to `model`; contents without parts are omitted. YouTube URLs in user text convert to external media parts.

| Canonical Part | Gemini API Part |
| --- | --- |
| text | `text`; with speaker/style includes `speechMetadata {speaker, style}`; thinking text includes `thought: true` |
| inline data | `inlineData {mimeType, data}`, data as standard Base64 |
| external media | `fileData {mimeType, fileUri}` |
| Drive file | Returns parameter error |
| function call | `functionCall {name, args, id?}` |
| function result | `functionResponse {name, response, id?}` |
| executable code | `executableCode {language, code}` |
| code execution result | `codeExecutionResult {outcome, output?}` |
| signature-only Part | `text: ""` |

- Thought signatures write to Part `thoughtSignature`; function calls without signatures set `skip_thought_signature_validator`.
- When function results lack names, call IDs correlate to unfulfilled calls in the current turn; if unmatched and exactly one call remains, its name is used.
- Function arguments and results as JSON objects write directly; other JSON values wrap in `{"result":<VALUE>}`.
- If code execution outcome is not `OUTCOME_OK`, `output` contains the error text.

### tools

| Tool | Gemini API Representation |
| --- | --- |
| Function declarations | `{"functionDeclarations":[{"name","description?","parametersJsonSchema?"}]}`, sending JSON Schema directly |
| Google Search, Image Search | `{"googleSearch":{...}}`; Image Search adds `searchTypes {imageSearch, webSearch?}`; time ranges set `timeRangeFilter {startTime, endTime}` (RFC 3339 UTC) |
| Code Execution | `{"codeExecution":{}}` |
| URL Context | `{"urlContext":{}}` |
| Google Maps | `{"googleMaps":{}}` |

Tool choice defaults or `auto` emit tool declarations; `none` omits `tools`; other values return parameter errors.

### generationConfig

| Field | Value |
| --- | --- |
| `maxOutputTokens` | Request value or catalog default, validated against model limit; omitted when speechConfig is present and not explicitly set |
| `temperature`, `topP`, `topK`, `seed` | Request value or catalog default |
| `responseMimeType` | Request value |
| `responseSchema` | Reuses Playground Schema validation and normalization, converted to protobuf JSON: nested `type` uppercase enum, string `const` to `enum`, null unions to `nullable` |
| `responseModalities` | Uppercase modalities; image models append `IMAGE`, `TEXT`; TTS and music models append `AUDIO` |
| `imageConfig` | `{aspectRatio?, imageSize?}`; image models with resolution support default to `1K` |
| `speechConfig` | Single voice: `voiceConfig.prebuiltVoiceConfig.voiceName`; multi-speaker: `multiSpeakerVoiceConfig.speakerVoiceConfigs[{speaker, voiceConfig}]` |
| `thinkingConfig` | Sent when supported: `includeThoughts: true`, `thinkingBudget`, `thinkingLevel` |

Thinking levels map from Playground enums: Low=`LOW`, Medium=`MEDIUM`, High=`HIGH`, Minimal=`MINIMAL`. Conversion rules between `reasoning_effort`, token budgets, and levels match Playground.

When TTS models support `speech_metadata`, `Speaker: text` scripts are split into segments with `speechMetadata`; other TTS models fold speaker and style back into text. Build requests only include `speakerVoiceConfigs` in `multiSpeakerVoiceConfig`; requests with `mode` route to Playground.

Playground internal fields are omitted from Build requests: account timezone, fixed slots in GenerateContent and generation config, and default tool slots for image resolution. `candidateCount != 1` and logprobs are rejected by public adapters.

## 5. Responses, Termination, and Usage

Each `GenerateContentResponse` decodes into canonical events:

| Field | Event |
| --- | --- |
| `text` (`thought` false) | text |
| `text` (`thought: true`) | reasoning |
| `inlineData` (`thought` false) | media, Base64 decoded to raw bytes |
| `executableCode` | executable code |
| `codeExecutionResult` | code execution result; `output` contains output on `OUTCOME_OK`, error otherwise |
| `functionCall` | tool call, defaulting `args` to `{}` if missing |
| Part with only `thoughtSignature` | thought signature |
| `citationMetadata.citationSources` | citation (`uri`, `title`, `startIndex`, `endIndex`) |
| `groundingMetadata` | grounding |
| `usageMetadata` | usage |
| `finishReason` | usage and finish |

- Part `thoughtSignature` attaches to events generated by that Part.
- `inlineData` with `thought: true` represents thinking sketches during image generation and is discarded from output media.
- `groundingMetadata` parses `webSearchQueries`, `searchEntryPoint {renderedContent, sdkBlob}`, `groundingChunks` (`web`, `retrievedContext`, `maps` with `uri`, `title`, `text`, `placeId`), `groundingSupports` (`partIndex`, `startIndex`, `endIndex`, `text`, `groundingChunkIndices`, `confidenceScores`), and `googleMapsWidgetContextToken`.
- When `usageMetadata.totalTokenCount > 0`: `promptTokenCount` is input, `candidatesTokenCount` is output, `thoughtsTokenCount` is thinking, and `toolUsePromptTokenCount` is tools; missing `candidatesTokenCount` calculates output as total minus the other three.
- On first arrival of `finishReason`, latest usage emits before finish; termination reason is the lowercase enum without `FINISH_REASON_` prefix, matching canonical Playground reasons (`UNSPECIFIED` becomes `unspecified`).
- When candidates are absent and `promptFeedback.blockReason` is present, returns prompt feedback error with lowercase enum without `BLOCK_REASON_` prefix.
- Protocol errors return when candidates count != 1, body is invalid JSON, Base64 decoding fails, or stream terminates without `finishReason`.

### Stop Sequences, Usage, and Termination

Event streams in Build and Playground follow unified processing:

1. Stop sequences are not sent upstream; matched locally within text events, truncating text and closing upstream stream upon hit with finish reason `stop_sequence`.
2. Requests with stop sequences invoke Playground `CountTokens` on the same account in parallel; on match, usage is constructed from counted input tokens plus emitted output tokens; omitted if counting fails.
3. When upstream omits usage, input, tools, thinking, and output tokens are estimated locally.
4. Usage is emitted before finish.

## 6. Errors, Quotas, and Capability Boundaries

Non-200 HTTP responses decode into protocol errors preserving HTTP status, google.rpc code, message, and `ErrorInfo` metadata. Error bodies have two shapes:

```json
[7, "The caller does not have permission"]
[null, [7, "The caller does not have permission"]]
```

In `ProxyStreamedCall` trailing status, code 0 denotes success; other codes map to HTTP statuses:

| Code | HTTP Status |
| ---: | ---: |
| 3, 9 | 400 |
| 4 | 504 |
| 5 | 404 |
| 7 | 403 |
| 8 | 429 |
| 13 | 500 |
| 14 | 503 |
| 16 | 401 |
| Other | 502 |

- HTTP 429 parses metadata or error message to identify minute vs daily quotas: minute limits cool down until window reset, daily limits cool down until next quota reset day; model-specific limits record `build:<model>`, while global limits record account `*`.
- Trailing stream errors follow mapped HTTP statuses; errors (401, 403, 404, 429, 5xx) occurring before the first upstream semantic event trigger account rotation, channel rotation, and cooldowns, while later errors terminate with in-stream errors.
- HTTP 403 Code 7 preserves account and model eligibility, switching requests to another account.
- Non-existent models return 404 Code 5; Computer Use-only models return 400 Code 3 on text-only requests and are excluded from Build.

### Capability Routing & Boundaries

| Capability | Current Handling |
| --- | --- |
| Live WebSocket | Uses Playground `BidiGenerateContent` |
| Files & cache uploads (`ProxyUnaryFileApiCall`) | Not implemented; attachments sent as `inlineData` |
| Embedding | Upstream `batchEmbedContents` returns 200, `embedContent` returns 404; unexposed |
| Interactions (`/v1beta/interactions` returns 404) | Uses Playground `CreateInteractionStream` |
| Subscription image models on Free accounts | Upstream returns 403; excluded via AccessModes |
| Direct connection with paid API key | Not utilized |

## 7. Implementation Architecture

| Path | Responsibility |
| --- | --- |
| `internal/config/config.go` | `UPSTREAM_CHANNELS` parsing and validation |
| `internal/app/channels.go` | Configuration to scheduling channel pool mapping |
| `internal/aistudio/channel.go` | Channel definitions, cooldown scopes, eligibility, combination sorting, and public `channels` |
| `internal/aistudio/accounts.go` | Candidate classification, lease channels, all-channel cooldown handling |
| `internal/aistudio/build.go` | Request encoding, proxy wire format, binding strings, response/trailer decoding, Build catalog |
| `internal/aistudio/generate.go` | Channel dispatching, shared preprocessing, stop sequences, and usage calculation |
| `internal/aistudio/service.go` | Protected transmission and Build proof field insertion |
| `internal/aistudio/upload.go` | Inline attachment handling for Build channel |
| `internal/app/runtime.go` | Retries, cooldown persistence, intra-account channel switching, file replication |
| `internal/app/admin.go`, `internal/api` | Channel fields in cooldowns, requests, logs, and model catalogs |
| `web/src` | Channel selection in settings and channel badges across UI lists |

### Adding Capabilities to Build

When adding a Build proxy method or routing an existing capability through Build, implement in the following order:

1. In `internal/aistudio/build.go`, define the Gemini API path, HTTP method, JSON request body, and `ProxyStreamedCall` / `ProxyUnaryCall` wrappers; binding strings must always derive from the final path and final JSON payload to prevent post-encoding mismatches.
2. In `internal/aistudio/channel.go`, declare candidate eligibility and Playground affinity conditions; file ownership, dedicated scopes, model methods, and benefit tiers must be resolved before acquiring a lease.
3. In `internal/aistudio/generate.go`, reuse canonical request preprocessing, mapping only public Gemini API fields for Build encoders while retaining Playground-specific slots in Playground encoders.
4. In `DecodeBuildStream` or `DecodeBuildUnary`, convert complete Gemini API responses into canonical events; streaming wrappers must segment along complete ProxyResponse boundaries without guessing JSON boundaries from network chunks.
5. Map errors to unified HTTP statuses, retry phases, and channel cooldown scopes; errors occurring after the first semantic event must terminate as in-stream error events.
6. Expose model `channels`, request `channel`, and cooldown `channel` through management APIs and the UI to ensure routing transparency.

### Minimal Acceptance Verification

| Scope | Required Outcome |
| --- | --- |
| Channel Selection | When `playground`, `build` are enabled independently or together, catalogs and routing reflect configuration |
| Quota Isolation | When one channel enters model cooldown, another channel on the same account can still serve requests; global cooldown applies to both |
| Protocol Consistency | Non-streaming and streaming payloads and termination semantics match across Chat, Responses, Anthropic, and Gemini |
| Content Fidelity | System prompts, multi-turn dialogs, functions, Google tools, image inputs, TTS, and image outputs encode according to model capabilities |
| Lifecycle | Cancellations, client disconnects, stop sequences, and trailing errors release leases and concurrency slots |
| Observability | Model catalogs, request lists, logs, and cooldown views consistently report the actual channel used |

When debugging proxy payloads, capture the final Gemini API JSON, outer array, and decoded google.rpc status. HTTP 200 merely indicates proxy RPC arrival; operational success requires receiving expected content, valid usage, and a single terminal state.
