package service

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"gbevent/internal/constants"
	"gbevent/internal/model"
	"gbevent/internal/repository"
	"gbevent/internal/util"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// commentTestFixture 装配基于 SQLite 内存库的真实 CommentService + ActivityService。
type commentTestFixture struct {
	svc         *CommentService
	commentRepo *repository.CommentRepository
	activity1   model.Activity // organizer_id = 100
	activity2   model.Activity // organizer_id = 200
	comments    []model.Comment
}

// newCommentTestFixture 每个测试用独立命名的共享缓存内存库，保证可重复且互不影响。
func newCommentTestFixture(t *testing.T) *commentTestFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.Activity{}, &model.Comment{}, &model.Registration{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	activityRepo := repository.NewActivityRepository(db)
	commentRepo := repository.NewCommentRepository(db)
	activitySvc := NewActivityService(activityRepo,
		repository.NewRegistrationRepository(db),
		repository.NewNotificationRepository(db),
		repository.NewCheckInRecordRepository(db), logger)
	svc := NewCommentService(commentRepo, activitySvc, logger)

	f := &commentTestFixture{svc: svc, commentRepo: commentRepo}
	f.activity1 = model.Activity{Title: "活动一", ActivityType: constants.ActivityTypeLecture,
		Status: constants.ActivityStatusPublished, OrganizerID: 100}
	f.activity2 = model.Activity{Title: "活动二", ActivityType: constants.ActivityTypeParty,
		Status: constants.ActivityStatusPublished, OrganizerID: 200}
	if err := activityRepo.Create(&f.activity1); err != nil {
		t.Fatalf("seed activity1: %v", err)
	}
	if err := activityRepo.Create(&f.activity2); err != nil {
		t.Fatalf("seed activity2: %v", err)
	}

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f.comments = []model.Comment{
		{ActivityID: f.activity1.ID, UserID: 10, Rating: 4, Content: "old", CreatedAt: base.Add(-2 * time.Hour)},
		{ActivityID: f.activity1.ID, UserID: 11, Rating: 5, Content: "mid", CreatedAt: base.Add(-1 * time.Hour)},
		{ActivityID: f.activity1.ID, UserID: 12, Rating: 2, Content: "new", CreatedAt: base},
		{ActivityID: f.activity2.ID, UserID: 10, Rating: 5, Content: "other activity", CreatedAt: base},
	}
	for i := range f.comments {
		if err := commentRepo.Create(&f.comments[i]); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
	}
	return f
}

// assertAppErrorCode 断言错误为指定错误码的业务错误。
func assertAppErrorCode(t *testing.T, err error, code int) {
	t.Helper()
	var appErr *util.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("want AppError code=%d, got: %v", code, err)
	}
	if appErr.Code != code {
		t.Fatalf("want code=%d, got code=%d message=%s", code, appErr.Code, appErr.Message)
	}
}

// pinnedOf 返回列表中置顶评论的数量与首条 ID。
func pinnedOf(list []model.Comment) (count int, firstID uint64) {
	for i, c := range list {
		if c.IsPinned {
			count++
			if i == 0 {
				firstID = c.ID
			}
		}
	}
	return count, firstID
}

