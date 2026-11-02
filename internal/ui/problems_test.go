package ui

import (
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
)

// 问题面板（临时仓库，检查命令是 shell 一行）：两个仓库的问题一起列、按 仓库 → 文件 → 行 排；底栏标签和状态栏数
// 错误 + 警告（提示不算）；单击开到那一行（预览标签）、编辑器里有诊断，双击固定；⇧⌘M 开关问题页；编辑器右键“检查”；清空。
func TestProblemsPanel(t *testing.T) {
	r1 := testutil.NewRepo(t, "shop")
	r1.Write("cart.go", "package shop\n\nfunc Add(x int) {\n\tvar unused int\n}\n")
	r1.Write("main.go", "package shop\nvar shadow = 1\n")
	r1.Commit("base")
	r2 := testutil.NewRepo(t, "lib")
	r2.Write("util.go", "package lib\n")
	r2.Commit("base")
	a, tt, wait := newUI(t, testutil.Workspace(t, r1.Root, r2.Root), "")
	s := &a.St.Settings
	s.Shell = "/bin/sh"
	// 每个仓库根下跑：不在这个仓库里的文件（stat 不到）自己丢掉
	s.Linters = map[string]string{"Go": "printf 'cart.go:4:6: declared and not used: unused\\nmain.go:2:5: warning: shadow\\nutil.go:1:1: note: just a note\\n'; exit 1"}
	shop, lib := a.RepoFor(r1.Root), a.RepoFor(r2.Root)
	main := a.OpenFile(shop, "main.go", true)
	util := a.OpenFile(lib, "util.go", true)
	wait("读好", func() bool { tt.Frame(); return main.Buf != nil && util.Buf != nil && !main.Loading && !util.Loading })
	idle := func() bool { return len(a.LintRunning()) == 0 }

	tt.Key(wui.Shift|wui.Cmd, wui.KeyM)
	tt.Frame()
	if !a.St.BottomBar || a.BottomTab != app.BottomProblems {
		t.Fatalf("⇧⌘M 没开问题页：底栏 %v，页 %d", a.St.BottomBar, a.BottomTab)
	}
	mustText(t, tt, "没有问题")

	a.Lint(util)
	tt.Frame()
	mustText(t, tt, "正在检查 printf…")
	wait("lib 检查完", idle)
	mustRightClick(t, tt, "编辑 util.go", "检查") // util.go 是当前标签；问题还是那一条
	wait("又检查完", idle)
	a.Lint(main)
	wait("shop 检查完", idle)
	tt.Frame()
	list, errs, warns := a.ProblemList()
	var got []string
	for _, q := range list {
		got = append(got, a.ProblemPlace(q))
	}
	if strings.Join(got, "|") != "shop · cart.go:4:6|shop · main.go:2:5|lib · util.go:1:1" || errs != 1 || warns != 1 {
		t.Fatalf("问题：%q（错误 %d 警告 %d）", got, errs, warns)
	}
	mustText(t, tt, "问题 2")
	above(t, tt, "declared and not used: unused", "shadow")
	above(t, tt, "shadow", "just a note")
	a.MoveRepo(lib, 0) // 侧栏拖动仓库：面板马上按新顺序排，不等下一次检查
	tt.Frame()
	if list, _, _ := a.ProblemList(); len(list) != 3 || a.ProblemPlace(list[0]) != "lib · util.go:1:1" {
		t.Fatalf("lib 挪到前面以后：%+v", list)
	}
	above(t, tt, "just a note", "declared and not used: unused")
	a.MoveRepo(lib, 1)
	tt.Frame()
	above(t, tt, "shadow", "just a note")
	if !shows(tt, i18n.Tn("%d 个问题", 2)) {
		t.Errorf("状态栏没有计数；texts=%q", tt.Texts())
	}

	mustClick(t, tt, "declared and not used: unused")
	wait("开到 cart.go", func() bool {
		tt.Frame()
		d := a.CurrentDoc()
		return d != nil && d.Rel == "cart.go" && d.Buf != nil && !d.Loading
	})
	tt.Frame()
	d := a.CurrentDoc()
	if p := d.St.CursorPos(); p != (editor.Pos{Line: 3, Col: 5}) || d.Pinned {
		t.Errorf("单击：光标 %+v，固定 %v", p, d.Pinned)
	}
	if ds := d.St.Diagnostics(); len(ds) != 1 || d.Buf.String()[ds[0].Start:ds[0].End] != "unused" {
		t.Errorf("编辑器里的诊断：%+v", ds)
	}
	at, ok := tt.Find("declared and not used: unused")
	if !ok {
		t.Fatal("行没了")
	}
	tt.ClickAt(at.X+at.W/2, at.Y+at.H/2)
	tt.ClickAt(at.X+at.W/2, at.Y+at.H/2)
	tt.Frame()
	if !a.CurrentDoc().Pinned {
		t.Error("双击没固定")
	}

	tt.Key(wui.Shift|wui.Cmd, wui.KeyM)
	tt.Frame()
	if a.St.BottomBar {
		t.Fatal("在问题页时 ⇧⌘M 没关底栏")
	}
	a.BottomTab = app.BottomOutput
	a.St.BottomBar = true
	tt.Frame()
	tt.Key(wui.Shift|wui.Cmd, wui.KeyM) // 底栏开在别的页：换到问题页，不关
	tt.Frame()
	if !a.St.BottomBar || a.BottomTab != app.BottomProblems {
		t.Fatal("底栏开在输出页时 ⇧⌘M 没换到问题页")
	}

	mustClick(t, tt, "清空")
	tt.Frame()
	if a.HasProblems() || !tt.HasText("没有问题") || shows(tt, i18n.Tn("%d 个问题", 2)) || len(a.CurrentDoc().St.Diagnostics()) != 0 {
		t.Errorf("清空以后：%v %q", a.HasProblems(), tt.Texts())
	}
	mustText(t, tt, "问题")
}
