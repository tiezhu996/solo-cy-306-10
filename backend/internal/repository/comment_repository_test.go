package repository

import (
	"errors"
	"testing"
	"time"

	"gbevent/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCommentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Comment{}); err != nil {
		t.Fatalf("migrate comment: %v", err)
	}
	return db
}

func seedComments(t *testing.T, repo *CommentRepository, base time.Time) []model.Comment {
	t.Helper()
	comments := []model.Comment{
		{ActivityID: 1, UserID: 10, Rating: 5, Content: "old", CreatedAt: base.Add(-2 * time.Hour)},
		{ActivityID: 1, UserID: 11, Rating: 4, Content: "mid", CreatedAt: base.Add(-1 * time.Hour)},
		{ActivityID: 1, UserID: 12, Rating: 3, Content: "new", CreatedAt: base},
		{ActivityID: 2, UserID: 10, Rating: 5, Content: "other activity", CreatedAt: base},
	}
	for i := range comments {
		if err := repo.Create(&comments[i]); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
	}
	return comments
}

func TestListByActivityPinnedFirst(t *testing.T) {
	repo := NewCommentRepository(setupCommentTestDB(t))
	comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	// 未置顶时按时间倒序
	list, err := repo.ListByActivity(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 || list[0].Content != "new" || list[2].Content != "old" {
		t.Fatalf("default order wrong: %+v", list)
	}

	// 置顶最旧的一条后应排最前，其余仍按时间倒序
	if err := repo.Pin(1, comments[0].ID); err != nil {
		t.Fatalf("pin: %v", err)
	}
	list, err = repo.ListByActivity(1)
	if err != nil {
		t.Fatalf("list after pin: %v", err)
	}
	if !list[0].IsPinned || list[0].ID != comments[0].ID {
		t.Fatalf("pinned comment not first: %+v", list)
	}
	if list[1].Content != "new" || list[2].Content != "mid" {
		t.Fatalf("rest not in time desc order: %+v", list)
	}
}

func TestPinKeepsSinglePinnedPerActivity(t *testing.T) {
	repo := NewCommentRepository(setupCommentTestDB(t))
	comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	if err := repo.Pin(1, comments[0].ID); err != nil {
		t.Fatalf("pin first: %v", err)
	}
	// 再置顶另一条，应替换旧置顶，每个活动最多一条
	if err := repo.Pin(1, comments[1].ID); err != nil {
		t.Fatalf("pin second: %v", err)
	}
	list, err := repo.ListByActivity(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	pinned := 0
	for _, c := range list {
		if c.IsPinned {
			pinned++
			if c.ID != comments[1].ID {
				t.Fatalf("wrong comment pinned: want %d got %d", comments[1].ID, c.ID)
			}
		}
	}
	if pinned != 1 {
		t.Fatalf("want exactly 1 pinned comment, got %d", pinned)
	}

	// 其他活动的置顶标记互不影响
	other, err := repo.ListByActivity(2)
	if err != nil {
		t.Fatalf("list activity 2: %v", err)
	}
	for _, c := range other {
		if c.IsPinned {
			t.Fatalf("activity 2 should have no pinned comment")
		}
	}
}

func TestUnpin(t *testing.T) {
	repo := NewCommentRepository(setupCommentTestDB(t))
	comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	if err := repo.Pin(1, comments[2].ID); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := repo.Unpin(1, comments[2].ID); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	list, err := repo.ListByActivity(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range list {
		if c.IsPinned {
			t.Fatalf("comment %d should be unpinned", c.ID)
		}
	}
	if list[0].Content != "new" {
		t.Fatalf("order should fall back to time desc: %+v", list)
	}
}

// TestPinTargetMissingKeepsOldPin 并发缺陷回归：置顶目标已被删除时，
// 必须返回 ErrNotFound 且原有置顶保持不变（不能清掉旧置顶后静默成功）。
func TestPinTargetMissingKeepsOldPin(t *testing.T) {
	repo := NewCommentRepository(setupCommentTestDB(t))
	comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	// 先置顶 mid，再删除 new，模拟"删除已提交后置顶请求才到达"的交错
	if err := repo.Pin(1, comments[1].ID); err != nil {
		t.Fatalf("pin mid: %v", err)
	}
	if err := repo.Delete(comments[2].ID); err != nil {
		t.Fatalf("delete new: %v", err)
	}

	err := repo.Pin(1, comments[2].ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("pin deleted comment: want ErrNotFound, got %v", err)
	}

	list, err := repo.ListByActivity(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 comments, got %d", len(list))
	}
	if !list[0].IsPinned || list[0].ID != comments[1].ID {
		t.Fatalf("old pin on comment %d must be preserved, got %+v", comments[1].ID, list)
	}
}

// TestPinAlreadyPinnedIdempotent 重复置顶同一条评论应成功且幂等
// （MySQL 的 UPDATE 对未变更行报 0 RowsAffected，不能据此误判不存在）。
func TestPinAlreadyPinnedIdempotent(t *testing.T) {
	repo := NewCommentRepository(setupCommentTestDB(t))
	comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	if err := repo.Pin(1, comments[0].ID); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := repo.Pin(1, comments[0].ID); err != nil {
		t.Fatalf("re-pin same comment should succeed, got %v", err)
	}
	list, err := repo.ListByActivity(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	pinned := 0
	for _, c := range list {
		if c.IsPinned {
			pinned++
			if c.ID != comments[0].ID {
				t.Fatalf("want comment %d pinned, got %d", comments[0].ID, c.ID)
			}
		}
	}
	if pinned != 1 {
		t.Fatalf("want exactly 1 pinned comment, got %d", pinned)
	}
}

// TestUnpinMissingComment 取消置顶不存在的评论返回 ErrNotFound。
func TestUnpinMissingComment(t *testing.T) {
	repo := NewCommentRepository(setupCommentTestDB(t))
	seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	if err := repo.Unpin(1, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unpin missing comment: want ErrNotFound, got %v", err)
	}
}
