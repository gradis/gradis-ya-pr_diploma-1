package accrual

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"go.uber.org/zap"
)

type workerRepositoryStub struct {
	status       model.OrderStatus
	accrualCents *int64
	nextCheckAt  time.Time
	claimed      []model.Order
	updates      []workerUpdate
	claimError   error
	updateError  error
}

type workerUpdate struct {
	number      string
	status      model.OrderStatus
	nextCheckAt time.Time
}

func (r *workerRepositoryStub) ClaimDueOrders(context.Context, int, time.Duration) ([]model.Order, error) {
	return r.claimed, r.claimError
}

func (r *workerRepositoryStub) UpdateOrderStatus(
	_ context.Context,
	number string,
	status model.OrderStatus,
	accrualCents *int64,
	nextCheckAt time.Time,
) error {
	r.status = status
	r.accrualCents = accrualCents
	r.nextCheckAt = nextCheckAt
	r.updates = append(r.updates, workerUpdate{number: number, status: status, nextCheckAt: nextCheckAt})
	return r.updateError
}

func TestWorkerReschedulesClaimedOrdersAfterRateLimit(t *testing.T) {
	fixedNow := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	repo := &workerRepositoryStub{claimed: []model.Order{
		{Number: "12345678903", Status: model.OrderStatusNew},
		{Number: "2377225624", Status: model.OrderStatusProcessing},
	}}
	worker := NewWorker(repo, checkerStub{err: &RateLimitError{RetryAfter: 5 * time.Second}}, zap.NewNop())
	worker.now = func() time.Time { return fixedNow }

	blockedUntil := worker.processBatch(context.Background())
	if !blockedUntil.Equal(fixedNow.Add(5 * time.Second)) {
		t.Fatalf("blocked until %s", blockedUntil)
	}
	if len(repo.updates) != 2 {
		t.Fatalf("updates = %d, want 2", len(repo.updates))
	}
	for _, update := range repo.updates {
		if !update.nextCheckAt.Equal(blockedUntil) {
			t.Fatalf("order %s next check = %s", update.number, update.nextCheckAt)
		}
	}
}

type checkerStub struct {
	result Result
	err    error
}

func (c checkerStub) Check(context.Context, string) (Result, error) {
	return c.result, c.err
}

func TestWorkerProcessesFinalAccrual(t *testing.T) {
	accrualCents := int64(50050)
	repo := &workerRepositoryStub{}
	worker := NewWorker(repo, checkerStub{result: Result{
		Status:       StatusProcessed,
		AccrualCents: &accrualCents,
	}}, zap.NewNop())
	fixedNow := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return fixedNow }

	retry, err := worker.processOrder(context.Background(), model.Order{
		Number: "12345678903",
		Status: model.OrderStatusNew,
	})
	if err != nil || retry != 0 {
		t.Fatalf("processOrder() retry = %s, error = %v", retry, err)
	}
	if repo.status != model.OrderStatusProcessed {
		t.Fatalf("status = %q", repo.status)
	}
	if repo.accrualCents == nil || *repo.accrualCents != accrualCents {
		t.Fatalf("accrual = %#v", repo.accrualCents)
	}
}

func TestWorkerHonorsRateLimit(t *testing.T) {
	repo := &workerRepositoryStub{}
	worker := NewWorker(repo, checkerStub{err: &RateLimitError{RetryAfter: 5 * time.Second}}, zap.NewNop())
	fixedNow := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return fixedNow }

	retry, err := worker.processOrder(context.Background(), model.Order{
		Number: "12345678903",
		Status: model.OrderStatusProcessing,
	})
	if err != nil || retry != 5*time.Second {
		t.Fatalf("processOrder() retry = %s, error = %v", retry, err)
	}
	if repo.status != model.OrderStatusProcessing || !repo.nextCheckAt.Equal(fixedNow.Add(5*time.Second)) {
		t.Fatalf("unexpected reschedule: status=%q next=%s", repo.status, repo.nextCheckAt)
	}
}

