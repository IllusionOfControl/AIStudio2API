package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const aiStudioOrigin = "https://aistudio.google.com"

const generateContentPath = "/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GenerateContent"

var publicHeaderNames = []string{
	"x-goog-api-key",
	"x-goog-authuser",
	"x-user-agent",
	"x-aistudio-g1-tier",
	"x-aistudio-visit-id",
	"x-goog-ext-519733851-bin",
	"user-agent",
}

// Worker manages a persistent Camoufox process and WAA service for a single account.
type Worker struct {
	mu         sync.Mutex
	process    *browserProcess
	connection *websocket.Conn
	client     *bidiClient
	contextID  string
	state      State
	closed     bool
}

// Start launches isolated Camoufox and performs an initial WAA bootstrap on the official site.
func Start(ctx context.Context, options Options) (*Worker, error) {
	state, err := loadStorageState(options.StorageStatePath)
	if err != nil {
		return nil, err
	}
	if options.Model == "" {
		return nil, errors.New("WAA bootstrap missing live catalog chat model")
	}
	if options.BootstrapPrompt == "" {
		options.BootstrapPrompt = fmt.Sprintf("AIStudio2API bootstrap %d", time.Now().UnixNano())
	}
	options.reportStartup(StartupPreparingBrowser)
	ffVersion, err := browserMajor(options.ExecutablePath)
	if err != nil {
		return nil, err
	}
	fingerprint, err := loadAccountCamoufoxConfig(options.StorageStatePath, ffVersion, options.Locale, options.Timezone)
	if err != nil {
		return nil, err
	}
	options.reportStartup(StartupLaunchingBrowser)
	process, endpoint, err := launchBrowser(ctx, options, fingerprint)
	if err != nil {
		return nil, err
	}
	worker := &Worker{process: process}
	failed := true
	defer func() {
		if failed {
			_ = worker.Close()
		}
	}()
	options.reportStartup(StartupConnectingBiDi)
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	connection, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("connect Camoufox BiDi: %w", err)
	}
	worker.connection = connection
	worker.client = newBiDiClient(connection)
	if err := worker.bootstrap(ctx, options, state); err != nil {
		return nil, err
	}
	failed = false
	return worker, nil
}

// ProtocolHeaders returns the public headers captured for GenerateContent.
func (worker *Worker) ProtocolHeaders(ctx context.Context) (http.Header, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if worker.closed {
		return nil, errors.New("Camoufox runtime is closed")
	}
	return worker.state.Headers.Clone(), nil
}

// Proof generates a fresh WAA proof for a given SHA-256 digest.
func (worker *Worker) Proof(ctx context.Context, digest string) (string, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return "", errors.New("Camoufox runtime is closed")
	}
	proof, err := worker.client.evaluateString(ctx, worker.contextID, takeProofExpression(digest))
	if err != nil {
		return "", fmt.Errorf("generate fresh WAA proof: %w", err)
	}
	if !strings.HasPrefix(proof, "!") {
		return "", errors.New("invalid fresh WAA proof prefix")
	}
	return proof, nil
}

// State returns an immutable snapshot of runtime state.
func (worker *Worker) State() State {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	state := worker.state
	state.Headers = state.Headers.Clone()
	return state
}

// Close terminates the BiDi session and cleans up the Camoufox profile.
func (worker *Worker) Close() error {
	if worker == nil {
		return nil
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil
	}
	worker.closed = true
	client := worker.client
	connection := worker.connection
	process := worker.process
	worker.mu.Unlock()
	if client != nil && connection != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = client.command(closeCtx, "session.end", map[string]any{})
		cancel()
		_ = connection.Close()
	}
	return process.Close()
}

