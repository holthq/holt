package ui

import (
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/testutil"
)

// ⌘T 工作区符号：带着 "#" 打开面板，"#main" 列出两个仓库里的 main（名字、签名、仓库/路径:行号），
// 回车打开那个文件跳到那一行。
func TestPaletteWorkspaceSymbols(t *testing.T) {
	one := testutil.NewRepo(t, "one")
	one.Write("main.go", "package main\n\nfunc main() {}\n")
	one.Commit("初始")
	two := testutil.NewRepo(t, "two")
	two.Write("tool.py", "import os\n\ndef main(argv):\n    pass\n")
	two.Commit("初始")
	a, tt, wait := newUI(t, testutil.Workspace(t, one.Root, two.Root), "")
	p := &a.Palette

	tt.Key(wui.Cmd, wui.KeyT)
	tt.Frame()
	if !p.Open || p.Text != "#" || !tt.Focused("快速打开或命令") {
		t.Fatalf("⌘T 应带着 # 打开面板：open %v text %q", p.Open, p.Text)
	}
	tt.Type("main")
	want := []string{"main，在 one/main.go:3", "main，在 two/tool.py:3"}
	wait("两个仓库的 main", func() bool {
		tt.Frame()
		texts := tt.Texts()
		return slices.Contains(texts, want[0]) && slices.Contains(texts, want[1])
	})
	mustText(t, tt, "two/tool.py:3")
	accessCheck(t, tt, "工作区符号")
	tt.Key(0, wui.KeyDown)
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	d := a.CurrentDoc()
	if p.Open || d == nil || d.Rel != "tool.py" {
		t.Fatalf("回车没打开 tool.py：open %v %+v", p.Open, d)
	}
	wait("读 tool.py", func() bool { return d.Buf != nil })
	if pos, _ := d.Cursor(); pos.Line != 2 {
		t.Errorf("光标在 %+v，应在 def main 那行", pos)
	}
}