func TestWorkerRetriesUnknownError(t *testing.T) {
	repo := &workerRepositoryStub{}
	worker := NewWorker(repo, checkerStub{err: errors.New("network")}, zap.NewNop())
	fixedNow := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return fixedNow }

	_, err := worker.processOrder(context.Background(), model.Order{
		Number: "12345678903",
		Status: model.OrderStatusNew,
	})
	if err == nil || !repo.nextCheckAt.Equal(fixedNow.Add(time.Second)) {
		t.Fatalf("error = %v, next = %s", err, repo.nextCheckAt)
	}
}

func TestWorkerMapsAccrualStatuses(t *testing.T) {
	fixedNow := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	accrualCents := int64(12345)
	tests := []struct {
		name        string
		orderStatus model.OrderStatus
		result      Result
		wantStatus  model.OrderStatus
		wantAccrual *int64
		wantNext    time.Time
	}{
		{
			name:        "registered new order",
			orderStatus: model.OrderStatusNew,
			result:      Result{Status: StatusRegistered, AccrualCents: &accrualCents},
			wantStatus:  model.OrderStatusNew,
			wantNext:    fixedNow.Add(orderRetryInterval),
		},
		{
			name:        "registered does not regress processing order",
			orderStatus: model.OrderStatusProcessing,
			result:      Result{Status: StatusRegistered},
			wantStatus:  model.OrderStatusProcessing,
			wantNext:    fixedNow.Add(orderRetryInterval),
		},
		{
			name:        "processing",
			orderStatus: model.OrderStatusNew,
			result:      Result{Status: StatusProcessing},
			wantStatus:  model.OrderStatusProcessing,
			wantNext:    fixedNow.Add(orderRetryInterval),
		},
		{
			name:        "invalid",
			orderStatus: model.OrderStatusProcessing,
			result:      Result{Status: StatusInvalid, AccrualCents: &accrualCents},
			wantStatus:  model.OrderStatusInvalid,
			wantNext:    fixedNow,
		},
		{
			name:        "processed without accrual",
			orderStatus: model.OrderStatusProcessing,
			result:      Result{Status: StatusProcessed},
			wantStatus:  model.OrderStatusProcessed,
			wantNext:    fixedNow,
		},
		{
			name:        "processed with accrual",
			orderStatus: model.OrderStatusProcessing,
			result:      Result{Status: StatusProcessed, AccrualCents: &accrualCents},
			wantStatus:  model.OrderStatusProcessed,
			wantAccrual: &accrualCents,
			wantNext:    fixedNow,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &workerRepositoryStub{}
			worker := NewWorker(repo, checkerStub{result: test.result}, nil)
			worker.now = func() time.Time { return fixedNow }

			retry, err := worker.processOrder(context.Background(), model.Order{
				Number: "12345678903",
				Status: test.orderStatus,
			})
			if err != nil || retry != 0 {
				t.Fatalf("processOrder() = %s, %v", retry, err)
			}
			if repo.status != test.wantStatus || !repo.nextCheckAt.Equal(test.wantNext) {
				t.Fatalf("status = %q, next = %s; want %q, %s", repo.status, repo.nextCheckAt, test.wantStatus, test.wantNext)
			}
			if test.wantAccrual == nil && repo.accrualCents != nil {
				t.Fatalf("unexpected accrual: %d", *repo.accrualCents)
			}
			if test.wantAccrual != nil && (repo.accrualCents == nil || *repo.accrualCents != *test.wantAccrual) {
				t.Fatalf("accrual = %#v, want %d", repo.accrualCents, *test.wantAccrual)
			}
		})
	}
}

func TestWorkerPropagatesRepositoryErrors(t *testing.T) {
	repositoryError := errors.New("database")
	worker := NewWorker(
		&workerRepositoryStub{updateError: repositoryError},
		checkerStub{result: Result{Status: StatusProcessed}},
		zap.NewNop(),
	)
	if _, err := worker.processOrder(context.Background(), model.Order{Number: "12345678903"}); err == nil {
		t.Fatal("order update error was lost")
	}

	worker = NewWorker(
		&workerRepositoryStub{claimError: repositoryError},
		checkerStub{},
		zap.NewNop(),
	)
	if blockedUntil := worker.processBatch(context.Background()); !blockedUntil.IsZero() {
		t.Fatalf("blocked until = %s", blockedUntil)
	}
}

func TestWorkerRunStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		NewWorker(&workerRepositoryStub{}, checkerStub{}, zap.NewNop()).Run(ctx)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}
