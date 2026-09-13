package repository

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gbevent/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupConcurrentCommentTestDB 并发测试专用库：每个子测试独立命名 DSN，且连接池限为 1。
// SQLite 多连接并发写会产生偶发 SQLITE_LOCKED，单连接把写事务串行化，
// goroutine 交错顺序仍不确定，但每个事务保持原子，断言与顺序无关的不变量即可稳定重复。
func setupConcurrentCommentTestDB(t *testing.T) *gorm.DB {
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
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Comment{}); err != nil {
		t.Fatalf("migrate comment: %v", err)
	}
	return db
}

// pinnedComments 返回列表中所有置顶评论。
func pinnedComments(list []model.Comment) []model.Comment {
	var out []model.Comment
	for _, c := range list {
		if c.IsPinned {
			out = append(out, c)
		}
	}
	return out
}

// TestConcurrentPinAndDelete 同时置顶和移除同一条评论。
// 两种合法交错：置顶先提交（目标随后被删，旧置顶已被替换，最终无置顶）；
// 或删除先提交（置顶必须返回 ErrNotFound，旧置顶保留）。其余结果均为缺陷。
func TestConcurrentPinAndDelete(t *testing.T) {
	pinFirst, deleteFirst := 0, 0
	for i := 0; i < 30; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			repo := NewCommentRepository(setupConcurrentCommentTestDB(t))
			comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
			oldPin, target := comments[0], comments[2]

			if err := repo.Pin(1, oldPin.ID); err != nil {
				t.Fatalf("pre-pin: %v", err)
			}

			var wg sync.WaitGroup
			var pinErr, delErr error
			wg.Add(2)
			// 单连接池下事务按 goroutine 实际调度顺序串行提交；
			// 按迭代轮换启动顺序，确保"置顶先提交"与"删除先提交"两种交错都被覆盖
			if i%2 == 0 {
				go func() { defer wg.Done(); pinErr = repo.Pin(1, target.ID) }()
				go func() { defer wg.Done(); delErr = repo.Delete(target.ID) }()
			} else {
				go func() { defer wg.Done(); delErr = repo.Delete(target.ID) }()
				go func() { defer wg.Done(); pinErr = repo.Pin(1, target.ID) }()
			}
			wg.Wait()

			if delErr != nil {
				t.Fatalf("delete: %v", delErr)
			}
			list, err := repo.ListByActivity(1)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			pinned := pinnedComments(list)

			if pinErr == nil {
				// 置顶先提交：目标随后被删除，旧置顶已被替换，最终应无置顶
				pinFirst++
				if len(pinned) != 0 {
					t.Fatalf("pin committed before delete: want 0 pinned, got %d", len(pinned))
				}
			} else {
				// 删除先提交：置顶必须报"评论不存在"，且旧置顶保持不变
				deleteFirst++
				if !errors.Is(pinErr, ErrNotFound) {
					t.Fatalf("pin after delete: want ErrNotFound, got %v", pinErr)
				}
				if len(pinned) != 1 || pinned[0].ID != oldPin.ID {
					t.Fatalf("old pin on %d must survive, got %+v", oldPin.ID, pinned)
				}
			}

			// 通用不变量：目标已删除；置顶数不超过 1
			for _, c := range list {
				if c.ID == target.ID {
					t.Fatalf("target comment %d should be deleted", target.ID)
				}
			}
			if len(pinned) > 1 {
				t.Fatalf("at most one pinned comment, got %d", len(pinned))
			}
		})
	}
	// 两种交错时序都必须真实出现过，否则并发覆盖形同虚设
	if pinFirst == 0 || deleteFirst == 0 {
		t.Fatalf("interleavings not covered: pin first %d times, delete first %d times", pinFirst, deleteFirst)
	}
}

