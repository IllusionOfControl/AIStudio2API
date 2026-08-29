package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/metrics"
)

const streamStallThreshold = 15 * time.Second

// UpstreamActivity tracks network throughput and activity timestamps.
type UpstreamActivity struct {
	bytes    atomic.Int64
	lastNano atomic.Int64
}

func (activity *UpstreamActivity) observe(count int) {
	if activity == nil || count <= 0 {
		return
	}
	now := time.Now().UnixNano()
	activity.lastNano.Store(now)
	activity.bytes.Add(int64(count))
	metrics.AddUpstreamNetworkBytes("received", int64(count))
}

func (activity *UpstreamActivity) logFields(now time.Time) string {
	if activity == nil {
		return "network_bytes=0"
	}
	lastNano := activity.lastNano.Load()
	if lastNano == 0 {
		return "network_bytes=0"
	}
	return fmt.Sprintf(
		"network_bytes=%d | last_network=%s",
		activity.bytes.Load(), now.Sub(time.Unix(0, lastNano)).Round(time.Millisecond),
	)
}

// RequestPreparationTiming tracks duration of individual preparation phases.
type RequestPreparationTiming struct {
	mu             sync.Mutex
	phase          aistudio.RequestPhase
	phaseStarted   time.Time
	waa            time.Duration
	responseHeader time.Duration
}

func newRequestPreparationTiming(startedAt time.Time) *RequestPreparationTiming {
	return &RequestPreparationTiming{phase: aistudio.RequestPhasePreparingWAA, phaseStarted: startedAt}
}

func (timing *RequestPreparationTiming) observe(phase aistudio.RequestPhase) {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	defer timing.mu.Unlock()
	if phase == timing.phase {
		return
	}
	now := time.Now()
	timing.finishPhaseLocked(now)
	timing.phase = phase
	timing.phaseStarted = now
}

func (timing *RequestPreparationTiming) snapshot(now time.Time) (string, time.Duration, time.Duration) {
	if timing == nil {
		return "stream established", 0, 0
	}
	timing.mu.Lock()
	defer timing.mu.Unlock()
	waa := timing.waa
	responseHeader := timing.responseHeader
	elapsed := now.Sub(timing.phaseStarted)
	switch timing.phase {
	case aistudio.RequestPhasePreparingWAA:
		waa += elapsed
	case aistudio.RequestPhaseSendingUpstream:
		responseHeader += elapsed
	}
	current := "stream established"
	switch timing.phase {
	case aistudio.RequestPhasePreparingWAA:
		current = "WAA proof"
	case aistudio.RequestPhaseSendingUpstream:
		current = "waiting for upstream response headers"
	}
	return current, waa, responseHeader
}

func (timing *RequestPreparationTiming) finishPhaseLocked(now time.Time) {
	if timing == nil {
		return
	}
	elapsed := now.Sub(timing.phaseStarted)
	switch timing.phase {
	case aistudio.RequestPhasePreparingWAA:
		timing.waa += elapsed
		metrics.ObservePreparationDuration("waa", elapsed)
	case aistudio.RequestPhaseSendingUpstream:
		timing.responseHeader += elapsed
		metrics.ObservePreparationDuration("response_header", elapsed)
	default:
		metrics.ObservePreparationDuration(string(timing.phase), elapsed)
	}
}

// GenerationPerformance records latency for a specific model and account.
type GenerationPerformance struct {
	firstEvent time.Duration
	observedAt time.Time
}

func (service *Service) observePerformance(accountID string, model string, firstEvent time.Duration) {
	if service == nil {
		return
	}
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if accountID == "" || model == "" || firstEvent <= 0 {
		return
	}
	service.performanceMu.Lock()
	if service.performance[accountID] == nil {
		service.performance[accountID] = make(map[string]GenerationPerformance)
	}
	service.performance[accountID][model] = GenerationPerformance{firstEvent: firstEvent, observedAt: time.Now()}
	service.performanceMu.Unlock()
	metrics.SetLatestPerformance(accountID, model, firstEvent)
}

