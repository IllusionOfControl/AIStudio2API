package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

type chromeCookieRefreshFunc func(context.Context, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, error)

// authRuntimeRefresher renews credentials in-place using saved Chrome OAuth material.
type authRuntimeRefresher struct {
	pool           *aistudio.AccountPool
	refresh        chromeCookieRefreshFunc
	importCurrent  func(context.Context, aistudio.AuthSource, *aistudio.ChromeOAuthMaterial, string) (aistudio.StorageState, error)
	reset          func(string) error
	prepareHeaders func(string) (func(bool), error)
	globalProxy    string
	requests       *requestRegistry
}

// authRetryTransport performs an auth renewal retry for standard RPC requests.
type authRetryTransport struct {
	transport aistudio.RPCTransport
	refresher *authRuntimeRefresher
}

// authRetryProtectedTransport performs an auth renewal retry for protected RPC requests.
type authRetryProtectedTransport struct {
	transport aistudio.ProtectedTransport
	refresher *authRuntimeRefresher
}

type bidiReleaseGate struct {
	mu        sync.Mutex
	once      sync.Once
	release   func() error
	requested bool
	committed bool
	abandoned bool
	err       error
}

func newBidiReleaseGate(release func() error) *bidiReleaseGate {
	return &bidiReleaseGate{release: release}
}

func (gate *bidiReleaseGate) Release() error {
	gate.mu.Lock()
	if gate.abandoned {
		gate.mu.Unlock()
		return nil
	}
	if !gate.committed {
		gate.requested = true
		gate.mu.Unlock()
		return nil
	}
	gate.mu.Unlock()

	return gate.releaseNow()
}

func (gate *bidiReleaseGate) Commit() error {
	gate.mu.Lock()
	gate.committed = true
	requested := gate.requested
	gate.mu.Unlock()

	if !requested {
		return nil
	}

	return gate.releaseNow()
}

func (gate *bidiReleaseGate) Abandon() {
	gate.mu.Lock()
	gate.abandoned = true
	gate.mu.Unlock()
}

func (gate *bidiReleaseGate) releaseNow() error {
	gate.once.Do(func() {
		if gate.release != nil {
			gate.err = gate.release()
		}
	})

	return gate.err
}

// UploadDrive delegates Drive uploads to the underlying authenticated transport.
func (transport *authRetryTransport) UploadDrive(
	ctx context.Context,
	accountID string,
	token string,
	request aistudio.UploadRequest,
) (aistudio.FileRef, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("transport does not support Drive uploads")
	}

	return drive.UploadDrive(ctx, accountID, token, request)
}

// DownloadDrive delegates Drive downloads to the underlying authenticated transport.
func (transport *authRetryTransport) DownloadDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) (aistudio.MediaStream, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.MediaStream{}, fmt.Errorf("transport does not support Drive downloads")
	}

	return drive.DownloadDrive(ctx, accountID, token, fileID)
}

// DeleteDrive delegates Drive file deletions to the underlying authenticated transport.
func (transport *authRetryTransport) DeleteDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) error {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return fmt.Errorf("transport does not support Drive deletion")
	}

	return drive.DeleteDrive(ctx, accountID, token, fileID)
}

// newAuthRuntimeRefresher creates a production authentication refresher.
func newAuthRuntimeRefresher(
	workers *accountWorkerManager,
	headers *accountHeaderProvider,
	requests *requestRegistry,
	globalProxy string,
) *authRuntimeRefresher {
	return &authRuntimeRefresher{
		pool:           workers.pool,
		refresh:        chromeauth.Refresh,
		importCurrent:  importCurrentChromeState,
		reset:          workers.Reset,
		prepareHeaders: headers.prepareInvalidate,
		globalProxy:    globalProxy,
		requests:       requests,
	}
}

// importCurrentChromeState updates authentication credentials for the same Google account from its original source
func importCurrentChromeState(ctx context.Context, source aistudio.AuthSource, material *aistudio.ChromeOAuthMaterial, proxy string) (aistudio.StorageState, error) {
	root, err := chromeauth.DefaultChromeRoot()
	if err != nil {
		return aistudio.StorageState{}, err
	}
	accounts, err := chromeauth.Discover(root)
	if err != nil {
		return aistudio.StorageState{}, err
	}
	var selected string
	for _, account := range accounts {
		if !account.Importable || !strings.EqualFold(account.Email, source.Email) || material != nil && account.GaiaID != material.GaiaID {
			continue
		}
		selected = account.ID
		if account.Profile == source.Profile {
			break
		}
	}
	if selected == "" {
		return aistudio.StorageState{}, fmt.Errorf("no renewable original Chrome account found for %s", source.Email)
	}
	results, err := chromeauth.Import(ctx, chromeauth.ImportOptions{ChromeRoot: root, Proxy: proxy, AccountIDs: []string{selected}})
	if err != nil {
		return aistudio.StorageState{}, err
	}
	if len(results) != 1 || !strings.EqualFold(results[0].Email, source.Email) {
		return aistudio.StorageState{}, fmt.Errorf("updated Chrome account does not match original account")
	}
	return results[0].State, nil
}

