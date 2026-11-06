package ui

import (
	"path/filepath"
	"testing"

	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 上次关窗口时 a.txt 滚到第 21 行、光标在第 26 行：再打开同一个工作区，标签回来，
// 编辑器排好版后滚回第 21 行，光标还在第 26 行；文件树里仓库展开着。
func TestRestoreTabs(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	st := workspace.Default()
	st.Sessions = map[string]*workspace.Session{ws: {
		Tabs:   []workspace.Tab{{Path: filepath.Join(root, "README.md")}, {Path: filepath.Join(root, "a.txt"), Pinned: true, Line: 25, Top: 20}},
		Active: 1, Repo: root, Open: []string{root},
	}}
	if err := workspace.SaveState(stateDir, st); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := newUI(t, ws, stateDir)
	a.Start()
	wait("标签回来、滚回去", func() bool {
		d := a.CurrentDoc()
		if d == nil || d.Buf == nil || d.Title != "a.txt" {
			return false
		}
		first, _ := d.St.VisibleLines()
		return d.Top == 0 && first == 20 && tt.HasText("README.md")
	})
	tt.Frame()
	d := a.CurrentDoc()
	if first, _ := d.St.VisibleLines(); first != 20 {
		t.Fatalf("又滚走了：第一行 %d", first)
	}
	if p, _ := d.Cursor(); p.Line != 25 {
		t.Fatalf("光标在第 %d 行", p.Line+1)
	}
	if !a.Repos[0].Expanded {
		t.Fatal("仓库没展开")
	}
	wait("文件树", func() bool { return a.Repos[0].Files.Loaded && tt.HasText("u.txt") })
}
