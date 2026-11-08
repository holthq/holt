package app

import (
	"strings"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// TestRunShell：命令面板 "$ 命令" 在仓库根、login shell 里跑；输出和退出码进底栏的输出，
// 失败时 `路径:行:列: 信息` 进问题面板，成功时清掉上次的；停止能停掉。
func TestRunShell(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()

	a.RunShell(r, `printf 'hello\n'`)
	wait("跑完", func() bool { return !a.Run.Running })
	if n := len(a.Output); n == 0 || a.Output[n-1].Text != "hello" || a.Output[n-1].Failed {
		t.Fatalf("输出：%+v", a.Output)
	}
	if a.Toast != "运行完成" {
		t.Fatalf("toast %q", a.Toast)
	}

	// 失败：编译错误进问题面板
	a.RunShell(r, `printf 'main.go:3:2: undefined: x\n'; exit 2`)
	wait("失败跑完", func() bool { return !a.Run.Running })
	if n := len(a.Output); n == 0 || !a.Output[n-1].Failed {
		t.Fatalf("失败没记成失败：%+v", a.Output)
	}
	list, _, _ := a.ProblemList()
	if len(list) == 0 || list[0].Rel != "main.go" || list[0].Line != 3 || list[0].Col != 2 {
		t.Fatalf("编译错误没进问题面板：%+v", list)
	}
	if !strings.Contains(a.Toast, "运行失败：退出码 2") {
		t.Fatalf("toast %q", a.Toast)
	}

	// 再成功一次：上次的编译错误清掉
	a.RunShell(r, "true")
	wait("成功跑完", func() bool { return !a.Run.Running })
	if list, _, _ := a.ProblemList(); len(list) != 0 {
		t.Fatalf("成功之后问题还在：%+v", list)
	}

	// 停掉
	a.RunShell(r, "sleep 10")
	wait("在跑", func() bool { return a.Run.Running })
	a.StopRun()
	wait("停掉", func() bool { return !a.Run.Running })
	if a.Toast != "已停止" {
		t.Fatalf("停掉的 toast %q", a.Toast)
	}
}

// TestRunPalette："$ 命令" 的面板项：有命令时一项，跑它；没写命令时只有提示。
func TestRunPalette(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, _ := newTestApp(t, ws)
	a.UpdatePalette()
	a.Palette.Text = "$ echo hi"
	a.UpdatePalette()
	if len(a.Palette.Items) != 1 || a.Palette.Items[0].Kind != ItemRun || a.Palette.Items[0].args != "echo hi" {
		t.Fatalf("$ 命令的项：%+v", a.Palette.Items)
	}
	a.Palette.Text = "$ "
	a.UpdatePalette()
	if len(a.Palette.Items) != 0 || a.Palette.Note == "" {
		t.Fatalf("没写命令：%+v %q", a.Palette.Items, a.Palette.Note)
	}
}
