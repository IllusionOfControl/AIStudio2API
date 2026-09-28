---
branch: feature/waa-auto-start
last_commit: 0dc94bd
status: ready
last_activity: 29-09-2026
---

## Task

Add auto-start capability for WAA / generation service on application launch and expose configuration in `.env`.
## Description

By default, AIStudio2API starts the HTTP admin server while leaving the generation service in a stopped state until manually started via the Web UI (`POST /api/control/start`). This feature introduces an `AUTO_START` configuration option (with `WAA_AUTO_START` alias and `--auto-start` CLI flag) to automatically trigger model catalog sync and WAA worker prewarming in the background immediately upon server startup.

## Changes

- **`internal/config/config.go`**: Added `AUTO_START` (and `WAA_AUTO_START` alias) environment variable parsing, `defaultAutoStart = false`, and `AutoStart bool` field in `Config`. Added unit tests in `internal/config/config_test.go`.
- **`internal/api/admin.go`**: Added `AutoStart` field to `RuntimeConfig` DTO.
- **`internal/app/admin.go`**: Mapped `AutoStart` in `runtimeConfigDTO`.
- **`internal/app/lifecycle.go`**: Added `autoStart` to `dataConfigOverrides` and `sameDataConfig`.
- **`internal/app/app.go`**: Added `--auto-start` CLI flag to `parseFlags`. Triggered `manager.StartService(ctx)` asynchronously in a background goroutine within `runServer` when `cfg.AutoStart` is enabled. Added unit tests in `internal/app/autostart_test.go`.
- **`web/`**: Added `auto_start` to `ServiceConfig` (`types.ts`), added localized strings in `i18n.ts` (en / zh), and added WAA Auto-start status inspector row in `SettingsPanel.vue`. Rebuilt frontend assets (`internal/webui/dist/`).
- **`.env.example`**: Documented `AUTO_START=false`.

## Result

Users can now enable `AUTO_START=true` or pass `--auto-start` to automatically launch the WAA generation service upon process startup without requiring interaction with the Web UI.
