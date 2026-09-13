package service

import (
	"log/slog"

	"gbevent/internal/constants"
	"gbevent/internal/model"
	"gbevent/internal/repository"
	"gbevent/internal/util"
)

// CommentService 评论业务逻辑。
type CommentService struct {
	repo        *repository.CommentRepository
	activitySvc *ActivityService
	logger      *slog.Logger
}

// NewCommentService 构造评论服务。
func NewCommentService(repo *repository.CommentRepository, activitySvc *ActivityService, logger *slog.Logger) *CommentService {
	return &CommentService{repo: repo, activitySvc: activitySvc, logger: logger}
}

// Create 发表评论与评分。
func (s *CommentService) Create(activityID, userID uint64, rating int, content string) (*model.Comment, error) {
	if rating < 1 || rating > 5 {
		return nil, util.NewAppError(constants.CodeValidationFailed, "Comment[rating="+itoa(uint64(rating))+"] create: rating must be 1-5")
	}
	if _, _, err := s.activitySvc.Get(activityID); err != nil {
		return nil, err
	}
	c := &model.Comment{ActivityID: activityID, UserID: userID, Rating: rating, Content: content}
	if err := s.repo.Create(c); err != nil {
		s.logger.Error(constants.LogCommentCreateFailed, "error", err)
		return nil, util.Wrap(err, "Comment[activity_id=%d] create failed", activityID)
	}
	s.logger.Info(constants.LogCommentCreateSuccess, "comment_id", c.ID, "rating", rating)
	return c, nil
}

// ListByActivity 查询活动评论。
func (s *CommentService) ListByActivity(activityID uint64) ([]model.Comment, error) {
	return s.repo.ListByActivity(activityID)
}

// AvgRating 查询平均评分。
func (s *CommentService) AvgRating(activityID uint64) (float64, error) {
	return s.repo.AvgRating(activityID)
}

// ListMine 查询我的评论。
func (s *CommentService) ListMine(userID uint64) ([]model.Comment, error) {
	return s.repo.ListByUser(userID)
}

// Delete 删除评论（本人或管理员）。
func (s *CommentService) Delete(id, operatorID uint64, operatorRole string) error {
	c, err := s.repo.FindByID(id)
	if err != nil {
		return util.Wrap(err, "Comment[id=%d] delete find failed", id)
	}
	if operatorRole != constants.RoleAdmin && c.UserID != operatorID {
		return util.NewAppError(constants.CodeForbidden, "Comment[id="+itoa(id)+"] delete forbidden: not owner")
	}
	if err := s.repo.Delete(id); err != nil {
		return util.Wrap(err, "Comment[id=%d] delete failed", id)
	}
	s.logger.Info(constants.LogCommentDeleteSuccess, "comment_id", id)
	return nil
}

// Pin 置顶评论（活动组织者或管理员；每个活动最多一条置顶，新置顶会替换旧置顶）。
func (s *CommentService) Pin(activityID, commentID, operatorID uint64, operatorRole string) error {
	if err := s.checkPinPermission(activityID, commentID, operatorID, operatorRole, "pin"); err != nil {
		return err
	}
	if err := s.repo.Pin(activityID, commentID); err != nil {
		s.logger.Error(constants.LogCommentPinFailed, "comment_id", commentID, "error", err)
		return util.Wrap(err, "Comment[id=%d] pin failed", commentID)
	}
	s.logger.Info(constants.LogCommentPinSuccess, "comment_id", commentID, "activity_id", activityID)
	return nil
}

// Unpin 取消置顶评论（活动组织者或管理员）。
func (s *CommentService) Unpin(activityID, commentID, operatorID uint64, operatorRole string) error {
	if err := s.checkPinPermission(activityID, commentID, operatorID, operatorRole, "unpin"); err != nil {
		return err
	}
	if err := s.repo.Unpin(activityID, commentID); err != nil {
		s.logger.Error(constants.LogCommentUnpinFailed, "comment_id", commentID, "error", err)
		return util.Wrap(err, "Comment[id=%d] unpin failed", commentID)
	}
	s.logger.Info(constants.LogCommentUnpinSuccess, "comment_id", commentID, "activity_id", activityID)
	return nil
}

// checkPinPermission 校验评论归属与操作者权限（活动组织者或管理员）。
func (s *CommentService) checkPinPermission(activityID, commentID, operatorID uint64, operatorRole, action string) error {
	c, err := s.repo.FindByID(commentID)
	if err != nil {
		return util.Wrap(err, "Comment[id=%d] %s find failed", commentID, action)
	}
	if c.ActivityID != activityID {
		return util.NewAppError(constants.CodeCommentNotBelong,
			"Comment[id="+itoa(commentID)+"] "+action+" failed: "+constants.MsgCommentNotBelong+" (activity_id="+itoa(activityID)+")")
	}
	a, _, err := s.activitySvc.Get(activityID)
	if err != nil {
		return err
	}
	if !IsOrganizer(operatorID, operatorRole, a.OrganizerID) {
		return util.NewAppError(constants.CodeForbidden,
			"Comment[id="+itoa(commentID)+"] "+action+" forbidden: "+constants.MsgCommentPinForbidden+" (role="+operatorRole+", organizer_id="+itoa(a.OrganizerID)+")")
	}
	return nil
}
