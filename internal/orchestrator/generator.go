package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

var (
	errStreamClosedBeforeFirstEvent = errors.New("AI Studio stream closed before first event")
	errStreamClosedBeforeFinish     = errors.New("AI Studio stream closed before finish")
)

// Generate executes a streaming generation request with automatic failover and retries.
func (service *Service) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	generationStartedAt := time.Now()
	api.SetAccessLogTarget(ctx, request.Model, "")
	api.SetAccessLogGenerationConfig(ctx, request.Config)
	api.StartAccessLog(ctx)
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		service.requests.Start(request, func() {})
		service.requests.Finish(request.ID, "failed", err)
		return nil, err
	}
	service.requests.Start(request, cancel)
	resourceID, err := service.pool.ResourceIDForContents(request.Contents)
	if err != nil {
		api.SetAccessLogError(requestCtx, err)
		cancel()
		service.requests.Finish(request.ID, finalRequestState(err), err)
		return nil, err
	}
	events := make(chan aistudio.Event, 8)
	go service.generateWithRetry(ctx, requestCtx, cancel, generationStartedAt, request, resourceID, events)
	return events, nil
}

func (service *Service) generateWithRetry(
	clientCtx context.Context,
	requestCtx context.Context,
	cancel context.CancelFunc,
	generationStartedAt time.Time,
	request aistudio.GenerateRequest,
	resourceID string,
	destination chan<- aistudio.Event,
) {
	maxAttempts := 1
	requestedAccountID := strings.TrimSpace(request.AccountID)
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	unbound := requestedAccountID == "" && resourceID == ""
	if unbound {
		eligible := 0
		for _, status := range service.pool.Status() {
			if status.Enabled && (status.State == aistudio.AccountReady || status.State == aistudio.AccountBusy) {
				eligible++
			}
		}
		if eligible > 1 {
			maxAttempts = eligible
		}
	}
	var lease *aistudio.AccountLease
	var source <-chan aistudio.Event
	var first aistudio.Event
	var activity *UpstreamActivity
	var err error
	attempted := make(map[string]struct{}, maxAttempts)
	recoveredWorker := make(map[string]struct{})
	recoveryAccountID := ""
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selectionAccountID := requestedAccountID
		recoveringAccount := recoveryAccountID != ""
		if recoveryAccountID != "" {
			selectionAccountID = recoveryAccountID
			recoveryAccountID = ""
		}
		selection := aistudio.AccountSelection{
			ModelID: modelID, Method: "generateContent", AccountID: selectionAccountID, ResourceID: resourceID,
		}
		if unbound && len(attempted) > 0 {
			for _, status := range service.pool.Status() {
				if _, exists := attempted[status.ID]; status.Enabled && !exists {
					selection.AllowedAccountIDs = append(selection.AllowedAccountIDs, status.ID)
				}
			}
		}
		warm, warmErr := service.warmCandidates(requestCtx, selection)
		if warmErr != nil {
			if err == nil || !errors.Is(warmErr, aistudio.ErrNoEligibleAccount) {
				err = warmErr
			}
			if recoveringAccount && unbound && requestCtx.Err() == nil {
				attempted[selectionAccountID] = struct{}{}
				continue
			}
			break
		}
		selection.AllowedAccountIDs = warm
		nextLease, acquireErr := service.pool.AcquireFor(requestCtx, selection)
		if acquireErr != nil {
			if err == nil || !errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) {
				err = acquireErr
			}
			if recoveringAccount && unbound && requestCtx.Err() == nil {
				attempted[selectionAccountID] = struct{}{}
				continue
			}
			break
		}
		lease = nextLease
		request.AccountID = lease.Account().ID
		attempted[request.AccountID] = struct{}{}
		accountLabel := lease.Account().Config.Label
		api.SetAccessLogTarget(requestCtx, modelID, accountLabel)
		service.requests.MarkRunning(request.ID, request.AccountID, accountLabel)
		prepareStartedAt := time.Now()
		prepareTiming := newRequestPreparationTiming(prepareStartedAt)
		prepareWarningDone := make(chan struct{})
		prepareWarning := time.AfterFunc(streamStallThreshold, func() {
			current, _, _ := prepareTiming.snapshot(time.Now())
			service.requests.Log(accountLabel, "WARN", fmt.Sprintf(
				"Request preparation waiting | elapsed=%s | current=%s | model=%s",
				streamStallThreshold, current, modelID,
			))
			close(prepareWarningDone)
		})
		activity = &UpstreamActivity{}
		attemptCtx := aistudio.ContextWithAccountLease(requestCtx, lease)
		attemptCtx = aistudio.ContextWithStreamActivityObserver(attemptCtx, activity.observe)
		attemptCtx = aistudio.ContextWithRequestPhaseObserver(attemptCtx, prepareTiming.observe)
		source, err = service.service.Generate(attemptCtx, request)
		prepareElapsed := time.Since(prepareStartedAt)
		if !prepareWarning.Stop() {
			<-prepareWarningDone
			_, waa, responseHeader := prepareTiming.snapshot(time.Now())
			service.requests.Log(accountLabel, "INFO", fmt.Sprintf(
				"Request preparation finished | elapsed=%s | waa=%s | resp_header=%s | model=%s",
				prepareElapsed.Round(time.Millisecond), waa.Round(time.Millisecond),
				responseHeader.Round(time.Millisecond), modelID,
			))
		}
		if err == nil {
			upstreamStartedAt := time.Now()
			firstEventDelayed := false
			first, err = firstGenerateEvent(requestCtx, source, func() {
				firstEventDelayed = true
				service.requests.Log(accountLabel, "WARN", fmt.Sprintf(
					"Upstream first event waiting | elapsed=%s | model=%s | %s",
					streamStallThreshold, modelID, activity.logFields(time.Now()),
				))
			})
			if firstEventDelayed && err == nil {
				service.requests.Log(accountLabel, "INFO", fmt.Sprintf(
					"Upstream first event arrived | elapsed=%s | event=%s | model=%s",
					time.Since(upstreamStartedAt).Round(time.Millisecond), first.Kind, modelID,
				))
			}
			if err == nil {
				api.SetAccessLogFirstEvent(requestCtx, time.Since(generationStartedAt))
				api.SetAccessLogTarget(requestCtx, first.ProviderModel, accountLabel)
				if changed, accessErr := service.pool.MarkModelAccess(request.AccountID, modelID, aistudio.ModelAccessVerified, ""); accessErr != nil {
					err = accessErr
				} else {
					if changed {
						service.publishModelAccess()
					}
					service.observePerformance(request.AccountID, modelID, time.Since(prepareStartedAt))
					break
				}
			}
		}
		workerFailed := service.workers.WorkerFailed(request.AccountID)
		waaRuntimeFailed := aistudio.DefinitiveWAARuntimeFailure(err)
		workerReplaced := errors.Is(err, ErrAccountWorkerReplaced)
		_, alreadyRecovered := recoveredWorker[request.AccountID]
		recoverWorker := (workerFailed || waaRuntimeFailed || workerReplaced) && !alreadyRecovered && requestCtx.Err() == nil
		localWorkerFailure := (workerFailed || workerReplaced) && requestCtx.Err() == nil
		retryable := retryableGenerateAccountError(requestCtx, err) || localWorkerFailure
		if workerFailed || waaRuntimeFailed {
			if resetErr := service.workers.Reset(request.AccountID); resetErr != nil {
				err = errors.Join(err, resetErr)
				retryable = false
			}
		}
		if aistudio.DefinitiveAuthenticationFailure(err) {
			if stateErr := service.pool.MarkAuthRequired(request.AccountID, err.Error()); stateErr != nil {
				err = errors.Join(err, stateErr)
				retryable = false
			}
		}
		modelAccessDenied := aistudio.DefinitiveModelAccessFailure(err)
		if modelAccessDenied {
			if changed, stateErr := service.pool.MarkModelAccess(request.AccountID, modelID, aistudio.ModelAccessDenied, err.Error()); stateErr != nil {
				err = errors.Join(err, stateErr)
				retryable = false
			} else if changed {
				service.publishModelAccess()
			}
		}
		releaseErr := lease.Release()
		lease = nil
		if releaseErr != nil {
			err = errors.Join(err, releaseErr)
			break
		}
		if !retryable {
			break
		}
		if recoverWorker {
			recoveredWorker[request.AccountID] = struct{}{}
			recoveryAccountID = request.AccountID
			delete(attempted, request.AccountID)
			maxAttempts++
		} else if !aistudio.DefinitiveAuthenticationFailure(err) && !modelAccessDenied {
			cooldownModel := modelID
			cooldownDuration := 30 * time.Second
			var workerInitError *AccountWorkerInitError
			if errors.As(err, &workerInitError) || workerFailed {
				cooldownModel = ""
				cooldownDuration = 5 * time.Minute
			} else if waaRuntimeFailed || workerReplaced {
				cooldownModel = ""
			}
			if cooldownErr := service.pool.MarkCooldown(request.AccountID, cooldownModel, time.Now().Add(cooldownDuration), err.Error()); cooldownErr != nil {
				err = errors.Join(err, cooldownErr)
				break
			}
		}
		if attempt+1 == maxAttempts {
			break
		}
		if recoverWorker {
			service.requests.Log(accountLabel, "WARN", fmt.Sprintf(
				"WAA worker rescheduled | model=%s | replaying current request", modelID,
			))
			continue
		}
		switchMessage := fmt.Sprintf(
			"Account switched | model=%s\nReason: %s",
			modelID, strings.TrimSpace(err.Error()),
		)
		service.requests.Log(accountLabel, "WARN", switchMessage)
	}
	if err != nil {
		api.SetAccessLogError(requestCtx, err)
		service.requests.Finish(request.ID, finalRequestState(err), err)
		select {
		case destination <- aistudio.Event{Kind: aistudio.EventError, Err: err}:
		case <-clientCtx.Done():
		}
		cancel()
		close(destination)
		return
	}
	service.forwardEvents(
		clientCtx, requestCtx, cancel, request.ID, generationStartedAt,
		first, source, destination, lease, activity,
	)
}

