# WAA Implementation

WAA (Web Application Attestation) provides BotGuard proofs for protected AI Studio RPCs. The service maintains a BotGuard VM within each account's WAA Worker, invoking `snapshot` on the SHA-256 digest of each request's binding string to obtain an attestation proof starting with `!`, which is subsequently written into the wire request body. This document defines the architectural boundaries between the two WAA backends, the end-to-end execution pipeline of the pure-Go backend, the Firefox 152 host simulation, data file generation procedures, upstream change diagnostic methodologies, and the Goja fork. For protected request wire fields, see [Protocol Specification](protocol.md); for Build proxy details, see [Build Channel](build.md).

## 1. Backends & Responsibility Boundaries

`WAA_BACKEND` determines the backend managing the BotGuard lifecycle:

| Value | VM Location | Protected Request Dispatch | Browser Dependency |
| --- | --- | --- | --- |
| `camoufox` (default) | Camoufox page with pinned account fingerprint | Native page `fetch` | Locates or downloads Camoufox on startup |
| `go` | In-process Goja VM within service process | Go HTTP with Firefox 152 network shape | Prepared on-demand only for account login & verification |

- Configuration source: `.env`, "WAA Backend" in management settings, or `waa_backend` via `PUT /api/config`; trimmed and lowercased; invalid values fail validation; saved values take effect on next generation service launch.
- When assembling runtimes under `go`, the service logs `runtime assembly | 2/3 | WAA backend=go | accounts=<count>`, skipping Camoufox discovery, download, launch, and legacy profile cleanup.
- Account page browser login and account verification employ an on-demand login driver: the driver locates Camoufox on first call, downloads the pinned release for the current platform if missing, and reuses the driver instance thereafter.
- Account fingerprints are generated in Go and persisted to `camoufox-fingerprint.json`; both backends consume the identical configuration file.
- If worker configuration lacks a Camoufox executable path, a pure-Go worker is launched; worker state reports `RuntimeID` as `go-waa` and `PID` as the service process PID.
- Scheduling, warm pool capacity, runtime leases, retries, and cooldowns operate identically across both backends (see [Development & Contributing](development.md)).
- The pure-Go backend contains zero platform-specific code and runs across all release platforms with the Go binary; browser login relies on Camoufox release packages covering Windows, Linux, and macOS.

Both backends expose a unified set of worker capabilities to the request encoder:

| Capability | Semantics |
| --- | --- |
| `Proof(digest, prompt)` | Synchronizes prompt on page and generates a fresh proof for the SHA-256 digest |
| `ProtocolHeaders` | Returns official public protocol headers |
| `SendProtected(url, headers, body)` | Streams protected requests matching official page request shapes |
| `StorageCookies` | Exports runtime's current cookies |
| `State`, `Close` | Status inspection and graceful shutdown |

`NativeWorker` adapts both runtimes into a `ProtectedPreparer`: computes the lowercase hexadecimal SHA-256 digest of the binding string, acquires the proof, writes the proof into the target field of the body array, and attaches public protocol headers. Protected RPCs utilize the following fields and binding strings:

| RPC | Proof Field | Binding String | Dispatcher |
| --- | ---: | --- | --- |
| `GenerateContent` | 5 | All content parts joined by spaces | Worker `SendProtected` |
| `CreateInteractionStream` | 5 | All transmitted text joined by spaces | Worker `SendProtected` |
| `ProxyStreamedCall`, `ProxyUnaryCall` | 3 | `<path> <body_json>` | Worker `SendProtected` |
| `GenerateVideo` | 8 | Video prompt string | MakerSuite Go HTTP transport |
| Bidi setup | 6 | `models/<model>` and each function's `name description`, joined by spaces | WebChannel |
| Bidi text, audio, image, media end | 6 | Empty string | WebChannel |
| Bidi tool response | 6 | First function response call ID | WebChannel |

`GenerateContent` part binding values:

| Part | Binding Value |
| --- | --- |
| text | Raw text |
| inline data | Standard Base64 of raw bytes |
| Drive file | File ID |
| external media, function, function result, code, thought signature | Empty string |

YouTube links in user text are converted to external media parts first, and the link is stripped from the text before joining the binding string.

Proof generation for the same account executes serially: `NativeWorker`, pure-Go runtime, and VM each maintain execution locks. The service does not call `Waa/Ping`.

### Camoufox Backend

1. Go launches an isolated, headless Camoufox process, creates a session via WebDriver BiDi, and injects account cookies and localStorage.
2. Navigates to `/prompts/new_chat?model=<bootstrap_model>` (appending `&temporary=true` when `TEMPORARY_CHAT=true`), waits for the prompt textarea, identifies Google login redirects, and dismisses known modal dialogs.
3. Locates the high-level function calling `.snapshot({` and referencing `content` within the `default_MakerSuite` namespace of the page bundle.
4. Injects a unique bootstrap prompt, attaches a `beforeRequestSent` interception listener for the official `GenerateContent` request, and clicks Run.
5. Captures the WAA service when the page invokes snapshot; once the request hits interception, saves public request headers, terminates the request in-browser via `network.failRequest`, and verifies the model ID in the request body.
6. For each operational request, synchronizes the prompt into the page textarea (retrying up to 5s until DOM value matches), invokes the captured service to obtain a proof, dispatches via native page `fetch`, and returns chunked streaming bytes to Go via BiDi.

- Bootstrap `GenerateContent` terminates prior to network egress, incurring zero token usage; `TEMPORARY_CHAT=true` additionally disables autosave on warm-up tabs.
- Cancelling worker startup closes BiDi, terminates the Camoufox process tree, and deletes temporary profile directories; pages hanging in unexpected states return startup errors with the current URL.

