---
branch: dev
last_commit: 0c91ef5
status: in-process
last_activity: 26-09-2026
---

## Task

Primary development branch for the `AIStudio2API` fork. Consolidates complete English localization, Docker-safe immutable runtime configuration, agent architecture documentation with Mermaid diagrams, and establishes the baseline for upstream protocol synchronization.

## Description

The `dev` branch serves as the central integration branch for this repository. It merges and standardizes two foundational refactoring streams (`refactor/translate-to-english` and `refactor/readonly-config`) on top of upstream base commit `3c8b472`, and establishes comprehensive technical documentation (`AGENTS.md` and `docs/agents/`) to guide AI coding assistants and human contributors.

Key architectural pillars established in `dev`:
1. **Full English-Language Standardization**: Complete removal of Chinese comments, docstrings, error messages, and system logs across all Go packages to provide clean, unified English output and documentation.
2. **Immutable Runtime Configuration**: Eliminating `.env` file modification at runtime to prevent Docker volume inode lockups (`EBUSY: Device or resource busy`) and environment variable precedence conflicts in containerized deployments.
3. **Comprehensive Agent & Developer Guides**: Structured technical runbooks and Mermaid diagrams covering internal architecture, wire formats (`JSON+protobuf`), conventions, and development workflows.

## Changes

1. **English Codebase Localization (`eb81bd9`)**:
   - Translated comments, docstrings, errors, and logs across 95 files in `internal/aistudio`, `internal/api`, `internal/camoufoxnative`, `internal/chromeauth`, `internal/app`, `internal/config`, `internal/setup`, `.env.example`, and `.gitignore`.
   - Converted all runtime error messages (`fmt.Errorf`, `errors.New`) and `slog` messages to idiomatic English.
   - Preserved protocol invariants and JSON schema normalization behaviors.

2. **Docker-Safe Immutable Configuration (`6e318e0`)**:
   - Removed `.env` file mutation methods (`Config.Save`, `atomicWrite`) in `internal/config/config.go`.
   - Removed mutating `PUT /api/config` HTTP endpoint and `UpdateRuntimeConfig` method.
   - Redesigned the Web UI Settings panel (`web/src/components/SettingsPanel.vue`) into a compact, read-only key-value configuration overview with informational notice.
   - Exposed `HEADLESS` mode toggle in `config.Config` and `.env.example` for Camoufox browser execution control.
   - Added comprehensive documentation for `ROUTING_STRATEGY` (`round-robin` vs `fill-first`) and `CAMOUFOX_PATH`.

3. **Agent & Architecture Documentation (`0c91ef5`)**:
   - Created root `AGENTS.md` with an architectural overview, core invariants, directory layout, and verification checklists.
   - Created `docs/agents/` suite:
     - `README.md`: Navigation index and fast-reference rules.
     - `architecture.md`: Detailed component responsibilities, request lifecycle sequence diagram, and account file lock hierarchy.
     - `conventions.md`: Coding standards, English translation guide, Docker config invariants, and error handling rules.
     - `protocols.md`: MakerSuite `JSON+protobuf` array indexing, tool call correlation, and streaming decoders.
     - `workflows.md`: Step-by-step runbooks for upstream synchronization, adding capabilities, and troubleshooting.
   - Standardized architectural and sequence diagrams in `mermaid` format (`flowchart TD`, `graph TD`, `sequenceDiagram`).

## Result

- Unified, production-ready development baseline in `dev`.
- Clean compilation across all Go packages (`go build ./...`).
- 100% test pass rate across all unit and compatibility test suites (`go test ./...`).
- Frontend typechecks and builds cleanly (`npm --prefix web run typecheck`, `npm --prefix web run build`).
- Ready for merging upstream synchronization branch (`sync/upstream-main`).
