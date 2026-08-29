package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// WorkerManager coordinates WAA workers, prewarming, and background browser lifecycles.
type WorkerManager struct {
	mu              sync.RWMutex
	fillMu          sync.Mutex
	rebalanceMu     sync.Mutex
	pool            *aistudio.AccountPool
	accounts        map[string]*AccountWorker
	requests        *RequestRegistry
	camoufox        string
	globalProxy     string
	initTimeout     time.Duration
	warmLimit       int
	warmConcurrency int
	temporaryChat   bool
	lifecycle       context.Context
	cancel          context.CancelFunc
	closed          bool
}

// AccountWorker represents an account's WAA worker runtime and lease.
type AccountWorker struct {
	mu           sync.Mutex
	id           string
	label        string
	config       camoufoxnative.Options
	worker       *aistudio.NativeWorker
	runtimeLease *aistudio.AccountRuntimeLease
	warm         atomic.Bool
}

// AccountWorkerPreparer wraps a native worker for safe concurrent request preparation.
type AccountWorkerPreparer struct {
	account *AccountWorker
	worker  *aistudio.NativeWorker
}

// ErrAccountWorkerReplaced indicates that a worker was updated or closed during preparation.
var ErrAccountWorkerReplaced = errors.New("WAA worker has been updated")

// Prepare prepares a fresh proof for the request.
func (preparer *AccountWorkerPreparer) Prepare(ctx context.Context, request aistudio.ProtectedRequest) (aistudio.PreparedProtectedRequest, error) {
	preparer.account.mu.Lock()
	defer preparer.account.mu.Unlock()
	if preparer.account.worker != preparer.worker {
		return aistudio.PreparedProtectedRequest{}, ErrAccountWorkerReplaced
	}
	return preparer.worker.Prepare(ctx, request)
}

// AccountWorkerInitError wraps worker initialization errors.
type AccountWorkerInitError struct {
	err error
}

func (err *AccountWorkerInitError) Error() string {
	return err.err.Error()
}

func (err *AccountWorkerInitError) Unwrap() error {
	return err.err
}

// NewWorkerManager creates and initializes a new WorkerManager.
func NewWorkerManager(
	pool *aistudio.AccountPool,
	accounts []*aistudio.Account,
	requests *RequestRegistry,
	camoufoxPath string,
	globalProxy string,
	initTimeout time.Duration,
	warmLimit int,
	warmConcurrency int,
	temporaryChat bool,
) *WorkerManager {
	lifecycle, cancel := context.WithCancel(context.Background())
	manager := &WorkerManager{
		pool: pool, accounts: make(map[string]*AccountWorker, len(accounts)), requests: requests, camoufox: camoufoxPath,
		globalProxy: globalProxy, initTimeout: initTimeout,
		warmLimit: warmLimit, warmConcurrency: warmConcurrency, temporaryChat: temporaryChat,
		lifecycle: lifecycle, cancel: cancel,
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		manager.accounts[account.ID] = manager.newAccountWorker(account)
	}
	return manager
}

// Add registers a new account in the worker manager.
func (manager *WorkerManager) Add(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("account not initialized")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return fmt.Errorf("WAA worker manager closed")
	}
	if _, exists := manager.accounts[account.ID]; exists {
		return fmt.Errorf("WAA worker account already exists: %s", account.ID)
	}
	manager.accounts[account.ID] = manager.newAccountWorker(account)
	return nil
}

