package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"github.com/gradis/ya-pr_diploma-1/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

type repositoryStub struct {
	createUserID    int64
	createUserError error
	createdLogin    string
	createdHash     string
	user            model.User
	userError       error
	orderOwner      repository.OrderOwner
	orderError      error
	orders          []model.Order
	ordersError     error
	balance         model.Balance
	balanceError    error
	withdrawError   error
	withdrawals     []model.Withdrawal
	withdrawalsErr  error
}

func (r *repositoryStub) CreateUser(_ context.Context, login, passwordHash string) (int64, error) {
	r.createdLogin = login
	r.createdHash = passwordHash
	return r.createUserID, r.createUserError
}
func (r *repositoryStub) UserByLogin(context.Context, string) (model.User, error) {
	return r.user, r.userError
}
func (r *repositoryStub) AddOrder(context.Context, int64, string) (repository.OrderOwner, error) {
	return r.orderOwner, r.orderError
}
func (r *repositoryStub) OrdersByUser(context.Context, int64) ([]model.Order, error) {
	return r.orders, r.ordersError
}
func (r *repositoryStub) BalanceByUser(context.Context, int64) (model.Balance, error) {
	return r.balance, r.balanceError
}
func (r *repositoryStub) Withdraw(context.Context, int64, string, int64) error {
	return r.withdrawError
}
func (r *repositoryStub) WithdrawalsByUser(context.Context, int64) ([]model.Withdrawal, error) {
	return r.withdrawals, r.withdrawalsErr
}

func TestRegisterHashesPasswordAndTrimsLogin(t *testing.T) {
	repo := &repositoryStub{createUserID: 11}
	userID, err := New(repo).Register(context.Background(), "  alice  ", "secret")
	if err != nil || userID != 11 {
		t.Fatalf("Register() = %d, %v", userID, err)
	}
	if repo.createdLogin != "alice" || repo.createdHash == "secret" {
		t.Fatalf("stored credentials = %q, %q", repo.createdLogin, repo.createdHash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repo.createdHash), []byte("secret")); err != nil {
		t.Fatalf("password was not hashed correctly: %v", err)
	}
}

func TestRegisterMapsDuplicateLogin(t *testing.T) {
	svc := New(&repositoryStub{createUserError: repository.ErrLoginExists})
	_, err := svc.Register(context.Background(), "alice", "secret")
	if !errors.Is(err, ErrLoginExists) {
		t.Fatalf("error = %v, want ErrLoginExists", err)
	}
}

func TestRegisterRejectsOversizedCredentials(t *testing.T) {
	svc := New(&repositoryStub{createUserID: 1})

	tests := []struct {
		login    string
		password string
	}{
		{login: "", password: "secret"},
		{login: "alice", password: ""},
		{login: "alice", password: "   "},
		{login: strings.Repeat("a", maxLoginLength+1), password: "secret"},
		{login: "alice", password: strings.Repeat("p", maxPasswordLength+1)},
	}

	for _, test := range tests {
		if _, err := svc.Register(context.Background(), test.login, test.password); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Register() error = %v, want ErrInvalidInput", err)
		}
	}
}

func TestLogin(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	svc := New(&repositoryStub{user: model.User{ID: 7, Login: "alice", PasswordHash: string(hash)}})
	userID, err := svc.Login(context.Background(), "alice", "secret")
	if err != nil || userID != 7 {
		t.Fatalf("Login() = %d, %v", userID, err)
	}

	if _, err := svc.Login(context.Background(), "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}

	if _, err := New(&repositoryStub{userError: repository.ErrUserNotFound}).Login(context.Background(), "alice", "secret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing user error = %v", err)
	}
	if _, err := New(&repositoryStub{userError: errors.New("database")}).Login(context.Background(), "alice", "secret"); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("repository error = %v", err)
	}
	if _, err := svc.Login(context.Background(), "", "secret"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid input error = %v", err)
	}
}

