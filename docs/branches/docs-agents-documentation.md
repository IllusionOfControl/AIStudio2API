---
branch: docs/agents-documentation
last_commit: 0c91ef5
status: completed
last_activity: 26-09-2026
---

## Task

Create comprehensive developer and AI agent documentation (`AGENTS.md` and `docs/agents/`) with Mermaid architecture and sequence diagrams to guide contributors, maintain invariants, and document core protocols.

## Description

As the codebase grew to support multiple upstream protocols, custom WAA proof mechanisms, and local Chrome credential recovery, onboarding human contributors and AI coding agents required clear, modular documentation.

This branch introduces:
1. Root `AGENTS.md` quick-reference guide.
2. A dedicated documentation suite in `docs/agents/` detailing architecture, conventions, wire protocols, and operational runbooks.
3. Interactive Mermaid flowcharts, component graphs, and request sequence diagrams.

## Changes

1. **Root Guide (`AGENTS.md`)**:
   - Outlined project capabilities (OpenAI, Anthropic, Gemini, Responses, Realtime, Media).
   - Documented core architecture using a Mermaid flowchart (`flowchart TD`).
   - Detailed repository invariants (English-only logs/code, Docker read-only config, Chrome Account IDs).
   - Added development commands and modification checklists.
2. **Agent Documentation Suite (`docs/agents/`)**:
   - `README.md`: Index and invariant quick reference.
   - `architecture.md`: Component responsibilities, Mermaid component graph (`graph TD`), and end-to-end request sequence diagram (`sequenceDiagram`).
   - `conventions.md`: Coding standards, Go error patterns, and branch documentation rules.
   - `protocols.md`: MakerSuite `JSON+protobuf` array indexing, tool call correlation, and streaming decoders.
   - `workflows.md`: Step-by-step guides for upstream synchronization, capability extensions, and frontend builds.

## Result

- Complete architectural and operational documentation suite available in the repository.
- Clear reference rules established for all future AI agents and contributors.
