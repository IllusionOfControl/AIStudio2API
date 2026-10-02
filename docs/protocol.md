# Google AI Studio Private Protocol

This document defines the Google AI Studio private protocol, authentication states, WAA runtime, JSON+protobuf arrays, incremental events, tools, and media chains used by AIStudio2API. Model methods, limits, and capabilities are returned in real time by the account's `ListModels`, and public APIs project raw structures into canonical events and compatible responses.

## 1. Protocol Scope, Endpoints, and Common Headers

| Purpose | Endpoint | Format |
| --- | --- | --- |
| Page origin | `https://aistudio.google.com` | HTTPS |
| MakerSuite RPC | `https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/<METHOD>` | `application/json+protobuf` |
| WAA RPC | `https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/<METHOD>` | `application/json+protobuf` |
| BotGuard interpreter | `https://www.google.com/js/bg/<INTERPRETER_HASH>.js` | JavaScript |
| Drive upload | `https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id` | `multipart/related` |
| Drive resumable upload | `https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&fields=id` | Chunked HTTPS body |
| Drive download | `https://www.googleapis.com/drive/v3/files/<FILE_ID>?alt=media` | HTTPS body |

MakerSuite requests use the following common headers:

| Header | Source |
| --- | --- |
| `content-type` | Fixed to `application/json+protobuf` |
| `user-agent` | Firefox UA with account-pinned fingerprint |
| `x-user-agent` | Official web gRPC-Web identifier |
| `x-goog-api-key` | Dynamic value from AI Studio landing page or active official web requests |
| `x-goog-authuser` | Active account official web request |
| `x-aistudio-visit-id` | Landing page initialization or active official web request |
| `x-aistudio-g1-tier` | `GetAiStudioBenefitTier` return value mapped to `TIER0`, `TIER1`, or `TIER2` |
| `x-goog-ext-519733851-bin` | Dynamic value from active official web requests; encoded by `GetLoggingContext` in pure Go WAA backend |
| `authorization` | Three-part SAPISID signature |
| `cookie` | Cookies visible to the target RPC for the current account |
| `origin`, `referer` | `https://aistudio.google.com` |
| `accept-language` | Account locale |

The `x-goog-api-key` header is a dynamic public value used by the AI Studio web page, distinct from user-created Google Cloud API keys; the free web pipeline still relies on Cookies, SAPISID signatures, and WAA proofs.

WAA-protected `GenerateContent` requests are dispatched via account-pinned Camoufox browser pages to preserve native Firefox TLS, HTTP/2, headers, cookies, and page fingerprints; other MakerSuite and Drive requests use a Go HTTP transport over the same account-pinned egress. When `WAA_BACKEND=go`, protected requests are dispatched by the service process matching a Firefox 152 network profile through the same pinned egress; see [WAA Implementation](waa.md).

JSON+protobuf represents protobuf messages using arrays. Array indices are 0-based while protobuf fields are 1-based, meaning field `N` corresponds to index `N-1`. Google responses allow omitting empty slots, resulting in `[,value]`; the decoder first normalizes omitted slots to `null` before extracting repeated messages from the full JSON root value. HTTPS chunks provide only raw byte sequences, with business event boundaries determined by the array structure.

The protocol core uses the following MakerSuite RPCs:

| RPC | Purpose |
| --- | --- |
| `ListModels` | Fetch models, methods, limits, default parameters, and capability options |
| `CountTokens` | Authoritative input token counting |
| `GenerateContent` | Text, thinking, function calling, Google tools, images, audio, and music |
| `GenerateAccessToken` | Retrieve Drive bearer token |
| `GenerateVideo` | Create Veo long-running task |
| `GetGenerateVideoOperation` | Poll Veo long-running task |

AI Studio page initialization also involves the following control plane RPCs:

| RPC | Purpose |
| --- | --- |
| `GetLoggingContext` | Page logging context |
| `GetUserPreferences` | User preferences and onboarding status |
| `UpdateUserPreferences` | Update user preferences such as onboarding status |
| `ListPromos` | Page promotional campaigns |
| `GetAiStudioBenefitTier` | Account tier enumeration and tier header |
| `ListRecentApplets` | Recent Applets |
| `ListPrompts` | Prompt catalog |
| `GetUserRestrictions` | Account restrictions |

Upon startup, the management process loads accounts and prepares common headers. `POST /api/control/start` refreshes the live model catalog, warms up WAA workers, and enables the data plane, after which business capabilities invoke corresponding RPCs on demand.

In this document, "data plane" refers to the generation service that handles public API requests and can be toggled via Stop/Start.

## 2. SAPISID, Chrome DBSC, Cookies, and Account State

### SAPISID Authorization

The `authorization` header is signed separately by three cookies:

| Token Label | Cookie |
| --- | --- |
| `SAPISIDHASH` | `SAPISID` |
| `SAPISID1PHASH` | `__Secure-1PAPISID` |
| `SAPISID3PHASH` | `__Secure-3PAPISID` |

All three segments use the same Unix timestamp in seconds:

```text
source = "<TIMESTAMP> <COOKIE_VALUE> https://aistudio.google.com"
digest = lowercase_hex(SHA1(source))
token = "<LABEL> <TIMESTAMP>_<DIGEST>"
authorization = token_1 + " " + token_2 + " " + token_3
```

When response headers arrive, `Set-Cookie` directives from MakerSuite responses are sequentially merged with the account's latest `storage-state.json` and atomically written back. Signing, cookie selection, and expiration checks are all evaluated against the account state freshly re-read at request time.

### Windows Chrome DBSC Import

Windows Chrome import recovers OAuth credentials and Device Bound Session Credentials from the profile:

```text
Chrome Local State + Profile Preferences + Web Data/token_service
  -> Gaia ID, v20 refresh token ciphertext, wrapped binding key
  -> Decrypt App-Bound v20 master key
  -> AES-256-GCM decrypt refresh token
  -> Obtain DBSC challenge via OAuthMultilogin sentinel request
  -> Issue ES256 assertion using NCrypt device key
  -> Decrypt server cookies via X25519/HPKE
  -> Save Playwright storage state structure and renewal credentials
```

`Local State.os_crypt.app_bound_encrypted_key` is Base64-encoded with an `APPB` prefix. The application loads an embedded ABE helper into an isolated, hidden Chrome process to retrieve the 32-byte master key; the transient process tree is managed by a Windows Job Object. `token_service.encrypted_token` uses the format `v20 || nonce[12] || ciphertext+tag`, which is decrypted with AES-GCM using this master key.

OAuthMultilogin uses the `MultiOAuth` header. The first assertion is `DBSC_CHALLENGE_IF_REQUIRED`, and the response provides a challenge; the JWT header of the second assertion uses `ES256` and `DEVICE_BOUND_SESSION_CREDENTIALS_ASSERTION`. The payload binds the Google OAuth client, challenge, device public key issuer, and ephemeral HPKE public key. Cookie ciphertexts are decrypted using X25519, HKDF-SHA256, and AES-128-GCM.

The Chrome import state preserves the source, Gaia ID, refresh token, and wrapped binding key within the `aistudio2api` extension of `storage-state.json`. When a standard or protected RPC first returns HTTP `401`, the service renews cookies on the same account egress, invalidates dynamic headers, terminates that account's WAA runtime, and retries the request once. HTTP `403` and protocol Code 7 preserve upstream errors without clearing the success state of the account or model; the service can failover to the next account with equivalent capabilities before the first upstream semantic event occurs. Isolated Camoufox logins and external storage states do not carry the Chrome OAuth extension.

`storage-state.json` preserves Playwright root fields and unknown extension fields; its defined schema is shown below. `wrapped_binding_key` is a Base64-encoded JSON string representing a Go `[]byte`.

```json
{
  "cookies": [
    {
      "name": "<NAME>",
      "value": "<VALUE>",
      "domain": ".example.com",
      "path": "/",
      "expires": -1,
      "httpOnly": true,
      "secure": true,
      "sameSite": "Lax",
      "partitionKey": "<OPTIONAL>"
    }
  ],
  "origins": [
    {
      "origin": "https://example.com",
      "localStorage": [{"name": "<NAME>", "value": "<VALUE>"}]
    }
  ],
  "aistudio2api": {
    "source": {"browser": "chrome", "profile": "<PROFILE>", "email": "<EMAIL>"},
    "oauth": {
      "gaia_id": "<GAIA_ID>",
      "refresh_token": "<REFRESH_TOKEN>",
      "wrapped_binding_key": "<BASE64>"
    }
  }
}
```

Cookie attributes `name`, `value`, `domain`, `path`, `expires`, `httpOnly`, `secure`, `sameSite`, and the optional `partitionKey` are persisted verbatim; `sameSite` accepts an empty value, `Lax`, `Strict`, or `None`. The `origin` must contain both scheme and host. Request cookies filter out expired entries and mismatched Secure/domain/path entries; cookies with the same name are sent in descending order of path length. `Set-Cookie` directives from standard HTTP responses replace or delete unpartitioned entries matching the same name, domain, and path, while partitioned entries with the same name are retained independently. During browser restoration, `partitionKey` maps to BiDi `storageKey.sourceOrigin`; empty values fall back to the default partition.

### Account Persistent State

| File | Description |
| --- | --- |
| `auth/<Google Email>/account.json` | Email, enabled, proxy, locale, timezone |
| `auth/<Google Email>/storage-state.json` | Cookies, localStorage, and optional Chrome renewal credentials |
| `auth/<Google Email>/camoufox-fingerprint.json` | Account-pinned navigator, screen, fonts, language, locale, and timezone configuration |
| `auth/<Google Email>/runtime-state.json` | Account benefit tier, verified model eligibility, cooldowns, and Drive/Veo resource bindings |
| `auth/<Google Email>/camoufox-cache/` | HTTP disk cache for this account's Camoufox instance, held exclusively by running WAA Workers |
| `auth/.leases/<Google Email>.lock` | Cross-process exclusivity lock for the same account directory |
| `auth/.leases/<Google Email>.runtime.lock` | Short-transaction lock for each `runtime-state.json` read, merge, and writeback cycle |
| `[User Cache]/AIStudio2API/runtime-leases/<Google Email>.lock` | WAA Worker reservation lock for this email on the local machine |

The lowercase Google email address serves as the account directory name, admin dashboard identifier, and logging source. The locale and timezone for new accounts are read from the host machine's settings; the admin dashboard uses browser language and IANA timezone, while the CLI uses operating system language and timezone. Initialization, WAA, MakerSuite, OAuth renewal, and Drive all route through the account's pinned proxy. The locale simultaneously configures navigator language, Accept-Language, and region; the timezone configures the browser timezone. Re-logins and WAA runtimes reuse the same account fingerprint. Multiple processes on the same machine share WAA runtime leases by email; the scheduler only spawns Workers for unoccupied emails.

The root fields of `runtime-state.json` are `cooldowns`, `resources`, `model_access`, `benefit_tier`, and `catalog_fingerprint`. Cooldown values follow `{until,reason?}`; model access values follow `{state,checked_at,reason?}`; resource values follow `{kind?,name?,mime?,size?,purpose?,created_at,video?:{model,seconds,size}}`.

Each runtime state transaction first acquires `auth/.leases/<Account>.runtime.lock`, retrying every 25ms with a maximum wait time of 2 seconds; transactions carrying a caller context (Go cancellation context) terminate immediately if the caller cancels or an earlier deadline is reached. After acquiring the lock, current on-disk values are re-read, only target fields are merged, and `runtime-state.json` is atomically replaced via a temporary file before synchronizing in-memory state and resource ownership. Lock acquisition failures, read failures, writeback failures, and unlock failures all return the original error chain.

The scheduler selects candidates based on real-time model methods, capability, AccessModes, and account benefit tiers. It prioritizes warmed accounts that have already successfully invoked the target scope (the state-tracking scope for model eligibility and cooldowns), then sorts them by the target model's most recent time-to-first-event. Code 7 preserves existing account and model success records. Resident Workers prioritize accounts capable of invoking a broader range of models. When the warmed pool falls below the capacity limit, standby accounts are promoted; when all warmed accounts are busy, requests queue for concurrency slots. WAA proofs for the same account are generated sequentially, while prepared MakerSuite HTTP requests execute concurrently; the first active request acquires `.leases/<Email>.lock`, and the last releases it. Drive files, Veo operations, and artifact files always use their originating creator account.

### Account Benefit Tiers

The `GetAiStudioBenefitTier` request payload is `[]`, and the enum mapping for response field 1 is as follows. `[]`, `[null]`, and `[0]` all represent Free; if subsequent fields exist in the response, the tier is still determined by field 1.

| Value | Benefit Tier | RPC Header |
| ---: | --- | --- |
| 0 | Free | None |
| 1 | Pro | `X-AIStudio-G1-Tier: TIER1` |
| 2 | Ultra | `X-AIStudio-G1-Tier: TIER2` |
| 3 | Plus | `X-AIStudio-G1-Tier: TIER0` |

The official web console injects this header for `GenerateContent`, `CountTokens`, Interaction, Code Assistant, and Veo RPCs. Model field 83 specifies the access mode: `1` denotes paid API key, `3` denotes Pro/Ultra subscription, and `4` denotes Ultra subscription. The public model catalog merges real-time `ListModels` records across all accounts; account scheduling selects specific accounts using AccessModes, BenefitTier, and successful invocation history.

## 3. WAA Challenge, Official VM, and Fresh Proof

Protected requests use the following pipeline:

```text
Waa/Create
  -> decode challenge
  -> load interpreter by current hash
  -> initialize official VM lifecycle
  -> SHA-256(binding prompt) as lowercase hex
  -> snapshot({TYb:{content:<DIGEST>}})
  -> write fresh proof into request
  -> send protected MakerSuite RPC
```

When `WAA_BACKEND=camoufox`, the official VM runs in a Camoufox page with a fixed account fingerprint, and protected requests are sent via the page's native `fetch`. When `WAA_BACKEND=go`, the VM runs in-process inside `goja` with a Firefox-shaped host environment, and protected requests are sent via Go HTTP matching the Firefox 152 network profile. For details on bootstrapping, challenge decoding, interpreters, initialization parameters, host environments, lifecycles, failure handling, and troubleshooting upstream changes, see [WAA Implementation](waa.md).

The `Waa/Create` request is a JSON+protobuf array. When a worker starts up, it only includes the request key; when the VM refreshes, it appends the current interpreter hash and the unbound snapshot from the previous VM:

```json
["lmnUSbltwc5ULv48iKLX"]
["lmnUSbltwc5ULv48iKLX", "<INTERPRETER_HASH>", "<PREVIOUS_SNAPSHOT>"]
```

Outer index `1` of the response contains the Base64 challenge. After decoding, each byte is incremented by `97` (+97), yielding the message ID, interpreter, program, global function name, and client experiments state. The interpreter is cached by hash, where the digest is unpadded Base64URL-encoded SHA-256. The program is bound to the current `Create` lifecycle, and the proof binds the current prompt digest to the VM's internal state, generating a fresh proof for each request.

The underlying input to the snapshot is a 4-slot array, with the first slot carrying the binding:

```javascript
[{content: sha256(bindingPrompt)}, undefined, undefined, undefined]
```

The return value is a proof string prefixed with `!`. For `GenerateContent` and `CreateInteractionStream`, it is written to field 5; for the Build proxy, field 3; for `GenerateVideo`, field 8; and for each Bidi client wire, field 6. All other slots of the original request remain unchanged. For the binding prompt specifications of each RPC, see [WAA Implementation](waa.md). In `GenerateContent`, prompts are concatenated using a single space in the original order of `contents` and `parts`.

`Waa/Ping` request and successful response:

```json
["lmnUSbltwc5ULv48iKLX", "<BOTGUARD_RESPONSE>"]
[]
```

Field 1 is `request_key`, and field 2 is `botguard_response`. A valid proof, a corrupted proof, an omitted field 2, an incorrect request key, or even missing account authentication can all return HTTP 200 and `[]`. A successful Ping only indicates that the WAA RPC endpoint, API consumer identity, and field types are reachable. The status of the Worker VM, snapshot proof, binding, account session, model eligibility, and `GenerateContent` acceptance are determined solely by the actual protected business RPC.

Upon startup, the generation service warms up account WAA Workers according to the configured resident count and startup concurrency. A single account Worker provides proofs for all standard generation models under that account, and switching business models directly reuses the active Worker. Snapshot executions for the same account are serialized.

## 4. ListModels, CountTokens, and GenerateContent Requests

### ListModels

Request body:

```json
[]
```

The root response shape is `[[<MODEL_ROW>, ...]]`. The model list is located at field 1; subsequent root fields do not alter the model catalog. Model row fields:

