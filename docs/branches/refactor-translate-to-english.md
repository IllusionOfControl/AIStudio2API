---
branch: refactor/translate-to-english
last_commit: 372f394
status: complete
last_activity: 20-09-2026
---

## Task

Completely localize the codebase into English by translating all Chinese code comments, exported type/method docstrings, system logs (`slog`), error descriptions (`fmt.Errorf`, `errors.New`), and configuration files.

## Description

Across all primary Go packages (`internal/aistudio`, `internal/api`, `internal/camoufoxnative`, `internal/chromeauth`, `internal/app`, `internal/config`, `internal/setup`), comments, error strings, and runtime logs were originally authored in Chinese. This introduced friction for international contributors, produced mixed-language console output, and reduced documentation clarity.

The objective of this branch is to translate all comments, error messages, and log entries into idiomatic English conforming to standard Go conventions, while strictly preserving protocol invariants, JSON schema validation rules, and runtime behavior.

## Changes

1. **`internal/aistudio/`** (35 files):
   - Translated all package, type, interface, and function docstrings to adhere to Go conventions (`// SymbolName ...`).
   - Standardized sentinel error definitions (`ErrInvalidArgument`, `ErrModelNotFound`, `ErrNoEligibleAccount`, `ErrAccountNotFound`, `ErrAccountLeased`, `ErrResourceNotFound`) and contextual error wrapping.
   - Converted quota cooldown categories and reasons (`minute_quota`, `daily_quota`).
   - Preserved protocol invariants and regression test assertions (including `"const"` substring matching in JSON Schema normalization).
2. **`internal/api/`** (12 files):
   - Localized OpenAI, Gemini, Anthropic, Files, Live WebSocket, and Admin panel route handlers and middleware.
   - Translated HTTP error responses and request validation messages.
3. **`internal/camoufoxnative/`** (12 files):
   - Translated browser lifecycle logs, WebDriver BiDi communication errors, DOM helper messages, and process isolation routines.
4. **`internal/chromeauth/`** (10 files):
   - Translated Windows App-Bound Encryption (ABE) routines, C decryptor helper (`abe_helper.c`), DPAPI / NCrypt implementations, and OAuth multilogin protocols.
5. **`internal/app/`, `internal/config/`, `internal/setup/`, `internal/webui/`**:
   - Localized runtime orchestration logging, admin SSE event streams, configuration parsing/validation, and setup wizard prompts.
6. **Repository Configuration**:
   - Translated comments in `.env.example` and `.gitignore`.
7. **Pull/Merge Request Preparation**:
   - Generated and saved the complete Merge Request description draft in `mr.md`.

## Result

- Zero Chinese characters (`[\u4e00-\u9fff]`) remaining in all Go and C source files across the repository.
- All unit, integration, and protocol compatibility test suites pass cleanly (`go test ./...`).
- Clean compilation without errors or warnings (`go build ./...`).
- Ready-to-use Merge Request documentation created in `mr.md`.
