package accrual

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gradis/ya-pr_diploma-1/internal/money"
	"go.uber.org/zap"
)

const maxResponseBodySize = 1 << 20

var ErrOrderNotRegistered = errors.New("order is not registered in accrual system")

type Status string

const (
	StatusRegistered Status = "REGISTERED"
	StatusProcessing Status = "PROCESSING"
	StatusInvalid    Status = "INVALID"
	StatusProcessed  Status = "PROCESSED"
)

type Result struct {
	Status       Status
	AccrualCents *int64
}

type RateLimitError struct {
	RetryAfter time.Duration
	until      time.Time
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("accrual rate limit exceeded; retry after %s", e.RetryAfter)
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	logg       *zap.Logger
	gate       *rateGate
}

type ClientOption func(*Client)

func WithLogger(logg *zap.Logger) ClientOption {
	return func(c *Client) {
		if logg != nil {
			c.logg = logg
		}
	}
}

func NewClient(baseURL string, httpClient *http.Client, options ...ClientOption) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	client := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
		logg:       zap.NewNop(),
		gate:       &rateGate{now: time.Now},
	}
	for _, option := range options {
		option(client)
	}
	return client
}

func (c *Client) Check(ctx context.Context, number string) (Result, error) {
	// Bound the entire check, not each retry independently, to fit order leases.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	gate := requestGate(ctx, c.gate)
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if delay := gate.remaining(); delay > 0 {
			return Result{}, &RateLimitError{RetryAfter: delay, until: gate.deadline()}
		}
		result, retry, err := c.checkOnce(ctx, number, gate)
		if !retry || attempt == 2 {
			return result, err
		}
		if err := waitRetry(ctx, 100*time.Millisecond*time.Duration(1<<attempt)); err != nil {
			return Result{}, err
		}
	}
}

func (c *Client) checkOnce(ctx context.Context, number string, gate *rateGate) (Result, bool, error) {
	endpoint := c.baseURL + "/api/orders/" + url.PathEscape(number)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, false, fmt.Errorf("create accrual request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Result{}, false, fmt.Errorf("send accrual request: %w", err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		result, err := decodeResult(response.Body, number)
		return result, false, err
	case http.StatusNoContent:
		return Result{}, false, ErrOrderNotRegistered
	case http.StatusTooManyRequests:
		raw := response.Header.Get("Retry-After")
		delay, err := retryAfter(raw, time.Now())
		// Publish the cooldown before logging or any database operation.
		until := gate.block(delay)
		if err != nil {
			c.logg.Warn("invalid accrual Retry-After; using default", zap.String("retry_after", raw), zap.Duration("fallback", delay), zap.Error(err))
		}
		return Result{}, false, &RateLimitError{RetryAfter: gate.remaining(), until: until}
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBodySize))
		retry := response.StatusCode >= 500 && response.StatusCode <= 599
		return Result{}, retry, fmt.Errorf("accrual service returned status %d", response.StatusCode)
	}
}

type resultResponse struct {
	Order   string      `json:"order"`
	Status  Status      `json:"status"`
	Accrual json.Number `json:"accrual"`
}

func decodeResult(body io.Reader, expectedOrder string) (Result, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBodySize+1))
	if err != nil {
		return Result{}, fmt.Errorf("read accrual response: %w", err)
	}
	if len(data) > maxResponseBodySize {
		return Result{}, errors.New("accrual response body is too large")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var response resultResponse
	if err := decoder.Decode(&response); err != nil {
		return Result{}, fmt.Errorf("decode accrual response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Result{}, errors.New("accrual response must contain one JSON value")
	}
	if response.Order != expectedOrder {
		return Result{}, fmt.Errorf("accrual response order %q does not match requested order %q", response.Order, expectedOrder)
	}

	switch response.Status {
	case StatusRegistered, StatusProcessing, StatusInvalid, StatusProcessed:
	default:
		return Result{}, fmt.Errorf("unknown accrual status %q", response.Status)
	}

	result := Result{Status: response.Status}
	if response.Accrual != "" {
		cents, err := money.ParseCents(response.Accrual.String())
		if err != nil {
			return Result{}, fmt.Errorf("parse accrual amount: %w", err)
		}
		result.AccrualCents = &cents
	}

	return result, nil
}

// retryAfter reports malformed values separately from an absent header.
func retryAfter(value string, now time.Time) (time.Duration, error) {
	const fallback = time.Minute
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		return time.Duration(seconds) * time.Second, nil
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(0, at.Sub(now)), nil
	}
	return fallback, fmt.Errorf("unsupported Retry-After value %q", value)
}