// Reset stops and closes an account's running WAA worker.
func (manager *WorkerManager) Reset(accountID string) error {
	manager.mu.RLock()
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return fmt.Errorf("WAA worker account not found: %s", accountID)
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.worker == nil {
		return nil
	}
	startedAt := time.Now()
	pid := account.worker.State().PID
	manager.requests.Log(account.label, "INFO", fmt.Sprintf("WAA worker stopping | PID=%d", pid))
	err := closeAccountWorker(account)
	if err != nil {
		manager.requests.Log(account.label, "ERROR", fmt.Sprintf(
			"WAA worker stop failed | PID=%d | elapsed=%s | error=%s",
			pid, time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return err
	}
	manager.requests.Log(account.label, "INFO", fmt.Sprintf(
		"WAA worker stopped | PID=%d | elapsed=%s",
		pid, time.Since(startedAt).Round(time.Millisecond),
	))
	return err
}

// Update refreshes an account's configuration and restarts its worker if running.
func (manager *WorkerManager) Update(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("account not initialized")
	}
	manager.mu.RLock()
	worker := manager.accounts[account.ID]
	manager.mu.RUnlock()
	if worker == nil {
		return fmt.Errorf("WAA worker account not found: %s", account.ID)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.worker != nil || worker.runtimeLease != nil {
		if err := closeAccountWorker(worker); err != nil {
			return err
		}
	}
	worker.config = manager.workerConfig(account)
	worker.label = account.Config.Label
	return nil
}

// ResetAll resets and stops all running account workers.
func (manager *WorkerManager) ResetAll() error {
	manager.mu.RLock()
	accountIDs := make([]string, 0, len(manager.accounts))
	for accountID := range manager.accounts {
		accountIDs = append(accountIDs, accountID)
	}
	manager.mu.RUnlock()
	var resetErrors []error
	for _, accountID := range accountIDs {
		resetErrors = append(resetErrors, manager.Reset(accountID))
	}
	return errors.Join(resetErrors...)
}

// Remove stops and removes an account from the manager.
func (manager *WorkerManager) Remove(accountID string) error {
	if err := manager.Reset(accountID); err != nil {
		return err
	}
	manager.mu.Lock()
	delete(manager.accounts, accountID)
	manager.mu.Unlock()
	return nil
}

func (manager *WorkerManager) newAccountWorker(account *aistudio.Account) *AccountWorker {
	return &AccountWorker{id: account.ID, label: account.Config.Label, config: manager.workerConfig(account)}
}

func (manager *WorkerManager) workerConfig(account *aistudio.Account) camoufoxnative.Options {
	return camoufoxnative.Options{
		ExecutablePath:   manager.camoufox,
		StorageStatePath: account.StoragePath,
		Locale:           account.Config.Locale,
		Timezone:         account.Config.Timezone,
		Proxy:            account.EffectiveProxy(manager.globalProxy),
		Headless:         false,
		TemporaryChat:    manager.temporaryChat,
	}
}

// WarmAccountIDs returns the IDs of currently prewarmed accounts.
func (manager *WorkerManager) WarmAccountIDs() []string {
	manager.mu.RLock()
	accounts := make([]*AccountWorker, 0, len(manager.accounts))
	for _, account := range manager.accounts {
		accounts = append(accounts, account)
	}
	manager.mu.RUnlock()
	warm := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account.warm.Load() {
			warm = append(warm, account.id)
		}
	}
	return warm
}

// PrewarmTarget returns the desired number of prewarmed accounts.
func (manager *WorkerManager) PrewarmTarget() int {
	available := 0
	for _, status := range manager.pool.Status() {
		if !status.Enabled || (status.State != aistudio.AccountReady && status.State != aistudio.AccountBusy) {
			continue
		}
		if models, err := manager.pool.BootstrapModels(status.ID); err == nil && len(models) > 0 {
			available++
		}
	}
	return min(manager.warmLimit, available)
}

func (manager *WorkerManager) classifyBootstrapCandidates(warm []string) (aistudio.AccountCandidateGroups, error) {
	combined := aistudio.AccountCandidateGroups{}
	seenWarmReady := make(map[string]struct{})
	seenWarmAvailable := make(map[string]struct{})
	seenWarmBusy := make(map[string]struct{})
	seenStandbyReady := make(map[string]struct{})
	seenStandbyBusy := make(map[string]struct{})
	appendUnique := func(target *[]string, seen map[string]struct{}, values []string) {
		for _, value := range values {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			*target = append(*target, value)
		}
	}
	var matched bool
	for _, modelID := range aistudio.BootstrapModelIDs() {
		groups, err := manager.pool.ClassifyCandidates(aistudio.AccountSelection{
			ModelID: modelID, Method: "generateContent",
		}, warm)
		if errors.Is(err, aistudio.ErrModelNotFound) {
			continue
		}
		if err != nil {
			return aistudio.AccountCandidateGroups{}, err
		}
		matched = true
		combined.Eligible = combined.Eligible || groups.Eligible
		if combined.EarliestCooldown.IsZero() || !groups.EarliestCooldown.IsZero() && groups.EarliestCooldown.Before(combined.EarliestCooldown) {
			combined.EarliestCooldown = groups.EarliestCooldown
		}
		appendUnique(&combined.WarmReady, seenWarmReady, groups.WarmReady)
		appendUnique(&combined.WarmAvailable, seenWarmAvailable, groups.WarmAvailable)
		appendUnique(&combined.WarmBusy, seenWarmBusy, groups.WarmBusy)
		appendUnique(&combined.StandbyReady, seenStandbyReady, groups.StandbyReady)
		appendUnique(&combined.StandbyBusy, seenStandbyBusy, groups.StandbyBusy)
	}
	if !matched {
		return aistudio.AccountCandidateGroups{}, aistudio.ErrNoEligibleAccount
	}
	combined.StandbyReady = manager.pool.PreferBroadCoverage(combined.StandbyReady)
	combined.StandbyBusy = manager.pool.PreferBroadCoverage(combined.StandbyBusy)
	return combined, nil
}

// WorkerFailed checks whether the account worker is currently in a failed state.
func (manager *WorkerManager) WorkerFailed(accountID string) bool {
	manager.mu.RLock()
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return false
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	return account.worker != nil && account.worker.State().Phase == aistudio.WorkerFailed
}

// Worker returns a running WAA preparer, launching it if necessary.
func (manager *WorkerManager) Worker(ctx context.Context, accountID string) (aistudio.ProtectedPreparer, error) {
	manager.mu.RLock()
	if manager.closed {
		manager.mu.RUnlock()
		return nil, fmt.Errorf("WAA worker manager closed")
	}
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.worker != nil {
		phase := account.worker.State().Phase
		if phase == aistudio.WorkerReady || phase == aistudio.WorkerBusy {
			return &AccountWorkerPreparer{account: account, worker: account.worker}, nil
		}
		if err := closeAccountWorker(account); err != nil {
			return nil, err
		}
	}
	startedAt := time.Now()
	runtimeLease, err := aistudio.AcquireAccountRuntimeLease(account.id)
	if err != nil {
		manager.requests.Log(account.label, "ERROR", fmt.Sprintf(
			"WAA worker startup failed | elapsed=%s | error=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return nil, &AccountWorkerInitError{err: err}
	}
	account.runtimeLease = runtimeLease
	workerReady := false
	defer func() {
		if !workerReady {
			_ = account.runtimeLease.Release()
			account.runtimeLease = nil
		}
	}()
	manager.requests.Log(account.label, "INFO", "WAA worker startup | 1/7 | Selecting bootstrap model")
	models, err := manager.pool.BootstrapModels(accountID)
	if err != nil {
		manager.requests.Log(account.label, "ERROR", fmt.Sprintf(
			"WAA worker startup failed | elapsed=%s | error=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return nil, err
	}
	var failures []error
	for index, model := range models {
		if err := ctx.Err(); err != nil {
			manager.requests.Log(account.label, "INFO", fmt.Sprintf(
				"WAA worker startup canceled | elapsed=%s",
				time.Since(startedAt).Round(time.Millisecond),
			))
			return nil, err
		}
		manager.requests.Log(account.label, "INFO", fmt.Sprintf(
			"WAA worker bootstrap model | %d/%d | model=%s", index+1, len(models), model,
		))
		modelStartedAt := time.Now()
		initCtx, cancel := context.WithTimeout(ctx, manager.initTimeout)
		options := account.config
		options.Model = model
		options.StartupProgress = func(stage camoufoxnative.StartupStage) {
			step, message := workerStartupProgress(stage)
			manager.requests.Log(account.label, "INFO", fmt.Sprintf("WAA worker startup | %d/7 | %s", step, message))
		}
		worker, initErr := aistudio.NewNativeWorker(initCtx, account.id, options)
		cancel()
		if initErr != nil {
			if err := ctx.Err(); err != nil {
				manager.requests.Log(account.label, "INFO", fmt.Sprintf(
					"WAA worker startup canceled | elapsed=%s",
					time.Since(startedAt).Round(time.Millisecond),
				))
				return nil, err
			}
			manager.requests.Log(account.label, "WARN", fmt.Sprintf(
				"WAA worker bootstrap model failed | %d/%d | model=%s | elapsed=%s | reason=%s",
				index+1, len(models), model, time.Since(modelStartedAt).Round(time.Millisecond), strings.TrimSpace(initErr.Error()),
			))
			failures = append(failures, fmt.Errorf("model %s: %w", model, initErr))
			continue
		}
		account.worker = worker
		account.warm.Store(true)
		workerReady = true
		manager.requests.Log(account.label, "INFO", fmt.Sprintf(
			"WAA worker ready | model=%s | PID=%d | elapsed=%s",
			model, worker.State().PID, time.Since(startedAt).Round(time.Millisecond),
		))
		return &AccountWorkerPreparer{account: account, worker: worker}, nil
	}
	err = errors.Join(failures...)
	manager.requests.Log(account.label, "ERROR", fmt.Sprintf(
		"WAA worker startup failed | elapsed=%s | error=%s",
		time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
	))
	return nil, &AccountWorkerInitError{err: err}
}

func workerStartupProgress(stage camoufoxnative.StartupStage) (int, string) {
	switch stage {
	case camoufoxnative.StartupPreparingBrowser:
		return 2, "Preparing browser profile"
	case camoufoxnative.StartupLaunchingBrowser:
		return 3, "Launching Camoufox"
	case camoufoxnative.StartupConnectingBiDi:
		return 4, "Connecting WebDriver BiDi"
	case camoufoxnative.StartupLoadingAIStudio:
		return 5, "Loading AI Studio"
	case camoufoxnative.StartupLocatingWAA:
		return 6, "Locating WAA service"
	case camoufoxnative.StartupBootstrappingWAA:
		return 7, "Executing WAA bootstrap"
	}
	panic(fmt.Sprintf("unknown WAA worker startup stage: %s", stage))
}

func (manager *WorkerManager) idleWarmVictim(excludeID string) string {
	statusByID := make(map[string]aistudio.AccountStatus)
	for _, status := range manager.pool.Status() {
		statusByID[status.ID] = status
	}
	warm := manager.WarmAccountIDs()
	var selected string
	var selectedUsed time.Time
	for _, accountID := range warm {
		if accountID == excludeID {
			continue
		}
		status := statusByID[accountID]
		if status.State == aistudio.AccountBusy {
			continue
		}
		lastUsed := time.Time{}
		if status.LastUsed != nil {
			lastUsed = *status.LastUsed
		}
		if selected == "" || lastUsed.Before(selectedUsed) {
			selected = accountID
			selectedUsed = lastUsed
		}
	}
	return selected
}

func (manager *WorkerManager) promote(ctx context.Context, accountID string) error {
	manager.rebalanceMu.Lock()
	defer manager.rebalanceMu.Unlock()
	warm := manager.WarmAccountIDs()
	for _, warmAccountID := range warm {
		if warmAccountID == accountID {
			return nil
		}
	}
	for len(warm) >= manager.warmLimit {
		victim := manager.idleWarmVictim(accountID)
		if victim != "" {
			if err := manager.Reset(victim); err != nil {
				return err
			}
			break
		}
		if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
			return err
		}
		warm = manager.WarmAccountIDs()
	}
	_, err := manager.Worker(ctx, accountID)
	return err
}

// StartPrewarm initiates the background prewarm routine.
func (manager *WorkerManager) StartPrewarm(ctx context.Context) <-chan error {
	first := make(chan error, 1)
	manager.mu.RLock()
	closed := manager.closed
	manager.mu.RUnlock()
	if closed {
		first <- fmt.Errorf("WAA worker manager closed")
		close(first)
		return first
	}
	if !manager.fillMu.TryLock() {
		if len(manager.WarmAccountIDs()) > 0 {
			first <- nil
		} else {
			first <- fmt.Errorf("WAA prewarm already in progress")
		}
		close(first)
		return first
	}
	fillContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(manager.lifecycle, cancel)
	go manager.fillWarm(fillContext, first, func() {
		stop()
		cancel()
	})
	return first
}

func (manager *WorkerManager) fillWarm(ctx context.Context, first chan<- error, cleanup func()) {
	startedAt := time.Now()
	defer cleanup()
	defer manager.fillMu.Unlock()
	defer close(first)
	notified := false
	notify := func(err error) {
		if notified {
			return
		}
		notified = true
		first <- err
	}
	var failures []error
	for {
		if err := ctx.Err(); err != nil {
			if !notified {
				notify(errors.Join(append(failures, err)...))
			}
			return
		}
		manager.rebalanceMu.Lock()
		warm := manager.WarmAccountIDs()
		if len(warm) >= manager.warmLimit {
			manager.rebalanceMu.Unlock()
			manager.requests.Log("service", "INFO", fmt.Sprintf(
				"WAA worker prewarm completed | workers=%d/%d | elapsed=%s",
				len(warm), manager.PrewarmTarget(), time.Since(startedAt).Round(time.Millisecond),
			))
			notify(nil)
			return
		}
		groups, err := manager.classifyBootstrapCandidates(warm)
		if err != nil {
			manager.rebalanceMu.Unlock()
			failures = append(failures, err)
			if !notified {
				notify(errors.Join(failures...))
			}
			return
		}
		if len(groups.StandbyReady) == 0 {
			manager.rebalanceMu.Unlock()
			if len(groups.StandbyBusy) > 0 {
				if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil && !notified {
					notify(errors.Join(append(failures, err)...))
				}
				continue
			}
			warm = manager.WarmAccountIDs()
			if len(warm) > 0 {
				manager.requests.Log("service", "INFO", fmt.Sprintf(
					"WAA worker prewarm completed | workers=%d/%d | failed=%d | elapsed=%s",
					len(warm), manager.PrewarmTarget(), len(failures), time.Since(startedAt).Round(time.Millisecond),
				))
				notify(nil)
				return
			}
			if len(failures) == 0 {
				failures = append(failures, aistudio.ErrNoEligibleAccount)
			}
			notify(errors.Join(failures...))
			return
		}
		remaining := manager.warmLimit - len(warm)
		batchSize := min(manager.warmConcurrency, remaining, len(groups.StandbyReady))
		results := make(chan warmResult, batchSize)
		for _, accountID := range groups.StandbyReady[:batchSize] {
			go func(accountID string) {
				_, err := manager.Worker(ctx, accountID)
				results <- warmResult{accountID: accountID, err: err}
			}(accountID)
		}
		for range batchSize {
			result := <-results
			if result.err == nil {
				notify(nil)
				continue
			}
			if ctx.Err() != nil {
				continue
			}
			failure := fmt.Errorf("prewarm account %s: %w", result.accountID, result.err)
			failures = append(failures, failure)
			if cooldownErr := manager.pool.MarkCooldown(result.accountID, "", time.Now().Add(5*time.Minute), result.err.Error()); cooldownErr != nil {
				failures = append(failures, cooldownErr)
			}
		}
		manager.rebalanceMu.Unlock()
	}
}

type warmResult struct {
	accountID string
	err       error
}

func (manager *WorkerManager) waitPrewarm() {
	manager.fillMu.Lock()
	manager.fillMu.Unlock()
}

// Close gracefully closes all account workers and cancels the manager context.
func (manager *WorkerManager) Close() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	manager.cancel()
	accounts := make([]*AccountWorker, 0, len(manager.accounts))
	for _, account := range manager.accounts {
		accounts = append(accounts, account)
	}
	manager.mu.Unlock()
	manager.waitPrewarm()
	var closeErrors []error
	for _, account := range accounts {
		account.mu.Lock()
		if account.worker != nil || account.runtimeLease != nil {
			closeErrors = append(closeErrors, closeAccountWorker(account))
		}
		account.mu.Unlock()
	}
	return errors.Join(closeErrors...)
}

func closeAccountWorker(account *AccountWorker) error {
	var closeErr error
	if account.worker != nil {
		closeErr = account.worker.Close()
		account.worker = nil
	}
	if account.runtimeLease != nil {
		closeErr = errors.Join(closeErr, account.runtimeLease.Release())
		account.runtimeLease = nil
	}
	account.warm.Store(false)
	return closeErr
}