// TestCommentPinSuccess 置顶成功：组织者与管理员均可置顶，置顶后排最前。
func TestCommentPinSuccess(t *testing.T) {
	f := newCommentTestFixture(t)
	act1 := f.activity1.ID

	if err := f.svc.Pin(act1, f.comments[0].ID, 100, constants.RoleOrganizer); err != nil {
		t.Fatalf("organizer pin: %v", err)
	}
	list, err := f.svc.ListByActivity(act1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	count, firstID := pinnedOf(list)
	if count != 1 || firstID != f.comments[0].ID {
		t.Fatalf("want comment %d pinned first, got count=%d first=%d", f.comments[0].ID, count, firstID)
	}

	// 管理员可置顶任意活动的评论
	if err := f.svc.Pin(act1, f.comments[1].ID, 999, constants.RoleAdmin); err != nil {
		t.Fatalf("admin pin: %v", err)
	}
	list, _ = f.svc.ListByActivity(act1)
	if _, firstID := pinnedOf(list); firstID != f.comments[1].ID {
		t.Fatalf("admin pin should take effect, got first=%d", firstID)
	}
}

// TestCommentPinForbidden 普通账号与非本活动组织者拒绝置顶/取消置顶。
func TestCommentPinForbidden(t *testing.T) {
	f := newCommentTestFixture(t)
	act1 := f.activity1.ID

	cases := []struct {
		name       string
		operatorID uint64
		role       string
	}{
		{"normal user", 300, constants.RoleUser},
		{"organizer of other activity", 200, constants.RoleOrganizer},
	}
	for _, tc := range cases {
		if err := f.svc.Pin(act1, f.comments[0].ID, tc.operatorID, tc.role); err == nil {
			t.Errorf("%s: pin should be forbidden", tc.name)
		} else {
			assertAppErrorCode(t, err, constants.CodeForbidden)
		}
		if err := f.svc.Unpin(act1, f.comments[0].ID, tc.operatorID, tc.role); err == nil {
			t.Errorf("%s: unpin should be forbidden", tc.name)
		} else {
			assertAppErrorCode(t, err, constants.CodeForbidden)
		}
	}
}

// TestCommentPinCrossActivityRejected 评论不属于该活动时拒绝置顶。
func TestCommentPinCrossActivityRejected(t *testing.T) {
	f := newCommentTestFixture(t)

	// comments[3] 属于 activity2，用 activity1 的路径置顶应报"评论不属于该活动"
	cross := f.svc.Pin(f.activity1.ID, f.comments[3].ID, 100, constants.RoleOrganizer)
	if cross == nil {
		t.Fatal("cross-activity pin should fail")
	}
	assertAppErrorCode(t, cross, constants.CodeCommentNotBelong)

	// 取消置顶同样校验归属
	cross = f.svc.Unpin(f.activity1.ID, f.comments[3].ID, 100, constants.RoleOrganizer)
	if cross == nil {
		t.Fatal("cross-activity unpin should fail")
	}
	assertAppErrorCode(t, cross, constants.CodeCommentNotBelong)

	// 归属校验先于权限校验：即使管理员跨活动操作也同样拒绝
	cross = f.svc.Pin(f.activity1.ID, f.comments[3].ID, 999, constants.RoleAdmin)
	assertAppErrorCode(t, cross, constants.CodeCommentNotBelong)
}

// TestCommentPinCommentNotFound 评论不存在时返回未找到错误。
func TestCommentPinCommentNotFound(t *testing.T) {
	f := newCommentTestFixture(t)
	act1 := f.activity1.ID

	if err := f.svc.Pin(act1, 99999, 100, constants.RoleOrganizer); err == nil {
		t.Fatal("pin missing comment should fail")
	} else if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got: %v", err)
	}
	if err := f.svc.Unpin(act1, 99999, 100, constants.RoleOrganizer); err == nil {
		t.Fatal("unpin missing comment should fail")
	} else if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got: %v", err)
	}
}