| JSON Index | Protobuf Field | Content |
| ---: | ---: | --- |
| 0 | 1 | `models/<MODEL_ID>` |
| 2 | 3 | Version |
| 3 | 4 | Display name |
| 4 | 5 | Description |
| 5 | 6 | Input token limit |
| 6 | 7 | Output token limit |
| 7 | 8 | Supported methods |
| 8 | 9 | Default temperature |
| 9 | 10 | Default topP |
| 10 | 11 | Default topK |
| 56 | 57 | Model aliases |
| 64 | 65 | Primary capability codes |
| 66 | 67 | TTS voice list |
| 70 | 71 | Veo configuration |
| 71 | 72 | Thinking default configuration |
| 74 | 75 | Secondary capability codes |
| 75 | 76 | Image aspect ratio codes |
| 76 | 77 | Image output resolution codes |
| 77 | 78 | Paid flag; displays Paid when value is `2` |
| 78 | 79 | Interaction configuration: index 2 is background task, index 3 type `1` is agent, `2` is model |
| 82 | 83 | Model access method |

Capability code mapping:

| Code | Capability | Code | Capability |
| ---: | --- | ---: | --- |
| 1 | chat model | 9 | code execution |
| 10 | function declarations | 12 | Google Search |
| 13 | URL Context | 20 | Veo route |
| 21 | image route | 25 | thinking |
| 26 | live route | 35 | thinking budget |
| 37 | speech route | 43 | media resolution |
| 47 | aspect ratio | 49 | output resolution |
| 52 | thinking level | 53 | music route |
| 54 | image search | 58 | Google Maps |
| 59 | private Interaction route | 85 | TTS segmented speaker |
| 46 | Live real-time translation | | |

Unknown capability codes are preserved as raw values using `capability_code_<N>` or `secondary_capability_code_<N>`.

The public model object adds the following semantic keys for known primary capability codes:

| Code | Capability Key | Code | Capability Key |
| ---: | --- | ---: | --- |
| 1 | `chat_model` | 9 | `code_execution` |
| 10 | `function_declarations` | 12 | `google_search` |
| 13 | `browse` | 20 | `video_route` |
| 21 | `image_route` | 25 | `thinking` |
| 26 | `live_route` | 35 | `thinking_budget` |
| 37 | `speech_route` | 43 | `media_resolution` |
| 47 | `aspect_ratio` | 49 | `output_resolution` |
| 52 | `thinking_level` | 53 | `music_route` |
| 54 | `image_search` | 58 | `google_maps` |
| 59 | `interaction_route` | 74 | `transcription_word_timestamps` |
| 76 | `transcription_language_codes` | 77 | `transcription_output` |
| 80 | `transcription_speaker_labels` | 81 | `transcription_custom_vocabulary` |
| 84 | `transcription_smart` | 85 | `speech_metadata` |
| 46 | `speech_translation` | | |

Each primary capability code is retained as `capability_code_<N>`, while adding semantic keys for known codes in the table above; secondary capability codes are retained as `secondary_capability_code_<N>`.

Image and video options use enum codes:

| Type | Code Value Mapping |
| --- | --- |
| Image/video aspect ratio | `1=1:1`, `2=9:16`, `3=16:9`, `4=3:4`, `5=4:3`, `6=3:2`, `7=2:3`, `8=5:4`, `9=4:5`, `10=21:9`, `11=9:21`, `12=1:4`, `13=4:1`, `14=1:8`, `15=8:1` |
| Image resolution | `1=1K`, `2=2K`, `3=4K`, `4=512` |
| Video duration | `1=5s`, `2=6s`, `3=7s`, `4=8s`, `5=4s` |
| Video resolution | `1=720p`, `2=1080p`, `3=4k`, `4=368p`, `5=360p` |

Veo field 71 aspect ratio, duration, and resolution are located at sub-indices `4`, `5`, and `9`, respectively. TTS field 67 is a repeated voice row, where index `0` of each row is the voice name. Thinking field 72's default level is located at sub-index `5`.

Field 57 alias can be `["models/<ALIAS>"]` or a repeated row; when shaped as rows, take index `0` of each row and remove the `models/` prefix. The complete set of public `capability_options` keys consists of `aliases`, `voices`, `image_aspect_ratios`, `image_output_resolutions`, `video_aspect_ratios`, `video_durations_seconds`, and `video_output_resolutions`; keys without values are omitted.

### CountTokens

Plain text without system:

```json
["models/<MODEL_ID>", [<CONTENT>, ...]]
```

With system, inline data, external media, or Drive file:

```json
["models/<MODEL_ID>", null, ["models/<MODEL_ID>", [<CONTENT>, ...], null, null, null, <SYSTEM>]]
```

Request shape selection:

| Condition | Root Structure | GenerateContent Sub-message Position |
| --- | --- | --- |
| Plain text contents | `[model, contents]` | — |
| system instruction | `[model, null, generate]` | `$[2][5]` |
| function / Google tools | `[model, null, generate]` | `$[2][6]` |
| inline data, external media, Drive, function call/result, code result | `[model, null, generate]` | `$[2][1]` |

Complete token count request including system and function declarations:

```json
[
  "models/gemini-3.6-flash",
  null,
  [
    "models/gemini-3.6-flash",
    [
      [
        [[null, "Call ping to check service"]],
        "user"
      ]
    ],
    null,
    null,
    null,
    [
      [[null, "You are a diagnostic assistant"]],
      "user"
    ],
    [
      [null, [["ping", "Check service"]]]
    ]
  ]
]
```

The response is a single-element array:

```text
[<INPUT_TOKEN_COUNT>]
```

Index `0` is the authoritative input token count. Other slots are preserved as opaque protocol fields.

### Content, Part, and system

Content shape:

```json
[[<PART>, ...], "user|model"]
```

Model completion frames with a finish reason can use `[null,"model"]`, which provides only the termination status and usage.

Client tool results use the `user` role. Part fields:

| JSON Index | Protobuf Field | Content |
| ---: | ---: | --- |
| 1 | 2 | text |
| 2 | 3 | inline data `[mime, base64]` |
| 5 | 6 | Drive file `[fileId]` |
| 6 | 7 | external media `[mime, url]` |
| 7 | 8 | executable code `[languageCode, code]` |
| 8 | 9 | code execution result `[outcomeCode, output]` |
| 10 | 11 | function call `[name, Struct, callId?]` |
| 11 | 12 | function result `[name, Struct, callId?]` |
| 12 | 13 | thought boolean |
| 14 | 15 | thought signature |
| 22 | 23 | transcription metadata `[text, speaker?, timestamp spans?]` |

system instruction:

```json
[[[null, "<SYSTEM_TEXT>"]], "user"]
```

### GenerateContent

Root message fields:

| JSON Index | Protobuf Field | Content |
| ---: | ---: | --- |
| 0 | 1 | `models/<MODEL_ID>` |
| 1 | 2 | contents |
| 2 | 3 | safety settings |
| 3 | 4 | generation config |
| 4 | 5 | fresh WAA proof |
| 5 | 6 | system instruction |
| 6 | 7 | tools |
| 10 | 11 | Fixed value `1` |
| 13 | 14 | `[[null,null,<TIMEZONE>]]` |
| 14 | 15 | User Cloud API key; remains `null` for free web chains |

safety settings:

```json
[
  [null, null, 7, 5],
  [null, null, 8, 5],
  [null, null, 9, 5],
  [null, null, 10, 5]
]
```

generation config fields:

| JSON Index | Protobuf Field | Content |
| ---: | ---: | --- |
| 1 | 2 | stop sequences |
| 3 | 4 | max output tokens |
| 4 | 5 | temperature |
| 5 | 6 | topP |
| 6 | 7 | topK |
| 7 | 8 | response MIME type |
| 8 | 9 | response schema |
| 13 | 14 | Fixed value `1` |
| 14 | 15 | response modalities: TEXT=`1`, IMAGE=`2`, AUDIO=`3` |
| 15 | 16 | speech config |
| 16 | 17 | thinking config `[1, budget?, null, level]` |
| 18 | 19 | seed |
| 26 | 27 | image config `[aspectRatio?, imageSize?]` |
| 31 | 32 | transcription config |

Generation parameter validation:

| Parameter | Default Source | Valid Values |
| --- | --- | --- |
| max output | ListModels field 7 | `1..model.outputTokenLimit` |
| temperature | ListModels field 9 | `0..2` |
| topP | ListModels field 10 | `0..1` |
| topK | ListModels field 11 | Non-negative integer |
| thinking level | ListModels field 72 | Low=`1`, Medium=`2`, High=`3`, Minimal=`4` |
| thinking budget | Request value | Model capability codes include thinking budget |

`reasoning_effort` / `thinkingLevel` / Anthropic `output_config.effort` accepts `none`, `minimal`, `low`, `medium`, and `high`. `none` uses a budget of 0 on models that only support thinking budget, the lowest available level on models supporting thinking level, and is ignored on models without thinking support. When a model only supports thinking budget, an explicit effort must also provide a budget; when a model only supports thinking level, an explicit budget degrades to level. If both capabilities are missing, a parameter error is returned.

response modalities:

`wire` refers to the raw array field sent upstream.

| Output | wire | Default Route |
| --- | --- | --- |
| text | `[1]` | chat |
| image | `[2]` | image route |
| image + text | `[2,1]` | Explicit combined request |
| audio | `[3]` | speech / music route |

AUDIO uses an independent output modality. JSON Schema type codes are string=`1`, number=`2`, integer=`3`, boolean=`4`, array=`5`, object=`6`; schema supports format, description, nullable, enum, items, properties, required, and field 23 `propertyOrdering`.

The following minimal combined request includes system, text, function declarations, generation config, WAA proof, and account timezone. Consecutive null slots are kept on the same line; see the tables above for field definitions:

```json
[
  "models/gemini-3.6-flash",
  [
    [
      [[null, "Call ping to check service"]],
      "user"
    ]
  ],
  [
    [null, null, 7, 5],
    [null, null, 8, 5],
    [null, null, 9, 5],
    [null, null, 10, 5]
  ],
  [null, null, null, 512, 0.2, 0.95, 40, null, null, null, null, null, null, 1],
  "!WAA_PROOF",
  [
    [[null, "You are a diagnostic assistant"]],
    "user"
  ],
  [
    [null, [["ping", "Check service"]]]
  ],
  null,
  null,
  null,
  1,
  null,
  null,
  [[null, null, "Asia/Taipei"]]
]
```

## 5. Incremental Streaming, Thinking, Usage, Sources, and Errors

`GenerateContent` returns an incrementally growing JSON+protobuf root array, where root index `0` represents repeated frames. The frame structure is as follows:

| Path | Content |
| --- | --- |
| `$[0][frame][0]` | candidates |
| `$[0][frame][0][0][0]` | candidate content |
| `$[0][frame][0][0][1]` | finish reason code |
| `$[0][frame][0][0][6]` | citations |
| `$[0][frame][0][0][7]` | grounding metadata |
| `$[0][frame][2]` | usage |
| `$[0][frame][7]` | response ID |
| `$[0][frame][3]` when frame 0 is empty | interaction metadata |

The transfer body is a single JSON root value supplied as bytes via network chunks; the decoder consumes each repeated frame in `$[0]` as soon as a complete frame becomes available. Each content frame contains a single candidate, with the candidate content formatted as `[[parts...], "model"]`. A completion frame may simultaneously carry the final set of Parts, usage, the response ID, and the finish reason; reading terminates once root array parsing is complete.

Text frame extracted from `$[0]`:

```json
[
  [
    [
      [
        [[null, "42"]],
        "model"
      ]
    ]
  ]
]
```

The subsequently arriving completion frame contains `finish=1`, usage, and the response ID:

```json
[
  [[null, 1]],
  null,
  [27, 1, 28, null, null, null, null, 0, null, 0],
  null,
  null,
  null,
  null,
  "response_01"
]
```

Quick Reference for High-Frequency Paths:

| Structure | JSONPath | Content |
| --- | --- | --- |
| GenerateContent | `$[0]` | model |
| GenerateContent | `$[1]` | contents |
| GenerateContent | `$[3]` | generation config |
| GenerateContent | `$[4]` | WAA proof |
| GenerateContent | `$[5]` | system instruction |
| GenerateContent | `$[6]` | tools |
| GenerateContent | `$[13][0][2]` | timezone |
| response root | `$[0][frame]` | repeated frame |
| candidate content | `$[0][frame][0][0][0]` | `[[parts], "model"]` |
| candidate finish | `$[0][frame][0][0][1]` | finish reason code |
| Part text | `...parts[part][1]` | text |
| Part inline data | `...parts[part][2]` | `[mime, base64]` |
| Part function call | `...parts[part][10]` | `[name, Struct, callId?]` |
| Part thought | `...parts[part][12]` | boolean |
| Part signature | `...parts[part][14]` | signature |
| frame usage | `$[0][frame][2]` | usage array |
| frame response ID | `$[0][frame][7]` | response ID |

When a text Part has `part[12]=true`, it represents a reasoning summary, whereas standard text represents visible content. An inline image with `part[12]=true` is a draft generated during the image model's reasoning process and is not returned as output media; the final image is returned separately as a standard Part. `part[14]` is the thought signature. Signatures may be attached to text, function calls, or standalone empty Parts, and must be passed back verbatim in the next turn:

| Public Protocol | Signature Input | Signature Output |
| --- | --- | --- |
| OpenAI Chat | `extra_content.google.thought_signature` of the assistant tool call | Extension field of the same name on the tool call |
| OpenAI Responses | `reasoning.encrypted_content` immediately preceding subsequent `function_call` | `encrypted_content` of the reasoning item |
| Anthropic | `signature` of a `thinking` or `redacted_thinking` block | `signature` of the thinking block |
| Gemini | `thoughtSignature` of a data Part or standalone Part | `thoughtSignature` of the Part |

When an assistant tool call in OpenAI Chat lacks `extra_content`, the service backfills the most recently issued signature from the current process based on call ID, function name, and arguments; if no match is found, it writes `skip_thought_signature_validator`.

An Anthropic redacted thinking block carries this same opaque state via `data`; adapters preserve this value on both input and output sides. Streaming responses do not emit signatures that arrive after the text.

The reasoning summary is the summary text returned by the server. The thought signature is passed back verbatim as a protocol state field in the subsequent request.

The protocol core emits events in network order: `text`, `reasoning`, `tool_call`, `executable_code`, `code_execution_result`, `grounding`, `citation`, `media`, `thought_signature`, `usage`, `finish`, and `error`.

### Grounding and Citations

Grounding metadata fields:

| JSON Index | Content |
| ---: | --- |
| 0 | search entry point `[renderedContent?, sdkBlob?]` |
| 1 | grounding chunks |
| 2 | grounding supports |
| 3 | retrieval metadata, with dynamic score at sub-index 1 |
| 4 | web search queries |
| 5 | second repeated web search query slot |
| 6 | Maps widget context token |

Indices `4` and `5` are merged by slot and element order, and deduplicated.

`oneof` indicates that at most one field from the group may be set.

The `oneof` indices `0/1/2` for a grounding chunk represent web, retrieved context, and maps, respectively; internal fields are URI, title, text, and place ID in order. A support is structured as `[segment, chunkIndices, confidenceScores]`, where segment is `[partIndex,startIndex,endIndex,text]`. Entries for candidate citations reside at metadata index 0, with each item's URL at index 2 and title at index 3.

Raw metadata containing web chunk, maps chunk, content support, retrieval score, and search queries:

```json
[
  ["<div>Search results</div>", "SDK_BLOB"],
  [
    [["https://example.com/gemini", "Gemini Guide", "Protocol overview"]],
    [null, null, ["https://maps.google.com/?cid=1", "Google Taipei", "", "ChIJ_demo"]]
  ],
  [
    [[0, 0, 12, "Gemini Guide"], [0], [0.98]]
  ],
  [null, 0.91],
  ["Gemini AI Studio protocol"],
  null,
  "MAPS_WIDGET_CONTEXT_TOKEN"
]
```

For Code Execution, the language codes are `0=LANGUAGE_UNSPECIFIED` and `1=PYTHON`. The execution outcome codes are `0=OUTCOME_UNSPECIFIED`, `1=OUTCOME_OK`, `2=OUTCOME_FAILED`, and `3=OUTCOME_DEADLINE_EXCEEDED`.

### Usage

Completion frame usage:

| Array Index | Semantics | Canonical Field |
| ---: | --- | --- |
| 0 | input tokens | `input_tokens` |
| 1 | visible output tokens | `output_tokens` |
| 2 | total tokens | `total_tokens` |
| 7 | tool tokens | `tool_tokens` |
| 9 | thought tokens | `reasoning_tokens` |

