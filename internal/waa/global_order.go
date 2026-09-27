package waa

import (
	"encoding/json"
	"strconv"
)

// numberGlobals are global names defined together in SpiderMonkey order during Number class resolution
var numberGlobals = []string{"isNaN", "isFinite", "parseInt", "parseFloat", "NaN", "Infinity", "Number"}

// stringGlobals are global names defined together in SpiderMonkey order during String class resolution
var stringGlobals = []string{"escape", "unescape", "decodeURI", "encodeURI", "decodeURIComponent", "encodeURIComponent", "String"}

// errorSubclasses are built-in error classes that require Error to be resolved first
var errorSubclasses = map[string]bool{"InternalError": true, "AggregateError": true, "EvalError": true, "RangeError": true, "ReferenceError": true, "SuppressedError": true, "SyntaxError": true, "TypeError": true, "URIError": true, "WebAssembly": true}

// legacyFactories are legacy factory functions defined immediately following interface object resolution
var legacyFactories = map[string]string{"HTMLImageElement": "Image", "HTMLAudioElement": "Audio", "HTMLOptionElement": "Option"}

// globalOrderShape represents the fields in shape table that determine global key order
type globalOrderShape struct {
	Interfaces []struct {
		N  string `json:"n"`
		CN string `json:"cn"`
		P  string `json:"p"`
	} `json:"interfaces"`
	Frame struct {
		FreshKeys []string `json:"freshKeys"`
	} `json:"frame"`
	Top struct {
		Global []struct {
			N string `json:"n"`
		} `json:"global"`
	} `json:"top"`
}

// firefoxGlobalOrder maintains own-key order of a Realm global object according to Gecko and SpiderMonkey lazy global resolution rules
type firefoxGlobalOrder struct {
	lazy      []string
	isLazy    map[string]bool
	resolved  map[string]bool
	native    []string
	inNative  map[string]bool
	parents   map[string]string
	factories map[string]string
}

// parseGlobalOrderShape reads global key order fields from shape table
func parseGlobalOrderShape(source string) (*globalOrderShape, error) {
	var shape globalOrderShape
	if err := json.Unmarshal([]byte(source), &shape); err != nil {
		return nil, err
	}
	return &shape, nil
}

// newFrameGlobalOrder initializes lazy name table and defined property order using initial enumeration of freshly created same-origin iframe
func newFrameGlobalOrder(shape *globalOrderShape) *firefoxGlobalOrder {
	order := newGlobalOrder(shape)
	split := len(shape.Frame.FreshKeys)
	for index, name := range shape.Frame.FreshKeys {
		if name == "Function" {
			split = index
			break
		}
	}
	for _, name := range shape.Frame.FreshKeys[:split] {
		order.lazy = append(order.lazy, name)
		order.isLazy[name] = true
	}
	for _, name := range shape.Frame.FreshKeys[split:] {
		order.appendNative(name)
	}
	return order
}

// newTopGlobalOrder initializes resolved order using global key order of top-level page at collection time
func newTopGlobalOrder(shape *globalOrderShape) *firefoxGlobalOrder {
	order := newGlobalOrder(shape)
	order.lazy = []string{"undefined"}
	order.isLazy["undefined"] = true
	order.resolved["undefined"] = true
	for _, entry := range shape.Top.Global {
		order.appendNative(entry.N)
	}
	return order
}

func newGlobalOrder(shape *globalOrderShape) *firefoxGlobalOrder {
	order := &firefoxGlobalOrder{isLazy: map[string]bool{}, resolved: map[string]bool{}, inNative: map[string]bool{}, parents: map[string]string{}, factories: legacyFactories}
	for _, info := range shape.Interfaces {
		if info.N == info.CN && info.P != "" {
			order.parents[info.N] = info.P
		}
	}
	return order
}

func (order *firefoxGlobalOrder) appendNative(name string) {
	if !order.inNative[name] {
		order.inNative[name] = true
		order.native = append(order.native, name)
	}
}

// group returns names defined together in order when resolving name
func (order *firefoxGlobalOrder) group(name string) []string {
	for factory, target := range map[string]string{"Image": "HTMLImageElement", "Audio": "HTMLAudioElement", "Option": "HTMLOptionElement"} {
		if name == factory {
			name = target
		}
	}
	for _, list := range [][]string{numberGlobals, stringGlobals} {
		for _, member := range list {
			if member == name {
				return list
			}
		}
	}
	if errorSubclasses[name] {
		return []string{"Error", name}
	}
	chain := []string{name}
	for parent := order.parents[name]; parent != ""; parent = order.parents[parent] {
		chain = append([]string{parent}, chain...)
	}
	if factory := order.factories[name]; factory != "" {
		chain = append(chain, factory)
	}
	return chain
}

// Resolve defines a lazy global when first accessed by name in script or internally by the engine
func (order *firefoxGlobalOrder) Resolve(name string) {
	if !order.isLazy[name] || order.resolved[name] {
		return
	}
	for _, member := range order.group(name) {
		if order.isLazy[member] && !order.resolved[member] {
			order.resolved[member] = true
			order.appendNative(member)
		}
	}
}

// Define records newly defined global property from script
func (order *firefoxGlobalOrder) Define(name string) {
	order.appendNative(name)
}

// Delete removes deleted global property
func (order *firefoxGlobalOrder) Delete(name string) {
	if !order.inNative[name] {
		return
	}
	delete(order.inNative, name)
	for index, current := range order.native {
		if current == name {
			order.native = append(order.native[:index:index], order.native[index+1:]...)
			break
		}
	}
}

// OrderKeys orders own string keys by enumerated hook names, defined properties, and remaining properties
func (order *firefoxGlobalOrder) OrderKeys(keys []string) []string {
	present := make(map[string]bool, len(keys))
	ordered := make([]string, 0, len(keys))
	added := make(map[string]bool, len(keys))
	add := func(name string) {
		if present[name] && !added[name] {
			added[name] = true
			ordered = append(ordered, name)
		}
	}
	for _, key := range keys {
		present[key] = true
		if _, err := strconv.ParseUint(key, 10, 32); err == nil {
			add(key)
		}
	}
	add("undefined")
	if order.isLazy["globalThis"] && !order.resolved["globalThis"] && present["globalThis"] {
		add("globalThis")
		order.resolved["globalThis"] = true
		order.appendNative("globalThis")
	}
	for _, name := range order.lazy {
		if !order.resolved[name] {
			add(name)
		}
	}
	for _, name := range order.native {
		add(name)
	}
	for _, key := range keys {
		add(key)
	}
	return ordered
}