// markAuthenticationRequired publishes the account status for the current auth generation
func (refresher *authRuntimeRefresher) markAuthenticationRequired(ctx context.Context, cause error) error {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok || ctx.Err() != nil || !aistudio.DefinitiveAuthenticationFailure(cause) {
		return cause
	}
	err := lease.MarkAuthenticationRequired(cause.Error())
	if refresher.pool != nil {
		statuses := refresher.pool.Status()
		accounts := make([]api.AdminAccount, 0, len(statuses))
		for _, status := range statuses {
			accounts = append(accounts, adminAccountDTO(status))
		}
		refresher.requests.publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	}
	return errors.Join(cause, err)
}

// Recover performs a login recovery once for the current auth generation and preserves the final failure cause
func (refresher *authRuntimeRefresher) Recover(ctx context.Context, cause error) error {
	var startup *accountWorkerInitError
	if errors.As(cause, &startup) && startup.authHandled {
		return cause
	}
	if refresher.Available(ctx) {
		if err := refresher.Refresh(ctx); err == nil {
			return nil
		} else {
			cause = errors.Join(cause, err)
		}
	}
	return refresher.markAuthenticationRequired(ctx, cause)
}

// do replays plain or protected RPCs before semantic output has been produced
func (refresher *authRuntimeRefresher) do(ctx context.Context, method string, send func() (*aistudio.RPCResponse, error)) (*aistudio.RPCResponse, error) {
	for attempt := 0; ; attempt++ {
		response, err := send()
		if err == nil && authenticationFailed(response) {
			original, readErr := readAuthenticationFailure(method, response)
			if readErr != nil {
				return nil, readErr
			}
			err = original
			response = nil
		}
		if !aistudio.DefinitiveAuthenticationFailure(err) {
			return response, err
		}
		if attempt == 1 {
			return nil, refresher.markAuthenticationRequired(ctx, err)
		}
		if err := refresher.Recover(ctx, err); err != nil {
			return nil, err
		}
	}
}

func (provider *accountHeaderProvider) prepareInvalidate(accountID string) (func(bool), error) {
	provider.mu.RLock()
	account := provider.accounts[accountID]
	provider.mu.RUnlock()

	if account == nil {
		return nil, fmt.Errorf("fixed egress for account does not exist: %s", accountID)
	}

	account.mu.Lock()
	previous := account.headers.Clone()
	account.headers = nil

	return func(committed bool) {
		if !committed {
			account.headers = previous
		}
		account.mu.Unlock()
	}, nil
}

// Do refreshes credentials for the same account upon receiving a 401 status and replays the request.
func (transport *authRetryTransport) Do(ctx context.Context, request aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	return transport.refresher.do(ctx, request.Method, func() (*aistudio.RPCResponse, error) {
		return transport.transport.Do(ctx, request)
	})
}

// DoProtected refreshes credentials for the same account upon receiving a 401 status and replays the protected request.
func (transport *authRetryProtectedTransport) DoProtected(
	ctx context.Context,
	request aistudio.GenerateRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	return transport.refresher.do(ctx, rpc.Method, func() (*aistudio.RPCResponse, error) {
		return transport.transport.DoProtected(ctx, request, rpc)
	})
}

// OpenBidiProtected refreshes credentials for the same account upon receiving a 401 status and re-establishes the WebChannel session.
func (transport *authRetryProtectedTransport) OpenBidiProtected(
	ctx context.Context,
	request aistudio.BidiRequest,
	runtime aistudio.RequestContext,
	lease *aistudio.AccountLease,
	release func() error,
) (*aistudio.BidiSession, error) {
	bidiTransport, ok := transport.transport.(aistudio.BidiProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport does not support BidiGenerateContent")
	}

	gate := newBidiReleaseGate(release)
	session, err := bidiTransport.OpenBidiProtected(ctx, request, runtime, lease, gate.Release)
	if err == nil {
		if releaseErr := gate.Commit(); releaseErr != nil {
			return nil, errors.Join(releaseErr, session.Close())
		}
		return session, nil
	}
	if !aistudio.DefinitiveAuthenticationFailure(err) || transport.refresher == nil {
		return nil, errors.Join(err, gate.Commit())
	}

	gate.Abandon()
	if recoverErr := transport.refresher.Recover(ctx, err); recoverErr != nil {
		return nil, recoverErr
	}
	session, err = bidiTransport.OpenBidiProtected(ctx, request, runtime, lease, release)
	if aistudio.DefinitiveAuthenticationFailure(err) {
		err = transport.refresher.markAuthenticationRequired(ctx, err)
	}
	return session, err
}

