package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const CookieName = "gophermart_session"

const tokenTTL = 24 * time.Hour

var ErrInvalidToken = errors.New("invalid authentication token")

type userIDContextKey struct{}

type Manager struct {
	secret       []byte
	secureCookie bool
	now          func() time.Time
}

type Option func(*Manager)

func WithSecureCookie(secure bool) Option {
	return func(m *Manager) { m.secureCookie = secure }
}

func NewManager(secret string, options ...Option) *Manager {
	m := &Manager{secret: []byte(secret), secureCookie: true, now: time.Now}
	for _, option := range options {
		option(m)
	}
	return m
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

	now := m.now()
	claims := jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(userID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

func (m *Manager) Verify(value string) (int64, error) {
	if len(m.secret) == 0 {
		return 0, ErrInvalidToken
	}
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(value, claims, func(_ *jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(m.now))
	if err != nil || !token.Valid {
		return 0, ErrInvalidToken
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
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
		Secure:   m.secureCookie,
		MaxAge:   int(tokenTTL / time.Second),
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}
