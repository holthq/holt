package ui

import (
	"strconv"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
)

// 界面测试用 Tester 不开窗口画整个界面：后台回调排进队列，由 wait 推帧时在测试 goroutine 上跑掉。
// 会改仓库的测试只在 testutil.TempRepo 建的临时仓库上做；用户的真实工作区只做只读的 status / diff / log。

// newUI 建一个读 ws 的 App 和画它的 Tester（1440×880）。stateDir 为空就用临时目录。
func newUI(t *testing.T, ws, stateDir string) (*app.App, *wui.Tester, func(what string, cond func() bool)) {
	t.Helper()
	if stateDir == "" {
		stateDir = t.TempDir()
	}
	a, err := app.New(ws, stateDir)
	if err != nil {
		t.Skipf("读不了工作区 %s: %v", ws, err)
	}
	a.LangOff = true // 界面测试里不起真的语言服务器
	q := testutil.NewQueue()
	a.Update = q.Post
	// 兜底：测试里不开浏览器、不开访达（要看开了什么的测试自己换成记下来的）
	a.OpenURL = func(string) error { return nil }
	a.ShowPath = func(string) error { return nil }
	a.InFinder = func(string) {}
	keepCopy := app.CopyFiles // 文件树的 ⌘C 不碰真的系统剪贴板
	app.CopyFiles = func([]string) {}
	t.Cleanup(func() { app.CopyFiles = keepCopy })
	t.Cleanup(a.Close)
	tt := wui.NewTester(View(a), 1440, 880)
	tt.SetLocale(i18n.Locale())
	return a, tt, testutil.Waiter(t, tt, q, 20*time.Second)
}

// loaded 是所有仓库都读完了状态。
func loaded(a *app.App) func() bool {
	return func() bool {
		for _, r := range a.Repos {
			if !r.Loaded || r.Loading {
				return false
			}
		}
		return true
	}
}

// idle 是仓库没有在跑的操作、也没有在读状态。
func idle(r *app.Repo) func() bool {
	return func() bool { return r.Busy == "" && len(r.Queue) == 0 && !r.Loading && r.Loaded }
}

func mustClick(t *testing.T, tt *wui.Tester, s string) {
	t.Helper()
	if err := tt.Click(s); err != nil {
		t.Fatalf("点 %q: %v；texts=%q", s, err, tt.Texts())
	}
	tt.Frame()
}

func mustRightClick(t *testing.T, tt *wui.Tester, s string, item ...string) {
	t.Helper()
	if err := tt.RightClick(s); err != nil {
		t.Fatalf("右键 %q: %v", s, err)
	}
	tt.Frame()
	if err := chooseMenu(t, tt, item...); err != nil {
		t.Fatalf("右键 %q 选 %q: %v；菜单=%q", s, item, err, shownMenu(t, tt))
	}
	tt.Frame()
	tt.Frame()
}

func mustText(t *testing.T, tt *wui.Tester, s string) {
	t.Helper()
	if !tt.HasText(s) {
		t.Fatalf("界面上没有 %q；texts=%q", s, tt.Texts())
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// showGroups 打开设置里的"在 Git 栏显示工作树 / 贮藏"（默认都关），和在设置窗口里拨开关一样走 SettingsChanged：
// 测工作树、贮藏的行、截图的测试在 setup 里调。
func showGroups(a *app.App) {
	a.St.Settings.ShowWorktrees, a.St.Settings.ShowStashes = true, true
	a.SettingsChanged()
}
