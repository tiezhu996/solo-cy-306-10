package repository

import (
	"errors"
	"fmt"

	"gbevent/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CommentRepository 评论仓储。
type CommentRepository struct {
	db *gorm.DB
}

// NewCommentRepository 构造评论仓储。
func NewCommentRepository(db *gorm.DB) *CommentRepository {
	return &CommentRepository{db: db}
}

// Create 创建评论。
func (r *CommentRepository) Create(c *model.Comment) error {
	if err := r.db.Create(c).Error; err != nil {
		return fmt.Errorf("create comment: %w", err)
	}
	return nil
}

// ListByActivity 查询某活动评论列表（置顶在前，其余按时间倒序）。
func (r *CommentRepository) ListByActivity(activityID uint64) ([]model.Comment, error) {
	var list []model.Comment
	if err := r.db.Where("activity_id = ?", activityID).Order("is_pinned DESC, created_at DESC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	return list, nil
}

// ListByUser 查询某用户的评论。
func (r *CommentRepository) ListByUser(userID uint64) ([]model.Comment, error) {
	var list []model.Comment
	if err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list user comments: %w", err)
	}
	return list, nil
}

// AvgRating 计算某活动平均评分。
func (r *CommentRepository) AvgRating(activityID uint64) (float64, error) {
	var avg float64
	if err := r.db.Model(&model.Comment{}).Where("activity_id = ?", activityID).
		Select("COALESCE(AVG(rating), 0)").Scan(&avg).Error; err != nil {
		return 0, fmt.Errorf("avg rating: %w", err)
	}
	return avg, nil
}

// FindByID 按 ID 查询评论。
func (r *CommentRepository) FindByID(id uint64) (*model.Comment, error) {
	var c model.Comment
	if err := r.db.First(&c, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find comment by id: %w", err)
	}
	return &c, nil
}

// Delete 删除评论。
func (r *CommentRepository) Delete(id uint64) error {
	if err := r.db.Delete(&model.Comment{}, id).Error; err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	return nil
}

// Pin 置顶指定评论，并在同一事务内清除该活动其他评论的置顶标记（每个活动最多一条置顶）。
// 目标评论不存在（如并发被删除）时返回 ErrNotFound 并回滚整个事务，原有置顶保持不变。
func (r *CommentRepository) Pin(activityID, commentID uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 先加锁确认目标评论存在且属于该活动：不能靠 UPDATE 的 RowsAffected 判断，
		// 因为 MySQL 默认只统计变更行数，重复置顶已置顶的评论会误判为不存在。
		var target model.Comment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND activity_id = ?", commentID, activityID).
			First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return fmt.Errorf("find comment for pin: %w", err)
		}
		if err := tx.Model(&model.Comment{}).
			Where("activity_id = ? AND id <> ? AND is_pinned = ?", activityID, commentID, true).
			Update("is_pinned", false).Error; err != nil {
			return fmt.Errorf("clear pinned comments: %w", err)
		}
		if err := tx.Model(&model.Comment{}).Where("id = ?", commentID).
			Update("is_pinned", true).Error; err != nil {
			return fmt.Errorf("pin comment: %w", err)
		}
		return nil
	})
}

// Unpin 取消指定评论的置顶标记。目标评论不存在时返回 ErrNotFound。
func (r *CommentRepository) Unpin(activityID, commentID uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var target model.Comment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND activity_id = ?", commentID, activityID).
			First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return fmt.Errorf("find comment for unpin: %w", err)
		}
		if err := tx.Model(&model.Comment{}).Where("id = ?", commentID).
			Update("is_pinned", false).Error; err != nil {
			return fmt.Errorf("unpin comment: %w", err)
		}
		return nil
	})
}
