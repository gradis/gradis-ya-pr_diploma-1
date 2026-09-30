package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gradis/ya-pr_diploma-1/internal/auth"
	"github.com/gradis/ya-pr_diploma-1/internal/middleware"
	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"github.com/gradis/ya-pr_diploma-1/internal/money"
	"github.com/gradis/ya-pr_diploma-1/internal/service"
	"go.uber.org/zap"
)

const maxRequestBodySize = 1 << 20

type LoyaltyService interface {
	Register(ctx context.Context, login, password string) (int64, error)
	Login(ctx context.Context, login, password string) (int64, error)
	UploadOrder(ctx context.Context, userID int64, number string) (service.UploadOrderResult, error)
	Orders(ctx context.Context, userID int64) ([]model.Order, error)
	Balance(ctx context.Context, userID int64) (model.Balance, error)
	Withdraw(ctx context.Context, userID int64, orderNumber string, sumCents int64) error
	Withdrawals(ctx context.Context, userID int64) ([]model.Withdrawal, error)
}

type Handler struct {
	service LoyaltyService
	auth    *auth.Manager
	logg    *zap.Logger
}

func New(service LoyaltyService, authManager *auth.Manager, logg *zap.Logger) *Handler {
	if logg == nil {
		logg = zap.NewNop()
	}
	if authManager == nil {
		authManager = auth.NewManager("")
	}

	return &Handler{service: service, auth: authManager, logg: logg}
}

func (h *Handler) Router() *gin.Engine {
	router := gin.New()
	router.Use(middleware.RequestLogger(h.logg))
	router.Use(gin.CustomRecovery(func(c *gin.Context, recovered any) {
		h.logg.Error(
			"panic recovered while handling HTTP request",
			zap.Any("panic", recovered),
			zap.ByteString("stack", debug.Stack()),
		)
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	router.Use(middleware.Gzip())

	router.POST("/api/user/register", h.register)
	router.POST("/api/user/login", h.login)

	user := router.Group("/api/user")
	user.Use(middleware.RequireAuthentication(h.auth))
	user.POST("/orders", h.uploadOrder)
	user.GET("/orders", h.orders)
	user.GET("/balance", h.balance)
	user.POST("/balance/withdraw", h.withdraw)
	user.GET("/withdrawals", h.withdrawals)

	return router
}

type credentialsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (h *Handler) register(c *gin.Context) {
	var request credentialsRequest
	if err := decodeJSON(c, &request); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	userID, err := h.service.Register(c.Request.Context(), request.Login, request.Password)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			c.Status(http.StatusBadRequest)
		case errors.Is(err, service.ErrLoginExists):
			c.Status(http.StatusConflict)
		default:
			h.internalError(c, "register user", err)
		}
		return
	}

	if err := h.auth.SetCookie(c.Writer, userID); err != nil {
		h.internalError(c, "set authentication cookie", err)
		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) login(c *gin.Context) {
	var request credentialsRequest
	if err := decodeJSON(c, &request); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	userID, err := h.service.Login(c.Request.Context(), request.Login, request.Password)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			c.Status(http.StatusBadRequest)
		case errors.Is(err, service.ErrInvalidCredentials):
			c.Status(http.StatusUnauthorized)
		default:
			h.internalError(c, "authenticate user", err)
		}
		return
	}

	if err := h.auth.SetCookie(c.Writer, userID); err != nil {
		h.internalError(c, "set authentication cookie", err)
		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) uploadOrder(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	number := strings.TrimSpace(string(body))
	if number == "" {
		c.Status(http.StatusBadRequest)
		return
	}

	result, err := h.service.UploadOrder(c.Request.Context(), userID, number)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidOrderNumber):
			c.Status(http.StatusUnprocessableEntity)
		case errors.Is(err, service.ErrOrderOwnedByAnother):
			c.Status(http.StatusConflict)
		default:
			h.internalError(c, "upload order", err)
		}
		return
	}

	if result == service.UploadOrderAlreadyExists {
		c.Status(http.StatusOK)
		return
	}

	c.Status(http.StatusAccepted)
}