func firstGenerateEvent(ctx context.Context, source <-chan aistudio.Event, onWait func()) (aistudio.Event, error) {
	timer := time.NewTimer(streamStallThreshold)
	defer timer.Stop()
	wait := timer.C
	for {
		select {
		case event, ok := <-source:
			if !ok {
				return aistudio.Event{}, errStreamClosedBeforeFirstEvent
			}
			if event.Kind != aistudio.EventError {
				return event, nil
			}
			for range source {
			}
			if event.Err != nil {
				return aistudio.Event{}, event.Err
			}
			return aistudio.Event{}, errors.New("AI Studio stream returned an empty error event")
		case <-wait:
			if onWait != nil {
				onWait()
			}
			wait = nil
		case <-ctx.Done():
			return aistudio.Event{}, ctx.Err()
		}
	}
}

func retryableGenerateAccountError(ctx context.Context, err error) bool {
	if errors.Is(err, errStreamClosedBeforeFirstEvent) {
		return true
	}
	var workerInitError *AccountWorkerInitError
	if errors.As(err, &workerInitError) {
		return ctx.Err() == nil
	}
	if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var rpcError *aistudio.RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	return rpcError.StatusCode == http.StatusUnauthorized || rpcError.StatusCode == http.StatusForbidden || rpcError.StatusCode == http.StatusNotFound ||
		rpcError.StatusCode == http.StatusTooManyRequests || rpcError.StatusCode >= http.StatusInternalServerError
}

