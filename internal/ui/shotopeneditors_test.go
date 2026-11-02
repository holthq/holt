package ui

import (
	"os"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestScreenshotOpenEditors 是"打开的编辑器"的截图（WORKBENCH_SHOT=目录 时才跑），临时仓库 shop：
//
//	open-editors        左栏文件模式上面的“打开的编辑器”：一组四个标签（当前的 cart.go 改过、README.md 是预览的斜体、
//	                    cart.go 的 Diff），灰字是仓库 / 目录，鼠标在头上（露出全部保存、关闭全部）；浅色、深色各一张
//	open-editors-split  拆成两组：“第 1 组 / 第 2 组”，键盘在第二组（只拍浅色）
func TestScreenshotOpenEditors(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now()
	shop := testutil.NewRepo(t, "shop")
	shop.Write("src/main.go", "package main\n\nimport \"fmt\"\n\n// main 打个招呼。\nfunc main() {\n\tfmt.Println(\"shop\")\n}\n")
	shop.Write("src/cart/cart.go", "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n")
	shop.Write("README.md", "# Shop\n\n一个小店。\n")
	shop.Write("notes.txt", "待办\n")
	shop.CommitAt("初始", now.Add(-5*time.Hour))
	shop.Write("src/cart/cart.go", "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n\n// Add 放一件。\nfunc (c *Cart) Add(s string) { c.Items = append(c.Items, s) }\n")
	ws := testutil.Workspace(t, shop.Root)

	a, tt, wait := newUI(t, ws, termState(t, false)) // 底栏关着、shell 是 /bin/sh
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.ShowSidebar(workspace.ModeFiles)
	a.St.Settings.ShowOpenEditors = true
	a.OpenFile(r, "src/main.go", true)
	a.ShowDiffFor(r, "src/cart/cart.go")
	a.CurrentDoc().Pinned = true
	a.OpenFile(r, "README.md", false)
	cart := a.OpenFile(r, "src/cart/cart.go", true)
	wait("cart.go 打开", func() bool { tt.Frame(); return cart.Buf != nil && !cart.Loading })
	mustClick(t, tt, "编辑 cart.go")
	cart.St.Select(0, 0)
	tt.Type("// TODO 优惠券\n")
	wait("文件树", func() bool { tt.Frame(); return a.TreeRowH > 0 && cart.Modified() })
	if h, ok := tt.Find("打开的编辑器"); ok { // 鼠标在侧栏上：头上的全部保存、关闭全部露出来
		tt.Move(h.X+h.W+40, h.Y+h.H/2)
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		tt.Frame()
		name := "open-editors-light.png"
		if dark {
			name = "open-editors-dark.png"
		}
		writeShot(t, tt, dir, name)
	}

	tt.SetDark(false)
	a.OpenFileSide(r, "notes.txt")
	wait("notes.txt 在第二组", func() bool {
		tt.Frame()
		return len(a.Groups()) == 2 && a.CurrentDoc() != nil && a.CurrentDoc().Buf != nil
	})
	tt.Move(1, 1)
	tt.Frame()
	tt.Frame()
	writeShot(t, tt, dir, "open-editors-split-light.png")
}
