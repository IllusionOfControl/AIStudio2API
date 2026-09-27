package aistudio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/waa"
)

const (
	waaCreateURL           = "https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/Create"
	waaRequestKey          = "lmnUSbltwc5ULv48iKLX"
	waaAPIKey              = "AIzaSyBGb5fGAyC-pRcRU6MUHb__b_vKha71HRE"
	waaLifetime            = 12 * time.Hour
	waaSettleQuiet         = 3 * time.Second
	loggingContextURL      = "https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GetLoggingContext"
	firefoxInnerHeightDiff = 57
)

// workerRuntime represents the shared worker capabilities provided by both Camoufox page and pure Go VM WAA backends
type workerRuntime interface {
	Proof(context.Context, string, string) (string, error)
	ProtocolHeaders(context.Context) (http.Header, error)
	SendProtected(context.Context, string, http.Header, []byte) (*camoufoxnative.ProtectedResponse, error)
	StorageCookies(context.Context) ([]byte, error)
	State() camoufoxnative.State
	Close() error
}

// waaInterpreterHashes records interpreter versions executed by this process
var waaInterpreterHashes sync.Map

// goWAARuntime assumes official site page WAA responsibilities via a pure Go BotGuard VM and account fixed-egress HTTP client
type goWAARuntime struct {
	options        camoufoxnative.Options
	client         *http.Client
	profile        waa.Profile
	userAgent      string
	acceptLanguage string
	pageURL        string
	headers        http.Header
	cacheDirectory string

	storageMu sync.Mutex
	storage   StorageState

	vmMu            sync.Mutex
	vm              *waa.Runtime
	vmExpires       time.Time
	interpreterHash string
	interpreter     string
	closed          bool
	refreshTimer    *time.Timer
}

// newGoWAAHTTPClient creates a fixed-egress client with redirects handled manually by runtime
func newGoWAAHTTPClient(proxyURL string) (*http.Client, error) {
	client, err := NewProxyHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client, nil
}

// NewGoWorker starts a pure Go WAA runtime for a single account without launching a browser
func NewGoWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*NativeWorker, error) {
	if accountID == "" {
		return nil, fmt.Errorf("missing account ID")
	}
	runtime, err := newGoWAARuntime(ctx, options)
	if err != nil {
		return nil, err
	}
	return &NativeWorker{
		accountID: accountID,
		runtime:   runtime,
		state: WorkerState{
			AccountID: accountID,
			Phase:     WorkerReady,
			PID:       os.Getpid(),
			RuntimeID: "go-waa",
			PageURL:   runtime.pageURL,
		},
	}, nil
}