The bootstrap model prioritizes `gemini-flash-latest` from the real-time catalog, otherwise selecting eligible models supporting `generateContent`, account tier, and chat capabilities in catalog order. Both backends share identical selection rules. A single account worker provides proofs for all standard generation models on that account, reusing the existing worker when switching operational models.

## 2. Pure-Go Bootstrap & Network Input

### Execution Flow

```text
Worker Startup
  storage-state.json + camoufox-fingerprint.json
  -> GET /prompts/new_chat?model=<bootstrap_model>     Page API key
  -> GetLoggingContext                                  x-goog-ext-519733851-bin
  -> Waa/Create ["lmnUSbltwc5ULv48iKLX"]                challenge
  -> interpreter: inline challenge / memory / disk cache / download, SHA-256 verified
  -> Goja top Realm installs Firefox host
  -> Execute interpreter, invoke <globalName>.a(program, ...)
       program creates iframe Realm, /generate_204 image requests, and event listeners
  -> Inject bootstrap prompt, dispatch input/change, click Run
  -> Await image request completion and settle for 3s -> ready

Each Protected Request
  -> Inject prompt, dispatch input/change
  -> snapshot(callback, [{content: SHA256(binding)}, undefined, undefined, undefined])
  -> "!" proof written to field 5 / 3
  -> SAPISID Authorization + public protocol headers + Firefox request headers + Cookies
  -> POST via account's pinned egress, streaming response
  -> Merge Set-Cookie to runtime, persist to storage-state.json

Every 12 Hours
  -> snapshot(callback, [undefined, undefined, undefined, undefined])
  -> Waa/Create [key, interpreterHash, previous_vm_snapshot]
  -> Close old VM once new VM is ready
```

Worker startup logs share 7 phase numbers with the Camoufox backend; pure-Go outputs phases 1, 2, 5, 6, and 7:

| Phase | Log Message | Pure-Go Action |
| ---: | --- | --- |
| 1 | `initializing page` with page model | Acquire runtime lease |
| 2 | `preparing browser profile` | Read cookies and fingerprint, create egress client |
| 5 | `loading AI Studio` | Request homepage and `GetLoggingContext` |
| 6 | `locating WAA service` | Call `Waa/Create` |
| 7 | `executing WAA bootstrap` | Load interpreter, initialize VM, run bootstrap interactions, settle |

### Accounts, Fingerprints, and Network Shape

#### Cookies

On worker launch, cookies from `storage-state.json` load into runtime memory. Outbound requests filter by expiration, domain, path, and Secure attributes to construct the `Cookie` header; response `Set-Cookie` headers merge back into runtime memory upon header arrival. `document.cookie` within the simulated DOM provides non-HttpOnly cookies matching domain `aistudio.google.com` joined by `; `. Page `localStorage` uses an in-memory map per VM, initially empty.

#### Fingerprints and Host Runtime Values

When `camoufox-fingerprint.json` is missing, it is generated based on Firefox 152, account locale, and timezone; both backends share the same file. Pure-Go mappings:

| Fingerprint Key | Host Property |
| --- | --- |
| `navigator.*` | Corresponding `navigator` property |
| `screen.*` | Corresponding `screen` property |
| `window.*` | Corresponding top-level Window property |
| `navigator.language` | Locale for timezone display names |
| `timezone` | Date local timezone and display names |
| `navigator.userAgent` | `User-Agent` request header |
| `headers.Accept-Language` | `Accept-Language` request header |

- `innerWidth` equals `outerWidth`; `innerHeight` equals `outerHeight` minus 57.
- `navigator.doNotTrack` is `"unspecified"`, `navigator.globalPrivacyControl` is `false`, and `document.hasFocus()` returns `true`.
- The iframe Realm Window inherits only `outerWidth`, `outerHeight`, `screenX`, `screenY`, `screenLeft`, `screenTop`, `devicePixelRatio`, and `mozInnerScreenX`; remaining properties derive from shape table iframe definitions.
- `location` and `document.URL` reflect the bootstrap page URL.
- When missing from fingerprint: UA defaults to Windows Firefox 152, `Accept-Language` defaults to `en-US,en;q=0.5`, and timezone defaults to `UTC`.

#### Network Shape

All outbound traffic routes through the account proxy (or global `PROXY`), adhering to Firefox 152 TLS ClientHello, HTTP/2 settings, pseudo-header order, and disabling automatic redirects. Headers are dispatched in the following sequence:

```text
user-agent, accept, accept-language, accept-encoding, referer, content-type,
x-goog-api-key, x-goog-authuser, x-user-agent, x-aistudio-g1-tier,
x-aistudio-visit-id, x-goog-ext-519733851-bin, authorization, cookie,
origin, sec-fetch-dest, sec-fetch-mode, sec-fetch-site, priority, te
```

Every request carries `User-Agent`, `Accept-Language`, `Accept-Encoding: gzip, deflate, br, zstd`, and matching `Cookie` headers. Additional headers per request type:

| Request | Method | Headers |
| --- | --- | --- |
| Homepage | GET | `Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8`, `Upgrade-Insecure-Requests: 1`, `Sec-Fetch-Dest: document`, `Sec-Fetch-Mode: navigate`, `Sec-Fetch-Site: none`, `Sec-Fetch-User: ?1`, `Priority: u=0, i` |
| `GetLoggingContext`, `Waa/Create` | POST | RPC headers |
| Interpreter | GET | `Accept: */*`, `Referer`, `Sec-Fetch-Dest: script`, `Sec-Fetch-Mode: no-cors`, `Sec-Fetch-Site: cross-site` |
| Page Image | GET | `Accept: image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5`, `Referer`, `Sec-Fetch-Dest: image`, `Sec-Fetch-Mode: no-cors`, `Sec-Fetch-Site: same-origin` |
| Protected Operational RPC | POST | Public protocol headers, `Authorization`, `Accept: */*`, `Referer`, `Origin`, `Sec-Fetch-Dest: empty`, `Sec-Fetch-Mode: cors`, `Sec-Fetch-Site: same-site` |