// TestCommentPinReplacesPrevious 重复置顶只保留一条，新置顶替换旧置顶。
func TestCommentPinReplacesPrevious(t *testing.T) {
	f := newCommentTestFixture(t)
	act1 := f.activity1.ID

	if err := f.svc.Pin(act1, f.comments[0].ID, 100, constants.RoleOrganizer); err != nil {
		t.Fatalf("pin first: %v", err)
	}
	if err := f.svc.Pin(act1, f.comments[2].ID, 100, constants.RoleOrganizer); err != nil {
		t.Fatalf("pin second: %v", err)
	}

	list, err := f.svc.ListByActivity(act1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	count, firstID := pinnedOf(list)
	if count != 1 {
		t.Fatalf("want exactly 1 pinned comment, got %d", count)
	}
	if firstID != f.comments[2].ID {
		t.Fatalf("newest pin should win, want %d got %d", f.comments[2].ID, firstID)
	}

	// 其他活动的置顶标记不受影响
	other, err := f.svc.ListByActivity(f.activity2.ID)
	if err != nil {
		t.Fatalf("list activity2: %v", err)
	}
	if count, _ := pinnedOf(other); count != 0 {
		t.Fatalf("activity2 should have no pinned comment, got %d", count)
	}
}

// TestCommentUnpinRestoresTimeOrder 取消置顶后列表回退为时间倒序。
func TestCommentUnpinRestoresTimeOrder(t *testing.T) {
	f := newCommentTestFixture(t)
	act1 := f.activity1.ID

	// 置顶最旧的一条，验证其排最前
	if err := f.svc.Pin(act1, f.comments[0].ID, 100, constants.RoleOrganizer); err != nil {
		t.Fatalf("pin: %v", err)
	}
	list, err := f.svc.ListByActivity(act1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list[0].ID != f.comments[0].ID {
		t.Fatalf("pinned oldest should be first, got %d", list[0].ID)
	}

	// 取消置顶后回退时间倒序：new, mid, old
	if err := f.svc.Unpin(act1, f.comments[0].ID, 100, constants.RoleOrganizer); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	list, err = f.svc.ListByActivity(act1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if count, _ := pinnedOf(list); count != 0 {
		t.Fatalf("no comment should be pinned after unpin, got %d", count)
	}
	want := []uint64{f.comments[2].ID, f.comments[1].ID, f.comments[0].ID}
	if len(list) != len(want) {
		t.Fatalf("want %d comments, got %d", len(want), len(list))
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("position %d: want comment %d, got %d", i, id, list[i].ID)
		}
	}
}

// TestCommentDeletePinnedKeepsListAndAvg 已置顶评论被移除后，列表回退时间序且平均分正确重算。
func TestCommentDeletePinnedKeepsListAndAvg(t *testing.T) {
	f := newCommentTestFixture(t)
	act1 := f.activity1.ID

	// 置顶评分 2 分的最新评论并删除（管理员可删任意评论）
	if err := f.svc.Pin(act1, f.comments[2].ID, 100, constants.RoleOrganizer); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := f.svc.Delete(f.comments[2].ID, 999, constants.RoleAdmin); err != nil {
		t.Fatalf("delete pinned comment: %v", err)
	}

	list, err := f.svc.ListByActivity(act1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 comments after delete, got %d", len(list))
	}
	if count, _ := pinnedOf(list); count != 0 {
		t.Fatalf("pinned marker should be gone with the deleted comment, got %d pinned", count)
	}
	// 剩余按时间倒序：mid, old
	if list[0].ID != f.comments[1].ID || list[1].ID != f.comments[0].ID {
		t.Fatalf("list should fall back to time desc: %+v", list)
	}

	// 平均分只统计剩余评论：(4+5)/2 = 4.5
	avg, err := f.svc.AvgRating(act1)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	if avg != 4.5 {
		t.Fatalf("want avg 4.5 after deleting pinned comment, got %v", avg)
	}
}

// TestCommentCreateAndAvgRating 原有发表评论与平均分路径回归：校验评分范围与平均分计算。
func TestCommentCreateAndAvgRating(t *testing.T) {
	f := newCommentTestFixture(t)
	act2 := f.activity2.ID

	// 非法评分拒绝
	for _, rating := range []int{0, 6} {
		if _, err := f.svc.Create(act2, 10, rating, "bad rating"); err == nil {
			t.Errorf("rating %d should be rejected", rating)
		} else {
			assertAppErrorCode(t, err, constants.CodeValidationFailed)
		}
	}

	// 正常发表：activity2 已有 1 条 5 分评论，再发 1 条 3 分
	created, err := f.svc.Create(act2, 11, 3, "还行")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.IsPinned {
		t.Fatalf("new comment should have id and not be pinned: %+v", created)
	}

	list, err := f.svc.ListByActivity(act2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != created.ID {
		t.Fatalf("new comment should appear first by time desc: %+v", list)
	}

	avg, err := f.svc.AvgRating(act2)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	if avg != 4.0 { // (5+3)/2
		t.Fatalf("want avg 4.0, got %v", avg)
	}
}

// TestCommentDeletePermission 原有评论删除权限路径回归：本人可删，他人非管理员拒绝。
func TestCommentDeletePermission(t *testing.T) {
	f := newCommentTestFixture(t)

	if err := f.svc.Delete(f.comments[0].ID, 11, constants.RoleUser); err == nil {
		t.Fatal("non-owner non-admin delete should be forbidden")
	} else {
		assertAppErrorCode(t, err, constants.CodeForbidden)
	}

	if err := f.svc.Delete(f.comments[0].ID, 10, constants.RoleUser); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	list, err := f.svc.ListByActivity(f.activity1.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 comments after owner delete, got %d", len(list))
	}
}

// TestCommentPinDeletedKeepsOldPin 并发缺陷回归（服务端到端）：
// 置顶目标已被删除时返回评论不存在，且原有置顶保持不变。
func TestCommentPinDeletedKeepsOldPin(t *testing.T) {
	f := newCommentTestFixture(t)
	act1 := f.activity1.ID

	// 先置顶 old，再由管理员删除 mid
	if err := f.svc.Pin(act1, f.comments[0].ID, 100, constants.RoleOrganizer); err != nil {
		t.Fatalf("pin old: %v", err)
	}
	if err := f.svc.Delete(f.comments[1].ID, 999, constants.RoleAdmin); err != nil {
		t.Fatalf("delete mid: %v", err)
	}

	// 置顶已删除的评论：必须报"评论不存在"
	if err := f.svc.Pin(act1, f.comments[1].ID, 100, constants.RoleOrganizer); err == nil {
		t.Fatal("pin deleted comment should fail")
	} else if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got: %v", err)
	}
	// 取消置顶已删除的评论同样报"评论不存在"
	if err := f.svc.Unpin(act1, f.comments[1].ID, 100, constants.RoleOrganizer); err == nil {
		t.Fatal("unpin deleted comment should fail")
	} else if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got: %v", err)
	}

	// 原有置顶保持不变，仍排最前
	list, err := f.svc.ListByActivity(act1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	count, firstID := pinnedOf(list)
	if count != 1 || firstID != f.comments[0].ID {
		t.Fatalf("old pin on comment %d must be preserved, got count=%d first=%d",
			f.comments[0].ID, count, firstID)
	}
}
