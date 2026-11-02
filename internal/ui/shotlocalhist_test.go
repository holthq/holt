package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	lhist "github.com/lemoeade/grove/internal/history" // ui 里已经有 history（右栏）
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestScreenshotLocalHistory 是本地历史 + 快捷键的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套，临时仓库 shop：
//
//	local-history         右栏 cart.go 的文件历史：本地的三条（保存、恢复前、磁盘上改了）混排在两个提交中间和上面
//	local-compare         本地历史 ↔ 工作区的只读比较标签：说明条上的“恢复到这一份”
//	settings-keys         设置 → 快捷键：分组、键帽、搜索框
//	key-recorder          录键卡片：按了 ⌘K，写着被谁占了
//	gitbar-untracked-dir  左栏 Git：未跟踪的 drafts/ 展开、两个子行（选中一个，开着它的 Diff），旁边一个未跟踪文件
func TestScreenshotLocalHistory(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now()
	shop := testutil.NewRepo(t, "shop")
	shop.Write("src/main.go", "package main\n\nimport \"fmt\"\n\n// main 打个招呼。\nfunc main() {\n\tfmt.Println(\"shop\")\n}\n")
	shop.Write("src/cart/cart.go", "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n")
	shop.Write("README.md", "# Shop\n\n一个小店。\n")
	shop.CommitAt("初始", now.Add(-5*time.Hour))
	shop.Write("src/cart/cart.go", "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n\n// Add 放一件。\nfunc (c *Cart) Add(s string) { c.Items = append(c.Items, s) }\n")
	shop.CommitAt("购物车能放东西", now.Add(-3*time.Hour))
	cart := "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n\n// Add 放一件。\nfunc (c *Cart) Add(s string) { c.Items = append(c.Items, s) }\n\n// Len 是件数。\nfunc (c *Cart) Len() int { return len(c.Items) }\n"
	shop.Write("src/cart/cart.go", cart)
	shop.Write("notes.txt", "待办\n")
	shop.Write("drafts/plan.md", "# 计划\n\n- 结账\n- 优惠券\n")
	shop.Write("drafts/ideas/coupon.md", "# 优惠券\n\n满 100 减 10。\n")
	ws := testutil.Workspace(t, shop.Root)
	suffix := func(dark bool) string {
		if dark {
			return "-dark.png"
		}
		return "-light.png"
	}
	shots := func(tt *wui.Tester, name string) {
		t.Helper()
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			writeShot(t, tt, dir, name+suffix(dark))
		}
	}

	a, tt, wait := newUI(t, ws, termState(t, false)) // 底栏关着、shell 是 /bin/sh
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", func() bool { return idle(r)() && r.Status.DirFiles["drafts/"] != nil })

	abs := filepath.Join(shop.Root, "src", "cart", "cart.go")
	s := a.LocalHist()
	var last lhist.Entry
	for _, e := range []struct {
		ago  time.Duration
		why  string
		body string
	}{
		{4 * time.Hour, lhist.WhySave, "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string; n int }\n"},
		{2 * time.Hour, lhist.WhyRestore, "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n\n// Add 放一件。\nfunc (c *Cart) Add(s string) { c.Items = append(c.Items, s) }\n\n// Clear 清空。\nfunc (c *Cart) Clear() { c.Items = nil }\n"},
		{25 * time.Minute, lhist.WhyDisk, "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n\n// Add 放一件。\nfunc (c *Cart) Add(s string) { c.Items = append(c.Items, s) }\n\n// Len 是件数。\nfunc (c *Cart) Len() int { return 0 }\n"},
	} {
		at := now.Add(-e.ago)
		s.Now = func() time.Time { return at }
		if _, err := s.Record(abs, []byte(e.body), e.why); err != nil {
			t.Fatal(err)
		}
	}
	s.Now = nil
	last, _ = s.Load(abs).Latest()
	a.ExpandChanged(r)
	a.OpenFile(r, "src/cart/cart.go", true)
	wait("cart.go 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil && !d.Loading })
	a.ShowLocalHistory(abs)
	wait("文件历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading && r.Hist.Done && len(r.Hist.Local) == 3 })
	shots(tt, "local-history")

	d := a.OpenLocalCompare(abs, last, true)
	wait("比较", func() bool { return d != nil && !d.Loading && d.DiffSt != nil })
	shots(tt, "local-compare")

	a.ToggleUntrackedDir(r, "drafts/")
	a.ShowSidebar(workspace.ModeGit)
	a.ShowDiffFor(r, "drafts/plan.md")
	wait("drafts/plan.md 的 diff", func() bool { d := a.CurrentDoc(); return d != nil && !d.Loading && d.Change.Path == "drafts/plan.md" })
	shots(tt, "gitbar-untracked-dir")

	sv := wui.NewTester(SettingsView(a), 720, 560)
	sv.SetScale(2)
	sv.SetLocale(i18n.Locale())
	sv.Frame()
	mustClick(t, sv, "快捷键")
	shots(sv, "settings-keys")
	mustClick(t, sv, "命令面板…")
	mustClick(t, sv, "改…")
	sv.Key(wui.Cmd, wui.KeyK)
	shots(sv, "key-recorder")
	sv.Key(0, wui.KeyEscape)
	sv.Frame()
}
