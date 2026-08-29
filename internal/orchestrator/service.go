package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/metrics"
)

// Service coordinates tracked requests, model discovery, and generation state.
type Service struct {
	lifecycle      context.Context
	service        aistudio.Service
	pool           *aistudio.AccountPool
	requests       *RequestRegistry
	workers        *WorkerManager
	timeout        time.Duration
	state          atomic.Int32
	lifecycleMu    sync.Mutex
	transitionDone chan struct{}
	transitionErr  error
	dataContext    context.Context
	dataCancel     context.CancelFunc
	modelsMu       sync.RWMutex
	models         []aistudio.Model
	performanceMu  sync.RWMutex
	performance    map[string]map[string]GenerationPerformance
}

// NewService creates a new tracked Service.
func NewService(
	lifecycle context.Context,
	service aistudio.Service,
	pool *aistudio.AccountPool,
	requests *RequestRegistry,
	workers *WorkerManager,
	timeout time.Duration,
) *Service {
	return &Service{
		lifecycle: lifecycle, service: service, pool: pool, requests: requests, workers: workers,
		timeout: timeout, performance: make(map[string]map[string]GenerationPerformance),
	}
}

type serviceStoppedError struct{}

// ErrServiceTransitioning is returned when service state change is already underway.
var ErrServiceTransitioning = errors.New("generation service is transitioning")

const (
	serviceStopped int32 = iota
	serviceLaunching
	serviceRunning
)

func (*serviceStoppedError) Error() string {
	return "AIStudio2API service stopped"
}

func (*serviceStoppedError) HTTPStatus() int {
	return http.StatusServiceUnavailable
}

func (*serviceStoppedError) ErrorCode() string {
	return "service_stopped"
}

// Running returns true if the generation service is currently active and serving requests.
func (service *Service) Running() bool {
	return service.state.Load() == serviceRunning
}

// State returns the string representation of current service lifecycle state.
func (service *Service) State() string {
	switch service.state.Load() {
	case serviceLaunching:
		return "LAUNCHING"
	case serviceRunning:
		return "RUNNING"
	default:
		return "STOPPED"
	}
}

// Start launches the service, syncs models, and warms workers.
func (service *Service) Start(ctx context.Context, launching func()) ([]aistudio.Model, bool, error) {
	service.lifecycleMu.Lock()
	if service.state.Load() == serviceRunning {
		service.lifecycleMu.Unlock()
		return service.modelSnapshot(), false, nil
	}
	if service.state.Load() == serviceLaunching || service.transitionDone != nil {
		service.lifecycleMu.Unlock()
		return service.modelSnapshot(), false, ErrServiceTransitioning
	}
	dataContext, dataCancel := context.WithCancel(service.lifecycle)
	transitionDone := make(chan struct{})
	service.dataContext = dataContext
	service.dataCancel = dataCancel
	service.transitionDone = transitionDone
	service.transitionErr = nil
	service.state.Store(serviceLaunching)
	service.lifecycleMu.Unlock()
	stopCaller := context.AfterFunc(ctx, dataCancel)
	service.clearPerformance()
	service.clearModels()
	if launching != nil {
		launching()
	}
	service.requests.Log("service", "INFO", "Generation service startup | 1/2 | Syncing model directory")
	models, err := service.refreshModels(dataContext)
	if err != nil || len(models) == 0 {
		stopCaller()
		return models, false, service.finishLaunch(transitionDone, dataCancel, err)
	}
	service.requests.Log("service", "INFO", fmt.Sprintf(
		"Generation service startup | 2/2 | Prewarming WAA workers | models=%d | target=%d",
		len(models), service.workers.PrewarmTarget(),
	))
	firstWarm := service.workers.StartPrewarm(dataContext)
	select {
	case <-dataContext.Done():
		stopCaller()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, dataContext.Err())
	case warmErr, ok := <-firstWarm:
		if !ok {
			warmErr = fmt.Errorf("WAA prewarm returned no ready accounts")
		}
		if warmErr != nil {
			stopCaller()
			return nil, false, service.finishLaunch(transitionDone, dataCancel, warmErr)
		}
	}
	if !stopCaller() {
		return nil, false, service.finishLaunch(transitionDone, dataCancel, ctx.Err())
	}
	service.lifecycleMu.Lock()
	if service.transitionDone != transitionDone || service.state.Load() != serviceLaunching || dataContext.Err() != nil {
		service.lifecycleMu.Unlock()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, dataContext.Err())
	}
	service.state.Store(serviceRunning)
	service.transitionDone = nil
	close(transitionDone)
	service.lifecycleMu.Unlock()
	return models, true, nil
}

