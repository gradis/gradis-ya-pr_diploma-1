package accrual

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"go.uber.org/zap"
)

const (
	workerPollInterval = 500 * time.Millisecond
	orderRetryInterval = time.Second
	workerLeaseTime    = time.Minute
	workerBatchSize    = 10
)

type orderRepository interface {
	ClaimDueOrders(ctx context.Context, limit int, lease time.Duration) ([]model.Order, error)
	UpdateOrderStatus(
		ctx context.Context,
		number string,
		status model.OrderStatus,
		accrualCents *int64,
		nextCheckAt time.Time,
	) error
}

type checker interface {
	Check(ctx context.Context, number string) (Result, error)
}

type Worker struct {
	repository orderRepository
	client     checker
	logg       *zap.Logger
	now        func() time.Time
}

func NewWorker(repository orderRepository, client checker, logg *zap.Logger) *Worker {
	if logg == nil {
		logg = zap.NewNop()
	}

	return &Worker{
		repository: repository,
		client:     client,
		logg:       logg,
		now:        time.Now,
	}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(workerPollInterval)
	defer ticker.Stop()

	var blockedUntil time.Time
	for {
		if !w.now().Before(blockedUntil) {
			blockedUntil = w.processBatch(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) time.Time {
	orders, err := w.repository.ClaimDueOrders(ctx, workerBatchSize, workerLeaseTime)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.logg.Error("failed to load orders for accrual", zap.Error(err))
		}
		return time.Time{}
	}

	for index, order := range orders {
		retryAfter, err := w.processOrder(ctx, order)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				w.logg.Error(
					"failed to process order accrual",
					zap.String("order", order.Number),
					zap.Error(err),
				)
			}
			continue
		}

		if retryAfter > 0 {
			blockedUntil := w.now().Add(retryAfter)
			for _, unprocessedOrder := range orders[index+1:] {
				if err := w.repository.UpdateOrderStatus(
					ctx,
					unprocessedOrder.Number,
					unprocessedOrder.Status,
					nil,
					blockedUntil,
				); err != nil && !errors.Is(err, context.Canceled) {
					w.logg.Error(
						"failed to reschedule order after accrual rate limit",
						zap.String("order", unprocessedOrder.Number),
						zap.Error(err),
					)
				}
			}

			return blockedUntil
		}
	}

	return time.Time{}
}

func (w *Worker) processOrder(ctx context.Context, order model.Order) (time.Duration, error) {
	result, err := w.client.Check(ctx, order.Number)
	if err != nil {
		nextCheckAt := w.now().Add(orderRetryInterval)

		var rateLimitError *RateLimitError
		if errors.As(err, &rateLimitError) {
			retryAfter := rateLimitError.RetryAfter
			if retryAfter <= 0 {
				retryAfter = orderRetryInterval
			}
			nextCheckAt = w.now().Add(retryAfter)

			if updateErr := w.repository.UpdateOrderStatus(
				ctx,
				order.Number,
				order.Status,
				nil,
				nextCheckAt,
			); updateErr != nil {
				return 0, fmt.Errorf("reschedule rate-limited order: %w", updateErr)
			}

			return retryAfter, nil
		}

		if errors.Is(err, ErrOrderNotRegistered) {
			return 0, w.repository.UpdateOrderStatus(
				ctx,
				order.Number,
				order.Status,
				nil,
				nextCheckAt,
			)
		}

		if updateErr := w.repository.UpdateOrderStatus(
			ctx,
			order.Number,
			order.Status,
			nil,
			nextCheckAt,
		); updateErr != nil {
			return 0, fmt.Errorf("reschedule order after accrual error: %w", updateErr)
		}

		return 0, err
	}

	nextCheckAt := w.now()
	status := order.Status
	accrualCents := result.AccrualCents

	switch result.Status {
	case StatusRegistered:
		nextCheckAt = w.now().Add(orderRetryInterval)
		accrualCents = nil
	case StatusProcessing:
		status = model.OrderStatusProcessing
		nextCheckAt = w.now().Add(orderRetryInterval)
		accrualCents = nil
	case StatusInvalid:
		status = model.OrderStatusInvalid
		accrualCents = nil
	case StatusProcessed:
		status = model.OrderStatusProcessed
	default:
		return 0, fmt.Errorf("unsupported accrual status %q", result.Status)
	}

	if err := w.repository.UpdateOrderStatus(
		ctx,
		order.Number,
		status,
		accrualCents,
		nextCheckAt,
	); err != nil {
		return 0, fmt.Errorf("update order after accrual response: %w", err)
	}

	return 0, nil
}
