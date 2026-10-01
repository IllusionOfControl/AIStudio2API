package aistudio

import "context"

// RequestPhase represents the current preparation phase of a protected request
type RequestPhase string

const (
	// RequestPhasePreparingWAA indicates generating a fresh WAA proof
	RequestPhasePreparingWAA RequestPhase = "preparing_waa"
	// RequestPhaseSendingUpstream indicates waiting for AI Studio response headers
	RequestPhaseSendingUpstream RequestPhase = "sending_upstream"
	// RequestPhaseStreaming indicates AI Studio has returned a streaming response
	RequestPhaseStreaming RequestPhase = "streaming"
)

type requestPhaseContextKey struct{}

type upstreamModeContextKey struct{}

// ContextWithUpstreamModeObserver records actual RPC, transport mode, and streaming fallback reason.
func ContextWithUpstreamModeObserver(ctx context.Context, observer func(string, string, string)) context.Context {
	return context.WithValue(ctx, upstreamModeContextKey{}, observer)
}

// reportUpstreamMode reports the upstream call mode actually used before dispatching.
func reportUpstreamMode(ctx context.Context, method, mode, reason string) {
	if observer, ok := ctx.Value(upstreamModeContextKey{}).(func(string, string, string)); ok {
		observer(method, mode, reason)
	}
}

// ContextWithRequestPhaseObserver records the protected request phase
func ContextWithRequestPhaseObserver(ctx context.Context, observer func(RequestPhase)) context.Context {
	return context.WithValue(ctx, requestPhaseContextKey{}, observer)
}

func reportRequestPhase(ctx context.Context, phase RequestPhase) {
	observer, _ := ctx.Value(requestPhaseContextKey{}).(func(RequestPhase))
	if observer != nil {
		observer(phase)
	}
}
