package v2

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

// statsStaleThreshold marks a processing row as stuck when its lease is older than
// this — comfortably beyond the default lease timeout so live sends are not flagged.
const statsStaleThreshold = 2 * time.Minute

// defaultFailedListLimit bounds the failed-rows listing.
const defaultFailedListLimit = 100

// maxFailedListLimit caps ?limit= so one request cannot dump the whole outbox.
const maxFailedListLimit = 1000

// requireAdmin ensures the caller resolved to the Admin role. It writes the error
// response and returns false when not.
func requireAdmin(c *gin.Context, logger *zap.Logger) bool {
	role, ok := getRoleFromContext(c, logger)
	if !ok {
		return false
	}
	if role != rbac.Admin {
		apiErrors.RaiseForbiddenErr(c, apiErrors.ErrAuthForbidden)
		return false
	}
	return true
}

// GetNotificationStatsHandler returns the outbox queue statistics (admin only).
func GetNotificationStatsHandler(dbInst *db.DB, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c, logger) {
			return
		}

		stats, err := dbInst.GetNotificationStats(c.Request.Context(), statsStaleThreshold)
		if err != nil {
			logger.Error("failed to get notification stats", zap.Error(err))
			apiErrors.RaiseInternalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, stats)
	}
}

// GetFailedNotificationsHandler lists recent outbox rows (admin only). It defaults to
// the failed ones, and accepts ?status= and ?limit= to inspect the rest of the queue.
func GetFailedNotificationsHandler(dbInst *db.DB, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c, logger) {
			return
		}

		status := c.DefaultQuery("status", db.NotificationStatusFailed)
		if !isListableStatus(status) {
			apiErrors.RaiseBadRequestErr(c, apiErrors.NewErrNotificationStatusInvalid(listableStatuses()))
			return
		}

		limit, err := parseListLimit(c.Query("limit"))
		if err != nil {
			apiErrors.RaiseBadRequestErr(c, err)
			return
		}

		rows, err := dbInst.ListNotificationsByStatus(c.Request.Context(), status, limit)
		if err != nil {
			logger.Error("failed to list notifications", zap.String("status", status), zap.Error(err))
			apiErrors.RaiseInternalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": rows, "status": status, "limit": limit})
	}
}

// listableStatuses is the set accepted by ?status=, in queue order.
func listableStatuses() []string {
	return []string{
		db.NotificationStatusPending,
		db.NotificationStatusProcessing,
		db.NotificationStatusSent,
		db.NotificationStatusFailed,
	}
}

func isListableStatus(status string) bool {
	return slices.Contains(listableStatuses(), status)
}

// parseListLimit bounds the page so a large queue cannot be dumped in one response.
func parseListLimit(raw string) (int, error) {
	if raw == "" {
		return defaultFailedListLimit, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxFailedListLimit {
		return 0, apiErrors.NewErrNotificationLimitInvalid(maxFailedListLimit)
	}

	return limit, nil
}

// RedriveNotificationsData is the optional re-drive request body.
type RedriveNotificationsData struct {
	// IDs limits the re-drive to specific outbox rows; empty means all failed rows.
	IDs []uint `json:"ids"`
}

// RedriveNotificationsHandler resets failed rows back to pending (admin only) and
// wakes the worker to retry them immediately.
func RedriveNotificationsHandler(dbInst *db.DB, logger *zap.Logger, pub ...*notification.Publisher) gin.HandlerFunc {
	publisher := optionalPublisher(pub)
	return func(c *gin.Context) {
		if !requireAdmin(c, logger) {
			return
		}

		var body RedriveNotificationsData
		// io.EOF means an empty body (valid: re-drive all); any other parse error is
		// a client error and must not be treated as "re-drive everything".
		if err := c.ShouldBindBodyWithJSON(&body); err != nil && !errors.Is(err, io.EOF) {
			logger.Warn("re-drive failed: invalid request body", zap.Error(err))
			apiErrors.RaiseBadRequestErr(c, err)
			return
		}

		count, err := dbInst.RedriveFailed(c.Request.Context(), body.IDs...)
		if err != nil {
			logger.Error("failed to re-drive notifications", zap.Error(err))
			apiErrors.RaiseInternalErr(c, err)
			return
		}

		if count > 0 {
			publisher.Notify() // wake the worker to pick up the re-driven rows
		}
		logger.Info("re-drove failed notifications", zap.Int64("count", count))
		c.JSON(http.StatusOK, gin.H{"redriven": count})
	}
}
