package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gradis/ya-pr_diploma-1/internal/auth"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := auth.NewManager("secret")
	router := gin.New()
	router.Use(RequireAuthentication(manager))
	router.GET("/", func(c *gin.Context) {
		userID, ok := auth.UserIDFromContext(c.Request.Context())
		if !ok || userID != 15 {
			t.Fatalf("unexpected user ID %d", userID)
		}
		c.Status(http.StatusOK)
	})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	token, err := manager.Sign(15)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	authorized := httptest.NewRecorder()
	router.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
}

func TestGzipResponseAndRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Gzip())
	router.POST("/", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatal(err)
		}
		c.JSON(http.StatusOK, gin.H{"body": string(body)})
	})

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte("hello"))
	_ = writer.Close()

	request := httptest.NewRequest(http.MethodPost, "/", &compressed)
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("unexpected content encoding %q", response.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatalf("open compressed response: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"body":"hello"}` {
		t.Fatalf("unexpected response %s", body)
	}
}

func TestRequestLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, entries := observer.New(zap.InfoLevel)
	router := gin.New()
	router.Use(RequestLogger(zap.New(core)))
	router.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ping", nil))

	if entries.Len() != 1 {
		t.Fatalf("log entries = %d, want 1", entries.Len())
	}
	fields := entries.All()[0].ContextMap()
	if fields["method"] != http.MethodGet || fields["uri"] != "/ping" {
		t.Fatalf("unexpected log fields: %#v", fields)
	}
}

func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{header: "gzip", want: true},
		{header: "br, gzip;q=0.5", want: true},
		{header: "gzip;q=0", want: false},
		{header: "gzip;q=0.0", want: false},
		{header: "gzip;q=invalid", want: false},
		{header: "*;q=0.5", want: true},
		{header: "*;q=1, gzip;q=0", want: false},
		{header: "br", want: false},
	}

	for _, test := range tests {
		t.Run(test.header, func(t *testing.T) {
			if got := acceptsGzip(test.header); got != test.want {
				t.Fatalf("acceptsGzip(%q) = %v, want %v", test.header, got, test.want)
			}
		})
	}
}

func TestGzipRejectsUnsupportedRequestEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Gzip())
	router.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	request.Header.Set("Content-Encoding", "br")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnsupportedMediaType)
	}
}