RPC headers include: `Accept: */*`, `Referer: https://aistudio.google.com/`, `Content-Type: application/json+protobuf`, `X-Goog-Api-Key`, `X-Goog-AuthUser: 0`, `X-User-Agent: grpc-web-javascript/0.1`, `Authorization`, `Origin: https://aistudio.google.com`, `Sec-Fetch-Dest: empty`, `Sec-Fetch-Mode: cors`, `Sec-Fetch-Site: same-site`. `Authorization` is a 3-part SAPISID signature (see [Protocol Specification](protocol.md)).

### Homepage, Public Headers, and Waa/Create

#### Homepage Navigation

The runtime issues a browser-like navigation request to `https://aistudio.google.com/prompts/new_chat?model=<bootstrap_model>` (with `&temporary=true` if configured). 3xx redirects follow `Location` up to 5 hops; redirects to `accounts.google.com` abort startup reporting invalid session. In the HTTP 200 response, `"WIu0Nc":"<value>"` provides the page API key; missing values abort startup.

#### GetLoggingContext & Extension Header

`GetLoggingContext` posts `[]` with the page API key and RPC headers. The response JSON+protobuf array is binary-encoded into protobuf wire format and Base64-encoded to produce `x-goog-ext-519733851-bin`:

| JSON Value | Protobuf Encoding |
| --- | --- |
| `null` or absent | Skipped |
| String | Wire type 2, varint length followed by UTF-8 bytes |
| `true`, `false` | Wire type 0, value 1 or 0 |
| Integer | Wire type 0 varint |

Field numbers correspond to JSON array index + 1. Other JSON types trigger encoding errors.

#### Public Protocol Headers

`ProtocolHeaders` returns 6 base headers upon which protected requests overlay RPC `Content-Type` and tier headers:

| Header | Value |
| --- | --- |
| `user-agent` | Fingerprint UA |
| `x-goog-api-key` | Page API key |
| `x-goog-authuser` | `0` |
| `x-user-agent` | `grpc-web-javascript/0.1` |
| `x-aistudio-visit-id` | `v1_` + Base64 of UUIDv4 string |
| `x-goog-ext-519733851-bin` | `GetLoggingContext` binary Base64 |

Image generation `GenerateContent` omits `x-goog-ext-519733851-bin`.

#### Waa/Create Request

`POST https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/Create` uses RPC headers with `X-Goog-Api-Key` set to WAA's dedicated public key (`waaAPIKey`, distinct from the page API key):

| Protobuf Field | Content |
| ---: | --- |
| 1 | Request key `lmnUSbltwc5ULv48iKLX` |
| 2 | Current VM interpreter hash |
| 3 | Current VM un-bound snapshot |

On initial worker launch, only field 1 is populated (trailing unset fields omitted):

```json
["lmnUSbltwc5ULv48iKLX"]
```

During VM refresh, all three fields are set. Official clients write `E:CTO` on snapshot timeout and `E:UCE` on exceptions; the pure-Go runtime writes `E:UCE` on snapshot errors:

```json
["lmnUSbltwc5ULv48iKLX", "<INTERPRETER_HASH>", "<PREVIOUS_SNAPSHOT>"]
```

#### Challenge Decoding

Index `1` of the outer response is a Base64 string. Adding 97 to each decoded byte (with byte overflow wrapping) produces a UTF-8 JSON array. If outer index `1` is empty or missing, outer index `0` contains a plaintext challenge array with identical schema; if both are empty, Create fails:

| JSON Index | Field | Extraction Rule |
| ---: | --- | --- |
| 0 | `MessageID` | String |
| 1 | `InterpreterJavaScript` | First non-empty string in list |
| 2 | `InterpreterURL` | First non-empty path in list, prefixed with `https:` |
| 3 | `InterpreterHash` | String, required |
| 4 | `Program` | String, required |
| 5 | `GlobalName` | String, required |
| 6 | Unused | |
| 7 | `ClientExperimentsStateBlob` | String |

Arrays with fewer than 8 elements or missing required fields abort Create. Captured samples typically report `MessageID` as `bfkj`, `GlobalName` as `botguard`, and interpreter path as `//www.google.com/js/bg/<INTERPRETER_HASH>.js`. The runtime dynamically reads these fields from each challenge; the program varies on every Create call and belongs to the current VM lifecycle.

#### Client Experiments

`ClientExperimentsStateBlob` is a JSON array deriving signal lists and persistent state:

- Index 5 contains `[[value, key], ...]`; entries where `key <= 53` enter group 1 in order of appearance, remaining enter group 2.
- Signal lists: `[[group1_values..., group2_values...], [group1_each_1..., group2_each_2...]]`.
- Index 4 provides persistent state if it is a non-empty string, otherwise `undefined`.
- Empty blobs default signal lists to `[[], []]`.

A typical blob sample is `[null,null,null,null,null,null,null,[],[]]`, mapping to `[[], []]` and `undefined`. The runtime parses this dynamically without hardcoded constants.

## 3. Interpreter, VM, and Realms

### Interpreter Management

The interpreter digest is the unpadded Base64URL-encoded SHA-256 hash of its source bytes, which must match `InterpreterHash`. Source code is obtained in the following order:

