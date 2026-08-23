package accrual

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClientCheck(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		retryAfter string
		wantStatus Status
		wantCents  int64
		wantError  error
		wantRate   time.Duration
	}{
		{
			name:       "processed",
			statusCode: http.StatusOK,
			body:       `{"order":"123","status":"PROCESSED","accrual":500.5}`,
			wantStatus: StatusProcessed,
			wantCents:  50050,
		},
		{
			name:       "not registered",
			statusCode: http.StatusNoContent,
			wantError:  ErrOrderNotRegistered,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			retryAfter: "3",
			wantRate:   3 * time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				header := make(http.Header)
				if test.retryAfter != "" {
					header.Set("Retry-After", test.retryAfter)
				}

				return &http.Response{
					StatusCode: test.statusCode,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(test.body)),
				}, nil
			})}

			client := NewClient("http://accrual.local", httpClient)
			result, err := client.Check(context.Background(), "123")

			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("error = %v, want %v", err, test.wantError)
				}
				return
			}
			if test.wantRate > 0 {
				var rateError *RateLimitError
				if !errors.As(err, &rateError) || rateError.RetryAfter != test.wantRate {
					t.Fatalf("rate limit error = %#v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Check(): %v", err)
			}
			if result.Status != test.wantStatus {
				t.Fatalf("status = %q, want %q", result.Status, test.wantStatus)
			}
			if result.AccrualCents == nil || *result.AccrualCents != test.wantCents {
				t.Fatalf("accrual = %#v, want %d", result.AccrualCents, test.wantCents)
			}
		})
	}
}

func TestClientRejectsInvalidSuccessfulResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "different order", body: `{"order":"456","status":"PROCESSED","accrual":1}`},
		{name: "unknown status", body: `{"order":"123","status":"UNKNOWN"}`},
		{name: "negative accrual", body: `{"order":"123","status":"PROCESSED","accrual":-1}`},
		{name: "sub-cent accrual", body: `{"order":"123","status":"PROCESSED","accrual":0.001}`},
		{name: "two JSON values", body: `{"order":"123","status":"PROCESSED"}{}`},
		{name: "malformed JSON", body: `{`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient("http://accrual.local", &http.Client{Transport: roundTripFunc(
				func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(test.body)),
					}, nil
				},
			)})

			if _, err := client.Check(context.Background(), "123"); err == nil {
				t.Fatal("invalid response unexpectedly accepted")
			}
		})
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	client := NewClient("http://accrual.local", &http.Client{Transport: roundTripFunc(
		func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(strings.Repeat(" ", maxResponseBodySize+1))),
			}, nil
		},
	)})

	if _, err := client.Check(context.Background(), "123"); err == nil {
		t.Fatal("oversized response unexpectedly accepted")
	}
}

type roundTripFunc func(request *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
