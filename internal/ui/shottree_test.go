package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestScreenshotTreeCompare 是文件树 + 窗口小功能的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark），临时仓库 shop：
//
//	exclude       .vscode/settings.json 的 files.exclude 藏掉 node_modules，悬停仓库行“按 files.exclude 藏了 2 个”
//	dirtip        悬停 src：“4 个文件 · 共 … · 最近改动 …”
//	compare       src/cart.go ↔ src/legacy/cart_old.go 的比较标签
//	compare-clip  src/price.go ↔ 剪贴板
//	git-terminal  >git commit 之后：底栏终端里开着编辑器（测试里是不读配置的 vi）
func TestScreenshotTreeCompare(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	t.Setenv("GIT_EDITOR", "vi -u NONE -i NONE -n --cmd 'set shm+=F'") // 不读用户的配置、不写 viminfo 和交换文件，不报文件名（临时目录长，会折行停下来等回车）
	var cart, old strings.Builder
	cart.WriteString("package shop\n\n// Cart 是购物车。\ntype Cart struct {\n\tItems []Item\n\tCoupon string\n}\n\n")
	old.WriteString("package shop\n\n// Cart 是购物车。\ntype Cart struct {\n\tItems []Item\n}\n\n")
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&cart, "// Step%d 是结账的第 %d 步。\nfunc (c *Cart) Step%d() error {\n\treturn c.check(%d)\n}\n\n", i, i, i, i)
		fmt.Fprintf(&old, "// Step%d 是结账的第 %d 步。\nfunc (c *Cart) Step%d() error {\n\treturn nil\n}\n\n", i, i, i)
	}
	price := "package shop\n\n// Price 是含税价。\nfunc Price(cents int) int {\n\treturn cents * 106 / 100\n}\n"
	shop := testutil.NewRepo(t, "shop")
	shop.Write(".vscode/settings.json", "{\n  // 树上不列依赖\n  \"files.exclude\": {\"**/node_modules\": true},\n}\n")
	shop.Write("node_modules/left-pad/index.js", "module.exports = s => s\n")
	shop.Write("node_modules/left-pad/package.json", "{}\n")
	shop.Write("src/cart.go", cart.String())
	shop.Write("src/price.go", price)
	shop.Write("src/legacy/cart_old.go", old.String())
	shop.Write("src/legacy/README.md", "老的结账流程，留着对照。\n")
	shop.Write("README.md", "# Shop\n")
	shop.Commit("初始")
	shop.Write("src/price.go", strings.Replace(price, "106", "108", 1))
	shop.Git("add", "src/price.go")

	stateDir := t.TempDir()
	st := workspace.Default()
	st.Settings.Shell, st.BottomBar, st.SidebarMode = "/bin/sh", false, workspace.ModeFiles
	if err := workspace.SaveState(stateDir, st); err != nil {
		t.Fatal(err)
	}
	clip := app.ReadClipboard
	app.ReadClipboard = func() string { return strings.Replace(price, "106", "110", 1) + "\n// 促销价另算\n" }
	t.Cleanup(func() { app.ReadClipboard = clip })

	a, tt, wait := newUI(t, testutil.Workspace(t, shop.Root), stateDir)
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	r.Expanded = true
	r.Files.SetOpen("src", true)
	a.EnsureFiles(r)
	wait("文件列表", func() bool { return idle(r)() && r.Files.Loaded && r.Excluded == 2 })
	shot := func(name string, dark bool) {
		t.Helper()
		tt.Frame()
		tt.Frame()
		suffix := "-light.png"
		if dark {
			suffix = "-dark.png"
		}
		writeShot(t, tt, dir, name+suffix)
	}
	hover := func(text string) {
		t.Helper()
		tt.Frame()
		b, ok := tt.Find(text)
		if !ok {
			t.Fatalf("找不到 %q", text)
		}
		tt.Move(b.X+b.W/2, b.Y+b.H/2)
		tt.Advance(2 * time.Second)
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		a.St.BottomBar = false
		tt.Frame()

		hover("shop")
		shot("exclude", dark)
		wait("算好大小", func() bool {
			for _, row := range a.TreeRows() {
				if row.Path == "src" && row.Dir {
					return strings.Contains(a.RowTip(row), "最近改动")
				}
			}
			return false
		})
		hover("src")
		shot("dirtip", dark)
		tt.Move(900, 500)

		cmp := a.CompareFiles(filepath.Join(shop.Root, "src", "cart.go"), filepath.Join(shop.Root, "src", "legacy", "cart_old.go"))
		wait("读比较", func() bool { return cmp != nil && !cmp.Loading })
		shot("compare", dark)
		clipDoc := a.CompareClipboard(r, "src/price.go")
		wait("读剪贴板比较", func() bool { return clipDoc != nil && !clipDoc.Loading })
		shot("compare-clip", dark)

		a.St.BottomH = 400
		if !dark {
			a.RunCommand(r, "commit")
		} else {
			a.St.BottomBar = true
		}
		_, _, p := a.ActivePane()
		wait("编辑器开了", func() bool {
			tt.Frame()
			return p.Term != nil && strings.Contains(p.Term.Text(), "# Please enter the commit message")
		})
		shot("git-terminal", dark)
	}
}
