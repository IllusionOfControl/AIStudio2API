# Development Conventions & Invariants

This guide outlines coding standards, language policies, configuration rules, and error handling conventions enforced in `AIStudio2API`.

---

## 1. Language & Documentation Policy

### English-Only Go Codebase
- **All code comments, struct docstrings, logs, and error strings must be in English.**
- When upstream features or bug fixes are integrated from `Mag1cFall/AIStudio2API`, they often arrive with Chinese comments and log messages. Agents **must** translate all new comments and error strings to concise, standard English before committing.

#### Translation Table for Common Upstream Terms

| Chinese Term | Standard English Equivalent |
|---|---|
| 官网 | official web / official page |
| 提示词 / 提示正文 | prompt / prompt text |
| 思考 / 思考签名 | thinking / thought signature |
| 工具结果 / 工具调用 | tool result / tool call |
| 无法调度 / 候选耗尽 | unschedulable / no eligible accounts |
| 冷却 | cooldown |
| 预热 | warm-up / pre-warming |
| 凭据续签 | credential renewal / refresh |
| 租约 | lease |
| 上游通道 | upstream channel |
| 纯 Go WAA | pure-Go WAA |
| 双通道 | dual channel |
| 占用 / 租约占用 | occupied / occupation |
| 解释器 | interpreter |

---

## 2. Configuration Immutability (Docker Invariant)

### Why Read-Only?
In containerized environments (such as Docker, Kubernetes, or read-only volume mounts), application processes cannot and should not modify their host `.env` files or execute self-restarts. Mutating `.env` at runtime causes split-brain state, mount permission crashes, and non-reproducible restarts.

### Rules
1. **No Runtime Modifiers**: Do **not** add HTTP endpoints like `PUT /api/config` or methods like `UpdateRuntimeConfig`.
2. **Environment Priority**: Configuration is loaded on startup from environment variables and `.env` files via `internal/config/config.go`.
3. **Web UI Role**: The Settings panel in `web/src/components/SettingsPanel.vue` is strictly an overview of active configuration parameters. It must remain read-only.
4. **Per-Account Config**: Account-specific properties (`label`, `proxy`, `locale`, `timezone`, `enabled`) live in `auth/<email>/account.json` and are modified exclusively via the Account management API (`PUT /api/accounts/{id}`).

---

## 3. Go Coding Standards

### Error Handling
- Wrap errors with descriptive context using `%w`:
  ```go
  if err != nil {
      return fmt.Errorf("encoding generate content request: %w", err)
  }
  ```
- Use `errors.Is` and `errors.As` instead of raw string comparisons or direct type assertions:
  ```go
  var notReady *aistudio.AccountsNotReadyError
  if errors.As(err, &notReady) {
      return http.StatusServiceUnavailable
  }
  ```
- Aggregate errors during cleanup using `errors.Join`:
  ```go
  defer func() {
      err = errors.Join(err, session.Close())
  }()
  ```

### Concurrency & Synchronization
- **Timers**: Always stop timers when finished to prevent goroutine leaks:
  ```go
  timer := time.NewTimer(delay)
  defer timer.Stop()
  select {
  case <-ctx.Done():
      return ctx.Err()
  case <-timer.C:
      // proceed
  }
  ```
- **Mutex Granularity**: Keep critical sections tight. Never perform blocking I/O (network requests, browser evaluations, or long sleep calls) while holding internal mutex locks.
- **Context Propagation**: Always pass `context.Context` to network calls, browser evaluations, and account leasing methods. Respect context cancellation immediately.

### Performance & Memory Efficiency
- **Streaming Hot Paths**: Avoid unnecessary allocations or re-encoding in the streaming path (`internal/api/*` and `internal/aistudio/decoder.go`).
- **Buffer Management**: Reuse byte buffers or pre-allocate slice capacities when array sizes are known (`make([]any, 0, len(parts))`).

---

## 4. Git Commit Guidelines

Follow the Conventional Commits format:
- `feat: <description>` — New user-facing or API feature.
- `fix: <description>` — Bug fix or protocol correction.
- `refactor: <description>` — Code reorganization without functional changes.
- `test: <description>` — Adding or updating test suites.
- `docs: <description>` — Documentation improvements.

Commit messages must be concise, accurate, and written in English.

---

## 5. Branch Documentation & Dev Merge Invariant

To maintain a clear historical audit trail of features, refactorings, and integrations:

1. **Branch Specification File**:
   - For every working branch (`feature/*`, `refactor/*`, `sync/*`, etc.), create a markdown document in `docs/branches/<branch-name>.md`.
   - Base the document on `docs/branches/_template.md` (metadata frontmatter with `branch`, `last_commit`, `status`, `last_activity`, followed by `Task`, `Description`, `Changes`, and `Result` sections).
   - Register the branch in the summary table of `docs/branches/_index.md`.

2. **Dev Merge Logging (`_dev.md`)**:
   - Whenever changes or branches are merged into `dev`, **must** add a corresponding record to `docs/branches/_dev.md`.
   - Record format:
     - Date (`YYYY-MM-DD`)
     - Commit hash
     - Author name and email
     - Source branch name
     - Concise bulleted summary of all changes merged
   - Update the branch status in `docs/branches/_index.md` from `in-process` or `ready` to `Merged into dev`.