// DoProtectedVideo refreshes credentials for the same account upon authentication failure and replays the Veo request.
func (transport *authRetryProtectedTransport) DoProtectedVideo(
	ctx context.Context,
	request aistudio.VideoRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	videoTransport, ok := transport.transport.(aistudio.VideoProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport does not support GenerateVideo")
	}
	return transport.refresher.do(ctx, rpc.Method, func() (*aistudio.RPCResponse, error) {
		return videoTransport.DoProtectedVideo(ctx, request, rpc)
	})
}

// Refresh renews credentials for the currently leased account and saves the new storage state.
func (refresher *authRuntimeRefresher) Refresh(ctx context.Context) error {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return fmt.Errorf("auth renewal missing account lease")
	}

	endRefresh, ok := lease.BeginAuthRefresh()
	if !ok {
		return fmt.Errorf("%w: account has active generation", aistudio.ErrAccountLeased)
	}
	defer endRefresh()
	if err := lease.WaitForAuthRefresh(ctx); err != nil {
		return err
	}
	account := lease.Account()
	startedAt := time.Now()

	refresher.requests.log(account.Config.Label, "INFO", "Account auth renewal | 1/2 | Refreshing cookies")

	err := lease.RefreshStorageState(func(state *aistudio.StorageState) error {
		extension, exists, err := state.AuthExtension()
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("account %s missing Chrome OAuth renewal material", account.ID)
		}
		proxy := account.EffectiveProxy(refresher.globalProxy)
		var cookies []aistudio.StateCookie
		if extension.OAuth != nil {
			cookies, err = refresher.refresh(ctx, *extension.OAuth, proxy)
		}
		if extension.OAuth == nil || errors.Is(err, chromeauth.ErrCredentialsRejected) {
			if extension.Source.Browser != "chrome" || !strings.EqualFold(extension.Source.Email, account.ID) {
				return errors.Join(fmt.Errorf("account %s missing renewable Chrome source", account.ID), err)
			}
			refresher.requests.log(account.Config.Label, "INFO", "Account auth renewal | Updating Chrome source material")
			updated, importErr := refresher.importCurrent(ctx, extension.Source, extension.OAuth, proxy)
			if importErr != nil {
				return errors.Join(err, importErr)
			}
			current, exists, importErr := updated.AuthExtension()
			if importErr != nil || !exists || current.OAuth == nil || !strings.EqualFold(current.Source.Email, account.ID) ||
				extension.OAuth != nil && current.OAuth.GaiaID != extension.OAuth.GaiaID {
				return fmt.Errorf("Chrome update result does not match account %s", account.ID)
			}
			if err := state.SetAuthExtension(current); err != nil {
				return err
			}
			cookies, err = updated.Cookies, nil
		}
		if err != nil {
			return fmt.Errorf("renew account %s: %w", account.ID, err)
		}

		state.Cookies = cookies
		_, err = aistudio.NewSigner().Sign(*state)
		return err
	}, func() (func(bool), error) {
		refresher.requests.log(account.Config.Label, "INFO", "Account auth renewal | 2/2 | Resetting protocol runtime")

		if err := refresher.reset(account.ID); err != nil {
			return nil, fmt.Errorf("reset runtime for account %s: %w", account.ID, err)
		}

		finish, err := refresher.prepareHeaders(account.ID)
		if err != nil {
			return nil, fmt.Errorf("refresh common headers for account %s: %w", account.ID, err)
		}

		return finish, nil
	})

	if err != nil {
		wrapped := fmt.Errorf("save storage state for account %s: %w", account.ID, err)
		refresher.requests.log(account.Config.Label, "ERROR", fmt.Sprintf(
			"Account auth renewal failed | duration=%s | error=%s",
			time.Since(startedAt).Round(time.Millisecond), wrapped.Error(),
		))
		return wrapped
	}

	refresher.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
		"Account auth renewal completed | duration=%s",
		time.Since(startedAt).Round(time.Millisecond),
	))

	return nil
}

// Available returns whether the currently leased account has saved Chrome OAuth refresh material.
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
	return err == nil && exists && (extension.OAuth != nil || extension.Source.Browser == "chrome")
}

func authenticationFailed(response *aistudio.RPCResponse) bool {
	return response != nil && response.Body != nil && response.StatusCode == http.StatusUnauthorized
}

// readAuthenticationFailure reads and closes an authentication failure response to preserve the original error reason.
func readAuthenticationFailure(method string, response *aistudio.RPCResponse) (*aistudio.RPCError, error) {
	body, readErr := io.ReadAll(response.Body)
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return nil, fmt.Errorf("read authentication failure response: %w", err)
	}

	return aistudio.DecodeRPCError(method, response.StatusCode, body), nil
}

var _ aistudio.RPCTransport = (*authRetryTransport)(nil)
var _ aistudio.DriveTransport = (*authRetryTransport)(nil)
var _ aistudio.ProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.VideoProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.BidiProtectedTransport = (*authRetryProtectedTransport)(nil)
