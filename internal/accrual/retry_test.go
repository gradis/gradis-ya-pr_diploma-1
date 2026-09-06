package accrual

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func responseWith(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type trackedBody struct {
	io.Reader
	closed *atomic.Int32
}

func (b trackedBody) Close() error { b.closed.Add(1); return nil }

func TestClientRetriesAndClosesBodies(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status, calls int
		fail          bool
	}{
		{"recovers", 500, 2, false}, {"exhausted", 503, 3, true}, {"no retry on 400", 400, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, closed atomic.Int32
			client := NewClient("http://accrual", &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				count := int(calls.Add(1))
				status := tc.status
				if tc.name == "recovers" && count == 2 {
					status = 200
				}
				response := responseWith(status, "")
				response.Body = trackedBody{strings.NewReader(`{"order":"123","status":"PROCESSED"}`), &closed}
				return response, nil
			})})
			_, err := client.Check(context.Background(), "123")
			if (err != nil) != tc.fail || int(calls.Load()) != tc.calls || closed.Load() != calls.Load() {
				t.Fatalf("err=%v calls=%d closed=%d", err, calls.Load(), closed.Load())
			}
		})
	}
}

func TestRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	client := NewClient("http://accrual", &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls.Add(1)
		cancel()
		return responseWith(500, ""), nil
	})})
	if _, err := client.Check(ctx, "123"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestRetryAfterParsingAndWarning(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", time.Minute, false}, {"3", 3 * time.Second, false}, {"0", 0, false},
		{now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second, false},
		{now.Add(-time.Second).Format(http.TimeFormat), 0, false},
		{"oops", time.Minute, true}, {"-1", time.Minute, true}, {"9223372036854775807", time.Minute, true},
	} {
		delay, err := retryAfter(tc.value, now)
		if delay != tc.want || (err != nil) != tc.invalid {
			t.Fatalf("%q: %v, %v", tc.value, delay, err)
		}
	}
	core, logs := observer.New(zap.WarnLevel)
	client := NewClient("http://accrual", &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		response := responseWith(429, "")
		response.Header.Set("Retry-After", "oops")
		return response, nil
	})}, WithLogger(zap.New(core)))
	if _, err := client.Check(context.Background(), "123"); err == nil {
		t.Fatal("missing 429")
	}
	if logs.Len() != 1 {
		t.Fatalf("warnings=%d", logs.Len())
	}
}

func Test429StopsConcurrentClientRetries(t *testing.T) {
	// Hold the first 500 response until another request has published a 429.
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	client := NewClient("http://accrual", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.Path == "/api/orders/first" {
			close(firstStarted)
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			return responseWith(500, ""), nil
		}
		response := responseWith(429, "")
		response.Header.Set("Retry-After", "60")
		return response, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Check(ctx, "first"); done <- err }()
	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err := client.Check(ctx, "limited")
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("err=%v", err)
	}
	close(release)
	select {
	case err := <-done:
		if !errors.As(err, &limited) {
			t.Fatalf("retry error=%v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if calls.Load() != 2 {
		t.Fatalf("requests continued after 429: %d", calls.Load())
	}
}
