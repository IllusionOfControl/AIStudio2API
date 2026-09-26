# Dev Branch Change Log

Chronological registry of commits and feature integrations merged into the `dev` branch on top of upstream base (`Mag1cFall/AIStudio2API` commit `3c8b472`).

---

## Commit Summary Table

| Date | Commit | Author | Source Branch | Summary |
|---|---|---|---|---|
| **2026-09-26** | `0c91ef5` | Sergey Skorokhod | `docs/agents-documentation` | Add `AGENTS.md` and `docs/agents/` suite with Mermaid architecture diagrams |
| **2026-09-20** | `6e318e0` | Sergey Skorokhod | `refactor/readonly-config` | Make configuration immutable at runtime for Docker, add `HEADLESS` mode |
| **2026-09-20** | `eb81bd9` | Sergey Skorokhod | `refactor/translate-to-english` | Full translation of comments, logs, and error messages to English |

---

## Detailed Commit Log

### 2026-09-26 — `0c91ef5`
- **Commit**: `0c91ef5`
- **Author**: Sergey Skorokhod (`<sergeyskorokhod2@gmail.com>`)
- **Branch**: `docs/agents-documentation` $\rightarrow$ `dev`
- **What was done**:
  - Created root `AGENTS.md` quick-reference guide for AI agents and human contributors.
  - Implemented Mermaid-based architecture diagrams (`flowchart TD`, `graph TD`, `sequenceDiagram`).
  - Added modular documentation suite under `docs/agents/`:
    - `README.md`: Quick navigation and invariant checklist.
    - `architecture.md`: Subsystem responsibilities, data flow, WAA warm pool, and lock hierarchy.
    - `conventions.md`: English-only policy, Docker config invariant, and Go error handling.
    - `protocols.md`: MakerSuite wire format (`JSON+protobuf`), tool result correlation, and streaming decoders.
    - `workflows.md`: Step-by-step guides for upstream sync, capability extensions, and frontend builds.

---

### 2026-09-20 — `6e318e0`
- **Commit**: `6e318e0`
- **Author**: Sergey Skorokhod (`<sergeyskorokhod2@gmail.com>`)
- **Branch**: `refactor/readonly-config` $\rightarrow$ `dev`
- **What was done**:
  - Removed `.env` file mutation methods (`Config.Save`, `atomicWrite`) in `internal/config/config.go` to prevent Docker `EBUSY` mount errors.
  - Removed mutable `PUT /api/config` HTTP endpoint and `UpdateRuntimeConfig` method.
  - Redesigned Web UI Settings panel (`web/src/components/SettingsPanel.vue`) into a compact, read-only key-value configuration dashboard.
  - Added `HEADLESS` configuration parameter (default `true`) for headless vs visible Camoufox browser execution.
  - Added documentation and descriptions for `ROUTING_STRATEGY` (`round-robin` vs `fill-first`) and `CAMOUFOX_PATH`.

---

### 2026-09-20 — `eb81bd9`
- **Commit**: `eb81bd9`
- **Author**: Sergey Skorokhod (`<sergeyskorokhod2@gmail.com>`)
- **Branch**: `refactor/translate-to-english` $\rightarrow$ `dev`
- **What was done**:
  - Localized 95 Go source and configuration files into English across all packages (`internal/aistudio`, `internal/api`, `internal/camoufoxnative`, `internal/chromeauth`, `internal/app`, `internal/config`, `internal/setup`).
  - Translated all package and exported function/type docstrings to standard Go documentation conventions.
  - Translated runtime error messages (`fmt.Errorf`, `errors.New`) and system logger entries (`slog`) into idiomatic English.
  - Translated configuration and documentation files (`.env.example`, `.gitignore`, `docs/`).
  - Preserved all protocol wire invariants, JSON Schema normalization tests, and runtime behavior.
