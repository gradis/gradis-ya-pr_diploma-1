package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func RequestLogger(logg *zap.Logger) gin.HandlerFunc {
	if logg == nil {
		logg = zap.NewNop()
	}

	return func(c *gin.Context) {
		startedAt := time.Now()
		c.Next()

		logg.Info(
			"http request completed",
			zap.String("method", c.Request.Method),
			zap.String("uri", c.Request.RequestURI),
			zap.Int("status", c.Writer.Status()),
			zap.Int("size", c.Writer.Size()),
			zap.Duration("duration", time.Since(startedAt)),
		)
	}
}