// TestConcurrentPinReplace 同一活动并发替换置顶：多个 goroutine 同时置顶不同评论，
// 全部应成功，最终恰好一条置顶且为其中之一（最后提交者胜出）。
func TestConcurrentPinReplace(t *testing.T) {
	winCount := map[uint64]int{}
	for i := 0; i < 50; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			repo := NewCommentRepository(setupConcurrentCommentTestDB(t))
			comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
			targets := []uint64{comments[0].ID, comments[1].ID, comments[2].ID}

			var wg sync.WaitGroup
			errs := make([]error, len(targets))
			// 轮换启动顺序，使每个目标都有"最后提交胜出"的轮次
			for k := range targets {
				idx := (k + i) % len(targets)
				wg.Add(1)
				go func(k int) {
					defer wg.Done()
					errs[k] = repo.Pin(1, targets[k])
				}(idx)
			}
			wg.Wait()

			for k, err := range errs {
				if err != nil {
					t.Fatalf("pin comment %d: %v", targets[k], err)
				}
			}
			list, err := repo.ListByActivity(1)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			pinned := pinnedComments(list)
			if len(pinned) != 1 {
				t.Fatalf("want exactly 1 pinned comment, got %d", len(pinned))
			}
			found := false
			for _, id := range targets {
				if pinned[0].ID == id {
					found = true
				}
			}
			if !found {
				t.Fatalf("pinned comment %d is not one of the targets %v", pinned[0].ID, targets)
			}
			winCount[pinned[0].ID]++
		})
	}
	// 50 轮中每个目标都应至少胜出一次，证明替换确实在并发竞争中发生
	if len(winCount) != 3 {
		t.Fatalf("expected all 3 targets to win at least once, got %v", winCount)
	}
}

// TestConcurrentUnpinAndDelete 同时取消置顶和移除同一条评论。
// 取消先提交则成功；删除先提交则取消必须返回 ErrNotFound。最终目标已删且无置顶。
func TestConcurrentUnpinAndDelete(t *testing.T) {
	unpinFirst, deleteFirst := 0, 0
	for i := 0; i < 30; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			repo := NewCommentRepository(setupConcurrentCommentTestDB(t))
			comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
			target := comments[2]

			if err := repo.Pin(1, target.ID); err != nil {
				t.Fatalf("pre-pin: %v", err)
			}

			var wg sync.WaitGroup
			var unpinErr, delErr error
			wg.Add(2)
			// 轮换启动顺序，覆盖"取消先提交"与"删除先提交"两种交错
			if i%2 == 0 {
				go func() { defer wg.Done(); unpinErr = repo.Unpin(1, target.ID) }()
				go func() { defer wg.Done(); delErr = repo.Delete(target.ID) }()
			} else {
				go func() { defer wg.Done(); delErr = repo.Delete(target.ID) }()
				go func() { defer wg.Done(); unpinErr = repo.Unpin(1, target.ID) }()
			}
			wg.Wait()

			if delErr != nil {
				t.Fatalf("delete: %v", delErr)
			}
			if unpinErr == nil {
				unpinFirst++
			} else {
				deleteFirst++
				if !errors.Is(unpinErr, ErrNotFound) {
					t.Fatalf("unpin after delete: want ErrNotFound, got %v", unpinErr)
				}
			}

			list, err := repo.ListByActivity(1)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			for _, c := range list {
				if c.ID == target.ID {
					t.Fatalf("target comment %d should be deleted", target.ID)
				}
			}
			if pinned := pinnedComments(list); len(pinned) != 0 {
				t.Fatalf("no comment should remain pinned, got %d", len(pinned))
			}
		})
	}
	// 两种交错时序都必须真实出现过
	if unpinFirst == 0 || deleteFirst == 0 {
		t.Fatalf("interleavings not covered: unpin first %d times, delete first %d times", unpinFirst, deleteFirst)
	}
}

// TestPinRollbackKeepsOldPinOnWriteFailure 数据库写入失败回滚：
// 用触发器强制"置顶目标"这一步写入失败，整个事务必须回滚，旧置顶保留。
func TestPinRollbackKeepsOldPinOnWriteFailure(t *testing.T) {
	db := setupConcurrentCommentTestDB(t)
	repo := NewCommentRepository(db)
	comments := seedComments(t, repo, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	oldPin, target := comments[1], comments[2]

	if err := repo.Pin(1, oldPin.ID); err != nil {
		t.Fatalf("pre-pin: %v", err)
	}

	// 触发器：更新目标评论时强制写入失败，模拟数据库层错误
	trigger := fmt.Sprintf(`CREATE TRIGGER fail_pin_target BEFORE UPDATE ON comments
		WHEN NEW.id = %d BEGIN SELECT RAISE(ABORT, 'forced write failure'); END`, target.ID)
	if err := db.Exec(trigger).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := repo.Pin(1, target.ID)
	if err == nil {
		t.Fatal("pin should fail on forced write error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("want infrastructure error, got ErrNotFound: %v", err)
	}

	// 事务回滚：旧置顶保留，目标未被置顶，恰好一条置顶
	list, err := repo.ListByActivity(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	pinned := pinnedComments(list)
	if len(pinned) != 1 || pinned[0].ID != oldPin.ID {
		t.Fatalf("old pin on %d must survive rollback, got %+v", oldPin.ID, pinned)
	}
}
