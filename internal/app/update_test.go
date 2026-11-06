package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	upd "github.com/holthq/holt/internal/update"
	"github.com/holthq/holt/internal/version"
)

// TestCheckUpdates 检查更新只在点了菜单时查（这里换成桩，不联网）：有新版本问要不要打开发布页、
// 已是最新（包括还没有 release）提示一句、查不了弹错误、开发版不查。
func TestCheckUpdates(t *testing.T) {
	t.Cleanup(func() {
		version.App, latestRelease = "dev", upd.Latest
	})
	version.App = "1.0.0"
	type result struct {
		rel upd.Release
		err error
	}
	next := result{}
	asked := 0
	latestRelease = func(context.Context, string, string) (upd.Release, error) {
		asked++
		return next.rel, next.err
	}
	run := func(t *testing.T) *App {
		t.Helper()
		a := &App{OpenURL: func(string) error { return nil }}
		done := make(chan struct{}, 1)
		a.Update = func(fn func()) { fn(); done <- struct{}{} }
		a.CheckUpdates()
		<-done
		if asked != 1 {
			t.Fatalf("查了 %d 次", asked)
		}
		asked = 0
		return a
	}

	t.Run("有新版本", func(t *testing.T) {
		next = result{rel: upd.Release{Version: "1.1.0", URL: "https://example.com/release"}}
		a := run(t)
		if !a.ConfirmOpen || !strings.Contains(a.Confirm.Title, "1.1.0") || a.Toast != "" {
			t.Fatalf("没有问要不要打开发布页：%+v、toast %q", a.Confirm, a.Toast)
		}
	})
	t.Run("已是最新", func(t *testing.T) {
		next = result{rel: upd.Release{Version: "1.0.0"}}
		if a := run(t); !strings.Contains(a.Toast, "已是最新版本（1.0.0）") || a.ConfirmOpen {
			t.Fatalf("toast %q、问了 %v", a.Toast, a.ConfirmOpen)
		}
	})
	t.Run("还没有 release", func(t *testing.T) {
		next = result{err: upd.ErrNoRelease}
		if a := run(t); !strings.Contains(a.Toast, "已是最新版本") || a.AlertOpen {
			t.Fatalf("toast %q、报错 %v", a.Toast, a.AlertOpen)
		}
	})
	t.Run("查不了", func(t *testing.T) {
		next = result{err: errors.New("网络不通")}
		a := run(t)
		if !a.AlertOpen || a.AlertTitle != "检查更新失败" || a.Toast != "" {
			t.Fatalf("没有报错：%q %q %v", a.AlertTitle, a.Toast, a.AlertOpen)
		}
	})
	t.Run("开发版", func(t *testing.T) {
		version.App = "dev"
		a := &App{Update: func(fn func()) { fn() }}
		a.CheckUpdates()
		if asked != 0 || !strings.Contains(a.Toast, "开发版不检查更新") {
			t.Fatalf("开发版也查了（%d 次）：%q", asked, a.Toast)
		}
	})
}
