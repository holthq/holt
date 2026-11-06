package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
)

// TestScreenshotEditorExtras 是编辑器小功能的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark），临时仓库 shop、tools：
//
//	sticky   shop.py 滚到 checkout 中间：顶上吸两行 class Shop: › def checkout(self):
//	zen      禅模式：三个面板全关，编辑器组在 1440 宽的窗口中间
//	symbols  ⌘T 打 check：两个仓库里的 checkout / check_all，右边"仓库/路径:行号"
func TestScreenshotEditorExtras(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	var py strings.Builder
	py.WriteString("class Shop:\n    \"\"\"购物车。\"\"\"\n\n    def checkout(self, cart):\n")
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&py, "        total = total + cart[%d]  # 第 %d 件\n", i, i)
	}
	py.WriteString("        return total\n\n    def refund(self, order):\n        return order.amount\n")
	shop := testutil.NewRepo(t, "shop")
	shop.Write("shop.py", py.String())
	shop.Write("cart/cart.go", "package cart\n\ntype Cart struct{}\n\nfunc (c *Cart) Checkout() error { return nil }\n\nfunc CheckStock(sku string) bool { return true }\n")
	shop.Write("README.md", "# Shop\n\n## Checkout flow\n")
	shop.Commit("初始")
	tools := testutil.NewRepo(t, "tools")
	tools.Write("lint.py", "def check_all(paths):\n    pass\n\nclass Checker:\n    def run(self):\n        pass\n")
	tools.Commit("初始")

	a, tt, wait := newUI(t, testutil.Workspace(t, shop.Root, tools.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
		wait("git status", idle(r))
	}
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
	d := a.OpenFile(a.Repos[0], "shop.py", true)
	wait("读 shop.py", func() bool { return d.Buf != nil && !d.Loading })
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		tt.Frame()
		d.St.SetScrollY(d.St.LineTop(40))
		tt.Frame()
		shot("sticky", dark)

		a.ToggleZen()
		tt.Frame()
		d.St.SetScrollY(d.St.LineTop(40))
		shot("zen", dark)
		a.ToggleZen()

		a.OpenPalette("#check")
		wait("建索引", func() bool { // 两个仓库都建完：checkout、Checkout flow、Checkout、CheckStock、check_all、Checker
			a.UpdatePalette()
			return len(a.Palette.Items) >= 6 && !strings.HasPrefix(a.Palette.Note, "正在建索引")
		})
		a.UpdatePalette()
		shot("symbols", dark)
		a.Palette.Open = false
	}
}
