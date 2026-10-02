# AI Studio Private Protocol Engineering Guide

The AI Studio web interface integrates authentication, JSON+protobuf RPCs, Web Application Attestation (WAA) content proofs, and WebChannel real-time protocols into a unified pipeline. This document explains official web page capture, single-variable field isolation, wire format reconstruction, canonical event decoding, and public API adaptation. For wire schemas and full field mappings, see [Protocol Specification](protocol.md).

## 1. Protocol Layering & Replication Methodology

The implementation is structured into strict functional layers. Upstream changes are absorbed by the corresponding layer while remaining layers continue operating with existing request, response, and state models.

| Layer | Inputs & State | Implementation Path | Output Format |
| --- | --- | --- | --- |
| Authentication | Cookies, SAPISID, DBSC, Authorization, dynamic headers | `internal/chromeauth`, `internal/aistudio/auth.go`, `internal/aistudio/transport_http.go` | Transmittable account credentials and request headers |
| Tiers & Catalog | BenefitTier, ListModels, AccessModes, methods, and capability flags | `internal/aistudio/benefit.go`, `internal/aistudio/models.go` | Per-account model catalogs, benefit tiers, and capability metadata |
| WAA | Official high-level snapshot service or pure-Go BotGuard VM, binding prompt digest, proof | `internal/camoufoxnative`, `internal/waa`, `internal/aistudio/runtime_native.go`, `internal/aistudio/runtime_go.go` | Fresh proof bound to the SHA-256 digest of the binding prompt |
| Request Encoding | Sparse JSON+protobuf arrays, media, and tool fields | `internal/aistudio/generate.go`, `internal/aistudio/tools.go` | MakerSuite wire request bodies |
| Transport | HTTP, SSE, WebChannel, cookie persistence | `internal/aistudio/transport_http.go`, `internal/aistudio/webchannel.go` | Raw incremental chunks / frames |
| Canonical Events | Text, reasoning, tool, media, usage, finish, error | `internal/aistudio/event.go`, `internal/aistudio/bidi.go` | Protocol-agnostic internal event stream |
| Public Adaptation | OpenAI, Responses, Anthropic, Gemini | `internal/api` | Client responses and termination semantics |

Field isolation and reproduction follows this workflow:

1. Trigger a minimal action on the official page, capturing the full request, response, response headers, and chronological event sequence.
2. Repeat the action with identical account, model, and inputs to differentiate stable protocol fields from ephemeral random identifiers.
3. Modify exactly one input variable, comparing array slot indices, dynamic headers, cookies, and completion frames.
4. Reconstruct the request payload in isolation, verifying that upstream servers accept the independently encoded body.
5. Decode upstream wire frames into canonical events, followed by adaptation into public client protocols.
6. Verify success, error, cancellation, and termination branches across public endpoints.

Always preserve raw bytes alongside decoded structures during comparison. Compression, transcoding, or pretty-printing obscures empty slots, trailing fields, omitted defaults, and raw network chunk boundaries.

A minimal diagnostic capture records action, account tier, model, raw transport bytes, and single-variable diffs in structured JSON:

```json
{
  "scenario": "generation-temperature",
  "action": "click_run",
  "account_tier": "Free",
  "model": "gemini-3.7-flash",
  "request": {
    "method": "POST",
    "url": "https://<upstream-rpc>",
    "ordered_headers": [["content-type", "application/json+protobuf"], ["authorization", "<raw-value>"]],
    "raw_body_base64": "<base64>"
  },
  "response": {
    "status": 200,
    "ordered_headers": [["content-type", "application/json+protobuf"]],
    "chunks": [{"t_ms": 0, "raw_base64": "<base64>"}]
  },
  "expected": {"temperature": 0.2},
  "observed": {"json_path": "$[3][4]", "value": 0.2},
  "control_variant_diff": [{"json_path": "$[3][4]", "control": 1.0, "variant": 0.2}]
}
```