func newGoWAARuntime(ctx context.Context, options camoufoxnative.Options) (*goWAARuntime, error) {
	if options.Model == "" {
		return nil, errors.New("WAA bootstrap missing live catalog chat model")
	}
	if options.BootstrapPrompt == "" {
		options.BootstrapPrompt = fmt.Sprintf("AIStudio2API bootstrap %d", time.Now().UnixNano())
	}
	reportGoStartup(options, camoufoxnative.StartupPreparingBrowser)
	storage, err := LoadStorageState(options.StorageStatePath)
	if err != nil {
		return nil, err
	}
	fingerprint, err := camoufoxnative.AccountFingerprint(options)
	if err != nil {
		return nil, err
	}
	client, err := newGoWAAHTTPClient(options.Proxy)
	if err != nil {
		return nil, err
	}
	runtime := &goWAARuntime{options: options, client: client, storage: storage}
	runtime.profile, runtime.userAgent, runtime.acceptLanguage = profileFromFingerprint(fingerprint)
	runtime.pageURL = aiStudioOrigin + "/prompts/new_chat?model=" + url.QueryEscape(options.Model)
	if options.TemporaryChat {
		runtime.pageURL += "&temporary=true"
	}
	runtime.profile.PageURL = runtime.pageURL
	runtime.profile.HasFocus = true
	runtime.profile.Locale, _ = fingerprint["navigator.language"].(string)
	runtime.profile.TimeZone, _ = fingerprint["timezone"].(string)
	runtime.cacheDirectory = filepath.Join(filepath.Dir(filepath.Dir(options.StorageStatePath)), ".waa-interpreters")
	reportGoStartup(options, camoufoxnative.StartupLoadingAIStudio)
	apiKey, err := runtime.loadPage(ctx)
	if err != nil {
		return nil, err
	}
	extension, err := runtime.loggingContextHeader(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	visitID, err := newVisitID()
	if err != nil {
		return nil, err
	}
	runtime.headers = http.Header{}
	runtime.headers.Set("user-agent", runtime.userAgent)
	runtime.headers.Set("x-goog-api-key", apiKey)
	runtime.headers.Set("x-goog-authuser", "0")
	runtime.headers.Set("x-user-agent", "grpc-web-javascript/0.1")
	runtime.headers.Set("x-aistudio-visit-id", visitID)
	if extension != "" {
		runtime.headers.Set("x-goog-ext-519733851-bin", extension)
	}
	reportGoStartup(options, camoufoxnative.StartupLocatingWAA)
	challenge, err := runtime.createChallenge(ctx, nil)
	if err != nil {
		return nil, err
	}
	reportGoStartup(options, camoufoxnative.StartupBootstrappingWAA)
	vm, err := runtime.startVM(ctx, challenge)
	if err != nil {
		return nil, err
	}
	if err := vm.FillPrompt(ctx, options.BootstrapPrompt, true); err != nil {
		vm.Close()
		return nil, fmt.Errorf("fill bootstrap prompt failed: %w", err)
	}
	if err := vm.Settle(ctx, waaSettleQuiet); err != nil {
		vm.Close()
		return nil, err
	}
	runtime.vm = vm
	runtime.scheduleRefresh()
	return runtime, nil
}

// scheduleRefresh rebuilds VM in the background following official workflow when VM lifetime expires
func (runtime *goWAARuntime) scheduleRefresh() {
	runtime.vmExpires = time.Now().Add(waaLifetime)
	if runtime.refreshTimer != nil {
		runtime.refreshTimer.Stop()
	}
	runtime.refreshTimer = time.AfterFunc(waaLifetime, func() {
		runtime.vmMu.Lock()
		defer runtime.vmMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := runtime.currentVM(ctx, true); err != nil {
			slog.Warn("WAA pure Go VM refresh failed", "error", err)
		}
	})
}

func reportGoStartup(options camoufoxnative.Options, stage camoufoxnative.StartupStage) {
	if options.StartupProgress != nil {
		options.StartupProgress(stage)
	}
}

// profileFromFingerprint maps account fingerprint to host values according to rules observed in Camoufox pages
func profileFromFingerprint(config map[string]any) (waa.Profile, string, string) {
	profile := waa.Profile{Window: map[string]any{}, Navigator: map[string]any{}, Screen: map[string]any{}, Document: map[string]any{}}
	for key, value := range config {
		scope, name, found := strings.Cut(key, ".")
		if !found {
			continue
		}
		switch scope {
		case "navigator":
			profile.Navigator[name] = value
		case "screen":
			profile.Screen[name] = value
		case "window":
			profile.Window[name] = value
		}
	}
	profile.Navigator["doNotTrack"] = "unspecified"
	profile.Navigator["globalPrivacyControl"] = false
	if width, ok := numberValue(profile.Window["outerWidth"]); ok {
		profile.Window["innerWidth"] = width
	}
	if height, ok := numberValue(profile.Window["outerHeight"]); ok {
		profile.Window["innerHeight"] = height - firefoxInnerHeightDiff
	}
	userAgent, _ := config["navigator.userAgent"].(string)
	if userAgent == "" {
		userAgent = publicDiscoveryUserAgent
	}
	acceptLanguage, _ := config["headers.Accept-Language"].(string)
	if acceptLanguage == "" {
		acceptLanguage = "en-US,en;q=0.5"
	}
	return profile, userAgent, acceptLanguage
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	}
	return 0, false
}

// cookies returns a copy of current account cookie state
func (runtime *goWAARuntime) cookies() StorageState {
	runtime.storageMu.Lock()
	defer runtime.storageMu.Unlock()
	state := runtime.storage
	state.Cookies = append([]StateCookie(nil), runtime.storage.Cookies...)
	return state
}

// mergeResponseCookies writes Set-Cookie headers from response back to account cookie state
func (runtime *goWAARuntime) mergeResponseCookies(response *http.Response, requestURL string) {
	headers := response.Header.Values("Set-Cookie")
	if len(headers) == 0 {
		return
	}
	runtime.storageMu.Lock()
	defer runtime.storageMu.Unlock()
	if err := runtime.storage.MergeSetCookieHeaders(headers, requestURL, time.Now()); err != nil {
		slog.Warn("WAA pure Go cookie write-back failed", "url", requestURL, "error", err)
	}
}