func (service *Service) finishLaunch(transitionDone chan struct{}, dataCancel context.CancelFunc, launchErr error) error {
	dataCancel()
	service.workers.waitPrewarm()
	resetErr := service.workers.ResetAll()
	service.lifecycleMu.Lock()
	if service.transitionDone == transitionDone {
		service.state.Store(serviceStopped)
		service.dataContext = nil
		service.dataCancel = nil
		service.transitionErr = resetErr
		service.transitionDone = nil
		close(transitionDone)
	}
	service.lifecycleMu.Unlock()
	return errors.Join(launchErr, resetErr)
}

// Stop terminates the running generation service.
func (service *Service) Stop() (bool, error) {
	service.lifecycleMu.Lock()
	state := service.state.Load()
	if state == serviceStopped {
		done := service.transitionDone
		service.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return false, nil
	}
	dataCancel := service.dataCancel
	if state == serviceLaunching {
		done := service.transitionDone
		service.state.Store(serviceStopped)
		service.lifecycleMu.Unlock()
		dataCancel()
		<-done
		service.requests.CancelAll()
		service.lifecycleMu.Lock()
		cleanupErr := service.transitionErr
		service.lifecycleMu.Unlock()
		return true, cleanupErr
	}
	transitionDone := make(chan struct{})
	service.transitionDone = transitionDone
	service.state.Store(serviceStopped)
	service.lifecycleMu.Unlock()
	dataCancel()
	service.workers.waitPrewarm()
	service.requests.CancelAll()
	resetErr := service.workers.ResetAll()
	service.lifecycleMu.Lock()
	service.dataContext = nil
	service.dataCancel = nil
	service.transitionErr = nil
	service.transitionDone = nil
	close(transitionDone)
	service.lifecycleMu.Unlock()
	return true, resetErr
}

// Models returns the active model snapshot.
func (service *Service) Models(context.Context) ([]aistudio.Model, error) {
	return service.modelSnapshot(), nil
}

func (service *Service) modelSnapshot() []aistudio.Model {
	service.modelsMu.RLock()
	models := make([]aistudio.Model, 0, len(service.models))
	for _, model := range service.models {
		if service.pool.HasEligibleModel(model.ID) {
			models = append(models, model)
		}
	}
	service.modelsMu.RUnlock()
	return models
}

func (service *Service) publishModelAccess() {
	statuses := service.pool.Status()
	accounts := make([]api.AdminAccount, 0, len(statuses))
	for _, status := range statuses {
		accounts = append(accounts, adminAccountDTO(status))
	}
	service.requests.Publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	service.requests.Publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": service.modelSnapshot()}})
}

func (service *Service) refreshModels(ctx context.Context) ([]aistudio.Model, error) {
	startedAt := time.Now()
	if len(service.pool.Status()) == 0 {
		models := []aistudio.Model{}
		service.modelsMu.Lock()
		service.models = models
		service.modelsMu.Unlock()
		return models, nil
	}
	requestCtx, cancel := service.lifecycleRequestContext(ctx)
	defer cancel()
	models, err := service.service.Models(requestCtx)
	if err != nil {
		if requestCtx.Err() != nil {
			service.requests.Log("service", "INFO", fmt.Sprintf(
				"Model directory sync canceled | elapsed=%s",
				time.Since(startedAt).Round(time.Millisecond),
			))
			return nil, requestCtx.Err()
		}
		service.requests.Log("service", "ERROR", fmt.Sprintf(
			"Model directory sync failed | elapsed=%s | error=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return nil, err
	}
	service.modelsMu.Lock()
	service.models = append([]aistudio.Model(nil), models...)
	service.modelsMu.Unlock()
	service.requests.Log("service", "INFO", fmt.Sprintf(
		"Model directory sync completed | models=%d | elapsed=%s",
		len(models), time.Since(startedAt).Round(time.Millisecond),
	))
	return append([]aistudio.Model(nil), models...), nil
}

// SyncModels triggers a manual sync of the model directory.
func (service *Service) SyncModels(ctx context.Context) error {
	service.lifecycleMu.Lock()
	if service.state.Load() != serviceRunning {
		service.lifecycleMu.Unlock()
		service.clearModels()
		return nil
	}
	dataContext := service.dataContext
	service.lifecycleMu.Unlock()
	syncContext, cancel := context.WithCancel(ctx)
	stopData := context.AfterFunc(dataContext, cancel)
	_, err := service.refreshModels(syncContext)
	stopData()
	cancel()
	if err != nil {
		service.clearModels()
		return err
	}
	service.lifecycleMu.Lock()
	running := service.state.Load() == serviceRunning && service.dataContext == dataContext
	service.lifecycleMu.Unlock()
	if running {
		service.workers.StartPrewarm(dataContext)
	}
	return nil
}

func (service *Service) clearModels() {
	service.modelsMu.Lock()
	service.models = nil
	service.modelsMu.Unlock()
}

func (service *Service) observedDataRequestContext(
	ctx context.Context,
	model string,
) (context.Context, context.CancelFunc, error) {
	api.SetAccessLogTarget(ctx, model, "")
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		return nil, nil, err
	}
	observed := aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		api.SetAccessLogTarget(requestCtx, model, account.Config.Label)
	})
	return observed, cancel, nil
}