`ordered_headers` preserves browser header order and duplicate keys; `raw_body_base64` and chunk `raw_base64` retain verbatim bytes. `expected` describes the UI interaction, `observed` describes the wire outcome, and `control_variant_diff` isolates classified differences between control and test runs.

## 2. Sparse JSON+Protobuf Encoding

MakerSuite serializes protobuf messages as JSON arrays. Protobuf field `N` corresponds to JSON index `N-1`. Intermediate unset fields are padded with `null`, while trailing default-valued fields may be omitted entirely.

The following shapes carry distinct semantics:

```json
[]
[null]
[0]
```

`GetAiStudioBenefitTier` treats all three variations as Free. For other enums or booleans, distinguish absent, `null`, zero values, and non-zero values before determining default semantics.

### Field Extraction

The parser preserves the raw root JSON and accesses elements by field number. Missing trailing fields return absent, explicit `null` returns null, and type mismatches return structured protocol errors with method names and JSON paths.

The following pattern illustrates mapping from protobuf field number to array index (implemented via `rawAt` and typed helpers in `internal/aistudio`):

```go
// WireField retrieves a value from a sparse array using its protobuf field number
func WireField(values []json.RawMessage, field int) (json.RawMessage, bool) {
	index := field - 1
	if index < 0 || index >= len(values) {
		return nil, false
	}
	return values[index], true
}
```

Parsing follows a fixed sequence:

1. Validate whether the current node is an array, object, string, number, or boolean.
2. Differentiate absent, `null`, and default values on recognized fields.
3. Permit subsequent unrecognized fields in root and nested messages.
4. Preserve unrecognized non-empty elements as provider events or raw extension fields.
5. Enforce strict type checking and oneof constraints on consumed fields.

### Single-Variable Field Isolation

For full GenerateContent, Content, Part, and GenerationConfig field definitions, see [Protocol Specification](protocol.md). When isolating newly observed fields, compare a baseline payload against a single-variable variant:

```json
{
  "baseline": [
    "models/gemini-3.7-flash",
    [[[[null, "Reply OK"]], "user"]],
    null,
    [null, null, null, 512, 1.0, 0.95, 64],
    "!FRESH_PROOF"
  ],
  "variant": [
    "models/gemini-3.7-flash",
    [[[[null, "Reply OK"]], "user"]],
    null,
    [null, null, null, 512, 0.2, 0.95, 64],
    "!FRESH_PROOF"
  ]
}
```

The test request modifies only the temperature setting. The stable diff occurs at root field 4, GenerationConfig field 5 (JSON path `$[3][4]`). Fresh proofs, visit IDs, timestamps, cookies, and request IDs are categorized as known dynamic fields and isolated before evaluating business field deltas.

Single-variable verification criteria:

1. Identical request method, URL, model, account tier, and session state.
2. Identical input prompts and unchanged UI parameters.
3. Array length modifications mapped back to field numbers, evaluating absent vs `null` vs default semantics.
4. Dynamic authentication and WAA proofs categorized independently.
5. Variable field successfully and predictably alters server behavior in standalone requests.

### Incremental Response Decoding

`GenerateContent` returns an incrementally growing JSON root array where `$[0]` contains repeated frames. Network chunks represent byte-level transport boundaries, whereas protocol frames constitute logical decoding units.

| JSON Path | Content |
| --- | --- |
| `$[0][frame][0]` | Candidates |
| `$[0][frame][0][0][0]` | Candidate content |
| `$[0][frame][0][0][1]` | Finish reason |
| `$[0][frame][0][0][6]` | Citations |
| `$[0][frame][0][0][7]` | Grounding metadata |
| `$[0][frame][2]` | Usage statistics |
| `$[0][frame][7]` | Response ID |

A terminal frame may contain solely `[null,"model"]`, usage, and finish reason. Decoders emit events immediately once a repeated frame is complete, confirming receipt of finish semantics when the root array closes.

Known finish reasons map to canonical termination reasons. Unrecognized integers are preserved as `provider_<code>`, allowing public adapters to return both compliant standard termination fields and raw provider codes.