1. Use `InterpreterJavaScript` from challenge directly if non-empty.
2. Reuse in-memory source if hash matches active VM.
3. Read `<account_root>/.waa-interpreters/<hash>.js` if file digest matches.
4. Download via `InterpreterURL`, verify digest, and persist to disk cache.

The account root defaults to `auth/`. The interpreter executes with `InterpreterURL` (defaulting to `https://www.google.com/js/bg/<hash>.js`) as script origin, appearing in `Error.stack` and `fileName`.

### Event Loop

Each VM operates a single dedicated goroutine running a task queue sequentially. VM initialization, prompt injection, snapshot execution, timer callbacks, and image load callbacks enqueue as tasks; expired timers push callbacks to the tail of the queue. Microtasks execute via the Goja agent as outermost script frames return; top-level and iframe Realms share the same microtask queue.

### Realms

| Realm | Creation Trigger | Shape | Global Key Ordering |
| --- | --- | --- | --- |
| Top-level | Prior to interpreter execution | `top` | Captured official page global key order |
| iframe | Script appends iframe to document, or accesses `contentWindow` of attached iframe | `frame` | Fresh same-origin iframe initial enumeration order |

- iframe Realms are created via the Goja fork's `NewRealm`, sharing heap, call stack, and microtask queue with top-level while maintaining independent global objects, built-ins, `Math.random` PRNG states, timer counters, and `performance` time origins.
- Appending an iframe dispatches an `isTrusted=true` `load` event via `setTimeout(0)`.
- Realms share brand tables, native function source registries, and Trusted Types records, preserving interface identities for DOM objects passed across boundaries.

### Initialization Call

After executing the interpreter, the runtime retrieves global `<GlobalName>` and invokes method `a` with the global object as `this`:

```javascript
botguard.a(program, ready, true, undefined, passEvent, signalLists, persistentState, false, loggers)
```

| Arg Index | Parameter | Value |
| ---: | --- | --- |
| 1 | program | Challenge `Program` |
| 2 | ready | Callback receiving `(snapshotFn, shutdownFn)` |
| 3 | enable flag | `true` |
| 4 | environment | `undefined` |
| 5 | passEvent | No-op callback `(v, x, C, G) => {}` |
| 6 | signal lists | Derived from client experiments |
| 7 | persistent state | Derived from client experiments |
| 8 | secondary flag | `false` |
| 9 | loggers | 4 no-op callbacks |

The VM becomes ready once the ready callback receives the snapshot function. Initialization fails if the global object is missing, method `a` is absent, or argument 1 to ready is not a function.

### Bootstrap Interactions & Settle

Following initialization, the runtime writes the bootstrap prompt (`AIStudio2API bootstrap <UnixNano>`) into the page textarea, dispatches `input` and `change` events, clicks Run, awaits completion of all image requests generated by the VM, and settles for 3 seconds. The pure-Go host has no Angular application, so the click dispatches DOM events without triggering upstream `GenerateContent`.

## 4. Firefox Host Simulation & Key Ordering

`internal/waa/dom.js` constructs Window, WebIDL interface prototypes, and instances in each Realm according to `firefox152.json`. Host scripts execute under origin `\x00waa-host`, omitting their frames from `Error.stack` and avoiding triggering lazy global property resolution during host setup.

### Shape Table Schema

Captured from a logged-in AI Studio session on Windows Firefox 152:

| Key | Content |
| --- | --- |
| `userAgent`, `capturedAt` | Captured browser UA and timestamp |
| `interfaces` | 714 interface definitions, parent interfaces first |
| `frame` | iframe Realm: `global` (992 own properties), `chain`, `windowValues`, `freshKeys` (992 initial keys) |
| `top` | Top Realm: `global` (1058 own properties), `chain`, `windowValues` |
| `namespaces` | Members of `CSS`, `console`, `WebAssembly`, `Intl` |
| `defaults` | Default instance values for 143 interfaces |
| `own` | Instance own-properties (e.g. `Location` members, event `isTrusted`) |
| `singletons` | 46 singleton paths in iframe Realm |
| `topSingletons` | 13 singleton paths in top Realm |
| `tagMap` | 140 HTML tag-to-interface mappings |
| `mediaQueries` | 77 media query evaluation results |
| `builtins` | Own members across 178 built-in object paths |
| `promises` | 207 Promise-returning members |

`interfaces` entry schema:

| Field | Description |
| --- | --- |
| `n` | Global interface name |
| `cn` | Constructor `name` (aliases may differ from `n`) |
| `p` | Parent constructor interface |
| `pp` | Parent prototype interface |
| `l` | Constructor `length` |
| `pw` | Prototype property writability |
| `st`, `pr` | Static and prototype members |
| `call` | Result when called without `new` |
| `construct` | Result on parameterless `new` (`ok:<tag>` or error string) |

```json
{"n":"Blob","cn":"Blob","p":null,"pp":"Object","l":0,"pw":false,"st":[],
 "pr":[{"n":"slice","f":"cew","k":"m","l":0,"fn":"slice","nat":true}],
 "call":"TypeError: Blob constructor: 'new' is required","construct":"ok:Blob"}
```

Member descriptor entry:

| Field | Description |
| --- | --- |
| `n` | Property name; Symbol keys represented as `@@<desc>` |
| `f` | Descriptor flags: `c` configurable, `e` enumerable, `w` writable |
| `k` | Kind: `v` value, `m` method, `a` accessor, `i` interface constructor, `o` object value |
| `v` | Encoded value |
| `l`, `fn`, `nat` | Function length, name, native flag |
| `g`, `gl`, `s`, `sl` | Getter/setter name and length |
| `ctor` | Whether built-in method is a constructor |
| `tag` | `Object.prototype.toString` result for object values |
| `err` | Descriptor read failure during capture |

Value encodings:

| `t` | Encoded Type |
| --- | --- |
| `u`, `null` | `undefined`, `null` |
| `s`, `b` | String, boolean |
| `n` | Number (`NaN`, `-0`, `Infinity` stored as strings) |
| `bigint`, `sym` | BigInt decimal string, Symbol description |
| `a` | Primitive array |
| `ref` | Singleton reference path (e.g. `frame.navigator`, `top.document`) |
| `f` | Function reference |
| `o` | Object reference |
| `throw` | Error thrown during property read |

### Interface Construction & Brand Checking

Interfaces are generated in table order. Existing Goja ECMAScript built-ins are reused; other interfaces instantiate prototypes (inheriting `pp`) and constructors (inheriting `p`), populating `st` and `pr` members.

- Calling constructors without `new` throws errors matching `call` (or constructs if `call` is `ok`). `new` invocations instantiate or throw matching `construct`.
- Event interfaces (`Event`, `CustomEvent`, `UIEvent`, `MouseEvent`, etc.) use custom constructor logic initializing dictionary attributes.
- Accessors and methods enforce brand checks: calling on objects that do not implement the interface throws `TypeError: '<member>' called on an object that does not implement interface <Interface>.`
- Unimplemented `promises` members return permanently pending Promises; brand check failures return rejected Promises.
- Function `name`, `length`, and `Function.prototype.toString` match native Firefox representations.

### Window and Global Properties

Each Realm's global prototype chain is `Window.prototype -> WindowProperties -> EventTarget.prototype`. Globals reconstruct according to Realm `global` order: interfaces register constructors, methods preserve existing implementations, accessors bind `windowValues` with fingerprint overrides, and namespaces instantiate object containers. Numeric indices, `undefined`, `NaN`, and `Infinity` retain engine definitions; unlisted configurable properties are deleted.

### Instance Property Evaluation

Each host instance records interface, value map, override map, caches, DOM parent/child references, attributes, and event listeners in a brand table. Accessors evaluate in order:

1. Write overrides and fingerprint overrides
2. Cached object, array, and function values
3. Custom host getter implementations
4. Instance value table, falling back to ancestor `defaults`

Singletons instantiate upon first access according to `topSingletons` or `singletons`; `document` automatically instantiates `html`, `head`, and `body` elements.

### Host Object Behaviors

| Object | Behavior |
| --- | --- |
| Node, Element | Insertion, removal, replacement, cloning, containment, parent/child/sibling traversal, `isConnected` |
| Attributes & Selectors | `id`, `class`, arbitrary attributes; selectors support tags, `#id`, `.class`, `*`, and comma-separated lists |
| Document | `createElement` resolves via `tagMap` (tags with `-` yield `HTMLElement`, unknown tags yield `HTMLUnknownElement`); `createTextNode`, `createComment`, `createDocumentFragment`, `createEvent`, `createRange`, `getElementById` |
| Events | Capture, target, bubble phases; `once`, `capture`, listener deduplication, `on<type>` properties, `stopPropagation`, `stopImmediatePropagation`, `preventDefault` |
| Layout | Top-level `html` and `body` return viewport dimensions, others return 0; `offsetHeight` computes from inline styles, children, and line heights |
| `IntersectionObserver` | Dispatches single callback 16ms post-`observe`, timestamp aligned to 60Hz frame |
| `getComputedStyle`, `matchMedia` | Returns default metrics and media query tables; unlisted `min/max-width/height` compute against viewport |
| Storage | `getItem`, `setItem`, `removeItem`, `key`, `clear` backed by in-memory map |
| Location | `toString` returns `href`; `assign`, `replace`, `reload` do not navigate |
| Performance | `now`, `timeOrigin`, `toJSON`; `getEntries*` returns navigation entries only |
| Navigator | `javaEnabled` is `false`, `sendBeacon` returns `true`, `getGamepads` is empty, `permissions.query` returns `prompt` |
| Canvas, GPU | `getContext('2d')` returns `CanvasRenderingContext2D` instance; `requestAdapter` returns `null` |
| Trusted Types | `createPolicy`, `createHTML`, `createScript`, `createScriptURL`; `TrustedScript` unboxes to source in `eval` |
| Images | Setting `src` initiates network fetch, dispatching `load` or `error` on completion |

### Realm Primitives

| Primitive | Behavior |
| --- | --- |
| `setTimeout`, `setInterval` | Independent counter per Realm; `setInterval` clamped to 4ms minimum; callback `this` is Realm global |
| `requestIdleCallback` | Alternates 0ms and 4ms delay; `timeRemaining()` returns 4 or 0 |
| `requestAnimationFrame` | Timestamp aligned to 60Hz frame boundaries (0.66ms offset) |
| `performance.now` | Milliseconds since Realm origin; top Realm origin set 2.5s before host installation |
| `Math.random` | SpiderMonkey XorShift128+ with independent PRNG seed per Realm |
| `atob`, `btoa` | Trims ASCII whitespace; invalid inputs throw `DOMException` (`InvalidCharacterError`) |
| `queueMicrotask` | Appends to shared agent microtask queue |
| `eval` | Accepts strings and `TrustedScript` instances |
| Call Stack | Depth capped at 20,000 frames; overflows throw `InternalError: too much recursion` |
| Date | Evaluated in account timezone with localized timezone string suffixes |

### Timezone & Date Formatting

Date local time evaluates against fingerprint `timezone`. `Date.prototype.toString` and `toTimeString` append Firefox Intl long timezone names (e.g. `GMT+0800 (Taipei Standard Time)`), resolved from `timezones.json.gz`:

1. Locale selection by `navigator.language`: exact match, case-insensitive match; `zh-HK`/`zh-MO` map to `zh-HK`, `zh-TW`/`Hant` to `zh-TW`, other Chinese to `zh-CN`, English to `en-US`.
2. Single name in table: used unconditionally.
3. Two names in table: uses January name if current daylight saving status matches Jan 15 12:00, July name otherwise.
4. Unlisted timezones format as `GMT±HH:MM` (`GMT` for zero offset).

### Lazy Global Resolution & Built-in Key Ordering

The BotGuard program queries `Object.getOwnPropertyNames(window)` in an iframe, selecting interfaces by pseudorandom indices and enumerating prototype members to embed into the proof. Property enumeration order must strictly match Firefox.

#### Lazy Global Properties

SpiderMonkey and Gecko define standard classes and WebIDL interfaces lazily: names become own properties only upon first access, appending to the end of defined own properties. Managed via `firefoxGlobalOrder`:

| Realm | Lazy Name Table | Defined Own Properties Initial Order |
| --- | --- | --- |
| iframe | Names before `Function` in `frame.freshKeys` | Names from `Function` onward in `frame.freshKeys` |
| Top-level | `undefined` only | `top.global` order |

Lazy resolution triggers in non-host scripts on:

- Property reads, `in`, `hasOwnProperty`, `getOwnPropertyDescriptor`, assignments, `defineProperty`, and `delete` on the global object.
- Internal engine prototype usage: string property access resolves `String`; number/boolean/regex/Symbol/Date/WeakMap/WeakSet/BigInt prototypes resolve their respective classes; iterators resolve `Iterator`; built-in errors resolve corresponding error constructors.
- Passing DOM objects to scripts (accessor return values or post-setup instances) resolves their interface constructors.
- Initial global enumeration resolves `globalThis`.

Resolving a name appends its associated dependency group to defined properties:

| Name | Dependency Group |
| --- | --- |
| `Number` and globals | `isNaN`, `isFinite`, `parseInt`, `parseFloat`, `NaN`, `Infinity`, `Number` |
| `String` and globals | `escape`, `unescape`, `decodeURI`, `encodeURI`, `decodeURIComponent`, `encodeURIComponent`, `String` |
| Error subclasses, `WebAssembly` | `Error`, followed by the requested name |
| WebIDL interface | Root ancestor through target interface; `HTMLImageElement`, `HTMLAudioElement`, `HTMLOptionElement` append `Image`, `Audio`, `Option` |
| Other | The requested name |

#### Global Enumeration Order

`Object.getOwnPropertyNames`, `Object.keys`, `Reflect.ownKeys`, and `for-in` enumerate string keys in sequence:

1. Array index keys
2. `undefined`
3. `globalThis` (resolved on first enumeration)
4. Unresolved lazy names in table order
5. Defined own properties in order of definition
6. Remaining keys

#### Built-in Object Member Alignment

Host setup aligns built-in objects against `builtins`:

1. Populates missing members with implementations or native stubs.
2. Removes unlisted configurable properties.
3. Reorders own string keys via Goja fork's `Object.OrderOwnKeys`.

Implementations include: `String.prototype` HTML methods and `isWellFormed`/`toWellFormed`, `Date.prototype.getYear`/`setYear`/`toGMTString`, `Object.prototype.__defineGetter__`, `Object.groupBy`, `Map.groupBy`, `Array.fromAsync`, `Promise.withResolvers`, `Promise.try`, `Error.captureStackTrace`, `Math.f16round`, `Math.sumPrecise`, Set methods, `RegExp.escape`, `Uint8Array` Base64 methods, `Atomics`, Iterator helpers, `RegExp.prototype.hasIndices`, and `ArrayBuffer.prototype.resizable`.

## 5. Prompts, Events, and Protected Requests

### Prompt Injection

Before generating proofs and during bootstrap, the runtime executes the prompt injection script within the event loop:

```javascript
((prompt, submit) => {
  let box = document.querySelector('ms-prompt-box');
  if (!box) {
    box = document.createElement('ms-prompt-box');
    document.body.appendChild(box);
    box.appendChild(document.createElement('textarea'));
    const run = document.createElement('ms-run-button');
    document.body.appendChild(run);
    run.appendChild(document.createElement('button'));
  }
  const textarea = box.querySelector('textarea');
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setter.call(textarea, prompt);
  textarea.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: prompt }));
  textarea.dispatchEvent(new Event('change', { bubbles: true }));
  if (submit) document.querySelector('ms-run-button').querySelector('button').click();
  return textarea.value;
})
```

The textarea normalizes CRLF and CR to LF; the return value must match the normalized prompt string, otherwise proof generation fails.

### Event Propagation Semantics

The BotGuard VM attaches capturing listeners on `body` for mouse, keyboard, pointer, focus, input, and clipboard events. Proofs from VMs receiving zero interaction events are rejected upstream.

`input` bubbles from `textarea` through `ms-prompt-box`, `body`, `html`, `document`, to `window`; capturing listeners run in order `window -> document -> html -> body`. Event `isTrusted`:

| Source | `isTrusted` |
| --- | --- |
| Script-constructed events, prompt `input`/`change` | `false` |
| `HTMLElement.click()` `PointerEvent` | `false` |
| iframe post-attach `load` | `true` |
| Image post-network `load`/`error` | `true` |

### /generate_204 Image Beacons

During initialization, the program creates image elements requesting `/generate_204?<token>` expecting `error` events. The host resolves `src` to absolute URLs, dispatching GET requests with Firefox image headers through the account proxy. HTTP 200 responses with `image/*` content-types dispatch `load`; other outcomes (including 204) dispatch `error`. The runtime awaits pending image requests before settling.

### Snapshot, Proof, and Dispatch

#### Proof Assembly

