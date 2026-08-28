package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

type chromeCookieRefreshFunc func(context.Context, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, error)

type authRuntimeRefresher struct {
	refresh     chromeCookieRefreshFunc
	reset       func(string) error
	invalidate  func(string) error
	globalProxy string
	requests    *requestRegistry
}

type authRetryTransport struct {
	transport aistudio.RPCTransport
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
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("transport does not support Drive upload")
	}
	return drive.UploadDrive(ctx, accountID, token, request)
}

func (transport *authRetryTransport) DownloadDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) (aistudio.Media, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.Media{}, fmt.Errorf("transport does not support Drive download")
	}
	return drive.DownloadDrive(ctx, accountID, token, fileID)
}

func newAuthRuntimeRefresher(
	workers *accountWorkerManager,
	headers *accountHeaderProvider,
	requests *requestRegistry,
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
	refresher.requests.log(account.Config.Label, "INFO", "Account auth refresh | 1/2 | Refreshing cookies")
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
		refresher.requests.log(account.Config.Label, "INFO", "Account auth refresh | 2/2 | Resetting protocol runtime")
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
		refresher.requests.log(account.Config.Label, "ERROR", fmt.Sprintf(
			"Account auth refresh failed | elapsed=%s | error=%s",
			time.Since(startedAt).Round(time.Millisecond), wrapped.Error(),
		))
		return wrapped
	}
	refresher.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
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
