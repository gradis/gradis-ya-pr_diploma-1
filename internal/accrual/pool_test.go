package accrual

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gradis/ya-pr_diploma-1/internal/model"
)

type checkerFunc func(context.Context, string) (Result, error)

func (f checkerFunc) Check(ctx context.Context, number string) (Result, error) { return f(ctx, number) }

func TestWorkerPoolBoundsConcurrency(t *testing.T) {
	repo := &workerRepositoryStub{}
	for i := range workerBatchSize {
		repo.claimed = append(repo.claimed, model.Order{Number: fmt.Sprint(i), Status: model.OrderStatusNew})
	}
	started := make(chan struct{}, workerBatchSize)
	release := make(chan struct{})
	var active, peak atomic.Int32
	var mu sync.Mutex
	calls := make(map[string]int)
	worker := NewWorker(repo, checkerFunc(func(ctx context.Context, number string) (Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		mu.Lock()
		calls[number]++
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		return Result{Status: StatusProcessed}, nil
	}), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); worker.processBatch(ctx) }()
	for range workerCount {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("pool did not run concurrently")
		}
	}
	if active.Load() != workerCount {
		t.Fatalf("active=%d", active.Load())
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("pool did not finish")
	}
	if peak.Load() != workerCount || len(repo.updates) != workerBatchSize {
		t.Fatalf("peak=%d updates=%d", peak.Load(), len(repo.updates))
	}
	for _, order := range repo.claimed {
		if calls[order.Number] != 1 {
			t.Fatalf("order %s processed %d times", order.Number, calls[order.Number])
		}
	}
}

func TestWorkerRateLimitSurvivesDatabaseFailure(t *testing.T) {
	fixed := time.Now()
	repo := &workerRepositoryStub{updateError: errors.New("database unavailable")}
	var calls atomic.Int32
	worker := NewWorker(repo, checkerFunc(func(context.Context, string) (Result, error) {
		calls.Add(1)
		return Result{}, &RateLimitError{RetryAfter: time.Minute}
	}), nil)
	worker.now = func() time.Time { return fixed }
	for _, number := range []string{"1", "2", "3"} {
		if err := worker.processOrder(context.Background(), model.Order{Number: number, Status: model.OrderStatusNew}); err == nil {
			t.Fatal("write error lost")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("requests continued during pause: %d", calls.Load())
	}
	if !worker.gate.deadline().Equal(fixed.Add(time.Minute)) {
		t.Fatalf("deadline=%v", worker.gate.deadline())
	}
	fixed = fixed.Add(time.Minute)
	_ = worker.processOrder(context.Background(), model.Order{Number: "4", Status: model.OrderStatusNew})
	if calls.Load() != 2 {
		t.Fatal("worker did not resume after cooldown")
	}
}

func TestWorkerCancelsInFlightChecks(t *testing.T) {
	repo := &workerRepositoryStub{}
	for i := range workerBatchSize {
		repo.claimed = append(repo.claimed, model.Order{Number: fmt.Sprint(i)})
	}
	started := make(chan struct{}, workerCount)
	var active atomic.Int32
	worker := NewWorker(repo, checkerFunc(func(ctx context.Context, _ string) (Result, error) {
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return Result{}, ctx.Err()
	}), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	for range workerCount {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("checks did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled pool is stuck")
	}
	if active.Load() != 0 {
		t.Fatal("checks survived shutdown")
	}
}