## 3. WAA Runtime

The execution pipeline for protected RPCs proceeds as follows. When `WAA_BACKEND=camoufox`, Waa/Create, interpreter downloading, dynamic program execution, Host/Realm instantiation, and persistent state management run in the official page, and the application hooks into the high-level snapshot service. When `WAA_BACKEND=go`, these stages execute in-process via Goja and a simulated Firefox 152 host environment. For implementation details, see [WAA Implementation](waa.md):

```text
Waa/Create
  -> decode challenge
  -> load interpreter by hash
  -> execute dynamic program
  -> install browser host and iframe realm
  -> initialize persistent snapshot state
  -> SHA-256(binding prompt)
  -> snapshot({TYb:{content:digest}})
  -> fresh proof
  -> write proof field
  -> send protected RPC
```

### Challenge Mechanics

In `Waa/Create`, request field 1 is the request key `lmnUSbltwc5ULv48iKLX`; fields 2 and 3 carry the current VM's interpreter hash and un-bound snapshot. On worker launch, only field 1 is set: `["lmnUSbltwc5ULv48iKLX"]`. Once downloaded, the interpreter's unpadded Base64URL SHA-256 digest must match the challenge hash. A representative challenge payload comprises a 33,695-character program and a 65,831-byte interpreter.

Slot 2 of the `Waa/Create` response is a Base64 string. Adding `97` to each decoded byte yields the challenge array (see [WAA Implementation](waa.md) for field definitions).

Interpreters are cached by hash. Programs, message IDs, experimental state, and snapshot contexts remain scoped to the current challenge lifecycle. Global function names are resolved dynamically from the challenge. Initialization arguments, signal lists, and persistent state derivation rules are specified in [WAA Implementation](waa.md); official VM lifecycles operate on 43,200,000ms expiration timers with 300,000ms check intervals.

`Waa/Ping` wire requests use `[request_key, botguard_response]`, returning `[]` on success. Valid proofs, corrupted proofs, omitted proofs, arbitrary request keys, and cookie-less requests all return successful empty responses. Ping validates only WAA RPC consumer identity and field types; proof validity is enforced exclusively on operational GenerateContent, GenerateVideo, or Bidi requests.

### Host and Realm Simulation

The dynamic program interrogates the following browser host semantics. Under Camoufox, these are provided by a real Firefox instance; under pure-Go, they are provided by `internal/waa` through Firefox shape tables and a patched Goja runtime (see [WAA Implementation](waa.md)):

- `navigator`, `screen`, `window`, `document`, `location`, `performance`, and `timezone`
- `TextEncoder`, `TextDecoder`, Base64, TypedArray, ArrayBuffer, Blob, and URL APIs
- Promises, microtasks, timers, event loop semantics, and high-precision timing
- Isolated iframe globals, prototypes, constructors, `eval`, and Trusted Types behavior
- DOM property descriptors, native object stringification, getter side-effects, and key enumeration order
- Global and built-in object own-key iteration orders and lazy global property resolution timing
- Error prototypes, message formats, `Error.stack` layouts, and Date timezone formatting
- Prompt input `input` / `change` events and `/generate_204` beacon image requests
- Persistent state input passing, mutation writeback, and subsequent snapshot reads

Dispatch channels operate across three configurations:

| Pipeline | Prerequisites | Verifiable Properties |
| --- | --- | --- |
| Full Angular | Real keyboard input, framework events, Run action, official snapshot, official request | Baseline fidelity of official web application |
| Page Context | Same worker, same request body, isolated fetch | Transport comparison within browser context |
| Go HTTP | Same worker, same headers/body, Go HTTP transport | Equivalence between Go transport and browser dispatch |

The full Angular path represents native browser execution. Account eligibility, model availability, proof validity, and runtime readiness are determined by protected RPCs dispatched over this path; page context and Go HTTP pipelines verify transport equivalence. An initial Code 7 in Angular indicates server-side rejection of that call.

Transport behaviors:

| Scenario | Behavior |
| --- | --- |
| Successive calls on same worker with fresh proofs | Can transition from initial Code 7 to subsequent HTTP 200 |
| Multiple fresh workers on same model | First results may independently yield HTTP 200 or Code 7 |
| Same worker across different models | One model may return 200 while another returns Code 7 |
| Angular, Page Context, Go HTTP using same validated worker | All three pipelines yield HTTP 200 |
| Baseline Angular returns Code 7 | Page Context and Go HTTP reproduce identical Code 7 |
| Valid, corrupted, or empty Ping proofs | All return HTTP 200 `[]` |

Business availability of any transport pipeline is verified directly by upstream acceptance of protected RPCs. Proof lengths, program byte counts, latencies, Ping responses, `sD()` execution results, model names, and snapshot outputs are recorded for diagnostics.

### Snapshot Interface

WAA provides proofs to request encoders through `ProtectedPreparer`, defined in `internal/aistudio/service.go` and `internal/aistudio/waa.go`:

```go
// ProtectedPreparer injects a fresh WAA proof into a request and dispatches it via a fingerprinted browser
type ProtectedPreparer interface {
	Prepare(context.Context, ProtectedRequest) (PreparedProtectedRequest, error)
	BrowserStorageState(context.Context) (StorageState, error)
	SendProtected(context.Context, ProtectedRequest) (*RPCResponse, error)
}

// ProtectedRequest represents an RPC requiring WAA attestation
type ProtectedRequest struct {
	URL        string
	Headers    http.Header
	Body       []byte
	Prompt     string
	ProofField int
}
```

The proof field index is 5 for GenerateContent and CreateInteractionStream, 3 for Build proxy, 8 for Veo GenerateVideo, and 6 for Bidi setup and real-time inputs. The request encoder constructs the proof-less wire array and binding prompt; the WAA worker invokes snapshot serially and writes the resulting proof into place. Binding prompts for each RPC are detailed in [WAA Implementation](waa.md); the digest is the lowercase hexadecimal SHA-256 hash of the binding prompt.

Official snapshot invocations receive a four-slot argument array:

```javascript
[{content: digest}, undefined, undefined, undefined]
```

Outer object key names, array lengths, explicit `undefined` values, and missing slots are evaluated for strict VM behavioral parity.

### Worker Lifecycle State Machine

```text
starting -> bootstrapping -> ready -> busy -> ready
     |             |          |        |
     +-----------> failed <----+--------+
ready -> closing -> closed
```

Snapshot operations execute serially per account. Page crashes, snapshot errors, VM expirations, credential renewals, or process shutdowns transition the runtime to failed/closed. The scheduler tracks worker instances with an incrementing `generation` counter; rebuilding increments the generation, allowing stale errors from previous instances to be ignored while preserving new instance state.

Camoufox bootstrap captures the snapshot service and dynamic headers directly from the official page. The bootstrap GenerateContent request is cancelled during `network.beforeRequestSent` via `network.failRequest`, generating zero upstream tokens. Standard models on the same account share this snapshot service. Interactive login, cookie extraction, and native browser automation reside in `internal/camoufoxnative`.

Pure-Go bootstrap issues HTTP requests in-process to fetch page assets and `GetLoggingContext` headers, calls `Waa/Create`, loads interpreters by hash, executes programs in Goja, sets the bootstrap prompt, and dispatches interaction events; the VM refreshes every 12 hours following the official lifecycle. Implementation resides in `internal/waa` and `internal/aistudio/runtime_go.go`.

## 4. WebChannel, Live, and Robotics

Live and Robotics interactions communicate over the `BidiGenerateContent` WebChannel protocol, comprising four request types: handshake, forward POST, long-polling backchannel, and termination.

Session state machine:

```text
new -> handshaking -> ready -> reconnecting -> ready
                       |                        |
                       +------> closing <-------+
                                  |
                                closed
```

