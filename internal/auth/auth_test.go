package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerSignAndVerify(t *testing.T) {
	manager := NewManager("test-secret")
	token, err := manager.Sign(42)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	userID, err := manager.Verify(token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if userID != 42 {
		t.Fatalf("user ID = %d, want 42", userID)
	}

	if _, err := manager.Verify(token + "forged"); err == nil {
		t.Fatal("forged token unexpectedly accepted")
	}
}

func TestManagerRejectsInvalidTokens(t *testing.T) {
	manager := NewManager("test-secret")
	validToken, err := manager.Sign(42)
	if err != nil {
		t.Fatal(err)
	}

	tests := []string{
		"",
		"42",
		"0.invalid",
		"-1.invalid",
		"not-a-number.invalid",
		validToken + ".extra",
		"42.legacy-signature",
	}
	for _, token := range tests {
		if _, err := manager.Verify(token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Verify(%q) error = %v, want ErrInvalidToken", token, err)
		}
	}

	emptyManager := NewManager("")
	if _, err := emptyManager.Sign(1); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Sign() with empty secret error = %v", err)
	}
	if _, err := emptyManager.Verify(validToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() with empty secret error = %v", err)
	}
}

func TestManagerSetsSafeSessionCookie(t *testing.T) {
	manager := NewManager("test-secret")
	response := httptest.NewRecorder()
	if err := manager.SetCookie(response, 42); err != nil {
		t.Fatal(err)
	}

	result := response.Result()
	defer result.Body.Close()
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != CookieName || cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe cookie attributes: %#v", cookie)
	}
}

func TestUserIDContext(t *testing.T) {
	ctx := WithUserID(context.Background(), 7)
	userID, ok := UserIDFromContext(ctx)
	if !ok || userID != 7 {
		t.Fatalf("UserIDFromContext() = %d, %v", userID, ok)
	}
}
