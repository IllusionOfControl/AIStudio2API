package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/browser"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// Engine is the central composition root connecting pool, storage, workers, and services.
type Engine struct {
	Service   *Service
	Pool      *aistudio.AccountPool
	Store     *aistudio.AccountStore
	Workers   *WorkerManager
	Headers   *HeaderProvider
	Registry  *RequestRegistry
	Login     aistudio.IsolatedLoginDriver
	Config    config.Config
	closeFunc func() error
}

// NewEngine creates and wires the entire runtime orchestrator.
func NewEngine(ctx context.Context, cfg config.Config) (*Engine, error) {
	startedAt := time.Now()
	requests := NewRequestRegistry(ctx)
	requests.Log("service", "INFO", "App startup | 1/4 | Loading accounts")

	store := aistudio.NewAccountStore(strings.Split(cfg.AuthStates, ",")...)
	accounts, err := store.Load()
	if err != nil {
		return nil, err
	}

	requests.Log("service", "INFO", fmt.Sprintf("App startup | 2/4 | Verifying Camoufox | accounts=%d", len(accounts)))
	camoufoxPath, err := browser.FindCamoufoxExecutable()
	if err != nil {
		return nil, err
	}
	login, err := aistudio.NewNativeLoginDriver(camoufoxPath, cfg.RequestTimeout)
	if err != nil {
		return nil, err
	}

	requests.Log("service", "INFO", "App startup | 3/4 | Assembling protocol runtime")
	pool := aistudio.NewAccountPool(accounts, cfg.PerAccountConcurrency)
	headers, err := NewHeaderProvider(accounts, cfg.Proxy)
	if err != nil {
		return nil, err
	}

	transport, err := aistudio.NewMakerSuiteHTTPTransport(aistudio.HTTPTransportOptions{
		Pool: pool, Signer: aistudio.NewSigner(), Headers: headers, GlobalProxy: cfg.Proxy,
	})
	if err != nil {
		return nil, err
	}

	workers := NewWorkerManager(
		pool, accounts, requests, camoufoxPath, cfg.Proxy, cfg.InitTimeout,
		cfg.WarmWorkerLimit, cfg.WarmStartupConcurrency, cfg.TemporaryChat,
	)
	protected, err := aistudio.NewWorkerProtectedTransport(aistudio.WorkerProtectedTransportOptions{
		Transport: transport, Workers: workers,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.Join(err, workers.Close())
	}

	requestContext, err := aistudio.NewPoolRequestContextProvider(pool)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.Join(err, workers.Close())
	}

	refresher := newAuthRuntimeRefresher(workers, headers, requests, cfg.Proxy)
	client, err := aistudio.NewClient(aistudio.ClientOptions{
		Transport:       &authRetryTransport{transport: transport, refresher: refresher},
		Protected:       &authRetryProtectedTransport{transport: protected, refresher: refresher},
		ContextProvider: requestContext,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.Join(err, workers.Close())
	}

	pooled, err := aistudio.NewPooledService(pool, client)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.Join(err, workers.Close())
	}

	service := NewService(ctx, pooled, pool, requests, workers, cfg.RequestTimeout)
	requests.Log("service", "INFO", fmt.Sprintf(
		"Protocol runtime ready | accounts=%d | elapsed=%s",
		len(accounts), time.Since(startedAt).Round(time.Millisecond),
	))

	closeFunc := func() error {
		err := workers.Close()
		transport.CloseIdleConnections()
		return err
	}

	return &Engine{
		Service:   service,
		Pool:      pool,
		Store:     store,
		Workers:   workers,
		Headers:   headers,
		Registry:  requests,
		Login:     login,
		Config:    cfg,
		closeFunc: closeFunc,
	}, nil
}

// Close cleanly releases all orchestrator runtime resources.
func (engine *Engine) Close() error {
	if engine.closeFunc != nil {
		return engine.closeFunc()
	}
	return nil
}

type authRuntimeRefresher struct {
	refresh     func(context.Context, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, error)
	reset       func(string) error
	invalidate  func(string) error
	globalProxy string
	requests    *RequestRegistry
}

type authRetryTransport struct {
	transport *aistudio.MakerSuiteHTTPTransport
	refresher *authRuntimeRefresher
}

type authRetryProtectedTransport struct {
	transport aistudio.ProtectedTransport
	refresher *authRuntimeRefresher
}

func (transport *authRetryTransport) UploadDrive(
	ctx context.Context,
	accountID string,
	token string,
	request aistudio.UploadRequest,
) (aistudio.FileRef, error) {
	driveTransport, ok := any(transport.transport).(aistudio.DriveTransport)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("transport does not support UploadDrive")
	}
	return driveTransport.UploadDrive(ctx, accountID, token, request)
}

func (transport *authRetryTransport) DownloadDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) (aistudio.Media, error) {
	driveTransport, ok := any(transport.transport).(aistudio.DriveTransport)
	if !ok {
		return aistudio.Media{}, fmt.Errorf("transport does not support DownloadDrive")
	}
	return driveTransport.DownloadDrive(ctx, accountID, token, fileID)
}