The handshake establishes both `SID` and `gsessionid`. Receiving `setup_complete` advances the session to ready. Network errors after establishing the initial backchannel transition to reconnecting; re-establishing the backchannel restores ready state. Explicit shutdowns transmit a termination request; protocol finishes and unrecoverable errors terminate network polling; both paths transition to closed once account leases are released.

### Handshake and Session Identifiers

The handshake query sets `VER=8`, a random `RID`, `CVER=22`, `X-HTTP-Session-Id=gsessionid`, dynamic auth headers, and `count=0`. Response headers supply `gsessionid`, and the first control frame provides `SID`:

```json
[[0, ["c", "<SID>", "", 8]]]
```

Subsequent forward POST requests carry:

```text
query: VER, gsessionid, SID, RID, AID, zx, t
form:  count=1, ofs=<OFFSET>, req0___data__=<JSON_PROTOBUF>
```

`RID` identifies forward requests and increments upon successful transmission. `ofs` tracks client message offsets and increments upon successful ACK. `AID` tracks consumed server envelope IDs, advancing with the first slot of received backchannel envelopes.

A WebChannel ACK is an array of three integers, validated by array length and integer types; the second integer is independent of local request sequences. Forcing ACK values into strict correlation with RID, AID, or ofs will trigger protocol validation errors on valid responses.

Commit sequences for RID, ofs, and AID:

```text
send(payload):
  lock sendMu
  snapshot rid, aid, ofs
  POST query(RID=rid, AID=aid) form(ofs=ofs, payload)
  require HTTP 200 and ACK=[int,int,int]
  commit rid=rid+1, ofs=ofs+1

consume_backchannel(envelope):
  require envelope=[serverAid,payload]
  decode payload events
  commit aid=max(aid,serverAid)
  emit payload events in order

reconnect_backchannel():
  keep SID, gsessionid and committed aid
  open RID=rpc with AID=aid
```

If HTTP transmission fails, ACK parsing fails, or cancellation occurs prior to commit, RID and ofs remain unchanged. AID is updated solely by successfully decoded server envelopes.

### Backchannel Mechanics

The backchannel performs long-polling using `RID=rpc` and the current committed `AID`. Wire frames use chunk-length framing formatted as decimal length, newline, and JSON bytes:

```text
<decimal-length><LF>
<json-bytes>
```

A JSON frame encapsulates one or more `[AID, payload]` envelopes. The payload may contain application arrays or status frames:

```json
{"__sm__":{"status":[[[7,"The caller does not have permission"]]]}}
```

Application payloads decode into setup, text, media, transcription, generation complete, turn complete, interrupted, session resumption, usage, go away, provider, closed, or error events. Unknown non-empty application slots are preserved as provider events maintaining chronological order.

Once established, transport errors on the backchannel trigger reconnections preserving the active `SID`, `gsessionid`, and `AID`. Handshake failures, protocol completion, explicit closure, or unrecoverable server errors terminate the logical session.

### Setup and Media Inputs

Setup configuration occupies outer field 7, defining model, generation parameters, resumption token, buffer configurations, and timezone. Live uses AUDIO output with Minimal thinking; Robotics uses TEXT output with High thinking. Public sessions transition to ready upon receiving `setup_complete`.

Text inputs reside in the realtime input text slot. Audio inputs use `audio/pcm`, image inputs use `image/jpeg`, and binary buffers are encoded in standard Base64. Media stream ends are signalled by dedicated finish frames.

When upstream issues a new session resumption token, it atomically replaces the previous token for that session. Resumption tokens maintain account affinity; new setup payloads specify the model and mode for the current session.

Live text-only interactions use the standard model scope; Live audio/image and Robotics use `bidi-media:<modelID>`. Turns that qualify model eligibility terminate with upstream error forwarding upon receiving Code 7.

## 5. Tiers, Catalogs, and Scheduling Priority

Candidate account sets derive from real-time model catalogs and account benefit tiers, prioritized by historical success:

```text
BenefitTier
  + ListModels.methods
  + ListModels.accessModes
  + model capability fields
  = account candidate set

successful model/scope history
  = candidate priority
```

