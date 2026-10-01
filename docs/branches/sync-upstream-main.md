---
branch: sync/upstream-main
last_commit: a02d32a
status: complete
last_activity: 01-10-2026
---

## Task

Synchronize the `sync/upstream-main` branch with upstream commits up to `52e3128` (release v0.2.0), resolve merge conflicts while strictly preserving repository invariants (English-only code/logs, immutable read-only runtime configuration, Docker-safe behavior), and merge into `dev`.

## Description

Upstream repository `Mag1cFall/AIStudio2API` introduced major features and improvements in v0.1.7–v0.2.0:
1. **Pure-Go WAA Backend**: An in-process BotGuard VM runtime based on a tailored fork of `goja` (`internal/waa/`), running Google's official challenge interpreter, dynamic script, and snapshot proofs inside the Go process without launching or downloading Camoufox for proofs.
2. **Dual Upstream Channels (`Playground` + `Build`)**: Independent quota channels on the same account (`UPSTREAM_CHANNELS=playground,build`). Requests route across both channels with per-channel cooldowns and failover. Build channels proxy Gemini API calls via `ProxyStreamedCall` and `ProxyUnaryCall`.
3. **Omni & Interaction Stream Models**: Support for `CreateInteractionStream` wire protocol (`internal/aistudio/interaction.go`), Gemini 3.8 TTS multi-speaker modes (`VERBATIM` / `CONVERSATIONAL`), realtime translation, and realtime transcription.
4. **Camoufox HTTP Disk Cache & Profile Isolation**: Per-account disk cache directories with flock locking, stale profile auto-cleanup, and automatic Google Drive OAuth authorization during onboarding.
5. **Global Cooldown & Concurrency Refinements**: Request queuing under total candidate cooldown, exponential backoff when an account runtime is occupied by another process, and improved worker recycling.

This sync integrates all these changes, resolves 24 conflicted files, and translates all newly introduced Chinese comments, log messages, and error strings into standard concise English.

## Changes

1. **Conflict Resolution & Configuration Invariant**:
   - `internal/config/config.go`: Added `UPSTREAM_CHANNELS` and `WAA_BACKEND` alongside `HEADLESS` and `CAMOUFOX_PATH`. Discarded upstream runtime `.env` saving methods (`Save`, `atomicWrite`) to keep configuration strictly immutable at runtime.
   - `web/src/components/SettingsPanel.vue`: Preserved the read-only settings inspector; added informative displays for active `WAA Backend` (`camoufox` vs `go`) and enabled `Upstream Channels` (`playground`, `build`).
   - `internal/api/admin.go` & `internal/app/admin.go`: Preserved read-only `GET /api/config` without `PUT /api/config`.
2. **Pure-Go WAA Integration (`internal/waa/` & `internal/aistudio/runtime_go.go`)**:
   - Integrated `internal/waa/` and `internal/waa/goja/` package suite for in-process WAA challenge execution.
   - Translated all comments, error strings, and log messages across `internal/waa/` and `runtime_go.go` to English.
   - Wired `prepareWAABackend` and `newWAAWorker` in `internal/app/waa_backend.go`.
3. **Dual Upstream Channels (`internal/aistudio/channel.go` & `internal/aistudio/build.go`)**:
   - Integrated channel routing, candidate expansion, and per-channel cooldown tracking.
   - Added wire encoders and decoders for Build proxy RPCs (`ProxyStreamedCall`, `ProxyUnaryCall`).
   - Translated all comments and error strings in `channel.go` and `build.go` to English.
4. **Omni Interactions & Bidi Protocols**:
   - Integrated `CreateInteractionStream` request encoding and stream decoding in `internal/aistudio/interaction.go`.
   - Added speech metadata folding and multi-speaker mode options in `internal/aistudio/generate.go`.
   - Added realtime translation and transcription configurations in `internal/aistudio/bidi.go` and `internal/api/live.go`.
5. **Camoufox Isolation & Drive Consent**:
   - Translated cache locking and cleanup in `internal/camoufoxnative/cache.go` and `profile.go`.
   - Translated Drive authorization automation in `internal/camoufoxnative/drive.go`.
6. **Frontend & Documentation**:
   - Rebuilt frontend production bundle into `internal/webui/dist/`.
   - Updated `README.md`, `README_en.md`, `.env.example`, and `docs/development.md`.

## Result

- Clean merge into `sync/upstream-main` (`af8cc22`) and subsequent merge into `dev` (`1efd89f`).
- All Go tests pass cleanly (`go test ./...`).
- Static analysis clean (`go vet ./...` reports 0 issues).
- Frontend builds and passes all checks (`npm run typecheck`, `npm run lint`, `npm run format:check`).
- Zero Chinese characters in source code files.
- Application binary builds and runs cleanly (`go build ./cmd/aistudio2api`).

---

### Update: Upstream v0.2.2 Synchronization (2026-10-01)

**Task**: Synchronize `sync/upstream-main` with upstream commits up to `e89ba29` (release v0.2.2), resolve merge conflicts preserving read-only configuration, translate all newly introduced Chinese text to English, and merge into `dev`.

**Upstream Additions**:
1. **Interactions API**: Public `/v1/interactions` and `/v1beta/interactions` endpoints for Gemini 3.8 / Omni Interactions, structured output schema serialization, multi-turn stateless step tracking, and Gemini 3.8 TTS single/multi-speaker support.
2. **Build Native Non-Streaming Unary Calls**: `ProxyUnaryCall` support for non-streaming requests (`BUILD_NATIVE_NONSTREAM=true`) routed to Build with fallback to streaming.
3. **Thought Signature Normalization**: Normalized `thoughtSignature` attachment directly to content parts, merged adjacent thought/text fragments in Gemini output to eliminate extra newlines in SillyTavern.
4. **Admin Authentication**: Web UI login, rate limiting, SameSite session cookies, and loopback authentication separation.

**Resolution & Adaptations**:
- Preserved immutable configuration invariant (no `PUT /api/config` or runtime `.env` mutations).
- SettingsPanel retains read-only status displays for Admin Auth and Build Native Nonstream.
- Translated 24 files with 72 Chinese comment/log lines into concise technical English.
- Adapted `internal/app/admin_config_test.go` to test read-only config loading and restart flag detection.
- Merged into `dev` (`a02d32a`). All tests (`go test ./...`) and lint checks pass cleanly.