```text
digest = lowercase_hex(SHA256(binding))
snapshot(callback, [{content: digest}, undefined, undefined, undefined])
proof  = callback_arg_0
```

The snapshot argument array contains four slots: slot 0 holds `{content: digest}`, remaining slots are `undefined`. The proof must start with `!`. `NativeWorker` writes the proof into `payload[field-1]`.

Build proxy binding strings concatenate path and JSON body with a single space:

```text
/v1beta/models/<MODEL_ID>:streamGenerateContent {"contents":[...],...}
```

#### Dispatch and Cookie Writeback

Assembled by `WorkerProtectedTransport`:

1. Export cookies from worker, generate SAPISID `Authorization`.
2. Overlay RPC-specific headers (`Content-Type`, `X-AIStudio-G1-Tier`) on public headers; remove `x-goog-ext-519733851-bin` for image routes.
3. Re-verify model and channel cooldowns before transmission.
4. Pure-Go runtime appends Firefox fetch headers and cookies, posting via account proxy.
5. On response headers, worker cookies atomically replace cookies in `storage-state.json`.

## 6. Lifecycles, Data, and Failure Recovery

### VM Lifecycle

| Parameter | Value |
| --- | --- |
| VM Lifetime | 12 hours (matching BotGuard parameter 43,200,000 ms) |
| Refresh Mode | Background timer refresh; also triggers if expired on proof request |
| Refresh Timeout | 2 minutes for background refresh |
| Settle | Awaits image completion and settles 3 seconds upon new VM ready |

During refresh, the old VM generates an un-bound snapshot, calling `Waa/Create` with current hash and snapshot; the old VM terminates only after the new VM is ready. Proof requests queue during refresh. Shutdown invokes the shutdown callback with a 1-second timeout before stopping the event loop.

### Worker States

Worker states: `starting`, `bootstrapping`, `ready`, `busy`, `closing`, `closed`, and `failed`. Transitions to `busy` during proof generation, returning to `ready` upon completion; non-cancellation errors transition to `failed`. Instances are distinguished by incrementing generation counters; stale errors from older generations are ignored.

### Waa/Ping & Readiness Validation

`Waa/Ping` (`POST https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/Ping`) is not called during standard operations, but serves to test endpoint reachability:

```json
["lmnUSbltwc5ULv48iKLX", "<BOTGUARD_PROOF>"]
```

Valid proofs, corrupted proofs, omitted field 2, arbitrary field 1, and cookie-less requests all return HTTP 200 `[]`. Removing the API key yields HTTP 403; invalid field types return HTTP 400. Ping verifies only API consumer identity and is not used for worker readiness.

Readiness tiers:

| Tier | Pass Criteria | Failure Implication |
| --- | --- | --- |
| VM Initialization | Ready callback invoked, bootstrap images settled | Challenge, interpreter, host, or event loop failure |
| Local Proof | Snapshot returns string starting with `!` | Current worker requires rebuilding |
| Business Acceptance | Protected RPC yields expected semantic events and termination | Differentiates account, model, quota, and worker errors |

A single worker services all standard generation models on an account. Real local readiness is confirmed by successful VM snapshot output; business acceptance is determined solely by upstream RPC responses.

### Failure Classification

| Signal | Remediation |
| --- | --- |
| Proof failure: snapshot exception, desynced prompt, missing `!` prefix, VM refresh failure | Worker transitions to `failed`; rebuilds worker on same account and replays request once |
| Protected request network error | Same as above |
| HTTP 404 Code 5 with `Ambiguous request for service ''` | Rebuilds worker on same account and replays request once |
| HTTP 403 or Code 7 | Preserves account and model eligibility; switches to untried account prior to first semantic event; does not rebuild worker |
| HTTP 429 | Cooldown recorded by minute/daily quota (`build:<model>` for Build); retries on alternate channel of same account if available |
| HTTP 401 | Refreshes Chrome credentials via same egress, rebuilds runtime, replays once; enters `auth_required` if refresh fails |
| Worker startup failure | Logs failure; request switches candidate accounts |
| Runtime lease held by another process | Suspends scheduling for account, retrying after 5s with backoff up to 1m |

Code 7 represents upstream rejection. It can be account- or model-specific: the same worker can return Code 7 on one model and 200 on another. A single Code 7 does not signify WAA host invalidation.

### Interpreter Logging

On first execution of an interpreter hash within a process, a JSON log is emitted:

```json
{"level":"INFO","msg":"WAA interpreter version","hash":"<INTERPRETER_HASH>","url":"https://www.google.com/js/bg/<INTERPRETER_HASH>.js"}
```

VM refreshes detecting new hashes log version changes (`previous`, `current`). New hashes download and execute automatically during worker startup or rebuild.

### Shape and Timezone Data Generation

Files in `internal/waa` are generated from live Firefox captures:

| File | Content | Format |
| --- | --- | --- |
| `firefox152.json` | Firefox shape table | Single-line JSON, UTF-8, LF |
| `timezones.json.gz` | Firefox Intl long timezone names | Gzip-compressed JSON, sorted keys, mtime=0 |
| `dom.js` | Host generation script | JavaScript source |

#### Capture Procedure

Captured in Camoufox logged into AI Studio on Windows Firefox 152 (no touch devices detected). Scripts capture properties from top and iframe windows, WebIDL prototypes, constructor results, media queries, namespaces, and built-in objects.

#### Sanitization & Export

Before export to the repository, all personal and session data is scrubbed:

- `cookie` properties in `defaults`, `singletons`, and `topSingletons` cleared.
- `Storage` instances stripped of keys, length set to 0.
- `localStorage` and `sessionStorage` singletons cleared.
- Window `name` and `status` cleared.
- DOM content properties (`innerText`, `textContent`, `innerHTML`, etc.) deleted.
- Sanitizer asserts absence of email addresses, Google auth cookies (`SID`, `HSID`, `SSID`, `APISID`, `SAPISID`, `SIDCC`, `NID`, `OSID`, `AEC`, `__Secure-*`, `__Host-*`), and internal keys.

## 7. Upstream Diagnostics & Engine Comparison

### Diagnostic Matrix

| Symptom | Primary Investigation |
| --- | --- |
| Worker startup fails on `WIu0Nc`, login redirect, or HTTP error | Homepage DOM structure and session validity |
| `Waa/Create` parsing fails | Response format: index 1 obfuscated vs index 0 plaintext, offset constant 97, field layout |
| Interpreter digest mismatch | Base64URL hashing rules and download URL |
| Initialization fails or ready not called | Global function name, argument order, client experiments structure |
| Pure-Go yields 403 on new hash while Camoufox yields 200 | Host simulation divergence (see comparison workflow below) |
| Both backends yield 403 | Account or model eligibility; verify on official web interface |

### Dual-Sided Challenge Comparison

1. Issue `Waa/Create` in Camoufox with the target account, saving raw response and interpreter.
2. In browser, instantiate a VM with the challenge and loaded interpreter, dispatch `input`/`change`, and capture proof.
3. In Go, parse the challenge via `waa.ParseChallenge` and invoke `NewRuntime`, `FillPrompt`, `Settle`, and `Proof`.
4. Dispatch `GenerateContent` using both proofs with identical account, model, and prompt.

Browser 200 with Go 403 isolates divergence to the pure-Go host or Goja; mutual 403 indicates account, model, or challenge state. Narrow down divergence using:

1. **Fixed PRNG Comparison**: Seed `Math.random` identically on both sides and compare proof bytes sequentially.
2. **charCodeAt Tracing**: Hook `String.prototype.charCodeAt` in both engines to log strings processed during encoding; the first diverging string pinpoints the differing host property.
3. **Exception Sequence Logging**: Enable break-on-exceptions via CDP in Firefox and trace throwing frames in Goja; the first mismatch indicates missing properties, brand check failures, or error message formatting differences.

### Goja Fork

`internal/waa/goja` is a patched MIT-licensed fork of `github.com/dop251/goja` (`v0.0.0-20260826204918-8f1c0696a37b`). Behavioral enhancements include:

- **Multi-Realm Support**: `NewAgent`, `NewWithAgent`, `NewRealm` allow multiple runtimes to share heaps, call stacks, and microtask queues while keeping independent globals.
- **Shared Microtasks**: `QueueMicrotask` queues microtasks per agent, executed when outermost script frames return.
- **Eval Transformers**: `SetEvalTransformer` intercepts eval arguments before execution, preserving caller line numbering.
- **Firefox Error Stacks**: `Error.prototype.stack` formatted as `functionName@file:line:column`, suppressing internal `\x00` frames.
- **SpiderMonkey Function Names**: Infers stack names for anonymous functions (e.g. `a.b/<`).
- **SpiderMonkey Error Messages**: Matches Firefox messages (`can't access property "p", x is undefined`, `InternalError: too much recursion`, etc.).
- **Global Key Ordering**: `SetGlobalObserver` tracks property definition, deletion, and resolution to preserve SpiderMonkey lazy resolution sequences.
- **Own Key Ordering**: `Object.OrderOwnKeys` reorders own string keys.
- **Date Timezones**: `SetTimeLocation` and `SetTimeZoneName` supply localized timezone strings for `toString` and `toTimeString`.

## 8. Implementation Architecture

| Path | Responsibility |
| --- | --- |
| `internal/config/config.go` | `WAA_BACKEND` parsing and validation |
| `internal/app/waa_backend.go` | Camoufox preparation or on-demand login driver setup based on backend |
| `internal/app/runtime.go` | Warm pool management, startup logging, leases, rebuilding, and rotation |
| `internal/app/auth_retry.go` | HTTP 401 credential renewal, runtime recreation, and replay |
| `internal/aistudio/runtime_go.go` | Pure-Go runtime: homepage, `GetLoggingContext`, `Waa/Create`, cache, VM refresh, proofs, cookies |
| `internal/aistudio/runtime_native.go` | `NativeWorker`: proof injection and state management |
| `internal/aistudio/service.go` | `WorkerProtectedTransport`, binding assembly, failure classification |
| `internal/aistudio/build.go` | Build binding and proof field specification |
| `internal/aistudio/transport_browser.go` | Firefox 152 TLS and header ordering |
| `internal/aistudio/transport_http.go` | Visit ID, page API key extraction, default user agents |
| `internal/camoufoxnative/` | Camoufox backend, browser automation, and isolated login |
| `internal/waa/challenge.go` | Challenge decoding and interpreter verification |
| `internal/waa/runtime.go` | VM initialization, client experiments, prompt injection, snapshots, settle, shutdown |
| `internal/waa/host.go` | Realm host setup, Profiles, `Function.prototype.toString`, timers |
| `internal/waa/realm.go` | iframe Realm, `Math.random`, `atob`/`btoa`, `queueMicrotask`, eval transforms, stack limits |
| `internal/waa/loop.go` | VM event loop |
| `internal/waa/global_order.go` | Lazy global property resolution and enumeration order |
| `internal/waa/timezone.go` | Timezone and display name resolution |
| `internal/waa/dom.js` | Generates Window, interfaces, and host objects from shape table |
| `internal/waa/firefox152.json` | Firefox shape table |
| `internal/waa/timezones.json.gz` | Timezone display names table |
| `internal/waa/goja/` | Patched Goja JavaScript engine fork |
