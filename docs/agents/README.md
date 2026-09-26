# Agent & Contributor Documentation

Welcome to the **AIStudio2API** Agent Documentation directory. This documentation is tailored for AI coding agents and human contributors maintaining, extending, and debugging the codebase.

---

## Documentation Index

| Document | Description |
|---|---|
| [`architecture.md`](architecture.md) | Component architecture, subsystem responsibilities, concurrency model, and WAA runtime lifecycle. |
| [`conventions.md`](conventions.md) | Coding style, English-only naming/comments/logs, error handling patterns, and Docker read-only config invariants. |
| [`protocols.md`](protocols.md) | MakerSuite wire format (`JSON+protobuf` array indexing), public API mapping, tool calls, and streaming mechanics. |
| [`workflows.md`](workflows.md) | Step-by-step procedures for upstream synchronization, adding new capabilities, frontend builds, and automated verification. |

---

## Quick Reference: Invariant Rules

1. **Language Invariant**: All Go comments, log entries, and error strings **MUST** be written in English.
2. **Configuration Invariant**: Runtime configuration is **strictly read-only**. Do not reintroduce mutable `/api/config` endpoints or runtime `.env` saving.
3. **Array Indexing Invariant**: In MakerSuite protobuf wire encoding, arrays are 0-indexed while protobuf fields are 1-indexed (`protobuf field N` corresponds to array index `N-1`).
4. **Verification Invariant**: Before concluding any code change turn, run `go test ./...` and `npm --prefix web run typecheck`.
5. **Branch Tracking & Dev Merge Invariant**: Always document branches in `docs/branches/<branch-name>.md` and log every merge into `dev` in `docs/branches/_dev.md`.