Complete usage is returned directly using upstream values. When the completion frame omits visible output tokens, the service reconstructs this value from the upstream total and the remaining breakdown fields. When complete usage is missing entirely, the built-in Gemini SentencePiece tokenizer calculates observable inputs, tool declarations, reasoning summaries, and actual outputs locally.

Input statistics for OpenAI and Anthropic are computed as input + tool, while output statistics are visible output + reasoning. Gemini projects `promptTokenCount`, `candidatesTokenCount`, `thoughtsTokenCount`, and `totalTokenCount` respectively. Hidden thinking token usage comes from upstream usage field 9; the local fallback tallies the reasoning summary returned by the server.

Anthropic streaming `message_start` writes an initial input estimate, while the final `message_delta` overrides it with the completion usage to provide authoritative input and output counts.

Requests carrying stop sequences execute `CountTokens` in parallel under the same account. When the generated text matches the actual sequence, the protocol core terminates the generation stream and constructs the final usage from the `CountTokens` total input count along with already emitted text, reasoning, and tool statistics; if counting fails, it still returns the `stop_sequence` terminal state and omits usage.

### Finish and Errors

| code | reason | code | reason |
| ---: | --- | ---: | --- |
| 0 | unspecified | 1 | stop |
| 2 | max_tokens | 3 | safety |
| 4 | recitation | 5 | other |
| 6 | language | 7 | blocklist |
| 8 | prohibited_content | 9 | spii |
| 10 | malformed_function_call | 11 | image_safety |
| 12 | unexpected_tool_call | 13 | too_many_tool_calls |
| 14 | image_prohibited_content | 15 | image_other |
| 16 | no_image | 17 | image_recitation |
| 18 | missing_thought_signature | 19 | `provider_19` |
| Other integers | `provider_<code>` | | |

The root shape of an error response is `[null,[code,message,...]]`. The protocol core preserves HTTP status, protocol code, and message; public adapters map these to OpenAI, Anthropic, or Gemini error objects. Chat, Responses, Anthropic Messages, and Gemini GenerateContent output normal text from media models as text results; dedicated image endpoints require image results. HTTP/protocol errors or missing completion frames are treated as failures; upstream finish reasons are preserved as normal terminal states and mapped to each public protocol.

Specific terminal state mappings across protocols are as follows:

| Upstream Terminal State | OpenAI Chat | OpenAI Responses | Anthropic Messages | Gemini GenerateContent |
| --- | --- | --- | --- | --- |
| `stop` with function call | `tool_calls` | `completed`, retaining function call item | `tool_use` | `STOP`, retaining functionCall Part |
| `stop_sequence` | `stop` | `completed` | `stop_sequence` with actual sequence | `STOP` |
| `max_tokens` | `length` | `incomplete/max_output_tokens` | `max_tokens` | `MAX_TOKENS` |
| policy, refusal, missing signature, and other anomalous terminal states | `content_filter` | `incomplete/content_filter` | `refusal` | Corresponding Gemini enum or `OTHER` |

Anomalous terminal states take precedence over tool call terminal states within the same result, while already emitted content, reasoning, tool events, and usage are retained in the response. `provider_*` retains its original value in `provider_finish_reason` within Chat choice, Responses response, Anthropic message, or `message_delta`; Gemini retains the code using `finishMessage`. `provider_19` corresponds to `Content blocked` on the AI Studio UI.

## 6. Functions, Google Tools, Drive, and Media

### Functions and Google Tools

Root field 7 is a repeated Tool:

| Tool | Tool Array Shape |
| --- | --- |
| Function declarations | `[null, [[name, description?, schema?], ...]]` |
| Code Execution | `[[]]` |
| Google Search | `[null,null,null,[null,[searchTypes]]]`, with index 0 of `searchTypes` being `[]` |
| Image Search | Same Search tool, with index 1 of `searchTypes` being `[]` |
| URL Context | 8-slot array, with index 7 being `[]` |
| Google Maps | 11-slot array, with index 10 being `[]` |

Index `3` of the Search tool is `[timeRange?,searchTypes]`. `timeRange` is `[start?,end?]`, where each timestamp value is encoded as `["<UNIX_SECONDS>"]`; indices `0` and `1` of `searchTypes` enable web search and image search, respectively.

Public tool names are normalized before generating the Tool arrays described above:

| AI Studio Tool | OpenAI Chat / Responses | Anthropic | Gemini |
| --- | --- | --- | --- |
| function declarations | `function` | Empty type or `custom` | `functionDeclarations` |
| Google Search | `web_search`, `web_search_preview` | `web_search*` | `googleSearch`, `googleSearchRetrieval` |
| Image Search | `image_search` | `image_search` | `imageSearch` |
| URL Context | `url_context` | `web_fetch*`, `url_context*` | `urlContext` |
| Code Execution | `code_interpreter` | `code_execution*` | `codeExecution` |
| Google Maps | `google_maps` | `google_maps*` | `googleMaps` |

The specific server tool types accepted by Anthropic are:

| AI Studio Tool | Anthropic Type |
| --- | --- |
| Google Search | `web_search_20250305` |
| Image Search | `image_search` |
| URL Context | `web_fetch_20250910`, `url_context` |
| Code Execution | `code_execution_20250522`, `code_execution_20250825` |
| Google Maps | `google_maps` |

Root field 7 is encoded item-by-item according to the request declarations; function declarations and various Google tools are encoded into their corresponding Tool entries as shown in the table above. The model's supported tool scope is derived from the real-time capability code. Root field 14 is `ToolConfig`; when a request carries both function declarations and Google tools, its field 3 `include_server_side_tool_invocations` is set to `true`, allowing the upstream to execute Google tools within the same turn and return function calls.

The encoder merges all function declarations into a single Tool entry; Google Search and Image Search are merged into a single search entry, occupying indices `0` and `1` of `searchTypes`, respectively; Code Execution, URL Context, and Maps each occupy one entry. Google Maps and Code Execution/URL Context form mutually exclusive tool groups; each request selects at most one of these groups.

The `name` of an Anthropic server tool must be `web_search`, `image_search`, `web_fetch`, `code_execution`, `url_context`, or `google_maps`. These definitions accept only `type` and the corresponding `name`; extra options, `description`, or `input_schema` return a `400 invalid_request_error`.

Function JSON Structs use protobuf `Struct`/`Value` arrays: maps are represented as `[[[key,value],...]]`; `Value` oneof indices `0..5` represent null, number, string, bool, Struct, and ListValue, respectively. Object keys are sorted before encoding.

For example, given the following function arguments:

```json
{
  "city": "Taipei",
  "days": 2,
  "metric": true,
  "note": null,
  "units": ["C", "F"]
}
```

The encoded Struct is:

```json
[
  [
    ["city", [null, null, "Taipei"]],
    ["days", [null, 2]],
    ["metric", [null, null, null, true]],
    ["note", [0]],
    [
      "units",
      [
        null,
        null,
        null,
        null,
        null,
        [[[null, null, "C"], [null, null, "F"]]]
      ]
    ]
  ]
]
```

The key slots of a complete function call Part are:

```json
[
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  ["multiply", [[["a", [null, 21]], ["b", [null, 2]]]], "call_01"],
  null,
  null,
  null,
  "!THOUGHT_SIGNATURE"
]
```

Where Part index `10` holds the function call, and index `14` holds the thought signature.

Function parameters and structured output schemas use the following protobuf fields:

| JSON Schema | Field | JSON Schema | Field |
| --- | ---: | --- | ---: |
| `type` | 1 | `format` | 2 |
| `description` | 3 | `nullable` | 4 |
| `enum` | 5 | `items` | 6 |
| `properties` | 7 | `required` | 8 |
| `minProperties` | 9 | `maxProperties` | 10 |
| `minimum` | 11 | `maximum` | 12 |
| `minLength` | 13 | `maxLength` | 14 |
| `pattern` | 15 | `example` | 16 |
| `oneOf` | 17 | `anyOf` | 18 |
| `allOf` | 19 | `not` | 20 |
| `maxItems` | 21 | `minItems` | 22 |
| `propertyOrdering` | 23 | | |

Schema normalization rules:

| Input Structure | Encoded Result |
| --- | --- |
| `$schema`, `default`, `additionalProperties`, `exclusiveMinimum`, `propertyNames`, `prefixItems` | Omitted from the wire schema |
| `type: [T, "null"]` | Root type `T` and `nullable=true` |
| `null` branch in `anyOf` / `oneOf` | Remove `null` branch and set `nullable=true` |
| Multiple non-null `type` values | First item serves as the root type; the full set of types is written to `anyOf` |
| Composite Schema missing a root `type` | First typed branch serves as the root type; `items` of this branch are also written to the root node |
| Other nodes missing `type` | If `properties` is present, type is object; if `items` or `prefixItems` is present, type is array; otherwise, string |
| Array missing `items` | Typed items in `prefixItems` form `anyOf`; if absent, defaults to string |
| Other Schema fields | Returns `400 invalid_request` / `INVALID_ARGUMENT` |

The AI Studio web protocol uses automatic function calling: `auto` requests carry only function declarations in root field 7, leaving the invocation decision to the model; `none` omits tools. Client tool choice mappings are as follows:

| Public Protocol | Accepted | Returns 400 |
| --- | --- | --- |
| OpenAI Chat / Responses | Default, `auto`, `none` | `required`, named function |
| Anthropic | Default, `auto`, `none` | `any`, named `tool` |
| Gemini | Default, `AUTO`, `NONE` | `ANY`, `allowedFunctionNames` |

The function call response Part is `[name, Struct, callId?]`; the next turn's function result uses the same shape and carries back the thought signature as-is. When a tool result explicitly provides a function name, that value is preserved; when the name is missing, it is first associated by call ID with pending calls from the current turn that have not yet returned results; if unmatched and only one call remains, its name is used. Each result corresponds to one call; assistant text between calls and results does not affect the association; associations are re-established when a new round of normal conversation begins. When ambiguity exists or call records are missing, an invalid argument error is returned. Function arguments and results use JSON objects; scalar or array results are wrapped as `{"result":<VALUE>}`.

### Drive Uploads and File Parts

```text
GenerateAccessToken ["users/me"]
  -> response ["<BEARER_TOKEN>"]
  -> POST Drive multipart/related
       part 1: {"mimeType":"<MIME>","name":"<NAME>"}
       part 2: raw bytes
  -> {"id":"<FILE_ID>"}
  -> GenerateContent Part field 6 ["<FILE_ID>"]
```

Drive tokens, uploads, and downloads use fixed egress endpoints belonging to the file's owning account. File IDs bound to accounts are written to `runtime-state.json`; generation requests can combine files from different accounts, where files from other accounts are temporarily copied to the account used for the current generation, and the copies are reclaimed after the request completes.

Inline attachments in generation requests are preferentially uploaded to the account used for the current generation, and the request body uses Drive file Parts. When `GenerateAccessToken` explicitly returns `401`, Code 16, and `OAuth error: unauthorized_client`, the body retains the original inline data Part, and the account continues participating in standard generation scheduling; other token errors maintain failure semantics. Uploads use the MIME type and raw byte content output by public adapters; inline GIFs have their first frame extracted and encoded as `image/png` at the adapter layer. Support for attachments such as images, audio, video, and PDFs is determined by the selected model. Plain text and external YouTube media retain their respective Part encodings.

The OpenAI file endpoint receives `multipart/form-data` with `file` and `purpose`, with a single-file limit of 512 MiB. Requests with unknown content length use Drive resumable uploads with 8 MiB chunking; once uploaded, `POST /v1/files` returns a persistent file object, and `GET /v1/files/{id}` reads the filename, size, purpose, and creation time from the resource binding. Client cancellations abort the upload and release the account lease.

### Gemini 3.5 Transcribe

`gemini-3.5-transcribe` uses Drive file Parts and GenerateContent generation config field 32. The transcription configuration subfields are as follows:

| protobuf field | Content |
| ---: | --- |
| 5 | Word timestamps, enabled with value `1` |
| 6 | Speaker labels, enabled with value `1` |
| 7 | Repeated custom vocabulary |
| 8 | Repeated language codes |
| 9 | Smart transcription, enabled with value `2` |

Transcription metadata resides in response Part field 23, where subfield 1 is text, 2 is speaker label, and 3 is repeated timestamp spans. Field 2 and field 3 of each span represent the start and end times, respectively; duration/timestamp messages use seconds and nanos.

`POST /v1/audio/transcriptions` accepts audio, MP4, or WebM files up to 512 MiB, with public formats including `json`, `text`, `verbose_json`, and `diarized_json`. Each account attempt creates a temporary Drive file, which is deleted on the same account once generation completes. Combining `smart_transcription` with explicit word timestamps or speaker labels returns a `400 invalid_request`.

### Nano, TTS, and Lyria

These three media types reuse `GenerateContent`:

| Route | generation config | Response |
| --- | --- | --- |
| Nano image | modalities `[2]`, image config `[aspectRatio?, imageSize?]` | Part field 3 `[mime, base64]` |
| TTS | modalities `[3]`, speech config | Part field 3 audio chunks |
| Lyria | modalities `[3]` | Part field 3 audio chunks |

Single-voice speech config is `[[[voiceName]]]`. Multi-speaker speech config is `[null,null,[null,[[speaker,[[voiceName]]],...],mode?]]`, where mode `1` is `VERBATIM` and `2` is `CONVERSATIONAL`. Text Part field 41 is SpeechMetadata `[speaker?, style?]`; TTS models with capability code 85 require every text Part in a multi-speaker request to include a speaker, and transcript lines must not contain `## Transcript:`. Adjacent audio Parts with matching MIME types are concatenated in order of arrival. Image aspect ratio, image resolution, and TTS voice must be selected from the current model's capability options.

### Veo

`GenerateVideo` uses an 8-slot array, with the WAA proof located in field 8:

```json
[
  "models/<MODEL_ID>",
  "<PROMPT>",
  [1, "<ASPECT_RATIO>", ["<SECONDS>"], "<RESOLUTION>"],
  ["<IMAGE_MIME>", "<BASE64>"] | null,
  ["<DRIVE_FILE_ID>"] | null,
  null,
  null,
  "<WAA_PROOF>"
]
```

The image source oneof for the starting frame is either an inline image or a Drive file. Field 1 of the creation response is the operation ID. Polling requests are `["<OPERATION_ID>"]`; field 1 of the polling response is `done`, and the artifact Drive file ID is located at `$[1][0][0][0]`. Both the operation and resulting file are bound to the creating account, and the media is subsequently downloaded via the Drive bearer token. Count, aspect ratio, duration in seconds, and resolution are validated against real-time model field 71.

### Omni Interaction

Models with catalog field 79 type `2` that are not background tasks (`gemini-omni-1.1-flash`, `gemini-omni-flash-preview`) do not accept `GenerateContent`. For such models, all four public generation APIs switch to `CreateInteractionStream`. Account selection, Worker, and WAA remain identical to GenerateContent; the WAA proof is located in field 5, and its binding is all transmitted text joined by spaces:

```json
[1, 1, null, <INTERACTION>, "<WAA_PROOF>", 1]
```

| Interaction Index | Content |
| ---: | --- |
| 6 | System instruction |
| 17 | `["models/<MODEL_ID>", [null,null,null,null,null,<THINKING_LEVEL>,1,<MAX_OUTPUT_TOKENS>]]`; config index 24 is `[]` when there is no model output in history |
| 26 | `[[<STEP>...]]`; user steps are `[[<CONTENT>...]]`, model steps are `[null,[<CONTENT>...]]`; text content is `[[<TEXT>]]`, Drive attachment content is `[null×8,["<DRIVE_FILE_ID>"]]` |
| 53 | Video output config `[[[null,null,null,[null,null,null,null,null,1]]]]` |

The thinking level values are MINIMAL=1, LOW=2, MEDIUM=3, and HIGH=4, converted from `reasoning_effort` or the thinking budget based on the levels supported by the model, defaulting to the catalog default level. Interaction requests accept text content from user and assistant, as well as image and video attachments from the user; inline attachments are first uploaded to the selected account's Drive and then referenced by file ID (file IDs do not participate in binding). Audio attachments are rejected upstream with code 3 `Audio input modality is not enabled for this model`, which maps to HTTP 400; functions, tools, and stop sequences return invalid argument errors; `temperature`, `topP`, `topK`, and `seed` are not sent.

The response is `[[<EVENT>...], <STATUS>?]`. In the content delta at event index 10, field 1 is the main text, field 5 is video `[1,"<BASE64 MP4>"]`, and field 6 is the thought summary, mapping to text, `video/mp4` media, and reasoning events, respectively; the final interaction status `3` at index 19 yields usage (prompt, completion, thinking, and total tokens reside at usage indices 0, 4, 8, and 9) and a `stop` terminal state, while statuses `4` and `5` return errors. The `google.rpc` status following the event list maps to HTTP status codes by its code (quota code 8 maps to 429); errors occurring before the first text event trigger account rotation and cooldown, while subsequent errors terminate the stream with an in-stream error.