For complete mappings between `GetAiStudioBenefitTier` field 1 and tier headers, see [Protocol Specification](protocol.md). Omitted values, `null`, and `0` map to Free, while Pro, Ultra, and Plus map to respective tiers. ListModels field 83 specifies access mode: `1` for paid API keys, `3` for Pro/Ultra subscriptions, and `4` for Ultra subscriptions. The Paid model flag represents model characteristics, while BenefitTier represents account entitlement; both are evaluated independently.

Catalog, tier, and history evaluation:

| In Catalog | Tier Permitted | Success History | Scheduling Outcome |
| --- | --- | --- | --- |
| No | Any | Any | Excluded |
| Yes | No | Any | Excluded |
| Yes | Yes | None | Eligible candidate |
| Yes | Yes | Yes | Prioritized candidate |
| Catalog / Tier changed | Recalculated | Stale records invalidated | Evaluated under new catalog |

Success history keys combine canonical model IDs (without `models/` prefix) and capability scopes. A `verified` status indicates confirmed successful execution on that model or capability:

| Operation | Scope | Verified Commit Point |
| --- | --- | --- |
| Standard Generation | `<modelID>` | Canonical `EventFinish` event |
| CountTokens | `count-tokens:<modelID>` | Does not write verified; clears scope cooldown |
| Transcribe | `<modelID>` | Non-empty text or segments |
| Veo GenerateVideo | `<modelID>` | Operation created successfully |
| Live Text | `<modelID>` | Setup complete; each text turn qualifies eligibility |
| Live Audio / Image | `bidi-media:<modelID>` | Media turn qualifies media eligibility |
| Robotics | `bidi-media:<modelID>` | Text turn qualifies model eligibility |

`model_access` stores `state` and `checked_at`, with `verified` as the sole success state. Code 7 preserves existing status; CountTokens updates `checked_at` under an isolated scope. Authentication failures are tracked at the account level.

Streaming generation text, reasoning, tool, usage, and initial events serve output and latency metrics; model verification commits on `EventFinish`. Premature disconnections, cancellations, and errors preserve existing verification records.

Scheduling selection sequence:

1. Filter candidate accounts by method, model, capabilities, AccessModes, and BenefitTier.
2. Select accounts with active warm workers and available concurrency slots, sorted by verified status, model TTFT samples, TTFT EWMA, free slots, and active loads.
3. If active workers lack capacity, start standby account workers up to `MAX_ACTIVE_WORKERS`; when at capacity, launch replacement workers before terminating least recently used idle workers.
4. If worker startup fails or requests cancel, retain existing workers; if old worker termination fails, retain both instances in capacity accounting; queue requests when all candidates are busy.

HTTP 403 Code 7 preserves verified status for that model/scope. Requests unpinned to an account retry across subsequent candidate accounts prior to the first upstream semantic event; pinned requests, resource-bound operations, and active streams forward the upstream error. HTTP 404 Code 5 with `Ambiguous request for service ''`, local worker crashes, or worker replacements trigger worker recreation and a single replay.

Credential persistence uses `authGeneration` and `checkedAt`; model verification and cooldowns use `modelAccessGeneration` and `checked_at`. Updates apply only when generations match and timestamps are not older than current state. Catalog, tier, or credential modifications increment respective generation counters.

Bidi setup success inherits `checked_at` from the active lease; in-session qualification turns assign strictly increasing timestamps. `turn_complete` consumes pending qualification turns, preserving status on Code 7. Authentication refresh follows identical sequencing.

`runtime-state.json` stores `benefit_tier`, `catalog_fingerprint`, `model_access:{state,checked_at,reason?}`, `cooldowns:{until,reason?}`, and `resources:{kind?,name?,mime?,size?,purpose?,created_at,video?:{model,seconds,size}}`. Video operation bindings persist public video metadata (model, duration, size, UTC creation timestamp) alongside creating accounts, enabling seamless recovery across restarts. Active requests hold account leases; reading, updating, and writing disk state executes under short transaction locks (`auth/.leases/<account>.runtime.lock`, retrying every 25ms up to 2s). Locks re-read disk state before writing and updating in-memory caches. Cache misses during cross-process reads trigger disk reloads to avoid overwriting updates from concurrent processes.

