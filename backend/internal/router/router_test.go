package router

import (
	"log/slog"
	"os"
	"testing"

	"gbevent/internal/config"
	"gbevent/internal/handler"
)

// TestSetupRoutes 验证路由装配不发生 Gin 路由冲突（含评论置顶新路由）。
func TestSetupRoutes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := &config.Config{JWTSecret: "test", RateLimitPerMinute: 120, UploadDir: "/tmp/uploads"}
	r := New(cfg, nil, logger,
		handler.NewUserHandler(nil, logger),
		handler.NewActivityHandler(nil, logger),
		handler.NewRegistrationHandler(nil, logger),
		handler.NewCheckInRecordHandler(nil, logger),
		handler.NewCommentHandler(nil, logger),
		handler.NewFavoriteHandler(nil, logger),
		handler.NewNotificationHandler(nil, logger),
		handler.NewUploadHandler(cfg, logger),
	)
	engine := r.Setup()
	found := map[string]bool{}
	for _, ri := range engine.Routes() {
		found[ri.Method+" "+ri.Path] = true
	}
	for _, want := range []string{
		"POST /api/v1/activities/:id/comments/:commentId/pin",
		"DELETE /api/v1/activities/:id/comments/:commentId/pin",
		"GET /api/v1/activities/:id/comments",
		"POST /api/v1/activities/:id/comments",
		"DELETE /api/v1/comments/:id",
	} {
		if !found[want] {
			t.Errorf("route missing: %s", want)
		}
	}
}
