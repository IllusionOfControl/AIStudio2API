# Development Workflows & Runbooks

This guide provides practical, step-by-step runbooks for common contributor tasks, including upstream synchronization, capability extensions, frontend modifications, and automated verification.

---

## 1. Upstream Synchronization Workflow

Because this repository maintains independent refactors (English codebase, read-only Docker config), upstream updates from `Mag1cFall/AIStudio2API` must be integrated via dedicated sync branches.

### Step-by-Step Procedure

1. **Fetch Upstream Changes**:
   ```bash
   git fetch upstream
   ```
2. **Update Local `upstream-main` Branch**:
   ```bash
   git branch -f upstream-main upstream/main
   ```
3. **Create a Dedicated Sync Branch**:
   ```bash
   git checkout dev
   git checkout -b sync/upstream-sync
   ```
4. **Merge Upstream**:
   ```bash
   git merge upstream-main
   ```
5. **Resolve Conflicts with Invariant Checks**:
   - **Language Check**: Translate all newly introduced Chinese comments, struct field descriptions, and error strings into clear English.
   - **Config Check**: Ensure `internal/config/config.go` and runtime configuration remain strictly read-only. Discard any upstream attempts to reintroduce mutable runtime config endpoints (`PUT /api/config`).
   - **Account Import Format**: Use the updated account-level ID structure (`AccountIDs []string`, `id: "<Profile>/<GaiaID>"`).
6. **Compile & Run Test Suites**:
   ```bash
   go test ./...
   go vet ./...
   npm --prefix web run typecheck
   ```
7. **Commit & Merge to `dev`**:
   Once verified, merge `sync/upstream-sync` into `dev`.

---

## 2. Adding or Updating Model Capabilities

When Google AI Studio introduces new models or capability flags:

1. **Model Discovery (`internal/aistudio/models.go`)**:
   - Check `decodeGenerationDefaults`: reads raw protobuf fields from `ListModels` responses.
   - Add new capability flags to `GenerationDefaults` (e.g. `ImageRoute`, `OutputResolution`).
2. **Wire Encoding (`internal/aistudio/generate.go`)**:
   - In `EncodeGenerateContentRequest`, apply default slots or constraints required for the model.
3. **Public API Projections (`internal/api/*`)**:
   - Update `openai.go`, `anthropic.go`, `gemini.go`, or `responses.go` if the capability introduces new options or parameters.
4. **Unit Testing (`internal/aistudio/compatibility_test.go`)**:
   - Add test cases asserting correct wire array encoding and event decoding.

---

## 3. Frontend Development & Build Workflow

The web UI is built with Vue 3, Vite, and Tailwind CSS. The compiled assets are embedded directly into the Go binary.

### Local Development Loop

1. **Start the Frontend Dev Server**:
   ```bash
   cd web
   npm run dev
   ```
2. **Start the Backend API**:
   ```bash
   go run ./cmd/aistudio2api --listen 127.0.0.1:2048
   ```

### Building for Production & Go Embedding

Before committing frontend changes or building the release binary, build the production frontend:

```bash
cd web
npm run typecheck
npm run build
```

This compiles assets into `internal/webui/dist/`, which is embedded via Go's `//go:embed dist` directive in `internal/webui/embed.go`.

---

## 4. Troubleshooting Common Failures

### 1. WAA Proof Timeout / 502 Bad Gateway
- **Cause**: The official page textarea failed to match the prompt digest.
- **Check**: Verify that CRLF vs LF line endings are normalized via `normalizePromptNewlines()`. Windows clients frequently send `\r\n` which breaks byte-for-byte comparisons against browser textarea values.

### 2. 403 Code 7 ("The caller does not have permission")
- **Cause**: Upstream protocol constraint violation on MakerSuite endpoints.
- **Check for Image Models**:
  - `responseModalities` must be `[IMAGE, TEXT]`.
  - Wire slot 2 (`safetySettings`) must be null.
  - Header `X-Goog-Ext-519733851-Bin` must be stripped.

### 3. "No Eligible Accounts" vs "Account Unavailable"
- **HTTP 400 (`account_required`)**: No account in the pool has the capability, method, or tier required for the requested model.
- **HTTP 503 (`account_unavailable`)**: Suitable accounts exist, but all are currently in cooldown, require re-authentication, or are disabled.
