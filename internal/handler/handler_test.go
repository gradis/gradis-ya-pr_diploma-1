package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gradis/ya-pr_diploma-1/internal/auth"
	"github.com/gradis/ya-pr_diploma-1/internal/model"
	"github.com/gradis/ya-pr_diploma-1/internal/service"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type serviceStub struct {
	registerResult int64
	registerError  error
	registerPanic  bool
	loginResult    int64
	loginError     error
	uploadResult   service.UploadOrderResult
	uploadError    error
	ordersResult   []model.Order
	ordersError    error
	balanceResult  model.Balance
	balanceError   error
	withdrawError  error
	withdrawCalls  int
	withdrawOrder  string
	withdrawCents  int64
	withdrawals    []model.Withdrawal
	withdrawalsErr error
}

func (s *serviceStub) Register(context.Context, string, string) (int64, error) {
	if s.registerPanic {
		panic("test panic")
	}
	return s.registerResult, s.registerError
}

func (s *serviceStub) Login(context.Context, string, string) (int64, error) {
	return s.loginResult, s.loginError
}

func (s *serviceStub) UploadOrder(context.Context, int64, string) (service.UploadOrderResult, error) {
	return s.uploadResult, s.uploadError
}

func (s *serviceStub) Orders(context.Context, int64) ([]model.Order, error) {
	return s.ordersResult, s.ordersError
}

func (s *serviceStub) Balance(context.Context, int64) (model.Balance, error) {
	return s.balanceResult, s.balanceError
}

func (s *serviceStub) Withdraw(_ context.Context, _ int64, order string, cents int64) error {
	s.withdrawCalls++
	s.withdrawOrder = order
	s.withdrawCents = cents
	return s.withdrawError
}

func (s *serviceStub) Withdrawals(context.Context, int64) ([]model.Withdrawal, error) {
	return s.withdrawals, s.withdrawalsErr
}

func TestRegisterSetsAuthenticationCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := auth.NewManager("secret")
	router := New(&serviceStub{registerResult: 42}, manager, zap.NewNop()).Router()

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/user/register",
		strings.NewReader(`{"login":"alice","password":"secret"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	result := response.Result()
	defer result.Body.Close()
	cookies := result.Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.CookieName {
		t.Fatalf("unexpected cookies: %#v", cookies)
	}
	userID, err := manager.Verify(cookies[0].Value)
	if err != nil || userID != 42 {
		t.Fatalf("cookie user ID = %d, error = %v", userID, err)
	}
}

func TestRegisterErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		serviceErr error
		wantStatus int
	}{
		{name: "malformed JSON", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "unknown JSON field", body: `{"login":"alice","password":"secret","admin":true}`, wantStatus: http.StatusBadRequest},
		{name: "multiple JSON values", body: `{"login":"alice","password":"secret"}{}`, wantStatus: http.StatusBadRequest},
		{name: "invalid input", body: `{"login":"","password":""}`, serviceErr: service.ErrInvalidInput, wantStatus: http.StatusBadRequest},
		{name: "duplicate login", body: `{"login":"alice","password":"secret"}`, serviceErr: service.ErrLoginExists, wantStatus: http.StatusConflict},
		{name: "internal error", body: `{"login":"alice","password":"secret"}`, serviceErr: errors.New("database error"), wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := New(
				&serviceStub{registerResult: 1, registerError: test.serviceErr},
				auth.NewManager("secret"),
				zap.NewNop(),
			).Router()

			request := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestRecoveryReturnsInternalServerErrorAndLogsRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, entries := observer.New(zap.DebugLevel)
	router := New(
		&serviceStub{registerPanic: true},
		auth.NewManager("secret"),
		zap.New(core),
	).Router()

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/user/register",
		strings.NewReader(`{"login":"alice","password":"secret"}`),
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	var recovered, requestLogged bool
	for _, entry := range entries.All() {
		switch entry.Message {
		case "panic recovered while handling HTTP request":
			recovered = true
		case "http request completed":
			requestLogged = entry.ContextMap()["status"] == int64(http.StatusInternalServerError)
		}
	}
	if !recovered || !requestLogged {
		t.Fatalf("panic and request logs missing: %#v", entries.All())
	}
}

func TestProtectedRouteRequiresCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := New(&serviceStub{}, auth.NewManager("secret"), zap.NewNop()).Router()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/user/balance", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestUploadOrderStatuses(t *testing.T) {
	tests := []struct {
		name       string
		result     service.UploadOrderResult
		err        error
		wantStatus int
	}{
		{name: "accepted", result: service.UploadOrderAccepted, wantStatus: http.StatusAccepted},
		{name: "same user", result: service.UploadOrderAlreadyExists, wantStatus: http.StatusOK},
		{name: "another user", err: service.ErrOrderOwnedByAnother, wantStatus: http.StatusConflict},
		{name: "invalid number", err: service.ErrInvalidOrderNumber, wantStatus: http.StatusUnprocessableEntity},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			manager := auth.NewManager("secret")
			router := New(&serviceStub{uploadResult: test.result, uploadError: test.err}, manager, zap.NewNop()).Router()
			token, err := manager.Sign(1)
			if err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader("12345678903"))
			request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestBalanceResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := auth.NewManager("secret")
	router := New(&serviceStub{balanceResult: model.Balance{
		CurrentCents:   50050,
		WithdrawnCents: 4200,
	}}, manager, zap.NewNop()).Router()
	token, _ := manager.Sign(1)

	request := httptest.NewRequest(http.MethodGet, "/api/user/balance", nil)
	request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"current":500.5,"withdrawn":42}` {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestWithdrawParsesAmountExactlyAndMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		serviceErr error
		wantStatus int
		wantCalls  int
	}{
		{
			name:       "exact large amount",
			body:       `{"order":" 2377225624 ","sum":90071992547409.91}`,
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{name: "sub-cent amount", body: `{"order":"2377225624","sum":0.001}`, wantStatus: http.StatusBadRequest},
		{name: "negative amount", body: `{"order":"2377225624","sum":-1}`, wantStatus: http.StatusBadRequest},
		{name: "unknown field", body: `{"order":"2377225624","sum":1,"extra":true}`, wantStatus: http.StatusBadRequest},
		{name: "invalid order", body: `{"order":"123","sum":1}`, serviceErr: service.ErrInvalidOrderNumber, wantStatus: http.StatusUnprocessableEntity, wantCalls: 1},
		{name: "insufficient funds", body: `{"order":"2377225624","sum":1}`, serviceErr: service.ErrInsufficientFunds, wantStatus: http.StatusPaymentRequired, wantCalls: 1},
		{name: "duplicate withdrawal", body: `{"order":"2377225624","sum":1}`, serviceErr: service.ErrWithdrawalExists, wantStatus: http.StatusUnprocessableEntity, wantCalls: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			manager := auth.NewManager("secret")
			stub := &serviceStub{withdrawError: test.serviceErr}
			router := New(stub, manager, zap.NewNop()).Router()
			token, _ := manager.Sign(1)

			request := httptest.NewRequest(http.MethodPost, "/api/user/balance/withdraw", strings.NewReader(test.body))
			request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus || stub.withdrawCalls != test.wantCalls {
				t.Fatalf("status = %d, calls = %d; want %d, %d", response.Code, stub.withdrawCalls, test.wantStatus, test.wantCalls)
			}
			if test.name == "exact large amount" {
				if stub.withdrawOrder != "2377225624" || stub.withdrawCents != 9007199254740991 {
					t.Fatalf("withdraw arguments = %q, %d", stub.withdrawOrder, stub.withdrawCents)
				}
			}
		})
	}
}

func TestOrdersAndWithdrawalsResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := auth.NewManager("secret")
	zero := int64(0)
	processedAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.FixedZone("UTC+3", 3*60*60))
	stub := &serviceStub{
		ordersResult: []model.Order{{
			Number:       "12345678903",
			Status:       model.OrderStatusProcessed,
			AccrualCents: &zero,
			UploadedAt:   processedAt,
		}},
		withdrawals: []model.Withdrawal{{
			OrderNumber: "2377225624",
			SumCents:    10050,
			ProcessedAt: processedAt,
		}},
	}
	router := New(stub, manager, zap.NewNop()).Router()
	token, _ := manager.Sign(1)

	tests := []struct {
		path string
		body string
	}{
		{path: "/api/user/orders", body: `[{"number":"12345678903","status":"PROCESSED","accrual":0,"uploaded_at":"2026-08-20T12:34:56+03:00"}]`},
		{path: "/api/user/withdrawals", body: `[{"order":"2377225624","sum":100.5,"processed_at":"2026-08-20T12:34:56+03:00"}]`},
	}

	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != test.body {
			t.Fatalf("%s: status = %d, body = %s", test.path, response.Code, response.Body.String())
		}
	}
}