In this context, `generation` denotes a single generation service instance created by a Stop/Start cycle. Catalogs reside in memory per generation; new generations start with an empty cache. `Start` uses verified `CachedModels` to publish public endpoints immediately, launching concurrent background `ListModels` calls across all enabled `ready` or `busy` accounts. Worker pre-warming begins immediately when the cache is populated; cold starts wait for the first non-empty verified catalog before warming workers. Non-empty results update `CachedModels`, emit events, and warm additional workers. When synchronization concludes, credential changes trigger snapshot updates.

Accounts returning errors or empty catalogs enter the pending retry set. After the initial fan-out completes, a 30-second ticker re-executes fan-out across pending accounts; non-empty results update shared catalogs and trigger worker pre-warming. Service shutdown waits up to 2 seconds for catalog tasks to complete; management lifecycle transitions wait up to 12 seconds before returning, retaining background cleanup errors.

## 6. Upstream Changes & Diagnostics

Upstream modifications divide into runtime value updates and structural protocol changes:

| Modification | Handling | Code Changes Required |
| --- | --- | --- |
| Model additions/deletions, aliases, token limits, sampling defaults | Refreshed via ListModels catalog rebuild | No |
| BenefitTier values, AccessModes changes | Recomputed in candidate evaluation | No |
| Challenge message IDs, dynamic program, interpreter hashes | Waa/Create re-invoked on worker start/refresh, downloading new interpreters by hash | No |
| Cookies, visit IDs, Set-Cookie updates, dynamic headers | Persisted in account and runtime state | No |
| Array field value changes or omitted trailing fields | Handled by existing sparse array parser and defaults | No |
| Array nesting levels, field types, oneofs, consumed slot changes | Update encoder/decoder array mappings | Yes |
| New finish reason enum semantics | Update canonical termination mapping and public adapter responses | Yes |
| Snapshot hook locations or web UI interaction flows | Update Camoufox DOM query selectors and automation hooks | Yes |
| Host properties, key iteration orders, or Goja engine semantics queried by program | Update Firefox shape tables, pure-Go host simulation, or Goja engine fork | Yes |
| WebChannel control frames, ACKs, envelopes, or payload shapes | Update WebChannel framing and Bidi parser | Yes |

Diagnostic resolution matrix:

| Symptom | Affected Subsystem | Rapid Remediation Action |
| --- | --- | --- |
| Upstream HTTP 401 | Cookie, SAPISID, or DBSC | Verify login state, refresh responses, Authorization signatures, and Set-Cookie sync |
| Web UI succeeds, API yields HTTP 403 | Dynamic headers, TLS, request body, or WAA binding | Compare header order, proof field index, and proof-less array against browser network inspect |
| Prompt inputs, Run button, or snapshot hooking fails | AI Studio web bundle updates | Re-identify DOM selector paths and high-level snapshot calls in web bundle |
| Pure-Go proof consistently 403, Camoufox succeeds | Pure-Go host environment or Goja | Cross-compare VM execution against identical challenge per [WAA Implementation](waa.md) |
| Field type validation errors with JSON path | Protobuf array schema or new oneof | Save raw upstream frame and update single field parser |
| `provider_<code>` observed | Upstream finish reason enum added | Inspect web behavior and update four public adapter mappings |
| ACK, setup, or backchannel failures | WebChannel | Verify RID, AID, ofs sequence, frame chunk lengths, and payload envelopes |
| Models missing or pervasive Code 7 errors | BenefitTier, ListModels, AccessModes, or WAA | Refresh catalog and compare WAA pipelines across available accounts |

Following recovery, verify official web behavior, reconstruct the upstream wire request independently, and validate public API endpoints. Public adapters preserve upstream errors, client cancellations, and termination orders; protocol modifications should modify only the affected layer while keeping canonical events and public adapters stable.