### Build Proxy

The official Build web app calls the MakerSuite proxy RPC via the host page to access the Gemini API, with quota tracked separately from Playground. When `UPSTREAM_CHANNELS` enables `build`, generation requests are encoded into Gemini API JSON on the Build channel:

```json
["/v1beta/models/<MODEL_ID>:streamGenerateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>"]
["/v1beta/models/<MODEL_ID>:generateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>", "POST"]
```

The former is dispatched to `ProxyStreamedCall`, and the latter to `ProxyUnaryCall`. The WAA proof resides in field 3, with the binding defined as the SHA-256 of the path and request body joined by a space. The entitlement header `X-AIStudio-G1-Tier` is only sent with `ProxyUnaryCall`; models requiring subscription entitlements are generated via `ProxyUnaryCall`, and the rest via `ProxyStreamedCall`. For model catalog, qualifications, scheduling and cooldown, request field mappings, response decoding, error mappings, and out-of-scope features, see [Build Channel](build.md).

### Live and Robotics WebSocket

`GET /v1/live` and `GET /v1/robotics/stream` upgrade to WebSocket. The first client JSON sent after the connection is established must be a setup message:

```json
{"type":"setup","model":"gemini-3.1-flash-live-preview","input_modalities":["text"],"output_modalities":["audio"],"tools":[{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"session_token":""}
```

Live input modalities can consist of text, audio, and image, while output modalities are `["audio"]` or `["text"]`, determined by model capabilities: real-time translation models with capability code 46 output audio and require `translation`; real-time transcription models with capability code 77 output text and may include `transcription`; other Live models output audio. Robotics input and output are both text. Both endpoints use the client-provided model, and the `bidiGenerateContent` method from the real-time model catalog selects the account. The client must send the initial setup frame within 10 seconds of WebSocket establishment; upstream handshake, backchannel readiness, and setup completion share `INIT_TIMEOUT`, after which `session_opened` and `setup_complete` are sent sequentially.

The upstream WebChannel consists of a handshake, forward POSTs, a long-polling backchannel, and termination. The handshake query uses `VER=8`, a random `RID`, `CVER=22`, `X-HTTP-Session-Id=gsessionid`, and `count=0`; the response header returns the gsessionid, and the first control envelope is:

```json
[[0,["c","<SID>","",8]]]
```

Query fields for subsequent forward requests are `VER`, `gsessionid`, `SID`, `RID`, `AID`, `zx`, and `t`; form fields are sequentially `count=<N>`, `ofs=<OFFSET>`, and `req0___data__` through `req<N-1>___data__`. Client messages are queued in arrival order; only one forward POST is in flight at any time. Messages arriving while a request is in flight are batched into the next POST, up to a maximum of 25 messages per POST, consistent with the official web client. `audio` and `image` frames return immediately upon enqueueing, whereas `text`, `tool_response`, and `media_end` wait for the ACK of their containing POST; therefore, when `media_end` returns, all preceding media frames have been delivered. If a forward POST fails, both queued and subsequent sends return that error. A successful response is a 3-integer ACK array; the three slots only validate integer shape, and the second slot does not bind to local RID, AID, or ofs. Upon HTTP 200 and a valid ACK, `RID+1` and `ofs+N` are committed; on failure, cancellation, or an invalid ACK, original values are retained.

The backchannel uses `RID=rpc`, the current SID, gsessionid, and AID. Each wire frame consists of a decimal length, LF, and the corresponding JSON text, where the length is counted in UTF-16 code units:

```text
<DECIMAL_UTF16_LENGTH><LF>
<JSON_TEXT>
```

The JSON root value contains repeated `[serverAID,payload]` envelopes. Once the payload is successfully decoded and published in order, the AID is committed as `max(currentAID,serverAID)`. Transient read failures after the initial backchannel is established retain the SID, gsessionid, and committed AID, and trigger a reconnect; initial establishment failures, terminal protocol states, explicit closes, or unrecoverable errors enter the shutdown pipeline. Session states follow `new -> handshaking -> ready -> reconnecting -> ready`, while the teardown path transitions through `closing -> closed`.

Client frames:

| type | Fields | Description |
| --- | --- | --- |
| `text` | `text` | Sends text input |
| `audio` | `mime_type: audio/pcm`, `data` | Sends PCM Base64 |
| `image` | `mime_type: image/jpeg`, `data` | Sends JPEG Base64 |
| `media_end` | None | Ends the current media input |
| `tool_response` | `tool_responses` | Batches function results, preserving the invoked `id` and `name` |
| `close` | None | Closes the logical session |

`tool_response` example:

```json
{"type":"tool_response","tool_responses":[{"id":"call-1","name":"get_weather","content":{"temperature":26}}]}
```

Server events use `session_opened`, `setup_complete`, `text`, `media`, `input_transcription`, `output_transcription`, `interim_input_transcription`, `tool_call`, `tool_call_cancellation`, `interrupted`, `generation_complete`, `turn_complete`, `session_resumption`, `usage`, `go_away`, `provider`, `closed`, and `error`. `tool_call` carries a single function call; when an upstream message contains multiple calls, multiple events are dispatched sequentially. `tool_call_cancellation.tool_call_ids` carries the IDs of cancelled calls. A new `session_resumption.session_token` atomically replaces the previous token; subsequent setup messages carrying this token bind to the original account for resumption.

Live pure text uses model scope, while Live audio/image and Robotics use the `bidi-media:<modelID>` scope. Upstream Code 7 is returned via an `error` event and terminates the current connection. The maximum client frame size is 8 MiB; exceeding this limit triggers WebSocket close code `1009`.

The upstream Bidi client wire format uses 6-slot or 7-slot sparse arrays. Setup is located at outer field 7:

| Setup JSON Index | Protobuf Field | Content |
| ---: | ---: | --- |
| 0 | 1 | `models/<MODEL_ID>` |
| 1 | 2 | generation configuration |
| 2 | 3 | tools |
| 6 | 7 | `[sessionToken]`; empty array `[]` for Live dialogue and Robotics when no token is present; omitted for real-time translation and transcription |
| 7 | 8 | `[104857,[52428]]` buffering parameters; omitted for real-time translation and transcription |
| 9 | 10 | Input audio transcription parameters; empty array, with real-time transcription language written at sub-index `7` |
| 10 | 11 | Fixed empty array |
| 15 | 16 | Timezone `[null,null,null,null,[zone]]` |

The configuration for Live dialogue and Robotics uses an 18-slot array:

| JSON Index | Content |
| ---: | --- |
| 14 | Output modalities; TEXT=`[1]`, AUDIO=`[3]` |
| 15 | Live voice `[[["Zephyr"]]]` |
| 16 | thinking `[1,null,null,level]`; Live Minimal=`4`, Robotics High=`3` |
| 17 | MediaResolution fixed value `2` |

Real-time translation models do not accept MediaResolution and use a 31-slot configuration: index 13 is `0`, index 14 is `[3]`, and index 30 is TranslationConfig `[echoTargetLanguage 0/1, targetLanguageCode]`. Real-time transcription models accept TEXT output only, with a configuration of `[null×14,[1]]`. Neither includes voice or thinking. The upstream returns input and translated text at serverContent indices 5 and 6; for real-time transcription, index 10 returns the current accumulated interim input transcription (mapped to `interim_input_transcription`), and index 5 returns the final input transcription after audio ends.

Minimal outer setup shape:

```json
[
  null, null, null, null, null, null,
  [
    "models/<MODEL_ID>",
    [null, null, null, null, null, null, null, null, null, null, null, null, null, null, [3], [[["Zephyr"]]], [1, null, null, 4], 2],
    null,
    null, null, null,
    [],
    [104857, [52428]],
    null,
    [],
    [],
    null, null, null, null,
    [null, null, null, null, ["Asia/Taipei"]]
  ]
]
```

Subsequent client wire frames:

| Public Frame | Outer Position | Sub-message |
| --- | --- | --- |
| `text` | index 2 / field 3 | Realtime input index 4 stores text |
| `audio` | index 2 / field 3 | Realtime input index 1 stores `[mime,base64]` |
| `image` | index 2 / field 3 | Realtime input index 3 stores `[mime,base64]` |
| `media_end` | index 2 / field 3 | Realtime input index 2 is `1` |
| `tool_response` | index 3 / field 4 | Sub-message index 1 stores repeated `[name,Struct,id]` |

Setup and each subsequent client wire message write a fresh WAA proof into outer index `5` / field `6` prior to transmission. Snapshot binding inputs:

| Wire | Binding Prompt |
| --- | --- |
| setup | `models/<MODEL_ID>`, followed by each `name + " " + description` appended in declaration order, separated by a single space |
| text, audio, image, media end | Empty string |
| tool response | Call ID of the first function response |

Server business message indices:

| JSON Index | Content |
| ---: | --- |
| 1 | setup complete |
| 2 | server content |
| 3 | tool calls |
| 4 | tool cancellation |
| 5 | usage raw |
| 6 | go away raw |
| 7 | session resumption |

server content indices `0/1/2/4/5/6` correspond to model content, turn complete, interrupted, generation complete, input transcription, and output transcription, respectively; index `0` of model content is repeated Part. Transcription sub-indices `0/1/2/3` represent text, finished, duration milliseconds, and language code, respectively. Tool calls are located at sub-index `1` of message index `3`, where each item is `[name,Struct,id]`; tool cancellation is located at sub-index `0` of message index `4`, whose value is repeated call IDs. Session resumption sub-indices `0/1` are token and resumable, respectively. The object state error schema is `{"__sm__":{"status":[[[code,message]]]}}`; string payloads `"noop"`, `"close"`, and `"stop"` represent no-op, normal close, and error stop, respectively.

## 7. Public Endpoints, State Mappings, and Implementation

| Protocol | Endpoints |
| --- | --- |
| OpenAI Chat | `GET /v1/models`, `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| OpenAI Media | `POST /v1/images/generations`, `POST /v1/audio/speech`, `POST /v1/videos`, `GET /v1/videos/{id}`, `GET /v1/videos/{id}/content` |
| Anthropic | `POST /v1/messages`, `POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`, `GET /v1beta/models/{model}`, `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens`, `:predictLongRunning`, `GET /v1beta/operations/{id}` |

Extended endpoints:

| Protocol | Endpoints |
| --- | --- |
| OpenAI Files | `POST /v1/files`, `GET /v1/files/{id}`, `GET /v1/files/{id}/content`, `DELETE /v1/files/{id}` |
| OpenAI Transcriptions | `POST /v1/audio/transcriptions` |
| Realtime WebSockets | `GET /v1/live`, `GET /v1/robotics/stream` |

The registration shapes for dynamic routes are `GET /v1/files/{file}`, `GET /v1/files/{file}/content`, `DELETE /v1/files/{file}`, `GET /v1/videos/{video}`, `GET /v1/videos/{video}/content`, `POST /v1beta/models/{action}`, and `GET /v1beta/operations/{operation}`; `{id}` in the endpoint tables denotes the corresponding resource identifier.

Public `/v1` and `/v1beta` endpoints accept `Authorization: Bearer`, `X-API-Key`, `X-Goog-API-Key`, or `?key=`, with resolution precedence: `?key=`, `X-Goog-API-Key`, `X-API-Key`, `Authorization: Bearer`. When the configuration is empty, local API key validation is disabled; in this case, HTTP/HTTPS web page requests where `Origin` is `null` or a non-localhost, non-loopback address return 401, while clients without an `Origin` header and other URI schemes remain unrestricted. `/v1*` responses allow any origin, permitting `GET/POST/PUT/DELETE/OPTIONS` and headers: `Authorization`, `Content-Type`, `X-API-Key`, `X-Goog-API-Key`, `Anthropic-Version`, and `Anthropic-Beta`. The maximum `/v1*` request body size is approximately 684 MiB, accommodating Base64-encoded 512 MiB files.

When `ADMIN_AUTH_ENABLED=false`, the `/api` control plane requires the source address to be loopback and the `Host` to be localhost or a loopback address. Once authentication is enabled, access is granted via cookie-backed sessions issued upon providing the admin username and password. Administrative requests enforce same-origin validation and include `Cache-Control: no-store`. All responses include `X-Frame-Options: DENY`, `Content-Security-Policy: frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`. `GET /health` returns `{"status":"ok"}`.

`POST /api/auth/login` accepts `{"username":"<ADMIN_USERNAME>","password":"<ADMIN_PASSWORD>"}` and returns `{"enabled":true,"authenticated":true,"username":"<ADMIN_USERNAME>"}` upon success. The session cookie is `aistudio_admin`, with Path `/api`, `HttpOnly`, and `SameSite=Strict`; `Secure` is appended under HTTPS or when the proxy provides `X-Forwarded-Proto: https`, with a 12-hour expiration. `GET /api/auth/session` returns the same status structure, with an empty username when unauthenticated. `POST /api/auth/logout` returns 204, invalidating the current session and aborting associated administrative requests. Failed logins return 401; after 5 consecutive failures from the same source within 1 minute, the endpoint returns 429 with `Retry-After: 60`.

| Control Capability | Endpoints |
| --- | --- |
| Admin Authentication | `GET /api/auth/session`, `POST /api/auth/login`, `POST /api/auth/logout` |
| Status & Models | `GET /api/status`, `GET /api/models` |
| Generation Service | `POST /api/control/start`, `POST /api/control/stop` |
| Accounts | `GET /api/accounts`, `POST /api/accounts`, `GET/POST /api/accounts/import/chrome`, `PUT /api/accounts/{id}`, `DELETE /api/accounts/{id}` |
| Account Authentication | `POST /api/accounts/{id}/login`, `POST /api/accounts/{id}/verify` |
| Configuration | `GET /api/config` |
| Cooldowns & Requests | `GET /api/cooldowns`, `GET /api/requests`, `POST /api/requests/{id}/cancel` |
| Logs & Events | `DELETE /api/logs`, `GET /api/events` |

The admin API uses DTOs (Data Transfer Objects) to represent request, response, and event payloads. Endpoint responses:

| Endpoint | Success Status | Body |
| --- | ---: | --- |
| `GET /api/status` | 200 | `AdminStatus` |
| `GET /api/models` | 200 | `{"models":[Model,...]}` |
| `GET /api/accounts` | 200 | `{"accounts":[AdminAccount,...]}` |
| `POST /api/accounts` | 201 | `{"account":AdminAccount}` |
| `GET /api/accounts/import/chrome` | 200 | `{"profiles":[ChromeImportProfile,...]}` |
| `POST /api/accounts/import/chrome` | 201 | `{"accounts":[AdminAccount,...]}` |
| `PUT /api/accounts/{id}` | 200 | `{"account":AdminAccount}` |
| `POST /api/accounts/{id}/login`, `verify` | 200 | `{"account":AdminAccount}` |
| `DELETE /api/accounts/{id}` | 204 | Empty body |
| `POST /api/control/start`, `stop` | 200 | `AdminStatus` |
| `GET /api/config` | 200 | `RuntimeConfig` |
| `GET /api/cooldowns` | 200 | `{"cooldowns":[AdminCooldown,...]}` |
| `GET /api/requests` | 200 | `{"requests":[AdminRequest,...]}` |
| `POST /api/requests/{id}/cancel` | 204 | Empty body |
| `DELETE /api/logs` | 204 | Empty body |
| `GET /api/events` | 200 SSE | `{"type":"<TYPE>","data":<DTO>}` |

Admin DTO fields:

| DTO | Fields |
| --- | --- |
| `AdminStatus` | `state`, `running`, `ready`, `version`, `active_requests`, `accounts` |
| `AdminAccountCounts` | `total`, `ready`, `busy`, `cooldown`, `auth_required` |
| `AdminAccount` | `id`, `label`, `enabled`, `state`, `proxy`, `locale`, `timezone`, `models`, `benefit_tier`, `message` |
| `AccountCreateInput` | `proxy`, `locale`, `timezone` |
| `AccountInput` | `label`, `enabled`, `proxy`, `locale`, `timezone` |
| `ChromeImportProfile` | `id`, `profile`, `display_name`, `email`, `locale` |
| `ChromeImportInput` | `account_ids`, `proxy`, `locale`, `timezone` |
| `AdminCooldown` | `account_id`, `account_label`, `model_id`, `until`, optional `reason` |
| `AdminRequest` | `id`, `model`, `account_id`, `account_label`, `state`, `started_at` |
| `AdminLog` | `time`, `level`, `source`, `message`, `event`; request events include `request`, which contains `id`, `state`, HTTP `status`, `model`, `duration_ms`, `tool_calls`, `usage`, and diagnostic fields; field specifications are detailed in [logging.md](logging.md) |
| `AdminEvent` | `type`, `data` |

`AdminStatus.state` can be `STOPPED`, `LAUNCHING`, or `RUNNING`; `running` is true only when in `RUNNING` state; `ready` requires `RUNNING` and at least one account in ready or busy state; `version` is sourced from build metadata; `active_requests` represents the number of active requests in the current process registry. `AdminAccount.message` records the reason for the current state, and `models` lists the real-time catalog IDs available to that account. `until` and `started_at` use RFC 3339 JSON timestamps.

The Chrome import list enumerates accounts individually based on the Gaia ID and email in `Preferences.account_info`. A single profile may contain multiple accounts, and each email is listed only once. `ChromeImportProfile.id` is formatted as `<Profile>/<Gaia ID>`; the import reads credentials from `token_service` where the service name is `AccountId-<Gaia ID>`. In the admin UI, all accounts in the list are unchecked by default, and a "Select All" option is provided. The CLI `--profile` flag imports all accounts under the specified profile, with interactive numbered options selecting individual accounts.

`AccountCreateInput` launches an isolated Camoufox instance for login, reading the email address directly from the AI Studio page. `ChromeImportInput.account_ids` allows selecting multiple accounts simultaneously. `AccountInput.label` must match the immutable Google email ID, `locale` and `timezone` must not be empty, and `proxy` must be an HTTP, HTTPS, or SOCKS5 origin without credentials, path, query, or fragment. Upon successful creation, import, login, or verification, the account's model catalog is immediately refreshed, and the latest account and model events are published.

The commit sequence for `PUT /api/accounts/{id}` is strictly ordered: validate the immutable email ID, acquire an exclusive lease on the account, create a new unpromoted egress, shut down the current worker and mark the new worker configuration as `pending` (unpromoted), atomically write `account.json` and update the account pool within a model catalog write lock, and subsequently promote the worker configuration, swap the egress, release the lease, and rebuild the model cache. Writing `account.json` is the sole point of durable commit. Any errors during egress creation, worker shutdown, or file write prior to commit discard the pending configuration and preserve the old configuration; any already-terminated worker is reconstructed according to the old configuration on subsequent requests. Once durably written, the new account configuration, worker configuration, and egress collectively transition to the committed state. A failure during lease release maintains this committed state while returning the original unlock error; a successful release logs completion and synchronizes the model cache.

`RuntimeConfig` fields. `response-only` indicates that the field is populated by the server and client-submitted values are not persisted:

| Field | Read/Write & Effective Timing |
| --- | --- |
| `auth_states`, `proxy`, `init_timeout`, `request_timeout` | Persisted; takes effect on next generation service start |
| `warm_worker_limit`, `max_active_workers`, `warm_startup_concurrency`, `per_account_concurrency` | Persisted; takes effect on next generation service start |
| `temporary_chat` | Persisted; takes effect on next generation service start |
| `build_native_nonstream` | Persisted; determines whether non-streaming requests prioritize Build on next generation service start |
| `admin_auth_enabled`, `admin_username` | Persisted; takes effect on next management process start |
| `admin_password` | Write-only; preserves current value if omitted, takes effect on next management process start |
| `admin_password_set` | Response-only; indicates whether an admin password has been set |
| `listen_addr`, `proxy_api_key` | Persisted; takes effect on next management process start |
| `active_listen_addr`, `active_proxy_api_key` | Response-only; static value for current management process |
| `management_restart_required` | Response-only; set when persisted listen address, API key, or admin credentials differ from the running management process |
| `service_restart_required` | Response-only; set when persisted generation service config differs from the running generation service instance |

`GET /api/config` returns the current runtime configuration (read-only). Configuration is loaded at startup from environment variables or `.env`, and runtime modification via the API is not supported.

The initial event replay order for `GET /api/events` is `status`, `models`, `accounts`, the latest 200 `log` entries, `cooldowns`, and active `request` items sorted by start time. Subsequent event `data` payloads conform to:

| `type` | `data` |
| --- | --- |
| `status` | `AdminStatus` |
| `models` | `{"models":[Model,...]}` |
| `accounts` | `{"accounts":[AdminAccount,...]}` |
| `log` | `AdminLog` |
| `cooldowns` | `[AdminCooldown,...]` |
| `request` | `AdminRequest`, re-emitted with the same ID upon state transitions |

Administrative errors are standardized as:

```json
{"error":{"code":"invalid_request","message":"..."}}
```

Control plane error codes include `control_plane_forbidden`, `control_plane_origin_forbidden`, `invalid_request`, `invalid_account`, `account_not_found`, `account_busy`, `account_required`, `request_not_found`, and `upstream_error`.

| HTTP | Control Error Code |
| ---: | --- |
| 400 | `invalid_request`, `invalid_account`, `account_required` |
| 403 | `control_plane_forbidden`, `control_plane_origin_forbidden` |
| 404 | `account_not_found`, `request_not_found` |
| 409 | `account_busy` |
| Upstream status or 502 | `upstream_error` |

The runtime state machine operates as follows. Catalog fan-out denotes concurrent `ListModels` calls to all eligible accounts; pending accounts are those awaiting the next round of catalog retries:

```text
process start
  -> control plane ready
  -> STOPPED

