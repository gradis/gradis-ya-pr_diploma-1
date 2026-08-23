package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"github.com/gradis/ya-pr_diploma-1/internal/repository"
	"github.com/gradis/ya-pr_diploma-1/internal/validator"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxLoginLength    = 256
	maxPasswordLength = 72
)

type userRepository interface {
	CreateUser(ctx context.Context, login, passwordHash string) (int64, error)
	UserByLogin(ctx context.Context, login string) (model.User, error)
}

type orderRepository interface {
	AddOrder(ctx context.Context, userID int64, number string) (repository.OrderOwner, error)
	OrdersByUser(ctx context.Context, userID int64) ([]model.Order, error)
}

type balanceRepository interface {
	BalanceByUser(ctx context.Context, userID int64) (model.Balance, error)
	Withdraw(ctx context.Context, userID int64, orderNumber string, sumCents int64) error
	WithdrawalsByUser(ctx context.Context, userID int64) ([]model.Withdrawal, error)
}

type Repository interface {
	userRepository
	orderRepository
	balanceRepository
}

type Service struct {
	users    userRepository
	orders   orderRepository
	balances balanceRepository
}

func New(repo Repository) *Service {
	return &Service{
		users:    repo,
		orders:   repo,
		balances: repo,
	}
}

func (s *Service) Register(ctx context.Context, login, password string) (int64, error) {
	login = strings.TrimSpace(login)
	if !validCredentials(login, password) {
		return 0, ErrInvalidInput
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("hash password: %w", err)
	}

	userID, err := s.users.CreateUser(ctx, login, string(passwordHash))
	if err != nil {
		if errors.Is(err, repository.ErrLoginExists) {
			return 0, ErrLoginExists
		}
		return 0, fmt.Errorf("create user: %w", err)
	}

	return userID, nil
}

func (s *Service) Login(ctx context.Context, login, password string) (int64, error) {
	login = strings.TrimSpace(login)
	if !validCredentials(login, password) {
		return 0, ErrInvalidInput
	}

	user, err := s.users.UserByLogin(ctx, login)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return 0, ErrInvalidCredentials
		}
		return 0, fmt.Errorf("get user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return 0, ErrInvalidCredentials
	}

	return user.ID, nil
}

func validCredentials(login, password string) bool {
	return login != "" &&
		len(login) <= maxLoginLength &&
		strings.TrimSpace(password) != "" &&
		len(password) <= maxPasswordLength
}

func (s *Service) UploadOrder(
	ctx context.Context,
	userID int64,
	number string,
) (UploadOrderResult, error) {
	if userID <= 0 {
		return 0, ErrInvalidCredentials
	}
	if !validator.ValidOrderNumber(number) {
		return 0, ErrInvalidOrderNumber
	}

	owner, err := s.orders.AddOrder(ctx, userID, number)
	if err != nil {
		return 0, fmt.Errorf("add order: %w", err)
	}

	switch owner {
	case repository.OrderCreated:
		return UploadOrderAccepted, nil
	case repository.OrderOwnedByUser:
		return UploadOrderAlreadyExists, nil
	case repository.OrderOwnedByAnotherUser:
		return 0, ErrOrderOwnedByAnother
	default:
		return 0, fmt.Errorf("unknown order owner result: %d", owner)
	}
}

func (s *Service) Orders(ctx context.Context, userID int64) ([]model.Order, error) {
	if userID <= 0 {
		return nil, ErrInvalidCredentials
	}

	orders, err := s.orders.OrdersByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user orders: %w", err)
	}
	return orders, nil
}

func (s *Service) Balance(ctx context.Context, userID int64) (model.Balance, error) {
	if userID <= 0 {
		return model.Balance{}, ErrInvalidCredentials
	}

	balance, err := s.balances.BalanceByUser(ctx, userID)
	if err != nil {
		return model.Balance{}, fmt.Errorf("get user balance: %w", err)
	}
	return balance, nil
}

func (s *Service) Withdraw(
	ctx context.Context,
	userID int64,
	orderNumber string,
	sumCents int64,
) error {
	if userID <= 0 {
		return ErrInvalidCredentials
	}
	if !validator.ValidOrderNumber(orderNumber) {
		return ErrInvalidOrderNumber
	}
	if sumCents <= 0 {
		return ErrInvalidInput
	}

	if err := s.balances.Withdraw(ctx, userID, orderNumber, sumCents); err != nil {
		switch {
		case errors.Is(err, repository.ErrInsufficientFund):
			return ErrInsufficientFunds
		case errors.Is(err, repository.ErrWithdrawalExists):
			return ErrWithdrawalExists
		default:
			return fmt.Errorf("withdraw balance: %w", err)
		}
	}

	return nil
}

func (s *Service) Withdrawals(ctx context.Context, userID int64) ([]model.Withdrawal, error) {
	if userID <= 0 {
		return nil, ErrInvalidCredentials
	}

	withdrawals, err := s.balances.WithdrawalsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user withdrawals: %w", err)
	}
	return withdrawals, nil
}
