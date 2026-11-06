package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
)

// TestScreenshotProblems 是检查与问题面板的截图（WORKBENCH_SHOT=目录 时才跑），临时仓库 shop（Go、shell）和 web（TypeScript）；
// 检查命令是临时目录里叫 go / tsc / shellcheck 的假脚本（只打印几行，不跑真工具）：
//
//	problems        底栏问题页 5 条、两个仓库；当前编辑器 cart.go 里两条波浪线；状态栏的计数。浅色、深色各一张
//	settings-lint   设置 → 格式化 底下的“保存时检查”（开着）和检查命令表：TypeScript 改过、Rust 写了 -，别的是灰字默认（只拍浅色）
func TestScreenshotProblems(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now()
	shop := testutil.NewRepo(t, "shop")
	shop.Write("src/cart/cart.go", "package cart\n\nimport \"fmt\"\n\n// Total 把购物车里的价钱加起来。\nfunc Total(prices []int) int {\n\tsum := 0\n\ttotal := 0\n\tfor _, p := range prices {\n\t\tsum += p\n\t}\n\t// 记一笔日志\n\tfmt.Sprintf(\"合计 %d\", sum)\n\treturn sum\n}\n")
	shop.Write("src/main.go", "package main\n\nfunc main() {}\n")
	shop.Write("deploy.sh", "#!/bin/sh\nset -e\ntarget=$1\nrsync -a dist/ $target\n")
	shop.CommitAt("初始", now.Add(-5*time.Hour))
	web := testutil.NewRepo(t, "web")
	web.Write("src/api.ts", "export function price(id: string): number {\n  const p = lookup(id)\n  return p\n}\n")
	web.Write("src/util.ts", "import path from 'node:path'\n\nexport const sep = '/'\n")
	web.CommitAt("初始", now.Add(-3*time.Hour))
	bin := t.TempDir()
	fake := func(name, out string) string {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\ncat <<'EOF'\n"+out+"EOF\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, tt, wait := newUI(t, testutil.Workspace(t, shop.Root, web.Root), termState(t, false))
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	s := &a.St.Settings
	s.Linters = map[string]string{
		"Go": fake("go", "# shop/src/cart\nvet: src/cart/cart.go:8:2: declared and not used: total\nsrc/cart/cart.go:13:2: result of fmt.Sprintf call not used\n") + " vet ./{dir}",
		"TypeScript": fake("tsc", "src/api.ts(3,3): error TS2322: Type 'string' is not assignable to type 'number'.\n"+
			"src/util.ts(1,8): error TS6133: 'path' is declared but its value is never read.\n") + " --noEmit --pretty false",
		"Shell": fake("shellcheck", "deploy.sh:4:15: note: Double quote to prevent globbing and word splitting. [SC2086]\n") + " -f gcc {file}",
	}
	r1, r2 := a.Repos[0], a.Repos[1]
	sh := a.OpenFile(r1, "deploy.sh", true)
	api := a.OpenFile(r2, "src/api.ts", true)
	cart := a.OpenFile(r1, "src/cart/cart.go", true)
	wait("读好", func() bool {
		tt.Frame()
		return cart.Buf != nil && !cart.Loading && sh.Buf != nil && !sh.Loading && api.Buf != nil && !api.Loading
	})
	for _, d := range []*docs.Doc{sh, api, cart} {
		a.Lint(d)
		wait("检查完", func() bool { return len(a.LintRunning()) == 0 })
	}
	list, _, _ := a.ProblemList()
	if len(list) != 5 {
		t.Fatalf("问题 %d 条：%+v", len(list), list)
	}
	a.St.RightBar = false // 右栏的历史和这版无关，腾地方
	a.Exec("view.problems")
	a.ProbSel = 1 // 选中 cart.go:8 那条、开到那一行（固定）
	a.OpenProblem(list[1], true)
	tt.Move(1, 1)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		tt.Frame()
		name := "problems-light.png"
		if dark {
			name = "problems-dark.png"
		}
		writeShot(t, tt, dir, name)
	}

	s.LintOnSave = true
	s.Linters = map[string]string{"TypeScript": "npx tsc --noEmit --pretty false", "Rust": "-"} // 一条改过的、一条关掉的，别的灰字默认
	sv := wui.NewTester(SettingsView(a), 760, 640)
	sv.SetScale(2)
	sv.SetLocale(i18n.Locale())
	mustClick(t, sv, "格式化")
	if at, ok := sv.Find("保存时检查"); ok {
		sv.Scroll(360, 320, 0, at.Y-90)
	}
	sv.Frame()
	sv.Frame()
	if at, ok := sv.Find("保存时检查"); !ok || at.Y < 40 || at.Y > 320 {
		t.Errorf("没滚到检查那两行：%+v %v", at, ok)
	}
	writeShot(t, sv, dir, "settings-lint-light.png")
}
