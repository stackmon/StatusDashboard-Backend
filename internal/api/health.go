package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// readinessTimeout bounds the database check behind /readyz.
const readinessTimeout = 3 * time.Second

func (a *API) initHealthRoutes() {
	a.r.GET("/healthz", a.livenessHandler())
	a.r.GET("/readyz", a.readinessHandler())
}

func (a *API) livenessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Status(http.StatusOK)
	}
}

func (a *API) readinessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()

		if err := a.db.Ping(ctx); err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		c.Status(http.StatusOK)
	}
}