func (service *Service) warmCandidates(ctx context.Context, selection aistudio.AccountSelection) ([]string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		warm := service.workers.WarmAccountIDs()
		groups, err := service.pool.ClassifyCandidates(selection, warm)
		if err != nil {
			return nil, err
		}
		if len(groups.WarmReady) > 0 {
			return service.preferFast(groups.WarmReady, selection.ModelID), nil
		}
		if len(groups.WarmAvailable) > 0 {
			return service.preferFast(groups.WarmAvailable, selection.ModelID), nil
		}
		if len(groups.StandbyReady) > 0 && len(warm) < service.workers.warmLimit {
			accountID := service.preferFast(groups.StandbyReady, selection.ModelID)[0]
			if err := service.workers.promote(ctx, accountID); err == nil {
				continue
			} else {
				if cooldownErr := service.pool.MarkCooldown(accountID, "", time.Now().Add(5*time.Minute), err.Error()); cooldownErr != nil {
					return nil, errors.Join(err, cooldownErr)
				}
				continue
			}
		}
		if len(groups.WarmBusy) > 0 {
			return service.preferFast(groups.WarmBusy, selection.ModelID), nil
		}
		if len(groups.StandbyReady) > 0 {
			accountID := service.preferFast(groups.StandbyReady, selection.ModelID)[0]
			if err := service.workers.promote(ctx, accountID); err == nil {
				continue
			} else {
				if cooldownErr := service.pool.MarkCooldown(accountID, "", time.Now().Add(5*time.Minute), err.Error()); cooldownErr != nil {
					return nil, errors.Join(err, cooldownErr)
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
		}
		if len(groups.StandbyBusy) > 0 {
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		return nil, aistudio.ErrNoEligibleAccount
	}
}

func waitWarmCandidate(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (service *Service) forwardEvents(
	clientCtx context.Context,
	requestCtx context.Context,
	cancel context.CancelFunc,
	requestID string,
	generationStartedAt time.Time,
	first aistudio.Event,
	source <-chan aistudio.Event,
	destination chan<- aistudio.Event,
	lease *aistudio.AccountLease,
	activity *UpstreamActivity,
) {
	state := "completed"
	var requestErr error
	terminal := false
	accountLabel := lease.Account().Config.Label
	modelID := strings.TrimPrefix(strings.TrimSpace(first.ProviderModel), "models/")
	var firstContent time.Duration
	var lastEventAt time.Time
	lastEventKind := "-"
	reasoningEvents := 0
	contentEvents := 0
	contentChars := 0
	var outputTokens int64
	var reasoningTokens int64
	stalled := false
	stallTimer := time.NewTimer(streamStallThreshold)
	if !stallTimer.Stop() {
		<-stallTimer.C
	}
	defer stallTimer.Stop()
	var stall <-chan time.Time
	resetStallTimer := func() {
		if !stallTimer.Stop() {
			select {
			case <-stallTimer.C:
			default:
			}
		}
		stallTimer.Reset(streamStallThreshold)
		stall = stallTimer.C
	}
	defer cancel()
	defer func() {
		api.SetAccessLogGenerationResult(requestCtx, firstContent, contentChars, outputTokens, reasoningTokens)
		if err := lease.Release(); err != nil {
			state = "failed"
			requestErr = errors.Join(requestErr, err)
			if clientCtx.Err() == nil {
				select {
				case destination <- aistudio.Event{Kind: aistudio.EventError, Err: err}:
				case <-clientCtx.Done():
				}
			}
		}
		api.SetAccessLogError(requestCtx, requestErr)
		service.requests.Finish(requestID, state, requestErr)
		close(destination)
	}()
	pendingFirst := true
	for {
		var event aistudio.Event
		var ok bool
		if pendingFirst {
			event = first
			ok = true
			pendingFirst = false
		} else {
			select {
			case event, ok = <-source:
			case <-stall:
				stalled = true
				stall = nil
				service.requests.Log(accountLabel, "WARN", fmt.Sprintf(
					"Event stream stalled | model=%s | elapsed=%s | last_event=%s | reasoning=%d | content=%d | %s",
					modelID, streamStallThreshold, lastEventKind, reasoningEvents, contentEvents,
					activity.logFields(time.Now()),
				))
				continue
			case <-requestCtx.Done():
				requestErr = requestCtx.Err()
				state = finalRequestState(requestErr)
				return
			}
		}
		if !ok {
			if err := requestCtx.Err(); err != nil {
				requestErr = err
				state = finalRequestState(err)
			} else if !terminal {
				requestErr = errStreamClosedBeforeFinish
				state = "failed"
				select {
				case destination <- aistudio.Event{Kind: aistudio.EventError, Err: requestErr}:
				case <-clientCtx.Done():
				}
			}
			return
		}
		now := time.Now()
		if stalled {
			service.requests.Log(accountLabel, "INFO", fmt.Sprintf(
				"Event stream resumed | model=%s | stalled=%s | current_event=%s",
				modelID, now.Sub(lastEventAt).Round(time.Millisecond), event.Kind,
			))
			stalled = false
		}
		lastEventAt = now
		lastEventKind = string(event.Kind)
		switch event.Kind {
		case aistudio.EventReasoning:
			reasoningEvents++
		case aistudio.EventText:
			contentEvents++
			contentChars += utf8.RuneCountInString(event.Text)
			if firstContent == 0 {
				firstContent = now.Sub(generationStartedAt)
			}
		case aistudio.EventUsage:
			if event.Usage != nil {
				outputTokens = event.Usage.OutputTokens
				reasoningTokens = event.Usage.ReasoningTokens
			}
		}
		api.SetAccessLogTarget(requestCtx, event.ProviderModel, lease.Account().Config.Label)
		if terminal {
			continue
		}
		if event.Kind == aistudio.EventError {
			requestErr = event.Err
			if aistudio.DefinitiveAuthenticationFailure(event.Err) {
				if stateErr := service.pool.MarkAuthRequired(lease.Account().ID, event.Err.Error()); stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				}
			}
			if aistudio.DefinitiveModelAccessFailure(event.Err) {
				if changed, stateErr := service.pool.MarkModelAccess(lease.Account().ID, modelID, aistudio.ModelAccessDenied, event.Err.Error()); stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				} else if changed {
					service.publishModelAccess()
				}
			}
			state = finalRequestState(event.Err)
			terminal = true
		}
		if event.Kind == aistudio.EventFinish {
			api.SetAccessLogFinishReason(requestCtx, event.FinishReason)
			state = "completed"
			terminal = true
		}
		if terminal {
			stall = nil
		} else {
			resetStallTimer()
		}
		select {
		case destination <- event:
		case <-requestCtx.Done():
			requestErr = requestCtx.Err()
			state = finalRequestState(requestErr)
			return
		}
	}
}

func finalRequestState(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if err != nil {
		return "failed"
	}
	return "completed"
}