POST /api/control/start
  -> LAUNCHING
  -> load CachedModels
  -> fan out ListModels to every enabled ready/busy account
  -> if cache is empty, wait for the first non-empty live catalog
  -> prewarm up to WARM_WORKER_LIMIT workers
     with WARM_STARTUP_CONCURRENCY bootstraps
  -> first worker ready
  -> RUNNING
  -> continue full catalog fan-out and remaining worker prewarm in background
  -> every 30s, fan out ListModels to every pending account

request
  -> resolve model and endpoint capability
  -> acquire one PER_ACCOUNT_CONCURRENCY slot
  -> prepare WAA proof
  -> send MakerSuite RPC
  -> stream frames
  -> release slot

POST /api/control/stop
  -> cancel launch or active requests
  -> bound catalog fan-out shutdown to 2s
  -> close WAA workers
  -> bound unfinished lifecycle-transition wait to 12s
  -> STOPPED
```

Administrative status fields use uppercase service states: `STOPPED`, `LAUNCHING`, and `RUNNING`. Request states use `queued`, `running`, `completed`, `cancelled`, and `failed`. Video states use `queued`, `completed`, and `failed`, corresponding to progress values of `0` or `100`.

The model catalog resides in memory for the duration of the current generation (`runtimeGeneration`, representing a single generation service instance); a new generation created by a clean Stop/Start sequence starts with an empty cache. At startup, the service establishes a public snapshot using `catalog.CachedModels()` from the current generation, immediately spawning a background `ListModels` task for each `enabled` account in `ready` or `busy` state. If the current generation's cache is non-empty, worker prewarming begins immediately. As each non-empty result arrives, it is merged into the public catalog from `CachedModels`, and updated `accounts` and `models` events are published; if the service is already `RUNNING`, this also triggers worker prewarming. The first non-empty result in a new generation unblocks the startup wait phase. When the full-account fan-out completes, an updated account and model snapshot is re-emitted if the `auth_required` set has changed, logging the number of successful syncs, non-empty responses, public models, pending retry accounts, and elapsed time. Once the first worker is ready, the current cache is re-evaluated, and the service transitions to `RUNNING` while holding the account and configuration mutation lock.

Account states:

| State | Scheduling Semantics |
| --- | --- |
| `ready` | Credentials valid with available concurrency slots |
| `busy` | Account is undergoing an exclusive operation, auth refresh, or has active requests; scheduling still evaluates remaining slots against `PER_ACCOUNT_CONCURRENCY` |
| `cooldown` | Global `*` cooldown for the account is active; model-scoped cooldowns only affect candidate routing for the matching request |
| `auth_required` | Account-level authentication failure |
| `unavailable` | Currently unavailable in the runtime |
| `disabled` | Disabled via configuration |

Auth state updates carry `authGeneration` and `checkedAt`, applying only to results where the account object, `authGeneration` value, and timestamp match the current state; in the event of identical timestamps, `ready` takes precedence as the final state. Model access and cooldown updates carry `modelAccessGeneration` and `checked_at`, applying only when the `modelAccessGeneration` value matches and the timestamp is equal to or newer than the current state; for identical timestamps, `verified` takes precedence as the final state. `verified` indicates that the account has successfully invoked the corresponding model or capability.

`ModelAccessKey(scope, model)` first strips the `models/` prefix; an empty scope returns the canonical model ID, whereas a non-empty scope returns `<scope>:<canonicalModelID>`. Capabilities map to the following keys:

| Capability | Scope | Success Recording |
| --- | --- | --- |
| Standard Generation | `<modelID>` | Marked `verified` upon receiving the canonical `EventFinish` event |
| CountTokens | `count-tokens:<modelID>` | Clears scope cooldown, preserves standard generation eligibility |
| Transcribe | `<modelID>` | Marked `verified` on non-empty text or segments |
| Live Text | `<modelID>` | Marked `verified` on setup complete; each text input initiates a model qualification check |
| Live Audio/Images | `bidi-media:<modelID>` | Media input initiates a media qualification check; Live text uses the standard model scope |
| Robotics | `bidi-media:<modelID>` | Text input initiates a model qualification check |

Code 7 retains the verified status of the current operation scope; authentication failures update the account-level state to `auth_required`. Non-generation failures, such as uploads or temp file cleanup errors, trigger a global `*` cooldown.

Standard streaming generation writes `verified` status once the canonical `EventFinish` event is consumed. Content, reasoning, tool calls, usage, and time-to-first-token are used for output and latency metrics; stream drops, client cancellations, or errors occurring prior to a terminal state preserve the existing verification status.

A successful Bidi setup records the `checkedAt` timestamp of the lease (the account lease held for the session). Each subsequent turn that updates model qualification is assigned a strictly increasing attempt timestamp within the session; `turn_complete` consumes the corresponding attempt and records qualification. Code 7 does not update model qualification. Bidi auth successes and 401 errors follow the same attempt sequence; a late 401 returned after setup can still transition the account to `auth_required`.

Once browser process termination is confirmed, process and worker states are set to `closed`. If shutdown fails, the worker, runtime lease, warm flag, and generation (worker instance version) are retained; subsequent Stop calls will re-execute the worker reset even if the service state is already `STOPPED`. Camoufox teardown phase upper bounds are 3 seconds for BiDi shutdown, 5 seconds for process termination and wait, and 2 seconds for profile deletion; errors across phases are joined and preserved using `errors.Join`.

The model catalog fan-out is bound to the context of the current generation service. Startup failures, startup cancellations, or Stop operations cancel all pending catalog tasks and wait up to 2 seconds to confirm background catalog goroutine exit. When Stop encounters an incomplete `LAUNCHING` state or another active start/stop lifecycle transition, it waits up to 12 seconds on the transition channel (notification channel for transition completion); this upper bound accommodates the 2-second catalog shutdown and bounded worker cleanup. Timeout errors and cleanup errors are returned aggregated via `errors.Join`.

On-demand hot replacement spawns a pending worker (a bootstrapping, unpromoted replacement worker) prior to shutting down the stale worker; the replacement becomes the active worker only after the old instance terminates cleanly. If shutting down the old instance and reclaiming the replacement worker fail simultaneously, both are retained for future cleanup, each occupying an active capacity slot; new worker creation halts once the capacity ceiling is reached. The sequence for a full generation service Stop/Start requires retrying the shutdown of the old instance before Start creates a new generation service; if the old PID fails to terminate, the stop error is returned and the original instance is preserved.

Administrative status is set to `STOPPED`. In this state, generation and token-counting endpoints return `503 service_stopped`. Code 7 does not clear the success status of an account or operation scope. Worker process failures, worker replacements, and protocol Code 5 errors rebuild the current account worker and replay the request once on the same account. When candidate accounts are exhausted and no accounts match the requested method, capability, and entitlement tier, an HTTP 400 is returned: OpenAI code `account_required`, Anthropic type `invalid_request_error`, and Gemini status `INVALID_ARGUMENT`. When all accounts supporting the request require re-authentication, are unavailable, or are disabled, an HTTP 503 is returned listing each account, its state, and the root cause: OpenAI code `account_unavailable`, Anthropic type `api_error`, and Gemini status `UNAVAILABLE`.

The pending retry set for the model catalog maintains account IDs awaiting re-synchronization. Accounts are added during the initial full-account fan-out at startup or during single-account syncs following creation, login, or verification if any error occurs or if an empty catalog is returned; they are removed once a non-empty catalog is retrieved or when the account is deleted. After background sync for all accounts completes, a single 30-second ticker is started. Each tick fans out concurrently to the sorted list of pending accounts, re-verifying at task initiation that the ID remains in the pending set. Accounts producing errors or empty catalogs remain in the set; each non-empty success immediately updates the account cache and public catalog snapshot, emits `accounts` and `models` events, and triggers worker prewarming if in `RUNNING` state. At batch completion, an updated account and model snapshot is emitted if the `auth_required` set changed; on-demand single-account synchronizations immediately emit snapshots regardless of success or failure.

Model catalog projections:

| Rule | Result |
| --- | --- |
| OpenAI | `GET /v1/models` returns the OpenAI model list |
| Anthropic | `GET /v1/models` with an `Anthropic-Version` header returns the Anthropic model list |
| Gemini | Model names use the format `models/<ID>` |
| Multi-account identical models | Union of generation methods and capability options |
| Multi-account token limits | Minimum positive value for input and output limits, respectively |
| Model aliases | Sourced from `ListModels` field 57 |
| Request matching | Candidate pool determined by model ID/alias, method, capability, AccessModes, account entitlements, and runtime state |
| Candidate ordering | Ordered by `verified` status and time-to-first-token for the target model; unverified accounts remain eligible candidates |
| Capability constraints | Satisfies both the endpoint-required capability and the account's real-time capabilities |

Complete admin model schema:

```json
{
  "id": "gemini-example",
  "name": "Gemini Example",
  "description": "...",
  "methods": ["countTokens", "generateContent"],
  "input_token_limit": 1048576,
  "output_token_limit": 65536,
  "capabilities": {"thinking": true, "capability_code_25": true},
  "capability_options": {"aliases": ["gemini-example-latest"]},
  "access_modes": [3, 4],
  "paid": true
}
```

OpenAI `GET /v1/models`:

```json
{
  "object": "list",
  "data": [{
    "id": "gemini-example",
    "object": "model",
    "created": 0,
    "owned_by": "google",
    "name": "Gemini Example",
    "description": "...",
    "supported_generation_methods": ["countTokens", "generateContent"],
    "input_token_limit": 1048576,
    "output_token_limit": 65536,
    "capabilities": {},
    "capability_options": {},
    "access_modes": [],
    "paid": true,
    "channels": ["playground", "build"]
  }]
}
```

When requested with an `Anthropic-Version` header, the same route returns:

```json
{
  "data": [{"id":"gemini-example","type":"model","display_name":"Gemini Example","created_at":"1970-01-01T00:00:00Z"}],
  "has_more": false,
  "first_id": "gemini-example",
  "last_id": "gemini-example"
}
```

Gemini `GET /v1beta/models` returns `{"models":[...]}`, while the single-model route returns an individual object. Fields include `name`, `displayName`, `description`, `supportedGenerationMethods`, `inputTokenLimit`, `outputTokenLimit`, and optional `capabilities`, `capabilityOptions`, `accessModes`, `paid`, and `channels`.

`GET /v1beta/models/{model}` resolves models strictly by canonical model ID; generation, token-counting, video, transcription, and Bidi endpoints accept both canonical IDs and aliases defined in `capability_options.aliases`. Aliases are resolved to their canonical ID prior to scheduling and upstream dispatch.

In the admin model object, `description`, token limits, capabilities, capability options, access modes, and false `paid` flags use `omitempty`. OpenAI and Gemini responses always include model identity, methods, and token limits, appending extended fields when maps/slices are non-empty or when `paid=true`.

The admin catalog aggregates the live upstream model sets across all accounts, sorted by ID. The public catalog exposes models for which at least one enabled account possesses access entitlements and whose call methods are supported by the public protocols. When the Build channel is enabled, it also includes Build-exclusive generative models, with `channels` listing the viable channels for invoking the model, as detailed in [Build Channel](build.md). Transient cooldowns and busy states are managed by request scheduling. When identical model IDs exist across multiple accounts, methods, capabilities, capability options, and access modes are merged as unions, `paid` uses logical OR, and positive token limits adopt the minimum value. Scheduling evaluates upstream methods, capabilities, access modes, account tiers, and the current runtime state.

Primary request formats:

| Endpoint | Required Fields | Primary Result |
| --- | --- | --- |
| `/v1/chat/completions` | `model`, non-empty `messages` | Chat completion or incremental chunks |
| `/v1/responses` | `model`, `input` | Response object or `response.*` events |
| `/v1/files` | multipart `file`, `purpose` | OpenAI file object |
| `/v1/messages` | `model`, non-empty `messages`, `max_tokens` | Anthropic message or message events |
| `:generateContent` / `:streamGenerateContent` | non-empty `contents` | Gemini candidates, usage, and grounding metadata |
| `/v1/images/generations` | `model`, `prompt`, fixed `n=1` | `b64_json` or data URL |
| `/v1/audio/speech` | `model`, `input` | WAV, PCM, or MP3 payload |
| `/v1/audio/transcriptions` | multipart `file`; `model` defaults to `gemini-3.5-transcribe` | Text or transcription JSON |
| `/v1/videos` | `model`, `prompt` | Long-running operation object, followed by polling and content download |

Anthropic assistant prefill is indicated by a trailing `assistant` message. Because AI Studio currently lacks a corresponding generation prefix field, `/v1/messages` rejects this input with `400 invalid_request_error`.

The four generation entry points share a canonical request format, with input parameters mapped as follows:

| Capability | OpenAI Chat | OpenAI Responses | Anthropic | Gemini |
| --- | --- | --- | --- | --- |
| system | `system` / `developer` messages | `instructions` and system/developer message items | `system` string or text blocks | `systemInstruction` text parts |
| text | String or text content part | String, message item | String or text block | Part `text` |
| image/document | Base64 data URL, `file_id` | `input_image`, `input_file` | Base64 source or URL source | `inlineData`, `fileData` |
| audio input | `input_audio` Base64 | `input_audio` in message content | Base64 document source | `inlineData` |
| YouTube | `video_url` / `input_video` | `input_video` | URL source | `fileData.fileUri` |
| function call | Assistant `tool_calls` | `function_call` item | `tool_use` block | `functionCall` Part |
| function result | Tool message | `function_call_output` item | `tool_result` block | `functionResponse` Part |
| structured output | `response_format` | `text.format` | — | `responseMimeType` and response schema |
| thinking | `reasoning_effort` or `reasoning.effort` | `reasoning.effort` | `thinking.budget_tokens`, `output_config.effort` | `thinkingConfig` |

Gemini attachments and image inputs for `predictLongRunning` accept `inlineData` / `inline_data`, `fileData` / `file_data`, `mimeType` / `mime_type`, and `fileUri` / `file_uri`. If duplicate aliases are supplied simultaneously, outer containers prioritize camelCase objects, and inner fields prioritize non-empty camelCase values.

Media Base64 inputs accept standard and URL-safe alphabets, optional `=` padding, and the `data:<MIME>;base64,` prefix. Inline GIF images and OpenAI video `input_reference` form attachments have their first frame extracted and re-encoded to PNG using the logical canvas dimensions and frame offsets before transmission. Transparent first frames preserve transparency; uncovered areas of opaque first frames are filled using the background color from the global color table.

OpenAI Chat and Anthropic drop historical messages that contain no parts after transformation; purely whitespace text, tool calls, tool results, and media retain their original content.

Generation parameter mapping:

| Parameter | Rule |
| --- | --- |
| OpenAI max tokens | `max_completion_tokens` takes precedence over `max_tokens` |
| Anthropic max tokens | `max_tokens` maps to generation config field 4 |
| Gemini max tokens | `maxOutputTokens` maps to generation config field 4 |
| temperature / topP / topK / seed | Map to generation config fields 5 / 6 / 7 / 19 |
| stop sequence | Maps to generation config field 2 |
| stop sequence match | Core protocol matches against the content stream and returns the exact matched sequence |
| structured output | MIME type maps to field 8, Schema maps to field 9 |
| OpenAI Chat `n` | Accepts only omitted or `1` |
| OpenAI Chat `parallel_tool_calls` | Accepts only omitted or `true` |
| OpenAI Chat `logprobs` / `logit_bias` | Accepts only omitted or `false`, and omitted or empty object, respectively |
| OpenAI Chat frequency / presence penalty | Accepts only `0` |
| OpenAI Chat function `strict` | Accepts omitted or `false`; `true` returns `400 invalid_request` |
| Responses `parallel_tool_calls` | Written to response metadata; function calling defaults to AI Studio auto mode |
| Responses `parallel_tool_calls` values | Accepts only omitted or `true` |
| Responses `truncation` | Accepts only omitted or `disabled` |
| Responses function `strict` | Accepts omitted or `false`; `true` returns `400 invalid_request` |
| Responses `store` | When omitted or `true`, persists the session node in the current process; `false` returns only the immediate result |
| Gemini frequency / presence penalty | Accepts only `0` |
| Gemini `candidateCount` | Accepts only omitted or `1` |
| Gemini `responseLogprobs` / `logprobs` | Accepts only omitted or `false`, and omitted or `0`, respectively |
| Gemini `googleSearchRetrieval` | Accepts only an empty object; `dynamicRetrievalConfig` returns `400 INVALID_ARGUMENT` |
| Anthropic `thinking` | `enabled` supplies `budget_tokens`; models supporting thinking budgets map the budget directly, while models supporting only thinking levels map values <= 0, 1024, 8192, and above to minimal, low, medium, and high, respectively; `adaptive` applies default model thinking |
| Anthropic thinking capability | Returns `invalid_request_error` if the model supports neither thinking budgets nor thinking levels; non-streaming calls return HTTP 400, streaming calls return an Anthropic error event |
| Anthropic thinking type | `disabled` and unrecognized types return `400 invalid_request_error` |

### OpenAI Chat Completions

`POST /v1/chat/completions` request fields:

| Field | Type and Semantics |
| --- | --- |
| `model` | Required model ID |
| `messages` | Required non-empty message array |
| `stream` | boolean |
| `stream_options.include_usage` | Send a usage-only chunk after the finish chunk |
| `tools` | Array of functions or Google server tools |
| `tool_choice` | Omitted / `auto` / `none` |
| `web_search_options` | Object; enables Google Search; `search_context_size` and `user_location` return 400 |
| `temperature`, `top_p` | Optional sampling parameters |
| `max_tokens`, `max_completion_tokens` | The latter takes precedence |
| `frequency_penalty`, `presence_penalty` | Omitted or `0` |
| `n` | Omitted or `1` |
| `parallel_tool_calls` | Omitted or `true` |
| `logprobs` | Omitted or `false` |
| `logit_bias` | Omitted, `null`, or empty object |
| `stop` | string or string array; empty strings are removed from the conditions |
| `response_format` | `{type:"text"}`, `{type:"json_object"}`, or `{type:"json_schema",json_schema:{schema}}` |
| `reasoning_effort` | Thinking effort |
| `reasoning.effort` | Nested thinking effort; maps to the same configuration as the top-level field |
| `seed` | 64-bit integer |

Message fields are `role`, `content`, optional `name`, `tool_call_id`, and `tool_calls`. Assistant tool call:

```json
{
  "id": "call_01",
  "type": "function",
  "function": {"name":"get_weather","arguments":"{\"city\":\"Taipei\"}"},
  "extra_content": {"google":{"thought_signature":"<SIGNATURE>"}}
}
```

`content` can be a string or an array of Parts. Part fields:

| `type` | Other Fields |
| --- | --- |
| `text`, `input_text`, `output_text` | `text` |
| `image_url`, `input_image` | `image_url` string or `{"url":"..."}` |
| `video_url`, `input_video` | `video_url` string or `{"url":"..."}` |
| `input_file`, `file` | `file_id`, or `filename` + `file_data` |
| `input_audio` | `input_audio.data`, `input_audio.format` |

When the OpenAI `image_url` / `input_image` value is a Base64 data URL, it forms inline data; when it is a YouTube URL, it forms external media; other non-data strings are resolved as uploaded file IDs. The adapter does not download standard HTTP image URLs. `video_url` / `input_video` only accepts YouTube URLs. `file_data` accepts a Base64 data URL or an uploaded file ID.

Function tools use `{"type":"function","function":{"name","description","parameters","strict"}}`. `strict` can be omitted or set to `false`. Google tool types are `web_search`, `web_search_preview`, `image_search`, `url_context`, `code_interpreter`, and `google_maps`.

Non-streaming response:

```json
{
  "id": "chatcmpl_...",
  "object": "chat.completion",
  "created": 0,
  "model": "gemini-example",
  "provider_model": "gemini-provider-id",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "...",
      "reasoning_content": "...",
      "tool_calls": [],
      "annotations": []
    },
    "finish_reason": "stop",
    "provider_finish_reason": "provider_19"
  }],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 20,
    "total_tokens": 30,
    "completion_tokens_details": {"reasoning_tokens": 5}
  }
}
```

`provider_model`, `provider_finish_reason`, `reasoning_content`, `tool_calls`, `annotations`, and `usage` appear only when corresponding data is present. The annotation structure is `{"type":"url_citation","url_citation":{"url","title","start_index","end_index"}}`.

Chat SSE sequence:

1. Role chunk: `delta={"role":"assistant","content":""}`
2. Body `delta.content`, reasoning `delta.reasoning_content`, tools `delta.tool_calls`, media or code rendering `delta.content`
3. Citations `delta.annotations`
4. Finish chunk: `finish_reason`, optional `provider_finish_reason`
5. When `include_usage=true` and upstream usage is present, a usage-only chunk with `choices:[]` is sent
6. `data: [DONE]`

Each standard chunk is `{id,object:"chat.completion.chunk",created,model,choices:[{index,delta,finish_reason}],usage?}`. Failures occurring after response headers have been sent are emitted as `data: {"error":{"message","type","code"}}`.

### OpenAI Responses

`POST /v1/responses` request fields:

| Field | Type and Semantics |
| --- | --- |
| `model` | Required model ID |
| `input` | string or array of input items |
| `instructions` | Top-level system instructions |
| `stream` | boolean |
| `tools`, `tool_choice` | function, namespace, and Google tools; functions within a namespace are expanded into function declarations, invocation results identify their parent namespace via the `namespace` field, and duplicate function names return 400; choice is `auto`/`none` |
| `temperature`, `top_p`, `max_output_tokens` | Generation parameters |
| `reasoning` | `{"effort":"..."}` |
| `text` | `{"format":{"type":"text|json_object|json_schema","schema":...}}` |
| `previous_response_id` | Previously saved response ID within the current process |
| `parallel_tool_calls` | Omitted or `true` |
| `truncation` | Omitted or `disabled` |
| `metadata` | string-to-string object |
| `store` | Omitted / `true` to persist the node; `false` to return the current result only |

Input item fields are `type`, `role`, `content`, `call_id`, `name`, `arguments`, `output`, and `encrypted_content`. Supports message, `function_call`, `function_call_output`, and reasoning items. Message content Parts:

| type | Fields |
| --- | --- |
| `input_text`, `output_text` | `text` |
| `input_image` | `image_url` string or `{url}` |
| `input_file` | `file_id`, or `filename` + `file_data` |
| `input_audio` | `input_audio:{data,format}` |
| `input_video` | `video_url` string or `{url}` |

Image, video, and file Parts in Responses reuse the data URL, file ID, and YouTube rules described above.

Responses tool fields:

| tool type | Fields |
| --- | --- |
| `function` | `name`, `description`, `parameters`, `strict` |
| `web_search`, `web_search_2025_08_26`, `web_search_preview`, `web_search_preview_2025_03_11` | Only accepts `type`; `search_context_size`, `user_location`, and `filters` must be omitted |
| `image_search`, `url_context`, `google_maps` | `type` |
| `code_interpreter` | `container` can be omitted, `"auto"`, or `{"type":"auto","file_ids":[]}`; non-empty `file_ids` returns 400 |

The response shell fields are always present:

```json
{
  "id": "resp_...",
  "object": "response",
  "created_at": 0,
  "completed_at": null,
  "status": "in_progress",
  "error": null,
  "incomplete_details": null,
  "instructions": null,
  "metadata": {},
  "model": "gemini-example",
  "output": [],
  "output_text": "",
  "parallel_tool_calls": true,
  "previous_response_id": null,
  "reasoning": null,
  "temperature": null,
  "text": {"format":{"type":"text"}},
  "tool_choice": "auto",
  "tools": [],
  "top_p": null,
  "truncation": "disabled",
  "max_output_tokens": null,
  "usage": null
}
```

Completed objects may additionally include `provider_model` and `provider_finish_reason`. `status` is `completed`, `incomplete`, or `failed`; length-based terminal states use `incomplete_details.reason=max_output_tokens`, and policy-based terminal states use `content_filter`.

Output item union types:

| type | Fields |
| --- | --- |
| `reasoning` | `id`, `status`, `summary:[{type:"summary_text",text}]`, optional `encrypted_content` |
| `message` | `id`, `status`, `role:"assistant"`, `content:[{type:"output_text",text,annotations}]` |
| `function_call` | `id`, `status`, `call_id`, `name`, `arguments` |
| `code_interpreter_call` | `id`, `status`, `code`, `container_id:"aistudio"`, `outputs:[{type:"logs",logs}]` |
| `image_generation_call` | `id`, `status`, Base64 `result` |
| `web_search_call` | `id`, `status`, `action` |

`web_search_call.action` is `{"type":"search","query":"...","sources":[{"type":"url","url":"..."}]}`, where queries are deduplicated by their first appearance and sources are deduplicated by URI. `code_interpreter_call.status` is `incomplete` when there are no results, `completed` on success, and `failed` for non-`OUTCOME_OK` outcomes; stdout is written to `outputs[].logs`, and failure text is prefixed with `stderr:`. Streaming `output_item.added` uses `in_progress`, and the corresponding `output_item.done` uses the final status.

Responses usage:

```json
{
  "input_tokens": 10,
  "output_tokens": 20,
  "total_tokens": 30,
  "input_tokens_details": {"cached_tokens":0},
  "output_tokens_details": {"reasoning_tokens":5}
}
```

Every Responses SSE payload contains `type` and a monotonically increasing `sequence_number` starting from 0:

| Event | Event Fields |
| --- | --- |
| `response.created`, `response.in_progress` | `response` shell |
| `response.output_item.added`, `response.output_item.done` | `output_index`, `item` |
| `response.reasoning_summary_part.added`, `response.reasoning_summary_part.done` | `item_id`, `output_index`, `summary_index`, `part` |
| `response.reasoning_summary_text.delta` | `item_id`, `output_index`, `summary_index`, `delta` |
| `response.reasoning_summary_text.done` | Indices listed above and `text` |
| `response.content_part.added`, `response.content_part.done` | `item_id`, `output_index`, `content_index`, `part` |
| `response.output_text.delta` | `item_id`, `output_index`, `content_index`, `delta`, `logprobs:[]` |
| `response.output_text.done` | Indices listed above, `text`, `logprobs:[]` |
| `response.output_text.annotation.added` | `item_id`, `output_index`, `content_index`, `annotation_index`, `annotation` |
| `response.function_call_arguments.delta` | `item_id`, `output_index`, `delta` |
| `response.function_call_arguments.done` | `item_id`, `output_index`, `arguments`, `name` |
| `response.image_generation_call.in_progress`, `response.image_generation_call.completed` | `item_id`, `output_index` |
| `response.code_interpreter_call.in_progress`, `response.code_interpreter_call.interpreting`, `response.code_interpreter_call.completed` | `item_id`, `output_index` |
| `response.code_interpreter_call_code.delta` | `item_id`, `output_index`, `delta` |
| `response.code_interpreter_call_code.done` | `item_id`, `output_index`, `code` |
| `response.web_search_call.in_progress`, `response.web_search_call.searching`, `response.web_search_call.completed` | `item_id`, `output_index` |
| `response.completed`, `response.incomplete`, `response.failed` | Full `response` |

When a web search occurs, the search call item precedes the message; if there is no grounding query, only the message is output. Deltas generated prior to `response.failed` retain their original ordering.

### Anthropic Messages

`POST /v1/messages` request fields:

| Field | Type & Semantics |
| --- | --- |
| `model` | Required model ID |
| `messages` | Required non-empty array of `{role,content}`; role can be `user`, `assistant`, or `system`; `system` messages are sent in-place as user content wrapped in `<system-reminder>` |
| `system` | string or array of text blocks |
| `max_tokens` | Required positive integer |
| `stop_sequences` | string array |
| `stream` | boolean |
| `temperature`, `top_p`, `top_k` | Generation parameters |
| `tools`, `tool_choice` | custom/server tools and auto/none |
| `thinking` | `{type:"enabled",budget_tokens:<INT>}` or `{type:"adaptive"}` |
| `output_config` | `{effort:"..."}` |

Message content can be a string or an array of blocks:

| Block Type | Fields |
| --- | --- |
| `text` | `text` |
| `thinking` | `thinking`, `signature` |
| `redacted_thinking` | `data` |
| `image`, `document` | `source:{type,media_type,data,url}` |
| `tool_use` | `id`, `name`, object `input` |
| `tool_result` | `tool_use_id`, `content`, `is_error` |
| `server_tool_use` | `id`, `name:"web_search"`, `input:{query}` |
| `web_search_tool_result` | `tool_use_id`, `content:[{type:"web_search_result",url,title,encrypted_content,page_age}]` |

For `image` / `document`, Base64 sources use `type:"base64"`, `media_type`, and `data`; URL sources use `type:"url"` with a non-empty `url`. When media type is omitted, `image` defaults to `image/*` and `document` defaults to `application/pdf`. `tool_result.is_error=true` wraps valid JSON content into `{"error":<CONTENT>}`; plain scalar or array results are wrapped into `{"result":<CONTENT>}`.

A custom tool is defined as `{name,description,input_schema}`, with an optional `type:"custom"`. Server tool fields:

| type | Required name |
| --- | --- |
| `web_search_20250305` | `web_search` |
| `image_search` | `image_search` |
| `web_fetch_20250910` | `web_fetch` |
| `code_execution_20250522`, `code_execution_20250825` | `code_execution` |
| `url_context` | `url_context` |
| `google_maps` | `google_maps` |

`web_search_20250305` accepts `max_uses`, with the actual invocation count determined upstream.

Server tools accept only their corresponding `type` and `name`. Providing `description`, `input_schema`, or extra options returns `invalid_request_error`. Tool choice accepts an omitted value, `{"type":"auto"}`, or `{"type":"none"}`; `any` and named `tool` return 400.

Non-streaming response:

```json
{
  "id": "msg_...",
  "type": "message",
  "role": "assistant",
  "model": "gemini-example",
  "content": [
    {"type":"thinking","thinking":"...","signature":"..."},
    {"type":"text","text":"..."},
    {"type":"tool_use","id":"call_01","name":"get_weather","input":{}}
  ],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "provider_model": "gemini-provider-id",
  "provider_finish_reason": "provider_19",
  "usage": {"input_tokens":10,"output_tokens":20}
}
```

Content output blocks can be `text`, `thinking`, `redacted_thinking`, `tool_use`, `server_tool_use`, or `web_search_tool_result`. Stop reasons include `end_turn`, `tool_use`, `stop_sequence`, `max_tokens`, `pause_turn`, or `refusal`. `POST /v1/messages/count_tokens` accepts the same message/system/tools input and returns `{"input_tokens":<INT>}`; it provides an independent count estimate, whereas final usage for media requests like PDFs is determined by `usage.input_tokens` in the generation response.

Media in Anthropic responses is encoded as Markdown data URLs within text blocks, and code is encoded as fenced text. Each deduplicated Google Search query generates a pair of `server_tool_use` and `web_search_tool_result`; results are deduplicated by URL, with the sources covering all queries from this search. `usage.server_tool_use.web_search_requests` records the upstream-reported deduplicated query count, and `web_fetch_requests` is `0`. Other citations, such as URL Context, append a Markdown `Sources:` list at the end.

The `encrypted_content` of search sources is generated by this service, preserving the upstream URL, title, and available snippets. The client passes back the two correlated search blocks as-is in the subsequent assistant message; the service uses the same `PROXY_API_KEY` to restore the source context. If the API key is changed, previous search contexts return `400 invalid_request_error`. This field is used for multi-turn sessions within this service and is managed separately from Anthropic's source tokens.

Anthropic SSE:

| Event | Payload |
| --- | --- |
| `message_start` | `{type,message:{id,type,role,model,content:[],stop_reason:null,stop_sequence:null,usage}}` |
| `content_block_start` | `{type,index,content_block}` |
| `content_block_delta` | `{type,index,delta}` |
| `content_block_stop` | `{type,index}` |
| `message_delta` | `{type,delta:{stop_reason,stop_sequence,provider_finish_reason?},usage}` |
| `message_stop` | `{type:"message_stop"}` |
| `error` | `{type:"error",error:{type,message}}` |

The delta union type includes `text_delta{text}`, `thinking_delta{thinking}`, `signature_delta{signature}`, and `input_json_delta{partial_json}`. The thinking signature is sent before the corresponding thinking block closes; redacted thinking uses a single start/stop block; `tool_use` first sends an empty input, followed by the complete argument JSON via `input_json_delta`. Search blocks are output as complete start/stop blocks once sources are aggregated, and query counts are returned with the final `message_delta.usage`.

### Gemini Interactions

`POST /v1beta/interactions` and `POST /v1/interactions` accept the same creation request, authenticated via `x-goog-api-key`, Bearer token, or the `key` query parameter.

```json
{
  "model": "gemini-3.8-flash-tts",
  "input": [{"type":"user_input","content":[{
    "type":"text","text":"Have a wonderful day!",
    "annotations":[{"type":"speech_metadata","style":"cheerful and friendly"}]
  }]}],
  "response_format": {"type":"audio","mime_type":"audio/l16","sample_rate":24000},
  "generation_config": {"speech_config":[{"voice":"Kore"}]},
  "stream": true
}
```

| Field | Mapping |
| --- | --- |
| `input` | String, single content block, array of content blocks, or array of steps; content types include `text`, `image`, `audio`, `video`, `document` |
| Media content | Either `mime_type` with `data` (Base64) or `uri`, reusing Gemini file and inline media parsing |
| Input steps | `user_input`, `model_output`, `thought`, `function_call`, `function_result`; function results match historical calls via `call_id` |
| `system_instruction` | System instruction for the current request |
| `generation_config` | `temperature`, `top_p`, `top_k`, `max_output_tokens`, `seed`, `stop_sequences`, `thinking_level`, `thinking_summaries`, `speech_config`, `tool_choice` |
| `response_format` | Single object or array; text uses `{type:"text",mime_type:"application/json",schema:{...}}` to request structured output; image uses `{type:"image",aspect_ratio?,image_size?}` |
| Voice config | `speech_config:[{voice}]`; multi-speaker uses `{speakers:[{speaker,voice}],mode?}`, where `mode` is `verbatim` or `conversational` |
| Voice text | `{type:"speech_metadata",speaker?,style?}` in text block `annotations` preserves speaker and style |
| Functions & tools | `tools:[{type:"function",name,description?,parameters?}]`; also accepts `google_search`, `url_context`, `code_execution`, `google_maps`; `tool_choice` is `auto` or `none` |
| Continuation | Saved by default; `previous_interaction_id` reconstructs prior content, `store:false` returns only the current response; the current service instance retains up to 256 response nodes |

Audio output is 24 kHz, 16-bit little-endian, mono. Non-streaming defaults to `audio/wav`, while streaming defaults to `audio/l16`; an explicit WAV stream sends a single valid WAV chunk once audio aggregation completes. `sample_rate` can be omitted or set to `24000`, and `delivery` can be omitted or set to `inline`. Creation requests execute within the current connection; `background` can be omitted or set to `false`.

Non-streaming responses contain `id`, `object:"interaction"`, `model`, `created`, `updated`, `status`, `steps`, and `usage`. `model_output.content` in `steps` stores text or media, with audio located in `{type:"audio",data,mime_type,sample_rate,channels}`; SDK `output_audio` and `output_text` are read from these steps. Function calls are returned as `function_call` steps with status `requires_action`; normal generation yields `completed`, while early termination like output limit exhaustion yields `incomplete`.

SSE uses matching event names and JSON `event_type`: `interaction.created` → `step.start` → `step.delta` → `step.stop` → `interaction.completed`. Steps correlate by `index`, and audio deltas are `{type:"audio",data,mime_type,sample_rate,channels}`. If no semantic events occur for 10 seconds, `: ping` is sent. Upstream errors or a missing terminal state trigger an `error` event and terminate the stream; client disconnections cancel upstream generation. Errors occurring prior to stream initiation use Gemini HTTP error objects.

### Gemini GenerateContent

`POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`, and `:countTokens` accept:

```json
{
  "contents": [{"role":"user","parts":[{"text":"Hello"}]}],
  "systemInstruction": {"role":"user","parts":[{"text":"Be concise"}]},
  "generationConfig": {},
  "tools": [],
  "toolConfig": {}
}
```

The Content fields are `role` and `parts`. Part oneof:

| Part | Fields |
| --- | --- |
| text/thought | `text`, optional `thought`, `thoughtSignature` |
| inline data | `inlineData:{mimeType,data}` |
| file data | `fileData:{mimeType,fileUri,displayName}` |
| function call | `functionCall:{id,name,args}` |
| function response | `functionResponse:{id,name,response}` |
| executable code | `executableCode:{language,code}` |
| code result | `codeExecutionResult:{outcome,output,error}` |

All `generationConfig` fields:

| Category | Fields |
| --- | --- |
| sampling | `temperature`, `topP`, `topK`, `frequencyPenalty`, `presencePenalty`, `seed` |
| output limits | `candidateCount`, `maxOutputTokens`, `stopSequences` |
| log probabilities | `responseLogprobs`, `logprobs` |
| structured output | `responseMimeType`, `responseSchema`, `responseJsonSchema` |
| modalities | `responseModalities` |
| image | `imageConfig:{aspectRatio,imageSize}` |
| thinking | `thinkingConfig:{thinkingBudget,thinkingLevel}` |
| transcription | `transcriptionConfig:{languageCodes,customVocabulary,wordTimestamps,speakerLabels,smartTranscription}` |
| speech | `speechConfig` |

`responseModalities` only accepts `TEXT`, `IMAGE`, and `AUDIO`; `AUDIO` is mutually exclusive with other modalities. When an image model omits modalities or requests only `IMAGE`, `[IMAGE,TEXT]` is sent; `imageConfig` preserves explicit aspect ratios and sizes, and models supporting output resolutions default to `1K` when the image config is omitted.

`speechConfig.voiceConfig` and `multiSpeakerVoiceConfig` are mutually exclusive; single-voice configurations must provide `prebuiltVoiceConfig.voiceName`, and each multi-speaker entry must provide a non-empty `speaker` and `voiceConfig.prebuiltVoiceConfig.voiceName`; `multiSpeakerVoiceConfig.mode` can be `VERBATIM` or `CONVERSATIONAL`. The `speechMetadata` (or `speech_metadata`) `{speaker,style}` of a text part is written to Part field 41. Models with capability code 85 split text without a speaker line by line, starting a new segment on lines prefixed with a configured speaker name and colon, merging continuation lines into the previous segment, and dropping any text prior to the first speaker line; text without matching lines is sent as-is. Legacy TTS models write `speechMetadata` back as a `speaker: line` prefix and a `style\n\n` instruction paragraph. `transcriptionConfig.smartTranscription=true` is mutually exclusive with explicitly set true for `wordTimestamps` or `speakerLabels`; the language code `detect` is normalized to empty for auto-detection.

Single-voice speech config:

```json
{"voiceConfig":{"prebuiltVoiceConfig":{"voiceName":"Kore"}}}
```

Multi-speaker:

```json
{
  "multiSpeakerVoiceConfig": {
    "speakerVoiceConfigs": [{
      "speaker": "Speaker A",
      "voiceConfig": {"prebuiltVoiceConfig":{"voiceName":"Kore"}}
    }]
  }
}
```

tool group fields:

| Tool | Fields |
| --- | --- |
| functions | `functionDeclarations:[{name,description,parameters,parametersJsonSchema}]` |
| search | `googleSearch` or `googleSearchRetrieval` |
| URL | `urlContext` |
| code | `codeExecution` |
| maps | `googleMaps` |
| image search | `imageSearch` |

`googleSearch.searchTypes` can contain empty `webSearch` and `imageSearch` objects; defaults to web search if not provided or if neither is enabled. `timeRangeFilter.startTime/endTime` uses RFC 3339 Nano. `googleSearchRetrieval` only accepts an empty object. Tool choice resides in `toolConfig.functionCallingConfig:{mode,allowedFunctionNames}`, accepting `AUTO` and `NONE`; `ANY` or a non-empty `allowedFunctionNames` returns 400.

`:countTokens` returns:

```json
{"totalTokens": 123}
```

Non-streaming generation response:

```json
{
  "candidates": [{
    "content": {"role":"model","parts":[]},
    "index": 0,
    "finishReason": "STOP",
    "finishMessage": "...",
    "groundingMetadata": {},
    "citationMetadata": {}
  }],
  "modelVersion": "gemini-provider-id",
  "responseId": "request-id",
  "usageMetadata": {
    "promptTokenCount": 10,
    "candidatesTokenCount": 20,
    "thoughtsTokenCount": 5,
    "toolUsePromptTokenCount": 0,
    "totalTokenCount": 35
  }
}
```

Output Parts use the same `text`, `thought`, `thoughtSignature`, `inlineData`, `fileData`, `functionCall`, `executableCode`, and `codeExecutionResult` as inputs. Transcribed text can include:

```json
{
  "text": "...",
  "transcriptionMetadata": {
    "speaker": "Speaker 1",
    "timestamps": [{
      "start":{"seconds":0,"nanos":0},
      "end":{"seconds":1,"nanos":250000000}
    }]
  }
}
```

`groundingMetadata` fields are `searchEntryPoint`, `groundingChunks`, `groundingSupports`, `retrievalMetadata`, `webSearchQueries`, and `googleMapsWidgetContextToken`. `searchEntryPoint` contains `renderedContent`, `sdkBlob`; `groundingChunks` element oneof is `web:{uri,title}`, `retrievedContext:{uri,title,text}`, or `maps:{uri,title,text,placeId}`; `groundingSupports` elements contain `segment:{partIndex,startIndex,endIndex,text}`, `groundingChunkIndices`, and optional `confidenceScores`; `retrievalMetadata` contains `googleSearchDynamicRetrievalScore`. Elements of `citationMetadata.citationSources` contain `uri`, `title`, `startIndex`, and `endIndex`.

Non-streaming results merge adjacent, homogeneous body or thought fragments that have no signature boundaries. Parts with tools, media, and transcription metadata remain separate; standalone signatures attach to the preceding unsigned Part, or fall back to `{"text":"","thought":true,"thoughtSignature":"..."}` when no attachable content exists.

`:streamGenerateContent` uses SSE. Each semantic event sends a partial `GenerateContentResponse` containing `responseId`, `modelVersion`, and a candidate Part, grounding, or citation; the final frame contains the candidate `finishReason`, optional `finishMessage`, and `usageMetadata`. Error frames after response headers are `data: {"error":{"code","message","status"}}`.

### Files, Transcribe, and Media

`POST /v1/files` accepts multipart `file` and `purpose`, which may arrive in any order. File limit is 512 MiB, with an additional 1 MiB allowed for multipart overhead per request; normal scalar parts are capped at 64 KiB. `filename`, non-empty `file`, and non-empty `purpose` are required. When `Content-Type` is empty or `application/octet-stream`, MIME is detected from the file prefix.

File object:

```json
{
  "id": "file_...",
  "object": "file",
  "bytes": 1234,
  "created_at": 0,
  "filename": "document.pdf",
  "purpose": "assistants",
  "status": "processed"
}
```

`POST /v1/files` and `GET /v1/files/{id}` return this object. `GET /v1/files/{id}/content` returns the raw body, setting `Content-Type`, attachment `Content-Disposition`, and `Content-Length` when known. `DELETE /v1/files/{id}` returns:

```json
{"id":"file_...","object":"file","deleted":true}
```

Unknown files return 404 `file_not_found`; exceeding the size limit returns 413 `file_too_large`. Drive files are bound to the creating account; cross-account generations temporarily copy the file and clean up the replica once the current attempt completes.

`POST /v1/audio/transcriptions` multipart fields:

| Field | Values and Handling |
| --- | --- |
| `file` | Required, non-empty; `audio/*`, `video/mp4`, `video/webm`; maximum 512 MiB |
| `model` | Default `gemini-3.5-transcribe`; accepts optional `models/` prefix |
| `response_format` | `json`, `text`, `verbose_json`, `diarized_json`; default `json` |
| `language` | Language code; `detect` and `auto` map to empty for auto-detection |
| `temperature` | `0..2` |
| `custom_vocabulary` | Repeatable text field or JSON string array |
| `word_timestamps`, `speaker_labels`, `smart_transcription` | `true` or `false` |
| `prompt` | Currently has no corresponding wire field; non-empty values return 400 |

`smart_transcription=true` is mutually exclusive with explicitly set true for word timestamps or speaker labels; custom vocabulary is mutually exclusive with word timestamps. Each account attempt creates a temporary Drive file, which is cleaned up under the same account once generation finishes, fails, or is canceled.

`text` format returns `text/plain; charset=utf-8`. `json` returns `{"text":"...","usage":...}`. Verbose format:

```json
{
  "task": "transcribe",
  "language": "en",
  "duration": 1.25,
  "text": "Hello",
  "segments": [{"id":0,"start":0,"end":1.25,"text":"Hello","speaker":"Speaker 1"}],
  "words": [{"word":"Hello","start":0,"end":1.25,"speaker":"Speaker 1"}],
  "usage": {"input_tokens":10,"output_tokens":2,"total_tokens":12}
}
```

`verbose_json` and `diarized_json` use the same verbose object shape. `language` is the normalized request language; it is empty and omitted when `detect` / `auto`. `segments` originates from response Part field 23; `words` is generated when the word count matches the timestamp span count.

`POST /v1/images/generations`:

| Field | Values and Handling |
| --- | --- |
| `model`, `prompt` | Required |
| `n` | Default and only allowed value is `1` |
| `size` | `auto`, `1024x1024`, `1536x1024`, `1024x1536` |
| `quality` | `auto`; `low/standard=1K`, `medium/hd=2K`, `high=4K` |
| `response_format` | `b64_json` returns Base64; other values return a data URL |

The response is `{"created":<UNIX>,"data":[{"b64_json":"...","revised_prompt":"..."}]}` or `{"created":<UNIX>,"data":[{"url":"data:<MIME>;base64,...","revised_prompt":"..."}]}`. `revised_prompt` appears only when upstream returns text concurrently. If upstream fails to return a final image, HTTP 502 `upstream_error` is returned; when the finish reason is abnormal, it is written to the error message, such as `image_recitation`.

`POST /v1/audio/speech`:

| Field | Values and Handling |
| --- | --- |
| `model`, `input` | Required |
| `voice` | Default `Zephyr` |
| `response_format` | Default `wav`; supports `wav`, `pcm`, and `mp3` when upstream already returns `audio/mpeg` |
| `speed` | Omitted/`0` or `1` |
| `instructions` | Passed as `speechMetadata.style` on the text part; legacy TTS models form the prompt via `instructions + "\n\n" + input` |

`pcm` returns the PCM body along with sampling parameters; audio data is extracted first when upstream returns PCM16 WAV. `wav` wraps upstream `audio/l16` into a 16-bit WAV according to effective rate and channels; native WAV preserves the respective audio format, with multiple chunks having their PCM data concatenated before encapsulation. Responses set `Content-Type` and `Content-Length`.

Voice requests for legacy TTS models prepend `## Transcript:\n` before the first text in accordance with the official wire format; AUDIO-only generation configs omit the default `maxOutputTokens`; `responseModalities` and `speechConfig` are written to their officially confirmed slots respectively.

### Video

OpenAI `POST /v1/videos` accepts JSON or multipart:

| Field | Values and Handling |
| --- | --- |
| `model`, `prompt` | Required |
| `seconds` | Integer string; default 4 |
| `size` | `1280x720`, `720x1280`, `1792x1024`, `1920x1080`, `1024x1792`, `1080x1920` |
| `input_reference` | File ID/data URL in JSON; file in multipart |

OpenAI video object:

```json
{
  "id": "operation-id",
  "object": "video",
  "model": "veo-example",
  "status": "queued",
  "progress": 0,
  "created_at": 0,
  "size": "1280x720",
  "seconds": "4"
}
```

`GET /v1/videos/{id}` returns the current object. Upon completion, status is `completed` and progress is `100`; when upstream is done but produces no file, status is `failed`. `GET /v1/videos/{id}/content` accepts an omitted parameter or `variant=video`, returning 409 `video_not_ready` when incomplete; successful downloads set media `Content-Type`, `attachment; filename="video.mp4"`, and `Content-Length` when known.

Upon successful video creation, the following resource binding is written keyed by operation ID:

```json
{
  "kind": "video-operation",
  "created_at": "2026-01-01T00:00:00Z",
  "video": {
    "model": "veo-example",
    "seconds": "4",
    "size": "1280x720"
  }
}
```

`model`, `seconds`, `size`, and UTC `created_at` are persisted with the creating account, and subsequent polling restores them from the resource binding; OpenAI POST and GET continue returning identical fields even after the generation service or process restarts. When an OpenAI request explicitly provides `size`, that public value is saved verbatim; when omitted, `1280x720` is saved based on the default 16:9, 720p. Gemini requests save normalized output dimensions: 720p is `1280x720` or `720x1280`, 1080p is `1920x1080` or `1080x1920`, and 4k is `3840x2160` or `2160x3840`. The public object's `created_at` is the binding creation time in Unix seconds.

Gemini `:predictLongRunning` request:

```json
{
  "instances": [{
    "prompt": "...",
    "image": {
      "inlineData": {"mimeType":"image/jpeg","data":"..."}
    }
  }],
  "parameters": {
    "numberOfVideos": 1,
    "sampleCount": 1,
    "aspectRatio": "16:9",
    "durationSeconds": 4,
    "resolution": "720p"
  }
}
```

`instances` must contain exactly one non-empty prompt; `image` must choose between `inlineData` and `fileData:{mimeType,fileUri}`. When `numberOfVideos` is 0, `sampleCount` is read; if both are 0, it defaults to 1, and ultimately only one result is accepted. `durationSeconds` accepts a JSON integer or a decimal string. When omitted, duration, aspect ratio, and resolution default to `4`, `16:9`, and `720p` respectively, and are validated against the live model's `video_durations_seconds`, `video_aspect_ratios`, and `video_output_resolutions`. Creation returns `{"name":"operations/<ID>"}`. Gemini operations use the same persisted metadata to restore the creating account and polling context; Gemini responses only contain `name`, `done`, and, upon completion, `response.generateVideoResponse.generatedSamples`.

`GET /v1beta/operations/{id}`:

```json
{
  "name": "operations/<ID>",
  "done": true,
  "response": {
    "generateVideoResponse": {
      "generatedSamples": [{
        "video": {"uri":"http://<HOST>/v1/videos/<ID>/content","mimeType":"video/mp4"}
      }]
    }
  }
}
```

`response` appears only when done; `generatedSamples` is empty when done without artifacts. Both operation and result file are bound to the creating account.

### Live and Robotics Public Frames

Send setup within 10 seconds after connection upgrade:

```json
{
  "type": "setup",
  "model": "gemini-live-model",
  "input_modalities": ["text", "audio", "image"],
  "output_modalities": ["audio"],
  "tools": [{"name":"get_weather","description":"...","parameters":{"type":"object"}}],
  "session_token": ""
}
```

Live input modalities must be a non-empty subset of `text`/`audio`/`image`, and output must be `["audio"]` or `["text"]`. Real-time translation models require output `["audio"]` along with `"translation":{"target_language_code":"es","echo_target_language":false}`; source text of the input audio is returned via `input_transcription`, while translated text is returned via `output_transcription` and `media`. Real-time transcription models require output `["text"]`, optionally with `"transcription":{"language_codes":["en"]}`; transcripts are returned via `interim_input_transcription` (current cumulative text) and `input_transcription` (final text). Neither model type accepts `tools`. Robotics input and output must be `["text"]` and `["text"]` respectively, and do not accept `translation` or `transcription`. Arrays do not accept empty strings or duplicate items.

Client objects following `setup` have standardized fields: `type`, optional `text`, `mime_type`, Base64 `data`, and `tool_responses`. Fields for each frame:

| type | Fields |
| --- | --- |
| `text` | `text`, and setup must declare text |
| `audio` | `mime_type:"audio/pcm"`, `data`; this default is used when MIME is omitted |
| `image` | `mime_type:"image/jpeg"`, `data`; this default is used when MIME is omitted |
| `media_end` | No additional fields |
| `tool_response` | `tool_responses:[{id,name,content}]`, setup must declare tools |
| `close` | No additional fields |

Complete set of server object fields:

```json
{
  "type": "text",
  "model": "gemini-live-model",
  "text": "...",
  "mime_type": "audio/l16;rate=24000",
  "data": "<BASE64>",
  "transcription": {"text":"...","finished":true,"duration_ms":1000,"language_code":"en"},
  "tool_call": {"id":"call-1","name":"get_weather","arguments":{}},
  "tool_call_ids": ["call-1"],
  "session_token": "...",
  "resumable": true,
  "raw": {},
  "error": "...",
  "code": "...",
  "retryable": true
}
```

Corresponding fields are used according to `type`: `session_opened{model}`, `setup_complete`, `text{text}`, `media{mime_type,data}`, `input_transcription/output_transcription/interim_input_transcription{transcription}`, `tool_call{tool_call}`, `tool_call_cancellation{tool_call_ids}`, `interrupted`, `generation_complete`, `turn_complete`, `session_resumption{session_token,resumable}`, `usage{raw}`, `go_away{raw}`, `provider{raw}`, `closed`, and `error{error,code,retryable,raw?}`.

If the initial frame or subsequent client fields are invalid, `{type:"error",code:"invalid_request",error:"..."}` is sent. Upstream errors retain their original `error` content. The client single-frame size limit is 8 MiB; exceeding this limit sends WebSocket close code 1009. The timeout for each write operation is 10 seconds; session termination allows up to 5 seconds each to wait for the read and send goroutines to exit.

Streaming endpoints uniformly use `text/event-stream`, with each SSE frame terminated by an empty line:

| Protocol | Initial Event | Content Sequence | usage | Termination Event |
| --- | --- | --- | --- | --- |
| OpenAI Chat | assistant role chunk | chat completion delta | Located after finish chunk when `include_usage=true` | `data: [DONE]` |
| OpenAI Responses | `response.created`, `response.in_progress` | output item / content part / delta / done | `usage` of completed response | `response.completed` or `response.incomplete` |
| Anthropic | `message_start` | `content_block_start`, delta, `content_block_stop` | `message_delta.usage` | `message_stop` |
| Gemini | candidate Part | `GenerateContentResponse` deltas | `usageMetadata` in the final frame | Finish reason in the final frame |

When the interval between upstream semantic events reaches 10 seconds, all four streaming protocols send an SSE comment frame `: ping` and flush immediately. OpenAI Chat sends the assistant role chunk first, Responses sends `response.created` and `response.in_progress` first, and Anthropic sends `message_start` first; these initial events can reach the client during account scheduling. Gemini's first frame originates from an upstream semantic event or `: ping`.

The `web_search_call` in Responses is triggered by an actual grounding query. When a search occurs, SSE first outputs the search call at index 0, followed by the message at index 1; when the model does not invoke a search, only the message is output and sent chunk-by-chunk via body events. If the upstream fails after the body, previously received body deltas precede `response.failed`.

Public adaptation rules:

| Canonical Event | OpenAI Chat | Responses | Anthropic | Gemini |
| --- | --- | --- | --- | --- |
| text | message/content delta | output_text | text block | candidate text Part |
| reasoning | `reasoning_content` | reasoning summary | thinking block | thought Part |
| function | `tool_calls` | function_call item | tool_use block | functionCall Part |
| function result | tool message | function_call_output | tool_result | functionResponse Part |
| code execution | readable Markdown | code_interpreter item | text block | executableCode/result Part |
| grounding/citation | annotations | output annotations | text sources | groundingMetadata |
| media | data URL / media endpoint | output content | content block | inlineData Part |
| usage | prompt/completion/total | input/output/total | input/output | prompt/candidates/thoughts/total |

Responses media uses image generation items; Anthropic media uses data URL Markdown within a text block. Anthropic sources use a `Sources:` Markdown list at the end of the text block.

OpenAI Chat carries generated images via Markdown data URLs; when the client passes assistant `message.content` back in the next turn, the adapter restores images within it to inline data Parts, preserving multi-turn image context. Image Base64 supports standard and URL-safe alphabets, optional padding, and CR/LF line breaks; text before and after images maintains its original order.

In user text, `youtu.be/<ID>`, `youtube.com/watch?v=<ID>`, `/shorts/<ID>`, `/live/<ID>`, and `/embed/<ID>` are converted into `video/*` external media parts and removed from the user text part; duplicate URLs are deduplicated into a single attachment. OpenAI `video_url`/`input_video`, Anthropic URL sources, and Gemini `fileData.fileUri` use the same external media encoding.

OpenAI Responses `previous_response_id` preserves up to 256 response nodes in-process and reconstructs the full contents; after a restart, the client must resubmit the complete context. Drive and Veo resource bindings are persisted to disk.

When `store` is omitted or set to `true`, a response node is created; `store=false` returns the current response while preserving the existing continuation chain.

Model, parameter, account, and upstream errors are projected according to the status table below. Client cancellation closes the upstream reader and releases the account lease.

Error objects and status semantics:

| Condition | HTTP | OpenAI | Anthropic | Gemini |
| --- | ---: | --- | --- | --- |
| Invalid parameters, schema, or tool choice | 400 | `invalid_request` | `invalid_request_error` | `INVALID_ARGUMENT` |
| No eligible accounts | 400 | `account_required` | `invalid_request_error` | `INVALID_ARGUMENT` |
| All accounts supporting the request are unschedulable | 503 | `account_unavailable` | `api_error` | `UNAVAILABLE` |
| Invalid local API key | 401 | `invalid_api_key` | `authentication_error` | `UNAUTHENTICATED` |
| Model or method not found | 404 | `model_not_found` | `not_found_error` | `NOT_FOUND` |
| Local file not found | 404 | `file_not_found` | `not_found_error` | `NOT_FOUND` |
| Upstream permission denied | 403 | `upstream_error` | `permission_error` | `PERMISSION_DENIED` |
| Video still generating | 409 | `video_not_ready` | `api_error` | `INTERNAL` |
| File exceeds 512 MiB | 413 | `file_too_large` | `request_too_large` | `INTERNAL` |
| Upstream quota or rate limited | 429 | `upstream_error` | `rate_limit_error` | `RESOURCE_EXHAUSTED` |
| All candidate accounts in cooldown and will not recover within 1 minute | 429 | `rate_limit_exceeded` | `rate_limit_error` | `RESOURCE_EXHAUSTED` |
| Upstream overloaded | 529 | `upstream_error` | `overloaded_error` | `INTERNAL` |
| Current client request canceled by administrator | 503 | `request_canceled` | `api_error` | `UNAVAILABLE` |
| Generation service stopped | 503 | `service_stopped` | `api_error` | `UNAVAILABLE` |
| Request deadline exceeded | 504 | `upstream_error` | `api_error` | `DEADLINE_EXCEEDED` |
| Transport, Content-Type, decoding, or missing terminal state | 502 | `upstream_error` | `api_error` | `INTERNAL` |

An upstream RPC HTTP 404 indicates a transport or upstream failure and maps to 502 by default; a public 404 corresponds to a local model directory or local resource lookup failure. When the client disconnects on its own, access logs record 499 and response writes are terminated.

Error object raw body:

**OpenAI Chat / Responses**

```json
{
  "error": {
    "message": "upstream response ended before finish frame",
    "type": "api_error",
    "code": "upstream_error"
  }
}
```

**Anthropic**

```json
{
  "type": "error",
  "error": {
    "type": "api_error",
    "message": "upstream response ended before finish frame"
  }
}
```

**Gemini**

```json
{
  "error": {
    "code": 502,
    "message": "upstream response ended before finish frame",
    "status": "INTERNAL"
  }
}
```

Terminal raw text after a streaming response has started:

```text
# OpenAI Chat
data: {"error":{"message":"...","type":"api_error","code":"upstream_error"}}

# OpenAI Responses
event: response.failed
data: {"response":{"id":"resp_...","object":"response","status":"failed","error":{"code":"upstream_error","message":"..."}}}

# Anthropic
event: error
data: {"type":"error","error":{"type":"api_error","message":"..."}}

# Gemini
data: {"error":{"code":502,"message":"...","status":"INTERNAL"}}
```

MakerSuite error parsing:

| Source | Path | Public Result |
| --- | --- | --- |
| HTTP status | response status | Preserves original status code |
| protocol code | `$[1][0]` | Maps to protocol error code/type/status |
| protocol message | `$[1][1]` | Written to public error `message` |
| Raw shape | `[null,[code,message,...]]` | Parsed into canonical error event |

Request lifecycle:

| Phase | HTTP / SSE Behavior | Resource State |
| --- | --- | --- |
| Failure before response headers | Returns corresponding HTTP status and protocol JSON error | Releases account slot |
| Failure after SSE has started | Sends OpenAI error, `response.failed`, Anthropic `error`, or Gemini error frame | Closes upstream reader and releases account slot |
| Completion frame | Outputs finish reason, usage, and protocol termination event | Merges Set-Cookie and releases account slot |
| Client cancellation | Terminates upstream read | Cancels request context and releases account slot |

Secondary development is welcome. If this project helps you, consider giving the repository a star!
