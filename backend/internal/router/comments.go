package router

import (
	"gbevent/internal/constants"
	"gbevent/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerCommentRoutes 评论路由。
func (r *Router) registerCommentRoutes(g *gin.RouterGroup) {
	comments := g.Group("/activities/:id/comments")
	comments.GET("", r.comment.List)
	comments.POST("", middleware.AuthRequired(r.cfg), r.comment.Create)

	manage := comments.Group("")
	manage.Use(middleware.AuthRequired(r.cfg), middleware.RequireRole(constants.RoleOrganizer, constants.RoleAdmin))
	manage.POST("/:commentId/pin", r.comment.Pin)
	manage.DELETE("/:commentId/pin", r.comment.Unpin)

	mine := g.Group("/comments")
	mine.Use(middleware.AuthRequired(r.cfg))
	mine.GET("/mine", r.comment.Mine)
	mine.DELETE("/:id", r.comment.Delete)
}
