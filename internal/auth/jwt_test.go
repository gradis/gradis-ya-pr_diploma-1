package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTValidation(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	manager := NewManager("secret")
	manager.now = func() time.Time { return now }
	for _, tc := range []struct {
		name    string
		subject string
		expiry  *jwt.NumericDate
		method  jwt.SigningMethod
		key     any
		valid   bool
	}{
		{"valid", "42", jwt.NewNumericDate(now.Add(time.Hour)), jwt.SigningMethodHS256, []byte("secret"), true},
		{"expired", "42", jwt.NewNumericDate(now.Add(-time.Second)), jwt.SigningMethodHS256, []byte("secret"), false},
		{"at expiry", "42", jwt.NewNumericDate(now), jwt.SigningMethodHS256, []byte("secret"), false},
		{"missing expiry", "42", nil, jwt.SigningMethodHS256, []byte("secret"), false},
		{"wrong key", "42", jwt.NewNumericDate(now.Add(time.Hour)), jwt.SigningMethodHS256, []byte("other"), false},
		{"wrong algorithm", "42", jwt.NewNumericDate(now.Add(time.Hour)), jwt.SigningMethodHS384, []byte("secret"), false},
		{"unsigned", "42", jwt.NewNumericDate(now.Add(time.Hour)), jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, false},
		{"invalid subject", "0", jwt.NewNumericDate(now.Add(time.Hour)), jwt.SigningMethodHS256, []byte("secret"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, err := jwt.NewWithClaims(tc.method, jwt.RegisteredClaims{Subject: tc.subject, ExpiresAt: tc.expiry}).SignedString(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.Verify(token)
			if (err == nil) != tc.valid {
				t.Fatalf("Verify error = %v, want valid %v", err, tc.valid)
			}
		})
	}
	token, err := manager.Sign(42)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(tokenTTL)
	if _, err := manager.Verify(token); err == nil {
		t.Fatal("issued token survived its TTL")
	}
}

func TestCookieSecureConfiguration(t *testing.T) {
	for _, secure := range []bool{true, false} {
		manager := NewManager("secret", WithSecureCookie(secure))
		recorder := httptest.NewRecorder()
		if err := manager.SetCookie(recorder, 42); err != nil {
			t.Fatal(err)
		}
		response := recorder.Result()
		response.Body.Close()
		cookie := response.Cookies()[0]
		if cookie.Secure != secure || cookie.MaxAge != 86400 {
			t.Fatalf("cookie = %+v", cookie)
		}
	}
}
