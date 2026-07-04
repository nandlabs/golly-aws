package sqs

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"oss.nandlabs.io/golly/messaging"
)

// TestProvider_ImplementsListenerRemover is the load-bearing test: as long
// as Provider satisfies messaging.ListenerRemover, golly's manager-level
// dispatcher will route RemoveListeners / RemoveNamedListener calls
// through us instead of returning ErrListenerRemovalUnsupported.
func TestProvider_ImplementsListenerRemover(t *testing.T) {
	var _ messaging.ListenerRemover = (*Provider)(nil)
}

// TestProvider_RemoveListeners_IdempotentOnUnknownURL ensures the no-op
// path is safe — callers that haven't registered a listener for a URL
// can still call RemoveListeners without surprise.
func TestProvider_RemoveListeners_IdempotentOnUnknownURL(t *testing.T) {
	p := &Provider{}
	u, _ := url.Parse("sqs://no-such-queue")
	if err := p.RemoveListeners(u); err != nil {
		t.Errorf("RemoveListeners on unknown URL should return nil; got %v", err)
	}
	if err := p.RemoveNamedListener(u, "anything"); err != nil {
		t.Errorf("RemoveNamedListener on unknown URL should return nil; got %v", err)
	}
}

// TestProvider_RemoveListeners_CancelsTrackedEntries verifies that
// registered listener cancel fns are invoked when their URL is removed.
// We skip the SDK call by injecting entries directly into p.listeners.
func TestProvider_RemoveListeners_CancelsTrackedEntries(t *testing.T) {
	p := &Provider{listeners: map[string][]sqsListenerEntry{}}
	u, _ := url.Parse("sqs://q1")

	var cancels atomic.Int32
	mk := func(name string) sqsListenerEntry {
		_, cancel := context.WithCancel(context.Background())
		return sqsListenerEntry{name: name, cancel: func() {
			cancel()
			cancels.Add(1)
		}}
	}
	p.listeners[u.Host] = []sqsListenerEntry{mk(""), mk("worker"), mk("worker")}

	if err := p.RemoveListeners(u); err != nil {
		t.Fatalf("RemoveListeners: %v", err)
	}
	if cancels.Load() != 3 {
		t.Errorf("expected 3 cancel fns invoked; got %d", cancels.Load())
	}
	if _, ok := p.listeners[u.Host]; ok {
		t.Errorf("expected URL entry to be deleted from map")
	}
}

func TestProvider_RemoveNamedListener_KeepsOthers(t *testing.T) {
	p := &Provider{listeners: map[string][]sqsListenerEntry{}}
	u, _ := url.Parse("sqs://q2")

	var unnamed, kept, dropped atomic.Int32
	mk := func(counter *atomic.Int32, name string) sqsListenerEntry {
		return sqsListenerEntry{name: name, cancel: func() { counter.Add(1) }}
	}
	p.listeners[u.Host] = []sqsListenerEntry{
		mk(&unnamed, ""),
		mk(&kept, "alpha"),
		mk(&dropped, "beta"),
		mk(&dropped, "beta"),
	}

	if err := p.RemoveNamedListener(u, "beta"); err != nil {
		t.Fatalf("RemoveNamedListener: %v", err)
	}
	if dropped.Load() != 2 {
		t.Errorf("expected 2 'beta' listeners cancelled; got %d", dropped.Load())
	}
	if unnamed.Load() != 0 || kept.Load() != 0 {
		t.Errorf("other listeners should not have been cancelled; unnamed=%d kept=%d",
			unnamed.Load(), kept.Load())
	}
	remaining := p.listeners[u.Host]
	if len(remaining) != 2 {
		t.Fatalf("expected 2 listeners remaining; got %d", len(remaining))
	}
}

func TestProvider_RemoveNamedListener_LastEntryDeletesURL(t *testing.T) {
	p := &Provider{listeners: map[string][]sqsListenerEntry{}}
	u, _ := url.Parse("sqs://q3")
	p.listeners[u.Host] = []sqsListenerEntry{{name: "solo", cancel: func() {}}}
	if err := p.RemoveNamedListener(u, "solo"); err != nil {
		t.Fatalf("RemoveNamedListener: %v", err)
	}
	if _, ok := p.listeners[u.Host]; ok {
		t.Errorf("URL entry should be deleted when last listener is removed")
	}
}

// TestProvider_Close_CancelsEverything ensures the refactored Close still
// terminates all registered listeners.
func TestProvider_Close_CancelsEverything(t *testing.T) {
	p := &Provider{listeners: map[string][]sqsListenerEntry{}}
	var cancels atomic.Int32
	cancel := func() { cancels.Add(1) }
	p.listeners["q4"] = []sqsListenerEntry{{cancel: cancel}, {cancel: cancel}}
	p.listeners["q5"] = []sqsListenerEntry{{cancel: cancel}}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cancels.Load() != 3 {
		t.Errorf("expected 3 cancel calls on Close; got %d", cancels.Load())
	}
	if p.listeners != nil {
		t.Errorf("Close should null out p.listeners")
	}
}

// TestProvider_RemoveListeners_ConcurrentSafe runs concurrent RemoveListeners
// and direct map insertions to catch obvious race regressions under -race.
func TestProvider_RemoveListeners_ConcurrentSafe(t *testing.T) {
	p := &Provider{listeners: map[string][]sqsListenerEntry{}}
	u, _ := url.Parse("sqs://race")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			p.mu.Lock()
			p.listeners[u.Host] = append(p.listeners[u.Host], sqsListenerEntry{cancel: func() {}})
			p.mu.Unlock()
		}()
		go func() {
			defer wg.Done()
			_ = p.RemoveListeners(u)
		}()
	}
	wg.Wait()
	// No assertion needed beyond -race not firing.
}
