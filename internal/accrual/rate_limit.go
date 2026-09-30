package accrual

import (
	"context"
	"sync"
	"time"
)

// rateGate is shared by every request in a worker, including client retries.
// Requests already admitted may finish, but each goroutine must check again
// before starting another attempt. No lock is held over network I/O.
type rateGate struct {
	mu    sync.Mutex
	until time.Time
	now   func() time.Time
}

func (g *rateGate) remaining() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return max(0, g.until.Sub(g.now()))
}

func (g *rateGate) block(delay time.Duration) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	if delay <= 0 {
		delay = orderRetryInterval
	}
	until := g.now().Add(delay)
	if until.After(g.until) {
		g.until = until
	}
	return g.until
}

func (g *rateGate) deadline() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.until
}

type rateGateKey struct{}

func requestGate(ctx context.Context, fallback *rateGate) *rateGate {
	if gate, ok := ctx.Value(rateGateKey{}).(*rateGate); ok {
		return gate
	}
	return fallback
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