func TestUploadOrder(t *testing.T) {
	tests := []struct {
		name      string
		owner     repository.OrderOwner
		number    string
		want      UploadOrderResult
		wantError error
		repoError error
	}{
		{name: "created", owner: repository.OrderCreated, number: "12345678903", want: UploadOrderAccepted},
		{name: "same user", owner: repository.OrderOwnedByUser, number: "12345678903", want: UploadOrderAlreadyExists},
		{name: "other user", owner: repository.OrderOwnedByAnotherUser, number: "12345678903", wantError: ErrOrderOwnedByAnother},
		{name: "invalid", owner: repository.OrderCreated, number: "123", wantError: ErrInvalidOrderNumber},
		{name: "invalid user", owner: repository.OrderCreated, number: "12345678903", wantError: ErrInvalidCredentials},
		{name: "repository error", owner: repository.OrderCreated, number: "12345678903", repoError: errors.New("database"), wantError: errors.New("wrapped")},
		{name: "unknown owner", owner: repository.OrderOwner(99), number: "12345678903", wantError: errors.New("unknown")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := New(&repositoryStub{orderOwner: test.owner, orderError: test.repoError})
			userID := int64(1)
			if test.name == "invalid user" {
				userID = 0
			}
			result, err := svc.UploadOrder(context.Background(), userID, test.number)
			if test.wantError == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError != nil && err == nil {
				t.Fatalf("error = nil, want %v", test.wantError)
			}
			if test.wantError == ErrInvalidOrderNumber || test.wantError == ErrInvalidCredentials || test.wantError == ErrOrderOwnedByAnother {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("error = %v, want %v", err, test.wantError)
				}
			}
			if result != test.want {
				t.Fatalf("result = %d, want %d", result, test.want)
			}
		})
	}
}

func TestReadOperations(t *testing.T) {
	orders := []model.Order{{Number: "12345678903"}}
	balance := model.Balance{CurrentCents: 100}
	withdrawals := []model.Withdrawal{{OrderNumber: "2377225624"}}
	repo := &repositoryStub{orders: orders, balance: balance, withdrawals: withdrawals}
	svc := New(repo)

	if got, err := svc.Orders(context.Background(), 1); err != nil || len(got) != 1 {
		t.Fatalf("Orders() = %#v, %v", got, err)
	}
	if got, err := svc.Balance(context.Background(), 1); err != nil || got != balance {
		t.Fatalf("Balance() = %#v, %v", got, err)
	}
	if got, err := svc.Withdrawals(context.Background(), 1); err != nil || len(got) != 1 {
		t.Fatalf("Withdrawals() = %#v, %v", got, err)
	}

	if _, err := svc.Orders(context.Background(), 0); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Orders() invalid user error = %v", err)
	}
	if _, err := svc.Balance(context.Background(), 0); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Balance() invalid user error = %v", err)
	}
	if _, err := svc.Withdrawals(context.Background(), 0); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Withdrawals() invalid user error = %v", err)
	}

	repositoryError := errors.New("database")
	errorService := New(&repositoryStub{
		ordersError:    repositoryError,
		balanceError:   repositoryError,
		withdrawalsErr: repositoryError,
	})
	if _, err := errorService.Orders(context.Background(), 1); err == nil {
		t.Fatal("Orders() repository error was lost")
	}
	if _, err := errorService.Balance(context.Background(), 1); err == nil {
		t.Fatal("Balance() repository error was lost")
	}
	if _, err := errorService.Withdrawals(context.Background(), 1); err == nil {
		t.Fatal("Withdrawals() repository error was lost")
	}
}

func TestWithdraw(t *testing.T) {
	tests := []struct {
		name       string
		userID     int64
		order      string
		cents      int64
		repoError  error
		wantError  error
		wantAnyErr bool
	}{
		{name: "success", userID: 1, order: "2377225624", cents: 100},
		{name: "invalid user", order: "2377225624", cents: 100, wantError: ErrInvalidCredentials},
		{name: "invalid order", userID: 1, order: "123", cents: 100, wantError: ErrInvalidOrderNumber},
		{name: "zero amount", userID: 1, order: "2377225624", wantError: ErrInvalidInput},
		{name: "insufficient", userID: 1, order: "2377225624", cents: 100, repoError: repository.ErrInsufficientFund, wantError: ErrInsufficientFunds},
		{name: "duplicate", userID: 1, order: "2377225624", cents: 100, repoError: repository.ErrWithdrawalExists, wantError: ErrWithdrawalExists},
		{name: "repository error", userID: 1, order: "2377225624", cents: 100, repoError: errors.New("database"), wantAnyErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := New(&repositoryStub{withdrawError: test.repoError}).Withdraw(
				context.Background(), test.userID, test.order, test.cents,
			)
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
			if test.wantAnyErr && err == nil {
				t.Fatal("expected an error")
			}
			if test.wantError == nil && !test.wantAnyErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
