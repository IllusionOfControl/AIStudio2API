package api

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// thoughtSignatureCapacity is the maximum number of tool call thought signatures retained.
const thoughtSignatureCapacity = 4096

// thoughtSignatureStore retains thought signatures for tool calls in this process's chat responses.
type thoughtSignatureStore struct {
	mu         sync.Mutex
	signatures map[string]string
	order      []string
}

func newThoughtSignatureStore() *thoughtSignatureStore {
	return &thoughtSignatureStore{signatures: make(map[string]string)}
}

// Remember stores a tool call with its signature from a response.
func (store *thoughtSignatureStore) Remember(calls []aistudio.FunctionCall) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, call := range calls {
		if call.ID == "" || call.ThoughtSignature == "" {
			continue
		}
		key := thoughtSignatureKey(call)
		if _, exists := store.signatures[key]; !exists {
			store.order = append(store.order, key)
		}
		store.signatures[key] = call.ThoughtSignature
	}
	for len(store.order) > thoughtSignatureCapacity {
		delete(store.signatures, store.order[0])
		store.order = store.order[1:]
	}
}

// Restore fills back in-process saved signatures for historic tool calls that clients returned without signatures.
func (store *thoughtSignatureStore) Restore(contents []aistudio.Content) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, content := range contents {
		for _, part := range content.Parts {
			call := part.FunctionCall
			if call == nil || call.ID == "" || call.ThoughtSignature != "" || part.ThoughtSignature != "" {
				continue
			}
			call.ThoughtSignature = store.signatures[thoughtSignatureKey(*call)]
		}
	}
}

// thoughtSignatureKey identifies a tool call by call ID, function name, and canonicalized arguments.
func thoughtSignatureKey(call aistudio.FunctionCall) string {
	arguments := []byte(call.Arguments)
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			arguments = canonical
		}
	}
	return call.ID + "\x00" + call.Name + "\x00" + string(arguments)
}
