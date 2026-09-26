---
branch: refactor/readonly-config
last_commit: d29366c
status: complete
last_activity: 20-09-2026
---

## Task

Make service configuration read-only at runtime by removing `.env` file mutation, optimize settings for Docker and containerized environments, expose Camoufox `HEADLESS` mode in configuration, and simplify the Web UI Service Config panel into a clean informational overview.

## Description

Previously, saving settings in the Web UI attempted to rewrite the `.env` file via atomic rename (`os.Rename`).
In Docker and server environments, this causes critical failures:
1. **Mount errors (`EBUSY: Device or resource busy`)**: when bind-mounting an `.env` file into a container (`volumes: [ ./.env:/app/.env ]`), atomic replacement via temporary file rename fails under Linux due to locked file inodes.
2. **Environment precedence conflict**: when configuration is injected via container environment variables (`environment:` in Compose or `-e`), OS environment variables take precedence over `.env` on startup. Changes saved through the Web UI to the file are effectively overridden and lost on restart.
3. **Container immutability**: in containerized and cloud deployments, configuration should be declarative, injected from the outside, and immutable (read-only) at runtime.

Furthermore, Camoufox WAA workers were hardcoded to headless mode without user control, and the Web UI Settings panel was overly complex with nested cards.

This branch eliminates runtime `.env` mutation, makes configuration strictly read-only, exposes `HEADLESS` mode, adds detailed documentation for `ROUTING_STRATEGY`, and redesigns the Web UI Service Config into a simplified, compact key-value display.

## Changes

1. **`internal/config/config.go`**:
   - Removed `(c Config) Save(path string)`, `formatEnvValue`, and `atomicWrite`.
   - Removed unused imports (`bytes`, `path/filepath`).
   - Added `HEADLESS` to `configKeys`, `Config` struct (`Headless bool`), `Default()`, `Load()`, `MarshalJSON()`, and `UnmarshalJSON()`.
   - Added `CAMOUFOX_PATH` to `configKeys` and exported it to `os.Setenv` when loaded from `.env`.
2. **`internal/api/admin.go` & `internal/app/`**:
   - Removed `UpdateRuntimeConfig` from `AdminService` interface, `*runtimeAdmin`, and `*runtimeManager`.
   - Removed `PUT /api/config` endpoint from API router; `GET /api/config` serves as a read-only endpoint.
   - Wired `Headless` from `config.Config` into `newAccountWorkerManager` and `workerConfigFor` in `internal/app/runtime.go`.
   - Updated `sameDataConfig` in `internal/app/lifecycle.go` to compare `Headless`.
3. **Web UI (`web/src/components/SettingsPanel.vue`, `web/src/types.ts`, `web/src/i18n.ts`, `web/src/App.vue`)**:
   - Redesigned `SettingsPanel.vue` from an editable form with nested cards into a compact, clean two-section key-value dashboard (Network & Access, Workers & Scheduling).
   - Added a prominent callout block informing the user that settings are loaded on startup from environment / `.env` and are read-only at runtime.
   - Removed the refresh button and obsolete save handlers.
   - Added `headless: boolean` to `ServiceConfig` and localized descriptions in `web/src/i18n.ts`.
4. **Environment & Documentation (`.env.example`, `docs/`, `README*.md`)**:
   - Added `HEADLESS=true` to `.env.example`, `README.md`, `README_en.md`, and `docs/development.md`.
   - Added detailed descriptions for `ROUTING_STRATEGY` (`round-robin` vs `fill-first`).
   - Documented `CAMOUFOX_PATH` and Docker listener guidance (`0.0.0.0:2048`).
   - Updated API docs in `docs/development.md`, `docs/protocol.md`, and `docs/logging.md` to reflect `GET /api/config` read-only status.

## Result

- Completely eliminated `.env` file writes and `PUT /api/config`, preventing Docker `EBUSY` mount errors and state drift.
- Full support for `HEADLESS` mode toggle (background silent execution vs visible browser debugging).
- Clean, compact, responsive Web UI settings overview without visual clutter.
- All Go tests pass cleanly (`go test ./...`) and binary compiles (`go build ./cmd/aistudio2api`).
- Frontend builds cleanly without type or lint errors (`vue-tsc --noEmit && vite build`, `eslint`, `prettier`).
