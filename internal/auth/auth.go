package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const CookieName = "gophermart_session"

var ErrInvalidToken = errors.New("invalid authentication token")

type userIDContextKey struct{}

type Manager struct {
	secret []byte
}

func NewManager(secret string) *Manager {
	return &Manager{secret: []byte(secret)}
}

func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDContextKey{}, userID)
}

func UserIDFromContext(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userIDContextKey{}).(int64)
	return userID, ok && userID > 0
}

func (m *Manager) Sign(userID int64) (string, error) {
	if userID <= 0 || len(m.secret) == 0 {
		return "", ErrInvalidToken
	}

	payload := strconv.FormatInt(userID, 10)
	signature := m.signature(payload)

	return payload + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (m *Manager) Verify(token string) (int64, error) {
	payload, encodedSignature, ok := strings.Cut(token, ".")
	if !ok || payload == "" || encodedSignature == "" || len(m.secret) == 0 {
		return 0, ErrInvalidToken
	}

	userID, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || userID <= 0 {
		return 0, ErrInvalidToken
	}

	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil || !hmac.Equal(signature, m.signature(payload)) {
		return 0, ErrInvalidToken
	}

	return userID, nil
}

func (m *Manager) SetCookie(writer http.ResponseWriter, userID int64) error {
	token, err := m.Sign(userID)
	if err != nil {
		return fmt.Errorf("sign authentication token: %w", err)
	}

	http.SetCookie(writer, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}

func (m *Manager) signature(payload string) []byte {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
