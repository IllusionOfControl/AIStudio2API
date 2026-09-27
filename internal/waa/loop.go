package waa

import (
	"sync"
	"time"
)

// eventLoop executes VM tasks and timer callbacks serially in arrival order in a single goroutine
type eventLoop struct {
	mu     sync.Mutex
	queue  []func()
	wake   chan struct{}
	done   chan struct{}
	closed bool
	timers map[*time.Timer]struct{}
}

func newEventLoop() *eventLoop {
	loop := &eventLoop{wake: make(chan struct{}, 1), done: make(chan struct{}), timers: make(map[*time.Timer]struct{})}
	go loop.run()
	return loop
}

func (loop *eventLoop) run() {
	for {
		loop.mu.Lock()
		if loop.closed {
			loop.mu.Unlock()
			return
		}
		if len(loop.queue) == 0 {
			loop.mu.Unlock()
			select {
			case <-loop.wake:
			case <-loop.done:
				return
			}
			continue
		}
		job := loop.queue[0]
		loop.queue[0] = nil
		loop.queue = loop.queue[1:]
		loop.mu.Unlock()
		job()
	}
}

// post appends a task to the event loop queue, returning false if loop is closed
func (loop *eventLoop) post(job func()) bool {
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		return false
	}
	loop.queue = append(loop.queue, job)
	loop.mu.Unlock()
	select {
	case loop.wake <- struct{}{}:
	default:
	}
	return true
}

// schedule appends a task to the event loop queue after a delay
func (loop *eventLoop) schedule(delay time.Duration, job func()) {
	if delay <= 0 {
		loop.post(job)
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		loop.mu.Lock()
		delete(loop.timers, timer)
		loop.mu.Unlock()
		loop.post(job)
	})
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		timer.Stop()
		return
	}
	loop.timers[timer] = struct{}{}
	loop.mu.Unlock()
}

func (loop *eventLoop) close() {
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		return
	}
	loop.closed = true
	for timer := range loop.timers {
		timer.Stop()
	}
	clear(loop.timers)
	loop.queue = nil
	loop.mu.Unlock()
	close(loop.done)
}
