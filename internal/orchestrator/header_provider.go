package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// HeaderProvider manages fixed HTTP clients and protocol headers per account.
type HeaderProvider struct {
	mu          sync.RWMutex
	accounts    map[string]*accountHeaderState
	globalProxy string
}

type accountHeaderState struct {
	mu      sync.Mutex
	client  *http.Client
	headers http.Header
}

// NewHeaderProvider creates a new HeaderProvider for the given accounts.
func NewHeaderProvider(accounts []*aistudio.Account, globalProxy string) (*HeaderProvider, error) {
	provider := &HeaderProvider{
		accounts: make(map[string]*accountHeaderState, len(accounts)), globalProxy: globalProxy,
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		if err := provider.Add(account); err != nil {
			return nil, err
		}
	}
	return provider, nil
}

// Add registers a new account and initializes its dedicated proxy client.
func (provider *HeaderProvider) Add(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("account not initialized")
	}
	client, err := aistudio.NewProxyHTTPClient(account.EffectiveProxy(provider.globalProxy))
	if err != nil {
		return fmt.Errorf("create fixed exit for account %s: %w", account.ID, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if _, exists := provider.accounts[account.ID]; exists {
		client.CloseIdleConnections()
		return fmt.Errorf("fixed exit already exists for account %s", account.ID)
	}
	provider.accounts[account.ID] = &accountHeaderState{client: client}
	return nil
}

// Update updates an existing account's proxy client.
func (provider *HeaderProvider) Update(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("account not initialized")
	}
	client, err := aistudio.NewProxyHTTPClient(account.EffectiveProxy(provider.globalProxy))
	if err != nil {
		return fmt.Errorf("create fixed exit for account %s: %w", account.ID, err)
	}
	provider.mu.Lock()
	current := provider.accounts[account.ID]
	if current == nil {
		provider.mu.Unlock()
		client.CloseIdleConnections()
		return fmt.Errorf("fixed exit not found for account %s", account.ID)
	}
	provider.accounts[account.ID] = &accountHeaderState{client: client}
	provider.mu.Unlock()
	current.client.CloseIdleConnections()
	return nil
}

// Remove removes an account and closes its idle connections.
func (provider *HeaderProvider) Remove(accountID string) error {
	provider.mu.Lock()
	account := provider.accounts[accountID]
	if account != nil {
		delete(provider.accounts, accountID)
	}
	provider.mu.Unlock()
	if account == nil {
		return fmt.Errorf("fixed exit not found for account %s", accountID)
	}
	account.client.CloseIdleConnections()
	return nil
}

// Invalidate clears cached headers for the given account.
func (provider *HeaderProvider) Invalidate(accountID string) error {
	provider.mu.RLock()
	account := provider.accounts[accountID]
	provider.mu.RUnlock()
	if account == nil {
		return fmt.Errorf("fixed exit not found for account %s", accountID)
	}
	account.mu.Lock()
	account.headers = nil
	account.mu.Unlock()
	return nil
}

// ProtocolHeaders returns the discovered or cached public headers for the account.
func (provider *HeaderProvider) ProtocolHeaders(ctx context.Context, accountID string) (http.Header, error) {
	provider.mu.RLock()
	account := provider.accounts[accountID]
	provider.mu.RUnlock()
	if account == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	if len(account.headers) == 0 {
		headers, err := aistudio.DiscoverPublicHeaders(ctx, account.client)
		if err != nil {
			return nil, err
		}
		account.headers = headers.Clone()
	}
	return account.headers.Clone(), nil
}
