package waa

import (
	_ "embed"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
)

//go:embed dom.js
var domJavaScript string

//go:embed firefox152.json
var firefoxShapeJSON string

// Profile holds host values determined by account fingerprint and page state
type Profile struct {
	PageURL      string
	Locale       string
	TimeZone     string
	Window       map[string]any
	Navigator    map[string]any
	Screen       map[string]any
	Document     map[string]any
	LocalStorage map[string]string
	HasFocus     bool
}

// frameWindowNames are Window properties whose values are shared between iframe and top-level page
var frameWindowNames = []string{"outerWidth", "outerHeight", "screenX", "screenY", "screenLeft", "screenTop", "devicePixelRatio", "mozInnerScreenX"}

// hostState holds host data shared among Realms within the same agent
type hostState struct {
	registry  *hostRegistry
	profile   Profile
	loadImage func(string) bool
	pending   *sync.WaitGroup
	schedule  func(time.Duration, func())
	location  *time.Location
	zoneName  func(time.Time) string
	shape     goja.Value
	order     *globalOrderShape
	brands    goja.Value
	topAPI    goja.Value
}

// topPageAge is the page duration between official page load and BotGuard VM initialization
const topPageAge = 2500 * time.Millisecond

// hostJavaScript installs Function.prototype.toString, timers, and Firefox DOM into a Realm
const hostJavaScript = `
(input => {
  const markNative = input.markNative;
  const nativeSourceOf = input.nativeSource;
  const originalFunctionToString = Function.prototype.toString;
  const nativeFunction = (name, implementation) => {
    Object.defineProperty(implementation, 'name', { configurable: true, value: name });
    markNative(implementation, 'function ' + name + '() {\n    [native code]\n}');
    return implementation;
  };
  const functionToString = function toString() {
    'use strict';
    const marked = nativeSourceOf(this);
    if (marked !== undefined) return marked;
    const source = originalFunctionToString.call(this);
    if (!source.includes('[native code]')) return source;
    const name = typeof this.name === 'string' ? this.name : '';
    return 'function ' + name + '() {\n    [native code]\n}';
  };
  markNative(functionToString, 'function toString() {\n    [native code]\n}');
  Function.prototype.toString = functionToString;
  const timers = input.timers;
  globalThis.setTimeout = nativeFunction('setTimeout', function setTimeout(callback, delay, ...args) { return timers.setTimeout(callback, delay, ...args); });
  globalThis.setInterval = nativeFunction('setInterval', function setInterval(callback, delay, ...args) { return timers.setInterval(callback, delay, ...args); });
  globalThis.clearTimeout = nativeFunction('clearTimeout', function clearTimeout(id) { timers.clear(id); });
  globalThis.clearInterval = nativeFunction('clearInterval', function clearInterval(id) { timers.clear(id); });
  let idleCallbackCount = 0;
  globalThis.requestIdleCallback = nativeFunction('requestIdleCallback', function requestIdleCallback(callback, options = {}) {
    const delay = idleCallbackCount++ % 2 === 0 ? 0 : 4;
    return globalThis.setTimeout(() => callback({
      didTimeout: delay >= (options.timeout || Infinity),
      timeRemaining: () => delay === 0 ? 4 : 0,
    }), delay);
  });
  globalThis.cancelIdleCallback = nativeFunction('cancelIdleCallback', function cancelIdleCallback(id) { timers.clear(id); });
  return input.installDOM(input.shape, input.profile, {
    markNative,
    brands: input.brands,
    realm: input.realm,
    topRealm: input.topRealm,
    trust: input.trust,
    setTimeout: globalThis.setTimeout,
    clearTimeout: globalThis.clearTimeout,
    performanceNow: input.performanceNow,
    timeOrigin: input.timeOrigin,
    createIframeWindow: input.createIframeWindow,
    loadImage: input.loadImage,
    resolveInterface: input.resolveInterface,
    orderKeys: input.orderKeys,
  });
})
`