func (service *Service) preferFast(accountIDs []string, model string) []string {
	result := append([]string(nil), accountIDs...)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	access := service.pool.ModelAccessStates(result, model)
	service.performanceMu.RLock()
	sort.SliceStable(result, func(left int, right int) bool {
		leftVerified := access[result[left]] == aistudio.ModelAccessVerified
		rightVerified := access[result[right]] == aistudio.ModelAccessVerified
		if leftVerified != rightVerified {
			return leftVerified
		}
		leftObserved, leftExact, leftLatency, leftTime := service.performanceRankLocked(result[left], model)
		rightObserved, rightExact, rightLatency, rightTime := service.performanceRankLocked(result[right], model)
		if leftObserved != rightObserved {
			return leftObserved
		}
		if !leftObserved {
			return false
		}
		if leftExact != rightExact {
			return leftExact
		}
		if leftLatency != rightLatency {
			return leftLatency < rightLatency
		}
		return leftTime.After(rightTime)
	})
	service.performanceMu.RUnlock()
	return result
}

func (service *Service) performanceRankLocked(accountID string, model string) (bool, bool, time.Duration, time.Time) {
	models := service.performance[accountID]
	if len(models) == 0 {
		return false, false, 0, time.Time{}
	}
	if observed, ok := models[model]; ok {
		return true, true, observed.firstEvent, observed.observedAt
	}
	var latest GenerationPerformance
	for _, observed := range models {
		if observed.observedAt.After(latest.observedAt) {
			latest = observed
		}
	}
	return true, false, latest.firstEvent, latest.observedAt
}

func (service *Service) clearPerformance() {
	service.performanceMu.Lock()
	clear(service.performance)
	service.performanceMu.Unlock()
}

// RequestRegistry manages active request tracking and real-time administrative logs.
type RequestRegistry struct {
	mu          sync.Mutex
	active      map[string]TrackedRequest
	logs        []api.AdminLog
	subscribers map[chan api.AdminEvent]struct{}
	console     chan api.AdminLog
}

// TrackedRequest holds metadata and cancellation for an in-flight request.
type TrackedRequest struct {
	Request api.AdminRequest
	Cancel  context.CancelFunc
}

// NewRequestRegistry creates and starts a new RequestRegistry.
func NewRequestRegistry(ctx context.Context) *RequestRegistry {
	registry := &RequestRegistry{
		active:      make(map[string]TrackedRequest),
		logs:        make([]api.AdminLog, 0, 128),
		subscribers: make(map[chan api.AdminEvent]struct{}),
		console:     make(chan api.AdminLog, 256),
	}
	go registry.writeConsole(ctx)
	return registry
}

func (registry *RequestRegistry) writeConsole(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-registry.console:
			switch strings.ToUpper(entry.Level) {
			case "ERROR":
				slog.Error(entry.Message, "source", entry.Source)
			case "WARN":
				slog.Warn(entry.Message, "source", entry.Source)
			default:
				slog.Info(entry.Message, "source", entry.Source)
			}
		}
	}
}

// Log records an event log entry and broadcasts it to subscribers.
func (registry *RequestRegistry) Log(source string, level string, message string) {
	registry.mu.Lock()
	entry := registry.appendLogLocked(source, level, message)
	registry.mu.Unlock()
	select {
	case registry.console <- entry:
	default:
	}
}

func (registry *RequestRegistry) appendLogLocked(source string, level string, message string) api.AdminLog {
	entry := api.AdminLog{Time: time.Now().UTC(), Level: level, Source: source, Message: message}
	registry.logs = append(registry.logs, entry)
	if len(registry.logs) >= 2200 {
		copy(registry.logs, registry.logs[len(registry.logs)-2000:])
		registry.logs = registry.logs[:2000]
	}
	registry.publishLocked(api.AdminEvent{Type: "log", Data: entry})
	return entry
}

