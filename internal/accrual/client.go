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
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("accrual rate limit exceeded; retry after %s", e.RetryAfter)
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (c *Client) Check(ctx context.Context, number string) (Result, error) {
	endpoint := c.baseURL + "/api/orders/" + url.PathEscape(number)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, fmt.Errorf("create accrual request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("send accrual request: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		return decodeResult(response.Body, number)

	case http.StatusNoContent:
		return Result{}, ErrOrderNotRegistered

	case http.StatusTooManyRequests:
		return Result{}, &RateLimitError{RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}

	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBodySize))
		return Result{}, fmt.Errorf("accrual service returned status %d", response.StatusCode)
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

func parseRetryAfter(value string) time.Duration {
	const defaultRetryAfter = time.Minute

	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}

	if retryAt, err := http.ParseTime(value); err == nil {
		duration := time.Until(retryAt)
		if duration > 0 {
			return duration
		}
	}

	return defaultRetryAfter
}