type orderResponse struct {
	Number     string            `json:"number"`
	Status     model.OrderStatus `json:"status"`
	Accrual    *money.Amount     `json:"accrual,omitempty"`
	UploadedAt string            `json:"uploaded_at"`
}

func (h *Handler) orders(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	orders, err := h.service.Orders(c.Request.Context(), userID)
	if err != nil {
		h.internalError(c, "get user orders", err)
		return
	}
	if len(orders) == 0 {
		c.Status(http.StatusNoContent)
		return
	}

	response := make([]orderResponse, 0, len(orders))
	for _, order := range orders {
		var accrual *money.Amount
		if order.AccrualCents != nil {
			value := money.Amount(*order.AccrualCents)
			accrual = &value
		}

		response = append(response, orderResponse{
			Number:     order.Number,
			Status:     order.Status,
			Accrual:    accrual,
			UploadedAt: order.UploadedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	c.JSON(http.StatusOK, response)
}

type balanceResponse struct {
	Current   money.Amount `json:"current"`
	Withdrawn money.Amount `json:"withdrawn"`
}

func (h *Handler) balance(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	balance, err := h.service.Balance(c.Request.Context(), userID)
	if err != nil {
		h.internalError(c, "get user balance", err)
		return
	}

	c.JSON(http.StatusOK, balanceResponse{
		Current:   money.Amount(balance.CurrentCents),
		Withdrawn: money.Amount(balance.WithdrawnCents),
	})
}

type withdrawRequest struct {
	Order string      `json:"order"`
	Sum   json.Number `json:"sum"`
}

func (h *Handler) withdraw(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	var request withdrawRequest
	if err := decodeJSON(c, &request); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	sumCents, err := money.ParseCents(request.Sum.String())
	if err != nil || sumCents <= 0 {
		c.Status(http.StatusBadRequest)
		return
	}

	err = h.service.Withdraw(
		c.Request.Context(),
		userID,
		strings.TrimSpace(request.Order),
		sumCents,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidOrderNumber), errors.Is(err, service.ErrWithdrawalExists):
			c.Status(http.StatusUnprocessableEntity)
		case errors.Is(err, service.ErrInvalidInput):
			c.Status(http.StatusBadRequest)
		case errors.Is(err, service.ErrInsufficientFunds):
			c.Status(http.StatusPaymentRequired)
		default:
			h.internalError(c, "withdraw user balance", err)
		}
		return
	}

	c.Status(http.StatusOK)
}

type withdrawalResponse struct {
	Order       string       `json:"order"`
	Sum         money.Amount `json:"sum"`
	ProcessedAt string       `json:"processed_at"`
}

func (h *Handler) withdrawals(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	withdrawals, err := h.service.Withdrawals(c.Request.Context(), userID)
	if err != nil {
		h.internalError(c, "get user withdrawals", err)
		return
	}
	if len(withdrawals) == 0 {
		c.Status(http.StatusNoContent)
		return
	}

	response := make([]withdrawalResponse, 0, len(withdrawals))
	for _, withdrawal := range withdrawals {
		response = append(response, withdrawalResponse{
			Order:       withdrawal.OrderNumber,
			Sum:         money.Amount(withdrawal.SumCents),
			ProcessedAt: withdrawal.ProcessedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	c.JSON(http.StatusOK, response)
}

func decodeJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return err
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}

	return nil
}

func currentUserID(c *gin.Context) (int64, bool) {
	userID, ok := auth.UserIDFromContext(c.Request.Context())
	if !ok {
		c.Status(http.StatusUnauthorized)
		return 0, false
	}
	return userID, true
}

func (h *Handler) internalError(c *gin.Context, operation string, err error) {
	h.logg.Error(
		operation+" failed",
		zap.String("method", c.Request.Method),
		zap.String("path", c.Request.URL.Path),
		zap.Error(err),
	)
	c.Status(http.StatusInternalServerError)
}