// installRealmHost installs host in designated Realm and returns its DOM interface
func (state *hostState) installRealmHost(vm *goja.Runtime, realm string, createIframe func() *goja.Object) (goja.Value, error) {
	if err := installBinaryEncoding(vm); err != nil {
		return nil, err
	}
	if err := installQueueMicrotask(vm); err != nil {
		return nil, err
	}
	installDOM, err := vm.RunScript(hostScriptName, domJavaScript)
	if err != nil {
		return nil, fmt.Errorf("compile WAA DOM host: %w", err)
	}
	hostFunction, err := vm.RunScript(hostScriptName, hostJavaScript)
	if err != nil {
		return nil, fmt.Errorf("compile WAA Realm host: %w", err)
	}
	install, ok := goja.AssertFunction(hostFunction)
	if !ok {
		return nil, errors.New("WAA Realm host is not a function")
	}
	if state.shape == nil {
		parse, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
		state.shape, err = parse(goja.Undefined(), vm.ToValue(firefoxShapeJSON))
		if err != nil {
			return nil, fmt.Errorf("parse Firefox shape table: %w", err)
		}
		state.order, err = parseGlobalOrderShape(firefoxShapeJSON)
		if err != nil {
			return nil, fmt.Errorf("parse Firefox global key order: %w", err)
		}
		weakMap, _ := goja.AssertConstructor(vm.Get("WeakMap"))
		state.brands, err = weakMap(nil)
		if err != nil {
			return nil, err
		}
	}
	timers := &realmTimers{vm: vm, schedule: state.schedule, active: make(map[int64]bool)}
	timerObject := vm.NewObject()
	_ = timerObject.Set("setTimeout", func(call goja.FunctionCall) goja.Value { return timers.start(call, false) })
	_ = timerObject.Set("setInterval", func(call goja.FunctionCall) goja.Value { return timers.start(call, true) })
	_ = timerObject.Set("clear", func(call goja.FunctionCall) goja.Value {
		delete(timers.active, call.Argument(0).ToInteger())
		return goja.Undefined()
	})
	input := vm.NewObject()
	_ = input.Set("markNative", func(call goja.FunctionCall) goja.Value {
		if object, ok := call.Argument(0).(*goja.Object); ok {
			state.registry.native[object] = call.Argument(1).String()
		}
		return call.Argument(0)
	})
	_ = input.Set("nativeSource", func(call goja.FunctionCall) goja.Value {
		if object, ok := call.Argument(0).(*goja.Object); ok {
			if source, exists := state.registry.native[object]; exists {
				return vm.ToValue(source)
			}
		}
		return goja.Undefined()
	})
	_ = input.Set("trust", func(call goja.FunctionCall) goja.Value {
		if object, ok := call.Argument(0).(*goja.Object); ok {
			state.registry.trusted[object] = call.Argument(1).String()
		}
		return goja.Undefined()
	})
	_ = input.Set("timers", timerObject)
	_ = input.Set("installDOM", installDOM)
	_ = input.Set("shape", state.shape)
	_ = input.Set("brands", state.brands)
	_ = input.Set("realm", realm)
	_ = input.Set("profile", state.realmProfile(realm))
	vm.SetTimeLocation(state.location)
	if state.zoneName != nil {
		vm.SetTimeZoneName(state.zoneName)
	}
	realmStarted := time.Now()
	if realm == "top" {
		realmStarted = realmStarted.Add(-topPageAge)
	}
	_ = input.Set("timeOrigin", float64(realmStarted.UnixMilli()))
	_ = input.Set("performanceNow", func() float64 {
		return math.Floor(float64(time.Since(realmStarted)) / float64(time.Millisecond))
	})
	if state.topAPI != nil {
		_ = input.Set("topRealm", state.topAPI)
	}
	if createIframe != nil {
		_ = input.Set("createIframeWindow", createIframe)
	}
	if state.loadImage != nil {
		_ = input.Set("loadImage", func(address string, callback goja.Callable) {
			state.pending.Add(1)
			go func() {
				defer state.pending.Done()
				loaded := state.loadImage(address)
				state.schedule(0, func() { _, _ = callback(goja.Undefined(), vm.ToValue(loaded)) })
			}()
		})
	}
	var order *firefoxGlobalOrder
	_ = input.Set("resolveInterface", func(name string) {
		if order != nil {
			order.Resolve(name)
		}
	})
	_ = input.Set("orderKeys", func(object *goja.Object, names []string) {
		object.OrderOwnKeys(names)
	})
	api, err := install(goja.Undefined(), input)
	if err != nil {
		return nil, fmt.Errorf("install WAA %s host: %w", realm, err)
	}
	if realm == "top" {
		order = newTopGlobalOrder(state.order)
	} else {
		order = newFrameGlobalOrder(state.order)
	}
	vm.SetGlobalObserver(order)
	return api, nil
}

// realmProfile generates location, window, navigator, screen, and document host values for designated Realm
func (state *hostState) realmProfile(realm string) map[string]any {
	profile := state.profile
	values := map[string]any{}
	if len(profile.Navigator) > 0 {
		values["navigator"] = profile.Navigator
	}
	if len(profile.Screen) > 0 {
		values["screen"] = profile.Screen
	}
	window := map[string]any{}
	if realm == "top" {
		for name, value := range profile.Window {
			window[name] = value
		}
		if len(profile.Document) > 0 {
			values["document"] = profile.Document
		}
	} else {
		for _, name := range frameWindowNames {
			if value, ok := profile.Window[name]; ok {
				window[name] = value
			}
		}
		if cookie, ok := profile.Document["cookie"]; ok {
			values["document"] = map[string]any{"cookie": cookie}
		}
	}
	result := map[string]any{"hasFocus": profile.HasFocus, "window": window, "values": values}
	if len(profile.LocalStorage) > 0 {
		localStorage := make(map[string]any, len(profile.LocalStorage))
		for key, value := range profile.LocalStorage {
			localStorage[key] = value
		}
		result["localStorage"] = localStorage
	}
	if realm == "top" && profile.PageURL != "" {
		result["location"] = locationProfile(profile.PageURL)
	}
	return result
}

// locationProfile parses page URL into Location fields
func locationProfile(raw string) map[string]any {
	protocol, rest, _ := strings.Cut(raw, "//")
	host, path := rest, "/"
	if index := strings.IndexByte(rest, '/'); index >= 0 {
		host, path = rest[:index], rest[index:]
	}
	search, hash := "", ""
	if index := strings.IndexByte(path, '#'); index >= 0 {
		path, hash = path[:index], path[index:]
	}
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path, search = path[:index], path[index:]
	}
	return map[string]any{
		"href": raw, "origin": protocol + "//" + host, "protocol": protocol, "host": host, "hostname": host,
		"port": "", "pathname": path, "search": search, "hash": hash,
	}
}
