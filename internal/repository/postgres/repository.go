package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"github.com/gradis/ya-pr_diploma-1/internal/repository"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type connection interface {
	Ping(context.Context) error
	Begin(context.Context) (pgx.Tx, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Repository struct {
	db connection
}

func New(db connection) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.db.Ping(ctx)
}

func (r *Repository) CreateUser(ctx context.Context, login, passwordHash string) (int64, error) {
	const query = `
INSERT INTO users (login, password_hash)
VALUES ($1, $2)
RETURNING id;`

	var userID int64
	if err := r.db.QueryRow(ctx, query, login, passwordHash).Scan(&userID); err != nil {
		if uniqueViolation(err) {
			return 0, repository.ErrLoginExists
		}
		return 0, fmt.Errorf("insert user: %w", err)
	}

	return userID, nil
}

func (r *Repository) UserByLogin(ctx context.Context, login string) (model.User, error) {
	const query = `
SELECT id, login, password_hash
FROM users
WHERE login = $1;`

	var user model.User
	if err := r.db.QueryRow(ctx, query, login).Scan(&user.ID, &user.Login, &user.PasswordHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.User{}, repository.ErrUserNotFound
		}
		return model.User{}, fmt.Errorf("select user by login: %w", err)
	}

	return user, nil
}

func (r *Repository) AddOrder(ctx context.Context, userID int64, number string) (repository.OrderOwner, error) {
	const insertQuery = `
INSERT INTO orders (number, user_id)
VALUES ($1, $2)
ON CONFLICT (number) DO NOTHING
RETURNING user_id;`

	var ownerID int64
	err := r.db.QueryRow(ctx, insertQuery, number, userID).Scan(&ownerID)
	if err == nil {
		return repository.OrderCreated, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("insert order: %w", err)
	}

	const ownerQuery = `SELECT user_id FROM orders WHERE number = $1;`
	if err := r.db.QueryRow(ctx, ownerQuery, number).Scan(&ownerID); err != nil {
		return 0, fmt.Errorf("select existing order owner: %w", err)
	}

	if ownerID == userID {
		return repository.OrderOwnedByUser, nil
	}

	return repository.OrderOwnedByAnotherUser, nil
}

func (r *Repository) OrdersByUser(ctx context.Context, userID int64) ([]model.Order, error) {
	const query = `
SELECT number, user_id, status, accrual_cents, uploaded_at, next_check_at
FROM orders
WHERE user_id = $1
	ORDER BY uploaded_at DESC, number DESC;`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("select user orders: %w", err)
	}
	defer rows.Close()

	orders := make([]model.Order, 0)
	for rows.Next() {
		var order model.Order
		if err := rows.Scan(
			&order.Number,
			&order.UserID,
			&order.Status,
			&order.AccrualCents,
			&order.UploadedAt,
			&order.NextCheckAt,
		); err != nil {
			return nil, fmt.Errorf("scan user order: %w", err)
		}
		orders = append(orders, order)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user orders: %w", err)
	}

	return orders, nil
}

func (r *Repository) BalanceByUser(ctx context.Context, userID int64) (model.Balance, error) {
	const query = `
SELECT balance_cents, withdrawn_cents
FROM users
WHERE id = $1;`

	var balance model.Balance
	if err := r.db.QueryRow(ctx, query, userID).Scan(
		&balance.CurrentCents,
		&balance.WithdrawnCents,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Balance{}, repository.ErrUserNotFound
		}
		return model.Balance{}, fmt.Errorf("select user balance: %w", err)
	}

	return balance, nil
}

func (r *Repository) Withdraw(
	ctx context.Context,
	userID int64,
	orderNumber string,
	sumCents int64,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin withdrawal transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockQuery = `SELECT balance_cents FROM users WHERE id = $1 FOR UPDATE;`
	var currentBalance int64
	if err := tx.QueryRow(ctx, lockQuery, userID).Scan(&currentBalance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ErrUserNotFound
		}
		return fmt.Errorf("lock user balance: %w", err)
	}

	if currentBalance < sumCents {
		return repository.ErrInsufficientFund
	}

	const insertQuery = `
INSERT INTO withdrawals (user_id, order_number, sum_cents)
VALUES ($1, $2, $3);`
	if _, err := tx.Exec(ctx, insertQuery, userID, orderNumber, sumCents); err != nil {
		if uniqueViolation(err) {
			return repository.ErrWithdrawalExists
		}
		return fmt.Errorf("insert withdrawal: %w", err)
	}

	const updateQuery = `
UPDATE users
SET balance_cents = balance_cents - $2,
    withdrawn_cents = withdrawn_cents + $2
WHERE id = $1;`
	if _, err := tx.Exec(ctx, updateQuery, userID, sumCents); err != nil {
		return fmt.Errorf("update user balance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit withdrawal transaction: %w", err)
	}

	return nil
}

