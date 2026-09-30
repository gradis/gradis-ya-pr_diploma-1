package repository

import "errors"

var (
	ErrLoginExists      = errors.New("login already exists")
	ErrUserNotFound     = errors.New("user not found")
	ErrOrderNotFound    = errors.New("order not found")
	ErrInsufficientFund = errors.New("insufficient funds")
	ErrWithdrawalExists = errors.New("withdrawal order already exists")
)

type OrderOwner int

const (
	OrderCreated OrderOwner = iota
	OrderOwnedByUser
	OrderOwnedByAnotherUser
)
