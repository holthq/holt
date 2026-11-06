package ui

import (
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
)

// 标签右边那一格（#17）：有没保存的改动时当前标签也是一个点、没有 ×；鼠标移到标签上变成 ×，点了照旧关（会问）；
// 保存以后当前标签回到 ×。没改动的非当前标签悬停才有 ×。那一格总占着，悬停前后标签一样宽。
func TestTabDirtyDot(t *testing.T) {
	r0 := testutil.NewRepo(t, "tabdot")
	r0.Write("a.txt", "a\n")
	r0.Write("b.txt", "b\n")
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	db := a.OpenFile(r, "b.txt", true)
	d := a.OpenFile(r, "a.txt", true)
	wait("两个文件读进来", func() bool { tt.Frame(); return d.Buf != nil && db.Buf != nil })
	tt.Move(1, 1) // 指针不在标签上
	tt.Frame()

	dot := i18n.T("有没保存的改动")
	closeA, closeB := i18n.Tf("关闭 %s", "a.txt"), i18n.Tf("关闭 %s", "b.txt")
	tabA, tabB := i18n.Tf("标签 %s", "a.txt"), i18n.Tf("标签 %s", "b.txt")
	if !shows(tt, closeA) || shows(tt, dot) {
		t.Fatalf("没改动的当前标签该是 ×、没有点：%q", tt.Texts())
	}
	if shows(tt, closeB) {
		t.Fatalf("没改动、没悬停的非当前标签不该有 ×：%q", tt.Texts())
	}
	hoverOn(t, tt, tabB)
	if !shows(tt, closeB) {
		t.Fatalf("悬停非当前标签该有 ×：%q", tt.Texts())
	}

	mustClick(t, tt, "编辑 a.txt")
	tt.Type("x")
	tt.Move(1, 1)
	tt.Frame()
	if !d.Modified() {
		t.Fatal("打了字该有改动")
	}
	if !shows(tt, dot) || shows(tt, closeA) {
		t.Fatalf("改过的当前标签该是点、没有 ×：%q", tt.Texts())
	}
	w0, _ := tt.Find(tabA)

	hoverOn(t, tt, tabA)
	if !shows(tt, closeA) || shows(tt, dot) {
		t.Fatalf("悬停改过的当前标签该变成 ×：%q", tt.Texts())
	}
	if w1, _ := tt.Find(tabA); w1.W != w0.W {
		t.Errorf("悬停前后标签宽度变了：%v → %v", w0.W, w1.W)
	}

	tt.Move(1, 1)
	tt.Frame()
	a.Exec("file.save")
	wait("保存", func() bool { tt.Frame(); return !d.Modified() })
	tt.Frame()
	if !shows(tt, closeA) || shows(tt, dot) {
		t.Fatalf("保存以后当前标签该回到 ×：%q", tt.Texts())
	}
}
