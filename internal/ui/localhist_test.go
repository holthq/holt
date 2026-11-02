package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	lhist "github.com/lemoeade/grove/internal/history"
	"github.com/lemoeade/grove/internal/testutil"
)

// 本地历史比较标签的面包屑是工作区里的那个文件（仓库 › 目录 › 文件），不是状态目录里的内容文件。
func TestLocalCompareBreadcrumb(t *testing.T) {
	r0 := testutil.NewRepo(t, "crumbs")
	r0.Write("src/a.go", "package a\n")
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	s := a.LocalHist()
	if s == nil {
		t.Fatal("没有本地历史")
	}
	abs := filepath.Join(r0.Root, "src", "a.go")
	if _, err := s.Record(abs, []byte("package a // 旧的\n"), lhist.WhySave); err != nil {
		t.Fatal(err)
	}
	e, ok := s.Load(abs).Latest()
	if !ok {
		t.Fatal("没记上")
	}
	d := a.OpenLocalCompare(abs, e, true)
	wait("比较", func() bool { return d != nil && !d.Loading })
	tt.Frame()
	if _, ok := tt.Find("位置 crumbs/src/a.go"); !ok {
		t.Errorf("面包屑该是 crumbs › src › a.go：%q", tt.Texts())
	}
	for _, x := range tt.Texts() {
		if strings.Contains(x, s.Dir) {
			t.Errorf("面包屑里有本地历史的目录：%q", x)
		}
	}
}
