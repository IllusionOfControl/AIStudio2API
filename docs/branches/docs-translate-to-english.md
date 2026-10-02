---
branch: docs/translate-to-english
last_commit: 3702587
status: completed
last_activity: 02-10-2026
---

## Task

Translate all technical documentation in `docs/` (`development.md`, `logging.md`, `build.md`, `reverse-engineering.md`, `waa.md`, `protocol.md`) from Chinese to English.

## Description

The technical documentation under `docs/` was inherited from the upstream `Mag1cFall/AIStudio2API` repository in Chinese. To comply with the repository-wide English documentation policy (`AGENTS.md`) and make the technical specifications accessible to international developers, this branch translates all core documentation files into idiomatic English while strictly preserving technical terminology, markdown tables, code blocks, protocol field mappings, and cross-references.

## Changes

1. **`docs/development.md`**: Translated environment setup, first-run workflows, account onboarding, Chrome import mechanisms, frontend build instructions, and testing guidelines.
2. **`docs/logging.md`**: Translated runtime logging structures, SSE admin event contracts, logging levels, diagnostics, and monitoring fields.
3. **`docs/build.md`**: Translated Build channel architecture, iframe sandbox mechanisms, proxy RPC wire format, model quota isolation, and field mappings.
4. **`docs/reverse-engineering.md`**: Translated private protocol reverse engineering workflows, protocol layering, wire format reconstruction, and testing methodologies.
5. **`docs/waa.md`**: Translated Web Application Attestation (WAA) dual-backend architecture (`camoufox` and `go`), BotGuard VM lifecycle, Firefox 152 DOM simulation in Goja, and proof generation mechanics.
6. **`docs/protocol.md`**: Translated complete MakerSuite private protocol specification, JSON+protobuf array conventions, RPC endpoints, tool calling, media handling, and streaming events.

## Result

- All 6 core documentation files in `docs/` fully translated into clear, idiomatic English.
- All internal markdown links, code blocks, tables, and wire schema indices preserved accurately.