func (worker *Worker) bootstrap(ctx context.Context, options Options, storage storageState) error {
	client := worker.client
	if _, err := client.command(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
		return err
	}
	tree, err := client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return err
	}
	contexts, _ := tree["contexts"].([]any)
	if len(contexts) == 0 {
		return errors.New("Camoufox BiDi did not return initial tab")
	}
	root, _ := contexts[0].(map[string]any)
	contextID, _ := root["context"].(string)
	if contextID == "" {
		return errors.New("invalid initial tab from Camoufox BiDi")
	}
	worker.contextID = contextID
	if err := client.installLocalStorage(ctx, contextID, storage.Origins); err != nil {
		return err
	}
	if err := client.installCookies(ctx, storage.Cookies); err != nil {
		return err
	}
	options.reportStartup(StartupLoadingAIStudio)
	target := aiStudioOrigin + "/prompts/new_chat?model=" + url.QueryEscape(options.Model)
	if options.TemporaryChat {
		target += "&temporary=true"
	}
	if _, err := client.command(ctx, "browsingContext.navigate", map[string]any{
		"context": contextID,
		"url":     target,
		"wait":    "interactive",
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		return fmt.Errorf("navigate to AI Studio: %w", err)
	}
	if err := client.waitFor(ctx, contextID, `(() => {
  const item = document.querySelector('ms-prompt-box textarea:last-of-type') || [...document.querySelectorAll('ms-prompt-box textarea')].at(-1);
  return Boolean(item && item.offsetParent !== null);
})()`, 120*time.Second); err != nil {
		pageURL, _ := client.evaluateString(ctx, contextID, "location.href")
		return fmt.Errorf("AI Studio prompt box not ready url=%s: %w", pageURL, err)
	}
	pageURL, err := client.evaluateString(ctx, contextID, "location.href")
	if err != nil {
		return err
	}
	if strings.Contains(pageURL, "accounts.google.com") {
		return fmt.Errorf("isolated login state expired url=%s", pageURL)
	}
	_, _ = client.evaluate(ctx, contextID, `(() => {
  const bar = document.querySelector('#glue-cookie-notification-bar-1');
  const button = bar?.querySelector('.glue-cookie-notification-bar__reject');
  if (button && button.offsetParent !== null) button.click();
  return Boolean(button);
})()`)
	if err := dismissVisibleDialogs(ctx, client, contextID); err != nil {
		return err
	}
	options.reportStartup(StartupLocatingWAA)
	snapshotKey, err := client.waitSnapshotFunction(ctx, contextID, 30*time.Second)
	if err != nil {
		return err
	}
	options.reportStartup(StartupBootstrappingWAA)
	filled, err := client.evaluateString(ctx, contextID, fillPromptExpression(options.BootstrapPrompt))
	if err != nil || filled != options.BootstrapPrompt {
		return fmt.Errorf("failed to fill bootstrap prompt value=%q err=%v", filled, err)
	}
	if _, err := client.command(ctx, "session.subscribe", map[string]any{
		"events":   []string{"network.beforeRequestSent"},
		"contexts": []string{contextID},
	}); err != nil {
		return err
	}
	intercept, err := client.command(ctx, "network.addIntercept", map[string]any{
		"phases":   []string{"beforeRequestSent"},
		"contexts": []string{contextID},
		"urlPatterns": []map[string]any{{
			"type":     "pattern",
			"protocol": "https",
			"hostname": "alkalimakersuite-pa.clients6.google.com",
			"pathname": generateContentPath,
		}},
	})
	if err != nil {
		return fmt.Errorf("install GenerateContent intercept: %w", err)
	}
	interceptID, _ := intercept["intercept"].(string)
	if interceptID == "" {
		return errors.New("invalid GenerateContent intercept ID")
	}
	clicked, err := client.evaluateBool(ctx, contextID, `(() => {
  const button = [...document.querySelectorAll('ms-run-button button[type="submit"]')].at(-1);
  if (!button || button.disabled) return false;
  button.click();
  return true;
})()`)
	if err != nil || !clicked {
		return fmt.Errorf("official Run button unavailable clicked=%t err=%v", clicked, err)
	}
	if err := client.waitFor(ctx, contextID, "Boolean(window.__aistudioWaaService)", 60*time.Second); err != nil {
		return fmt.Errorf("official WAA service not exposed: %w", err)
	}
	requestID, err := client.waitBlockedGenerateRequest(ctx, contextID, 60*time.Second)
	if err != nil {
		return err
	}
	if _, err := client.command(ctx, "network.failRequest", map[string]any{"request": requestID}); err != nil {
		return fmt.Errorf("terminate bootstrap GenerateContent: %w", err)
	}
	headers := make(http.Header, len(publicHeaderNames))
	for _, name := range publicHeaderNames {
		value := client.generateHeaders[name]
		if value != "" {
			headers.Set(name, value)
		}
	}
	for _, name := range []string{"user-agent", "x-goog-api-key", "x-goog-authuser", "x-user-agent"} {
		if headers.Get(name) == "" {
			return fmt.Errorf("official GenerateContent missing required header %s", name)
		}
	}
	userAgent, _ := client.evaluateString(ctx, contextID, "navigator.userAgent")
	platform, _ := client.evaluateString(ctx, contextID, "navigator.platform")
	timezone, _ := client.evaluateString(ctx, contextID, "Intl.DateTimeFormat().resolvedOptions().timeZone")
	worker.state = State{
		PID:         worker.process.command.Process.Pid,
		PageURL:     pageURL,
		UserAgent:   userAgent,
		Platform:    platform,
		Timezone:    timezone,
		SnapshotKey: snapshotKey,
		Headers:     headers,
	}
	return nil
}

// dismissVisibleDialogs closes visible announcement dialogs appearing on page startup.
func dismissVisibleDialogs(ctx context.Context, client *bidiClient, contextID string) error {
	for range 8 {
		clicked, err := client.evaluateBool(ctx, contextID, `(() => {
  const visible = (item) => item instanceof HTMLElement && item.offsetParent !== null;
  const dialogs = [...document.querySelectorAll('dialog[open], [role="dialog"]')].filter(visible);
  for (const dialog of dialogs.reverse()) {
    const buttons = [...dialog.querySelectorAll('button, [role="button"]')]
      .filter((button) => visible(button) && !button.disabled && button.getAttribute('aria-disabled') !== 'true');
    if (buttons.length === 0) continue;
    buttons.at(-1).click();
    return true;
  }
  return false;
})()`)
		if err != nil {
			return fmt.Errorf("dismiss AI Studio dialogs: %w", err)
		}
		if !clicked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

func loadStorageState(path string) (storageState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return storageState{}, fmt.Errorf("read storage state: %w", err)
	}
	var state storageState
	if err := json.Unmarshal(data, &state); err != nil {
		return storageState{}, fmt.Errorf("parse storage state: %w", err)
	}
	if len(state.Cookies) == 0 {
		return storageState{}, errors.New("storage state has no cookies")
	}
	return state, nil
}