// CountTokens counts tokens for the specified request.
func (service *Service) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		metrics.ObserveTokenCount(request.Model, false)
		return aistudio.TokenCount{}, err
	}
	defer cancel()
	count, requestErr := service.service.CountTokens(requestCtx, request)
	metrics.ObserveTokenCount(request.Model, requestErr == nil)
	api.SetAccessLogError(requestCtx, requestErr)
	return count, requestErr
}
// GenerateVideo starts video generation.
func (service *Service) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		metrics.ObserveVideoRequest("error")
		return aistudio.VideoOperation{}, err
	}
	defer cancel()
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		metrics.ObserveVideoRequest("error")
		return aistudio.VideoOperation{}, fmt.Errorf("video service unavailable")
	}
	operation, requestErr := video.GenerateVideo(requestCtx, request)
	if requestErr != nil {
		metrics.ObserveVideoRequest("error")
	} else {
		metrics.ObserveVideoRequest("success")
	}
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}
// GetGenerateVideoOperation polls a video generation operation.
func (service *Service) GetGenerateVideoOperation(ctx context.Context, operationID string) (aistudio.VideoOperation, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.VideoOperation{}, err
	}
	defer cancel()
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service unavailable")
	}
	operation, requestErr := video.GetGenerateVideoOperation(requestCtx, operationID)
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}

// DownloadFile downloads media from Google Drive storage.
func (service *Service) DownloadFile(ctx context.Context, fileID string) (aistudio.Media, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.Media{}, err
	}
	defer cancel()
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return aistudio.Media{}, fmt.Errorf("video service unavailable")
	}
	media, requestErr := video.DownloadFile(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)
	return media, requestErr
}

func (service *Service) lifecycleRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	requestCtx, cancel := context.WithTimeout(ctx, service.timeout)
	stopLifecycle := context.AfterFunc(service.lifecycle, cancel)
	return requestCtx, func() {
		stopLifecycle()
		cancel()
	}
}

func (service *Service) dataRequestContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	service.lifecycleMu.Lock()
	if service.state.Load() != serviceRunning || service.dataContext == nil {
		service.lifecycleMu.Unlock()
		return nil, nil, &serviceStoppedError{}
	}
	requestCtx, cancel := context.WithTimeout(ctx, service.timeout)
	stopData := context.AfterFunc(service.dataContext, cancel)
	service.lifecycleMu.Unlock()
	return requestCtx, func() {
		stopData()
		cancel()
	}, nil
}

func adminAccountDTO(status aistudio.AccountStatus) api.AdminAccount {
	models := make([]string, len(status.Models))
	copy(models, status.Models)
	return api.AdminAccount{
		ID:          status.ID,
		Label:       status.Label,
		Enabled:     status.Enabled,
		State:       string(status.State),
		Proxy:       status.Proxy,
		Locale:      status.Locale,
		Timezone:    status.Timezone,
		Models:      models,
		BenefitTier: status.BenefitTier,
		Message:     status.Message,
	}
}

var _ aistudio.Service = (*Service)(nil)