// do sends request shaped like Firefox headers and writes back response cookies
func (runtime *goWAARuntime) do(ctx context.Context, method string, target string, body []byte, headers http.Header) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header = headers.Clone()
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", runtime.userAgent)
	}
	if request.Header.Get("Accept-Language") == "" {
		request.Header.Set("Accept-Language", runtime.acceptLanguage)
	}
	if request.Header.Get("Accept-Encoding") == "" {
		request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	}
	if cookieHeader, err := runtime.cookies().CookieHeader(target, time.Now()); err == nil && cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	}
	response, err := runtime.client.Do(request)
	if err != nil {
		return nil, err
	}
	runtime.mergeResponseCookies(response, target)
	return response, nil
}

// loadPage requests official page shaped like browser navigation and returns public page API key
func (runtime *goWAARuntime) loadPage(ctx context.Context) (string, error) {
	headers := http.Header{}
	headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	headers.Set("Upgrade-Insecure-Requests", "1")
	headers.Set("Sec-Fetch-Dest", "document")
	headers.Set("Sec-Fetch-Mode", "navigate")
	headers.Set("Sec-Fetch-Site", "none")
	headers.Set("Sec-Fetch-User", "?1")
	headers.Set("Priority", "u=0, i")
	target := runtime.pageURL
	for redirect := 0; redirect < 5; redirect++ {
		response, err := runtime.do(ctx, http.MethodGet, target, nil, headers)
		if err != nil {
			return "", fmt.Errorf("load AI Studio page: %w", err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read AI Studio page: %w", readErr)
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location, err := response.Location()
			if err != nil {
				return "", fmt.Errorf("invalid AI Studio page redirect: %w", err)
			}
			if strings.Contains(location.Host, "accounts.google.com") {
				return "", fmt.Errorf("isolated login state expired url=%s", location.String())
			}
			target = location.String()
			continue
		}
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("AI Studio page returned HTTP %d", response.StatusCode)
		}
		match := makerSuiteAPIKeyPattern.FindSubmatch(body)
		if len(match) != 2 || len(match[1]) == 0 {
			return "", fmt.Errorf("AI Studio page missing WIu0Nc")
		}
		return string(match[1]), nil
	}
	return "", errors.New("too many AI Studio page redirects")
}

// rpcHeaders returns common headers for official page grpc-web requests
func (runtime *goWAARuntime) rpcHeaders(apiKey string) (http.Header, error) {
	authorization, err := NewSigner().Authorization(runtime.cookies())
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Content-Type", "application/json+protobuf")
	headers.Set("X-Goog-Api-Key", apiKey)
	headers.Set("X-Goog-AuthUser", "0")
	headers.Set("X-User-Agent", "grpc-web-javascript/0.1")
	headers.Set("Authorization", authorization)
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-site")
	return headers, nil
}