func newAuthRuntimeRefresher(
	workers *WorkerManager,
	headers *HeaderProvider,
	requests *RequestRegistry,
	globalProxy string,
) *authRuntimeRefresher {
	return &authRuntimeRefresher{
		refresh: chromeauth.Refresh, reset: workers.Reset, invalidate: headers.Invalidate, globalProxy: globalProxy,
		requests: requests,
	}
}

func (transport *authRetryTransport) Do(ctx context.Context, request aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	response, err := transport.transport.Do(ctx, request)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}
	if !transport.refresher.Available(ctx) {
		return response, nil
	}
	if err := response.Body.Close(); err != nil {
		return nil, fmt.Errorf("close auth failure response: %w", err)
	}
	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, authenticationRefreshError(request.Method, response.StatusCode, err)
	}
	return transport.transport.Do(ctx, request)
}

func (transport *authRetryProtectedTransport) DoProtected(
	ctx context.Context,
	request aistudio.GenerateRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	response, err := transport.transport.DoProtected(ctx, request, rpc)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}
	if !transport.refresher.Available(ctx) {
		return response, nil
	}
	if err := response.Body.Close(); err != nil {
		return nil, fmt.Errorf("close auth failure response: %w", err)
	}
	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, authenticationRefreshError(rpc.Method, response.StatusCode, err)
	}
	return transport.transport.DoProtected(ctx, request, rpc)
}

func (transport *authRetryProtectedTransport) DoProtectedVideo(
	ctx context.Context,
	request aistudio.VideoRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	videoTransport, ok := transport.transport.(aistudio.VideoProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport does not support GenerateVideo")
	}
	response, err := videoTransport.DoProtectedVideo(ctx, request, rpc)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}
	if !transport.refresher.Available(ctx) {
		return response, nil
	}
	if err := response.Body.Close(); err != nil {
		return nil, fmt.Errorf("close auth failure response: %w", err)
	}
	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, authenticationRefreshError(rpc.Method, response.StatusCode, err)
	}
	return videoTransport.DoProtectedVideo(ctx, request, rpc)
}

func (refresher *authRuntimeRefresher) Refresh(ctx context.Context) error {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return fmt.Errorf("auth refresh missing account lease")
	}
	endRefresh, ok := lease.BeginAuthRefresh()
	if !ok {
		return fmt.Errorf("%w: account has active generation", aistudio.ErrAccountLeased)
	}
	defer endRefresh()
	account := lease.Account()
	startedAt := time.Now()
	refresher.requests.Log(account.Config.Label, "INFO", "Account auth refresh | 1/2 | Refreshing cookies")
	err := lease.RefreshStorageState(func(state *aistudio.StorageState) error {
		extension, exists, err := state.AuthExtension()
		if err != nil {
			return err
		}
		if !exists || extension.OAuth == nil {
			return fmt.Errorf("account %s missing Chrome OAuth refresh material", account.ID)
		}
		cookies, err := refresher.refresh(ctx, *extension.OAuth, account.EffectiveProxy(refresher.globalProxy))
		if err != nil {
			return fmt.Errorf("refresh account %s: %w", account.ID, err)
		}
		state.Cookies = cookies
		return nil
	}, func() error {
		refresher.requests.Log(account.Config.Label, "INFO", "Account auth refresh | 2/2 | Resetting protocol runtime")
		if refresher.invalidate != nil {
			if err := refresher.invalidate(account.ID); err != nil {
				return fmt.Errorf("refresh common headers for account %s: %w", account.ID, err)
			}
		}
		if err := refresher.reset(account.ID); err != nil {
			return fmt.Errorf("reset runtime for account %s: %w", account.ID, err)
		}
		return nil
	})
	if err != nil {
		wrapped := fmt.Errorf("save auth state for account %s: %w", account.ID, err)
		refresher.requests.Log(account.Config.Label, "ERROR", fmt.Sprintf(
			"Account auth refresh failed | elapsed=%s | error=%s",
			time.Since(startedAt).Round(time.Millisecond), wrapped.Error(),
		))
		return wrapped
	}
	refresher.requests.Log(account.Config.Label, "INFO", fmt.Sprintf(
		"Account auth refresh completed | elapsed=%s",
		time.Since(startedAt).Round(time.Millisecond),
	))
	return nil
}

func (refresher *authRuntimeRefresher) Available(ctx context.Context) bool {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return false
	}
	state, err := lease.ReloadStorageState()
	if err != nil {
		return false
	}
	extension, exists, err := state.AuthExtension()
	return err == nil && exists && extension.OAuth != nil
}

func authenticationFailed(response *aistudio.RPCResponse) bool {
	return response != nil && response.Body != nil && response.StatusCode == http.StatusUnauthorized
}

func authenticationRefreshError(method string, statusCode int, err error) error {
	return errors.Join(&aistudio.RPCError{
		Method: method, StatusCode: statusCode, Message: http.StatusText(statusCode),
	}, err)
}

var _ aistudio.RPCTransport = (*authRetryTransport)(nil)
var _ aistudio.DriveTransport = (*authRetryTransport)(nil)
var _ aistudio.ProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.VideoProtectedTransport = (*authRetryProtectedTransport)(nil)
