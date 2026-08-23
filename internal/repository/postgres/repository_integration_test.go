package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gradis/ya-pr_diploma-1/internal/database"
	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"github.com/gradis/ya-pr_diploma-1/internal/repository"
	postgresrepository "github.com/gradis/ya-pr_diploma-1/internal/repository/postgres"
)

func TestRepositoryTransactions(t *testing.T) {
	databaseURI := os.Getenv("TEST_DATABASE_URI")
	if databaseURI == "" {
		t.Skip("TEST_DATABASE_URI is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := database.RunMigrations(databaseURI); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	db, err := database.New(ctx, databaseURI)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(db.Close)

	clean := func(cleanupContext context.Context) {
		if _, err := db.Pool.Exec(cleanupContext, `TRUNCATE withdrawals, orders, users RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("clean test database: %v", err)
		}
	}
	clean(ctx)
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		clean(cleanupContext)
	})

	repo := postgresrepository.New(db.Pool)
	firstUserID, err := repo.CreateUser(ctx, "integration-first", "hash")
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	secondUserID, err := repo.CreateUser(ctx, "integration-second", "hash")
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}

	const orderNumber = "12345678903"
	owner, err := repo.AddOrder(ctx, firstUserID, orderNumber)
	if err != nil || owner != repository.OrderCreated {
		t.Fatalf("add order = %v, %v", owner, err)
	}
	owner, err = repo.AddOrder(ctx, firstUserID, orderNumber)
	if err != nil || owner != repository.OrderOwnedByUser {
		t.Fatalf("add same order = %v, %v", owner, err)
	}
	owner, err = repo.AddOrder(ctx, secondUserID, orderNumber)
	if err != nil || owner != repository.OrderOwnedByAnotherUser {
		t.Fatalf("add another user's order = %v, %v", owner, err)
	}

	claimed, err := repo.ClaimDueOrders(ctx, 10, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].Number != orderNumber {
		t.Fatalf("first claim = %#v, %v", claimed, err)
	}
	claimed, err = repo.ClaimDueOrders(ctx, 10, time.Minute)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("leased order was claimed twice: %#v, %v", claimed, err)
	}

	const accrualCents = int64(10_000)
	var updateWaitGroup sync.WaitGroup
	updateErrors := make(chan error, 12)
	for range 12 {
		updateWaitGroup.Add(1)
		go func() {
			defer updateWaitGroup.Done()
			updateErrors <- repo.UpdateOrderStatus(
				ctx,
				orderNumber,
				model.OrderStatusProcessed,
				pointer(accrualCents),
				time.Now(),
			)
		}()
	}
	updateWaitGroup.Wait()
	close(updateErrors)
	for err := range updateErrors {
		if err != nil {
			t.Fatalf("concurrent order update: %v", err)
		}
	}

	balance, err := repo.BalanceByUser(ctx, firstUserID)
	if err != nil || balance.CurrentCents != accrualCents {
		t.Fatalf("balance after idempotent accrual = %#v, %v", balance, err)
	}

	withdrawErrors := make(chan error, 2)
	var withdrawWaitGroup sync.WaitGroup
	for _, withdrawalOrder := range []string{"2377225624", "9278923470"} {
		withdrawWaitGroup.Add(1)
		go func(number string) {
			defer withdrawWaitGroup.Done()
			withdrawErrors <- repo.Withdraw(ctx, firstUserID, number, 7_500)
		}(withdrawalOrder)
	}
	withdrawWaitGroup.Wait()
	close(withdrawErrors)

	var successful, insufficient int
	for err := range withdrawErrors {
		switch {
		case err == nil:
			successful++
		case errors.Is(err, repository.ErrInsufficientFund):
			insufficient++
		default:
			t.Fatalf("unexpected withdrawal error: %v", err)
		}
	}
	if successful != 1 || insufficient != 1 {
		t.Fatalf("successful withdrawals = %d, insufficient = %d", successful, insufficient)
	}

	balance, err = repo.BalanceByUser(ctx, firstUserID)
	if err != nil || balance.CurrentCents != 2_500 || balance.WithdrawnCents != 7_500 {
		t.Fatalf("balance after concurrent withdrawals = %#v, %v", balance, err)
	}
	withdrawals, err := repo.WithdrawalsByUser(ctx, firstUserID)
	if err != nil || len(withdrawals) != 1 || withdrawals[0].SumCents != 7_500 {
		t.Fatalf("withdrawals = %#v, %v", withdrawals, err)
	}
}

func pointer(value int64) *int64 {
	return &value
}