// loggingContextHeader calls GetLoggingContext and encodes as official X-Goog-Ext-519733851-Bin
func (runtime *goWAARuntime) loggingContextHeader(ctx context.Context, apiKey string) (string, error) {
	headers, err := runtime.rpcHeaders(apiKey)
	if err != nil {
		return "", err
	}
	response, err := runtime.do(ctx, http.MethodPost, loggingContextURL, []byte("[]"), headers)
	if err != nil {
		return "", fmt.Errorf("read official logging context: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GetLoggingContext returned HTTP %d", response.StatusCode)
	}
	encoded, err := encodeJSPBFields(body)
	if err != nil {
		return "", fmt.Errorf("encode official logging context: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}

// encodeJSPBFields encodes numbers and strings in JSPB array as protobuf binary in field order
func encodeJSPBFields(raw []byte) ([]byte, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var output []byte
	for index, field := range fields {
		number := uint64(index + 1)
		trimmed := bytes.TrimSpace(field)
		if len(trimmed) == 0 || string(trimmed) == "null" {
			continue
		}
		switch trimmed[0] {
		case '"':
			var text string
			if err := json.Unmarshal(trimmed, &text); err != nil {
				return nil, err
			}
			output = appendVarint(output, number<<3|2)
			output = appendVarint(output, uint64(len(text)))
			output = append(output, text...)
		case 't', 'f':
			output = appendVarint(output, number<<3)
			if trimmed[0] == 't' {
				output = appendVarint(output, 1)
			} else {
				output = appendVarint(output, 0)
			}
		default:
			var value int64
			if err := json.Unmarshal(trimmed, &value); err != nil {
				return nil, fmt.Errorf("field %d is not an integer: %s", number, trimmed)
			}
			output = appendVarint(output, number<<3)
			output = appendVarint(output, uint64(value))
		}
	}
	return output, nil
}

func appendVarint(output []byte, value uint64) []byte {
	for value >= 0x80 {
		output = append(output, byte(value)|0x80)
		value >>= 7
	}
	return append(output, byte(value))
}

// createChallenge calls Waa/Create matching official request body, passing loaded interpreter hash and previous VM snapshot upon refresh
func (runtime *goWAARuntime) createChallenge(ctx context.Context, refresh []string) (waa.Challenge, error) {
	requestBody := []string{waaRequestKey}
	requestBody = append(requestBody, refresh...)
	body, _ := json.Marshal(requestBody)
	headers, err := runtime.rpcHeaders(waaAPIKey)
	if err != nil {
		return waa.Challenge{}, err
	}
	response, err := runtime.do(ctx, http.MethodPost, waaCreateURL, body, headers)
	if err != nil {
		return waa.Challenge{}, fmt.Errorf("call Waa/Create: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return waa.Challenge{}, err
	}
	if response.StatusCode != http.StatusOK {
		return waa.Challenge{}, fmt.Errorf("Waa/Create returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	return waa.ParseChallenge(raw)
}

// loadInterpreter reads cached or downloads interpreter by challenge-specified hash and verifies digest
func (runtime *goWAARuntime) loadInterpreter(ctx context.Context, challenge waa.Challenge) (string, error) {
	if challenge.InterpreterJavaScript != "" {
		if hash := waa.InterpreterHash([]byte(challenge.InterpreterJavaScript)); hash != challenge.InterpreterHash {
			return "", fmt.Errorf("WAA interpreter digest mismatch expected=%s actual=%s", challenge.InterpreterHash, hash)
		}
		return challenge.InterpreterJavaScript, nil
	}
	if runtime.interpreterHash == challenge.InterpreterHash && runtime.interpreter != "" {
		return runtime.interpreter, nil
	}
	path := filepath.Join(runtime.cacheDirectory, challenge.InterpreterHash+".js")
	if cached, err := os.ReadFile(path); err == nil && waa.InterpreterHash(cached) == challenge.InterpreterHash {
		return string(cached), nil
	}
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Sec-Fetch-Dest", "script")
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", "cross-site")
	response, err := runtime.do(ctx, http.MethodGet, challenge.InterpreterURL, nil, headers)
	if err != nil {
		return "", fmt.Errorf("download WAA interpreter: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download WAA interpreter returned HTTP %d", response.StatusCode)
	}
	if hash := waa.InterpreterHash(data); hash != challenge.InterpreterHash {
		return "", fmt.Errorf("WAA interpreter digest mismatch expected=%s actual=%s", challenge.InterpreterHash, hash)
	}
	if err := os.MkdirAll(runtime.cacheDirectory, 0o755); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
	return string(data), nil
}

// startVM loads challenge-specified interpreter and initializes new VM
func (runtime *goWAARuntime) startVM(ctx context.Context, challenge waa.Challenge) (*waa.Runtime, error) {
	interpreter, err := runtime.loadInterpreter(ctx, challenge)
	if err != nil {
		return nil, err
	}
	if _, seen := waaInterpreterHashes.LoadOrStore(challenge.InterpreterHash, true); !seen {
		slog.Info("WAA interpreter version", "hash", challenge.InterpreterHash, "url", challenge.InterpreterURL)
	}
	if runtime.interpreterHash != "" && runtime.interpreterHash != challenge.InterpreterHash {
		slog.Warn("WAA interpreter version changed", "previous", runtime.interpreterHash, "current", challenge.InterpreterHash)
	}
	runtime.interpreterHash, runtime.interpreter = challenge.InterpreterHash, interpreter
	cookie, _ := documentCookie(runtime.cookies())
	profile := runtime.profile
	profile.Document = map[string]any{"cookie": cookie}
	return waa.NewRuntime(ctx, waa.Options{Challenge: challenge, Interpreter: interpreter, Profile: profile, LoadImage: runtime.loadImage})
}

// documentCookie returns non-HttpOnly cookie string visible to page scripts
func documentCookie(state StorageState) (string, error) {
	parts := make([]string, 0, len(state.Cookies))
	for _, cookie := range state.Cookies {
		if cookie.HTTPOnly || !cookieDomainMatches(cookie.Domain, "aistudio.google.com") {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; "), nil
}

// loadImage issues page img request shaped like Firefox image request, returning whether load succeeded based on decoding result
func (runtime *goWAARuntime) loadImage(address string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("Accept", "image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Sec-Fetch-Dest", "image")
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", "same-origin")
	response, err := runtime.do(ctx, http.MethodGet, address, nil, headers)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode == http.StatusOK && strings.HasPrefix(response.Header.Get("Content-Type"), "image/") && len(body) > 0
}

// currentVM returns active unexpired VM, rebuilding according to official refresh flow when expired
func (runtime *goWAARuntime) currentVM(ctx context.Context, force bool) (*waa.Runtime, error) {
	if runtime.closed {
		return nil, errors.New("pure Go WAA runtime closed")
	}
	if !force && runtime.vm != nil && time.Now().Before(runtime.vmExpires) {
		return runtime.vm, nil
	}
	var refresh []string
	if runtime.vm != nil {
		previous, err := runtime.vm.RefreshSnapshot(ctx)
		if err != nil {
			previous = "E:UCE"
		}
		refresh = []string{runtime.interpreterHash, previous}
	}
	challenge, err := runtime.createChallenge(ctx, refresh)
	if err != nil {
		return nil, err
	}
	vm, err := runtime.startVM(ctx, challenge)
	if err != nil {
		return nil, err
	}
	if err := vm.Settle(ctx, waaSettleQuiet); err != nil {
		vm.Close()
		return nil, err
	}
	if runtime.vm != nil {
		runtime.vm.Close()
	}
	runtime.vm = vm
	runtime.scheduleRefresh()
	return vm, nil
}

// Proof synchronizes page prompt and generates fresh WAA proof for SHA-256 digest
func (runtime *goWAARuntime) Proof(ctx context.Context, digest string, prompt string) (string, error) {
	runtime.vmMu.Lock()
	defer runtime.vmMu.Unlock()
	vm, err := runtime.currentVM(ctx, false)
	if err != nil {
		return "", err
	}
	proof, err := vm.Proof(ctx, digest, prompt)
	if err != nil {
		return "", fmt.Errorf("generate fresh WAA proof: %w", err)
	}
	if !strings.HasPrefix(proof, "!") {
		return "", errors.New("invalid fresh WAA proof prefix")
	}
	return proof, nil
}

// ProtocolHeaders returns the six common headers for official GenerateContent
func (runtime *goWAARuntime) ProtocolHeaders(ctx context.Context) (http.Header, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return runtime.headers.Clone(), nil
}

// SendProtected streams protected request shaped like Firefox official page fetch
func (runtime *goWAARuntime) SendProtected(ctx context.Context, rawURL string, headers http.Header, body []byte) (*camoufoxnative.ProtectedResponse, error) {
	request := headers.Clone()
	request.Set("Accept", "*/*")
	request.Set("Referer", aiStudioOrigin+"/")
	request.Set("Origin", aiStudioOrigin)
	request.Set("Sec-Fetch-Dest", "empty")
	request.Set("Sec-Fetch-Mode", "cors")
	request.Set("Sec-Fetch-Site", "same-site")
	request.Del("Cookie")
	response, err := runtime.do(ctx, http.MethodPost, rawURL, body, request)
	if err != nil {
		return nil, err
	}
	return &camoufoxnative.ProtectedResponse{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body}, nil
}

// StorageCookies returns account current cookies formatted matching Camoufox storage.getCookies export
func (runtime *goWAARuntime) StorageCookies(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(runtime.cookies().Cookies)
}

// State returns pure Go runtime page and protocol state
func (runtime *goWAARuntime) State() camoufoxnative.State {
	return camoufoxnative.State{
		PID:       os.Getpid(),
		PageURL:   runtime.pageURL,
		UserAgent: runtime.userAgent,
		Platform:  fmt.Sprint(runtime.profile.Navigator["platform"]),
		Timezone:  runtime.options.Timezone,
		Headers:   runtime.headers.Clone(),
	}
}

// Close terminates VM lifetime
func (runtime *goWAARuntime) Close() error {
	runtime.vmMu.Lock()
	defer runtime.vmMu.Unlock()
	runtime.closed = true
	if runtime.refreshTimer != nil {
		runtime.refreshTimer.Stop()
	}
	if runtime.vm != nil {
		runtime.vm.Close()
		runtime.vm = nil
	}
	return nil
}
