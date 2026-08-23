package model

import "time"

type OrderStatus string

const (
	OrderStatusNew        OrderStatus = "NEW"
	OrderStatusProcessing OrderStatus = "PROCESSING"
	OrderStatusInvalid    OrderStatus = "INVALID"
	OrderStatusProcessed  OrderStatus = "PROCESSED"
)

func (s OrderStatus) IsFinal() bool {
	return s == OrderStatusInvalid || s == OrderStatusProcessed
}

type User struct {
	ID           int64
	Login        string
	PasswordHash string
}

type Order struct {
	Number       string
	UserID       int64
	Status       OrderStatus
	AccrualCents *int64
	UploadedAt   time.Time
	NextCheckAt  time.Time
}

type Balance struct {
	CurrentCents   int64
	WithdrawnCents int64
}

type Withdrawal struct {
	OrderNumber string
	SumCents    int64
	ProcessedAt time.Time
}