func (r *Repository) WithdrawalsByUser(ctx context.Context, userID int64) ([]model.Withdrawal, error) {
	const query = `
SELECT order_number, sum_cents, processed_at
FROM withdrawals
WHERE user_id = $1
	ORDER BY processed_at DESC, id DESC;`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("select user withdrawals: %w", err)
	}
	defer rows.Close()

	withdrawals := make([]model.Withdrawal, 0)
	for rows.Next() {
		var withdrawal model.Withdrawal
		if err := rows.Scan(
			&withdrawal.OrderNumber,
			&withdrawal.SumCents,
			&withdrawal.ProcessedAt,
		); err != nil {
			return nil, fmt.Errorf("scan user withdrawal: %w", err)
		}
		withdrawals = append(withdrawals, withdrawal)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user withdrawals: %w", err)
	}

	return withdrawals, nil
}

func (r *Repository) ClaimDueOrders(
	ctx context.Context,
	limit int,
	lease time.Duration,
) ([]model.Order, error) {
	const query = `
WITH due_orders AS (
    SELECT number
    FROM orders
    WHERE status IN ('NEW', 'PROCESSING')
      AND next_check_at <= NOW()
    ORDER BY next_check_at, uploaded_at
    FOR UPDATE SKIP LOCKED
    LIMIT $1
)
UPDATE orders AS claimed
SET next_check_at = NOW() + make_interval(secs => $2)
FROM due_orders
WHERE claimed.number = due_orders.number
RETURNING claimed.number,
          claimed.user_id,
          claimed.status,
          claimed.accrual_cents,
          claimed.uploaded_at,
          claimed.next_check_at;`

	leaseSeconds := int64(lease / time.Second)
	if limit <= 0 || leaseSeconds <= 0 {
		return nil, errors.New("order claim limit and lease must be positive")
	}

	rows, err := r.db.Query(ctx, query, limit, leaseSeconds)
	if err != nil {
		return nil, fmt.Errorf("claim due orders: %w", err)
	}
	defer rows.Close()

	orders := make([]model.Order, 0, limit)
	for rows.Next() {
		var order model.Order
		if err := rows.Scan(
			&order.Number,
			&order.UserID,
			&order.Status,
			&order.AccrualCents,
			&order.UploadedAt,
			&order.NextCheckAt,
		); err != nil {
			return nil, fmt.Errorf("scan due order: %w", err)
		}
		orders = append(orders, order)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due orders: %w", err)
	}

	return orders, nil
}

func (r *Repository) UpdateOrderStatus(
	ctx context.Context,
	number string,
	status model.OrderStatus,
	accrualCents *int64,
	nextCheckAt time.Time,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin order update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockQuery = `SELECT user_id, status FROM orders WHERE number = $1 FOR UPDATE;`
	var userID int64
	var currentStatus model.OrderStatus
	if err := tx.QueryRow(ctx, lockQuery, number).Scan(&userID, &currentStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ErrOrderNotFound
		}
		return fmt.Errorf("lock order: %w", err)
	}

	if currentStatus.IsFinal() {
		return nil
	}

	const updateOrderQuery = `
UPDATE orders
SET status = $2, accrual_cents = $3, next_check_at = $4
WHERE number = $1;`
	if _, err := tx.Exec(ctx, updateOrderQuery, number, status, accrualCents, nextCheckAt); err != nil {
		return fmt.Errorf("update order status: %w", err)
	}

	if status == model.OrderStatusProcessed && accrualCents != nil && *accrualCents > 0 {
		const updateBalanceQuery = `
UPDATE users
SET balance_cents = balance_cents + $2
WHERE id = $1;`
		if _, err := tx.Exec(ctx, updateBalanceQuery, userID, *accrualCents); err != nil {
			return fmt.Errorf("credit user balance: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit order update transaction: %w", err)
	}

	return nil
}

func uniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
