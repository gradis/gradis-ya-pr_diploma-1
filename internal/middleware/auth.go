package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gradis/ya-pr_diploma-1/internal/auth"
)

func RequireAuthentication(manager *auth.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		cookie, err := c.Request.Cookie(auth.CookieName)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		userID, err := manager.Verify(cookie.Value)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		requestContext := auth.WithUserID(c.Request.Context(), userID)
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	}
}
