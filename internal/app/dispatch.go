package app

import (
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// dispatchQueue groups waiting requests by account selection criteria and wakes them first-come, first-served.
type dispatchQueue struct {
	mu     sync.Mutex
	queues map[string][]*dispatchWaiter
}

// dispatchWaiter represents a request waiting for an account slot.
type dispatchWaiter struct {
	key  string
	wake chan struct{}
}

func newDispatchQueue() *dispatchQueue {
	return &dispatchQueue{queues: make(map[string][]*dispatchWaiter)}
}

// dispatchKey returns the queue key shared by interchangeable requests.
func dispatchKey(selection aistudio.AccountSelection) string {
	allowed := slices.Clone(selection.AllowedAccountIDs)
	slices.Sort(allowed)
	return strings.Join([]string{
		selection.ModelID, selection.Method, selection.Capability, selection.AccountID, selection.ResourceID,
		strings.Join(allowed, ","), strconv.FormatBool(selection.PlaygroundOnly),
	}, "\x00")
}

// join enqueues the request at the end of the matching queue and wakes the head to recheck free slots if the queue was non-empty.
func (queue *dispatchQueue) join(key string) *dispatchWaiter {
	waiter := &dispatchWaiter{key: key, wake: make(chan struct{}, 1)}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.queues[key] = append(queue.queues[key], waiter)
	queue.queues[key][0].signal()
	return waiter
}

// leave removes the request and wakes the next request if this was at the head.
func (queue *dispatchQueue) leave(waiter *dispatchWaiter) {
	if waiter == nil {
		return
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	waiters := queue.queues[waiter.key]
	index := slices.Index(waiters, waiter)
	if index < 0 {
		return
	}
	waiters = slices.Delete(waiters, index, index+1)
	if len(waiters) == 0 {
		delete(queue.queues, waiter.key)
		return
	}
	queue.queues[waiter.key] = waiters
	if index == 0 {
		waiters[0].signal()
	}
}

// wakeHeads wakes the head of each queue when account or worker state changes.
func (queue *dispatchQueue) wakeHeads() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, waiters := range queue.queues {
		waiters[0].signal()
	}
}

func (waiter *dispatchWaiter) signal() {
	select {
	case waiter.wake <- struct{}{}:
	default:
	}
}