func (registry *RequestRegistry) publishLocked(event api.AdminEvent) {
	for subscriber := range registry.subscribers {
		select {
		case subscriber <- event:
		default:
			delete(registry.subscribers, subscriber)
			close(subscriber)
		}
	}
}

// Publish broadcasts an admin event to all subscribed listeners.
func (registry *RequestRegistry) Publish(event api.AdminEvent) {
	registry.mu.Lock()
	registry.publishLocked(event)
	registry.mu.Unlock()
}

// Subscribe returns an event channel receiving real-time admin events.
func (registry *RequestRegistry) Subscribe(ctx context.Context) <-chan api.AdminEvent {
	events := make(chan api.AdminEvent, 256)
	registry.mu.Lock()
	registry.subscribers[events] = struct{}{}
	registry.mu.Unlock()
	go func() {
		<-ctx.Done()
		registry.mu.Lock()
		if _, ok := registry.subscribers[events]; ok {
			delete(registry.subscribers, events)
			close(events)
		}
		registry.mu.Unlock()
	}()
	return events
}

// Start tracks a newly queued generation request.
func (registry *RequestRegistry) Start(request aistudio.GenerateRequest, cancel context.CancelFunc) {
	if registry == nil {
		return
	}
	tracked := TrackedRequest{
		Request: api.AdminRequest{
			ID: request.ID, Model: request.Model, AccountID: request.AccountID,
			State: "queued", StartedAt: time.Now().UTC(),
		},
		Cancel: cancel,
	}
	registry.mu.Lock()
	registry.active[request.ID] = tracked
	registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.Request})
	registry.mu.Unlock()
	metrics.IncActiveGeneration(request.Model)
}
// MarkRunning marks a pending request as running on a specific account.
func (registry *RequestRegistry) MarkRunning(id string, accountID string, accountLabel string) {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	if exists {
		tracked.Request.AccountID = accountID
		tracked.Request.AccountLabel = accountLabel
		tracked.Request.State = "running"
		registry.active[id] = tracked
		registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.Request})
	}
	registry.mu.Unlock()
}

// Finish records the completion of a tracked request.
func (registry *RequestRegistry) Finish(id string, state string, requestErr error) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	if exists {
		delete(registry.active, id)
		tracked.Request.State = state
		registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.Request})
	}
	registry.mu.Unlock()
	if exists {
		metrics.DecActiveGeneration(tracked.Request.Model)
	}
}

// List returns a sorted list of all active requests.
func (registry *RequestRegistry) List() []api.AdminRequest {
	registry.mu.Lock()
	requests := make([]api.AdminRequest, 0, len(registry.active))
	for _, tracked := range registry.active {
		requests = append(requests, tracked.Request)
	}
	registry.mu.Unlock()
	sort.Slice(requests, func(left int, right int) bool {
		return requests[left].StartedAt.Before(requests[right].StartedAt)
	})
	return requests
}

// Count returns the number of active in-flight requests.
func (registry *RequestRegistry) Count() int {
	registry.mu.Lock()
	count := len(registry.active)
	registry.mu.Unlock()
	return count
}

// CancelAll cancels all active in-flight requests.
func (registry *RequestRegistry) CancelAll() {
	registry.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(registry.active))
	for _, tracked := range registry.active {
		cancels = append(cancels, tracked.Cancel)
	}
	registry.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Cancel cancels a specific request by ID.
func (registry *RequestRegistry) Cancel(id string) bool {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	registry.mu.Unlock()
	if !exists || tracked.Cancel == nil {
		return false
	}
	tracked.Cancel()
	return true
}

// LogsSnapshot returns a snapshot of recorded logs.
func (registry *RequestRegistry) LogsSnapshot() []api.AdminLog {
	registry.mu.Lock()
	logs := append([]api.AdminLog(nil), registry.logs...)
	registry.mu.Unlock()
	return logs
}

// ClearLogs clears recorded logs.
func (registry *RequestRegistry) ClearLogs() {
	registry.mu.Lock()
	registry.logs = registry.logs[:0]
	registry.mu.Unlock()
}
