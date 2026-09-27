package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// NativeWorker adapts Camoufox or pure-Go WAA runtime as a protected request preparer
type NativeWorker struct {
	accountID   string
	runtime     workerRuntime
	operationMu sync.Mutex
	stateMu     sync.RWMutex
	state       WorkerState
}

var _ ProtectedPreparer = (*NativeWorker)(nil)
var _ ProtocolHeaderProvider = (*NativeWorker)(nil)

// NewNativeWorker starts a Camoufox WAA runtime for a single account
func NewNativeWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*NativeWorker, error) {
	if accountID == "" {
		return nil, fmt.Errorf("missing account ID")
	}
	runtime, err := camoufoxnative.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	runtimeState := runtime.State()
	return &NativeWorker{
		accountID: accountID,
		runtime:   runtime,
		state: WorkerState{
			AccountID: accountID,
			Phase:     WorkerReady,
			PID:       runtimeState.PID,
			RuntimeID: "native-webdriver-bidi",
			PageURL:   runtimeState.PageURL,
		},
	}, nil
}

// Prepare generates fresh proof and writes to the request's designated WAA field
func (worker *NativeWorker) Prepare(ctx context.Context, request ProtectedRequest) (PreparedProtectedRequest, error) {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerBusy
		state.RequestCount++
		state.LastError = ""
	})
	digest := sha256.Sum256([]byte(request.Prompt))
	proof, err := worker.runtime.Proof(ctx, fmt.Sprintf("%x", digest), request.Prompt)
	if err != nil {
		if ctx.Err() != nil {
			worker.updateState(func(state *WorkerState) { state.Phase = WorkerReady })
		} else {
			worker.fail(err)
		}
		return PreparedProtectedRequest{}, err
	}
	var payload []any
	if err := json.Unmarshal(request.Body, &payload); err != nil {
		worker.fail(err)
		return PreparedProtectedRequest{}, fmt.Errorf("parse protected request: %w", err)
	}
	if request.ProofField < 1 || len(payload) < request.ProofField {
		err := fmt.Errorf("protected request missing WAA field %d", request.ProofField)
		worker.fail(err)
		return PreparedProtectedRequest{}, err
	}
	payload[request.ProofField-1] = proof
	body, err := json.Marshal(payload)
	if err != nil {
		worker.fail(err)
		return PreparedProtectedRequest{}, fmt.Errorf("encode protected request: %w", err)
	}
	headers, err := worker.runtime.ProtocolHeaders(ctx)
	if err != nil {
		if ctx.Err() != nil {
			worker.updateState(func(state *WorkerState) { state.Phase = WorkerReady })
		} else {
			worker.fail(err)
		}
		return PreparedProtectedRequest{}, err
	}
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerReady
	})
	return PreparedProtectedRequest{
		Body:    body,
		Headers: headers,
	}, nil
}

// SendProtected streams the prepared request via the account's WAA runtime
func (worker *NativeWorker) SendProtected(ctx context.Context, request ProtectedRequest) (*RPCResponse, error) {
	response, err := worker.runtime.SendProtected(ctx, request.URL, request.Headers, request.Body)
	if err != nil {
		if ctx.Err() == nil {
			worker.fail(err)
		}
		return nil, err
	}
	return &RPCResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
	}, nil
}

// BrowserStorageState returns the current cookie state of the account's WAA runtime
func (worker *NativeWorker) BrowserStorageState(ctx context.Context) (StorageState, error) {
	encoded, err := worker.runtime.StorageCookies(ctx)
	if err != nil {
		return StorageState{}, err
	}
	var cookies []StateCookie
	if err := json.Unmarshal(encoded, &cookies); err != nil {
		return StorageState{}, fmt.Errorf("parse browser cookies: %w", err)
	}
	state := StorageState{Cookies: cookies}
	if err := state.Validate(); err != nil {
		return StorageState{}, err
	}
	return state, nil
}

// ProtocolHeaders returns dynamic common headers for official requests of the current account
func (worker *NativeWorker) ProtocolHeaders(ctx context.Context, accountID string) (http.Header, error) {
	if accountID != "" && accountID != worker.accountID {
		return nil, fmt.Errorf("runtime account mismatch")
	}
	return worker.runtime.ProtocolHeaders(ctx)
}

// State returns the account's WAA runtime state
func (worker *NativeWorker) State() WorkerState {
	worker.stateMu.RLock()
	defer worker.stateMu.RUnlock()
	return worker.state
}

// Close shuts down the account's WAA runtime
func (worker *NativeWorker) Close() error {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerClosing
		state.LastError = ""
	})
	err := worker.runtime.Close()
	worker.updateState(func(state *WorkerState) {
		if err != nil {
			state.LastError = err.Error()
			return
		}
		state.Phase = WorkerClosed
	})
	return err
}

func (worker *NativeWorker) updateState(update func(*WorkerState)) {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	update(&worker.state)
}

func (worker *NativeWorker) fail(err error) {
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerFailed
		state.LastError = err.Error()
	})
}
