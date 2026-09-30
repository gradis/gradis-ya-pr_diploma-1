package service

import "errors"

var (
	ErrInvalidInput        = errors.New("invalid input")
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrLoginExists         = errors.New("login already exists")
	ErrInvalidOrderNumber  = errors.New("invalid order number")
	ErrOrderOwnedByAnother = errors.New("order belongs to another user")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrWithdrawalExists    = errors.New("withdrawal order already exists")
)

type UploadOrderResult int

const (
	UploadOrderAccepted UploadOrderResult = iota
	UploadOrderAlreadyExists
)
