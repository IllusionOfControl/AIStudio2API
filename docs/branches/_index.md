# Repository Branches Overview

This directory contains historical documentation for active, merged, and archived branches in the `AIStudio2API` repository: what was changed, when, by whom, and in which files.

> **Rule for Agents & Contributors**:
> - Always create a branch document `docs/branches/<branch-name>.md` using `_template.md` when working on a branch.
> - Always log merges into `dev` in [`_dev.md`](_dev.md) and update the branch status below to `Merged into dev`.

## Summary Table

| Branch | Purpose / Scope | Active Date | Commits | Status | Document |
|---|---|---|---|---|---|
| [`dev`](_dev.md) | Central development branch consolidating English localization, Docker read-only config, and agent architecture documentation | 2026-09-26 | `0c91ef5` (3 commits) | Active | [_dev.md](_dev.md) |
| [`refactor/translate-to-english`](refactor-translate-to-english.md) | Full translation of comments, error messages, and logs to English across all Go packages | 2026-09-20 | `eb81bd9` (squashed) | Merged into `dev` | [refactor-translate-to-english.md](refactor-translate-to-english.md) |
| [`refactor/readonly-config`](refactor-readonly-config.md) | Immutable configuration at runtime for Docker environments, Camoufox HEADLESS mode, and simplified Web UI SettingsPanel | 2026-09-20 | `6e318e0` (squashed) | Merged into `dev` | [refactor-readonly-config.md](refactor-readonly-config.md) |
| [`docs/agents-documentation`](docs-agents-documentation.md) | Technical documentation suite (`AGENTS.md` and `docs/agents/`) with v0.2.0 architectural updates | 2026-09-27 | `9cd9b79` | Merged into `dev` | [docs-agents-documentation.md](docs-agents-documentation.md) |
| [`sync/upstream-main`](sync-upstream-main.md) | Upstream v0.2.2 integration (Interactions API, Build native Unary calls, thought signature normalization, admin auth) | 2026-10-01 | `a02d32a` (merge) | Merged into `dev` | [sync-upstream-main.md](sync-upstream-main.md) |
| [`feature/waa-auto-start`](feature-waa-auto-start.md) | Auto-start WAA / generation service on application launch via AUTO_START in .env and --auto-start CLI flag | 2026-09-29 | `b994a57` | Merged into `dev` | [feature-waa-auto-start.md](feature-waa-auto-start.md) |
