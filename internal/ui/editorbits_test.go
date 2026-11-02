package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/snippets"
	"github.com/lemoeade/grove/internal/testutil"
)

// 底栏标题行：shell 的标题很长时它自己省略，标签（「输出」）整个留着——标题从「输出」的右边以后才开始。
func TestBottomTitleShrinks(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	r := a.Repos[0]
	wait("git status", idle(r))
	p := readyPane(t, a, tt)
	long := "user@workbench:" + strings.Repeat("/很长的目录", 40)
	p.Title = long // sh 不设标题，这里放着不会被盖掉
	tt.Frame()
	out, ok := tt.Find("输出")
	if !ok {
		t.Fatal("找不到「输出」标签")
	}
	title, ok := tt.Find(long)
	if !ok {
		t.Fatal("找不到 shell 的标题")
	}
	if title.X < out.X+out.W || title.W <= 0 {
		t.Fatalf("标题 %+v 挤到了「输出」%+v 上", title, out)
	}
	if title.X+title.W > 1440 {
		t.Fatalf("标题 %+v 没省略、伸出了窗口", title)
	}
}

// 粘性滚动的光标露出来：光标跳到被吸着的行盖住的那一行（GoTo 只在看不见时才滚），编辑器往上滚到它正好在叠层下面；
// 五层的一样；光标不动、滚轮把它滚到叠层底下不拽回来；方向键往上走进叠层也露出来。
func TestStickyCursorReveal(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "long.go"), []byte(stickyGo()), 0o644)
	var py strings.Builder
	for i, h := range []string{"class A:", "def b(self):", "def c():", "def d():", "def e():"} {
		py.WriteString(strings.Repeat("    ", i) + h + "\n")
	}
	for range 120 {
		py.WriteString(strings.Repeat("    ", 5) + "x = 1\n")
	}
	os.WriteFile(filepath.Join(root, "deep.py"), []byte(py.String()), 0o644)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	d := a.OpenFile(r, "long.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()
	tt.Frame()
	st := &d.St
	lh := st.LineTop(11) - st.LineTop(10)

	st.SetScrollY(st.LineTop(50))
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("func Long() {"); !ok {
		t.Fatalf("没吸 func Long：%q", tt.Texts())
	}
	st.GoTo(editor.Pos{Line: 50, Col: 3})
	tt.Frame()
	tt.Frame()
	if y, want := st.ScrollY(), st.LineTop(49); y != want {
		t.Errorf("光标在吸着的行底下：滚动 %v，应滚到 %v（第 51 行在叠层下面）", y, want)
	}
	// 光标不动，滚轮（这里直接设滚动）把它滚到叠层底下：不拽回来
	st.SetScrollY(st.LineTop(50))
	tt.Frame()
	tt.Frame()
	if y := st.ScrollY(); y != st.LineTop(50) {
		t.Errorf("光标没动也滚了：%v", y)
	}

	p := a.OpenFile(r, "deep.py", true)
	wait("读好 deep.py", func() bool { return p.Buf != nil && !p.Loading })
	tt.Frame()
	tt.Frame()
	ps := &p.St
	ps.SetScrollY(ps.LineTop(60))
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("def e():"); !ok {
		t.Fatalf("应吸五行：%q", tt.Texts())
	}
	ps.GoTo(editor.Pos{Line: 62})
	tt.Frame()
	tt.Frame()
	if y, want := ps.ScrollY(), ps.LineTop(62)-5*lh; y != want {
		t.Errorf("五层：滚动 %v，应把第 63 行滚到五行吸顶下面（%v）", y, want)
	}
	// 方向键往上一行，进了叠层：再往上滚一行
	a.FocusDoc = true
	tt.Frame()
	tt.Key(0, wui.KeyUp)
	tt.Frame()
	tt.Frame()
	if c := ps.CursorPos(); c.Line != 61 {
		t.Fatalf("↑ 后光标在 %+v", c)
	}
	if top := ps.LineTop(61) - ps.ScrollY(); top < 5*lh {
		t.Errorf("↑ 后光标那一行离顶 %v，被五行吸顶（%v）盖着", top, 5*lh)
	}
}

// 镜像标签：标签的右键菜单“在另一组里再开一份”向右分出一组，新的那份标题后面有个双方块，悬停说是第几份；
// 正本的标签没有。
func TestMirrorTabUI(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, termState(t, false))
	r := a.Repos[0]
	d := a.OpenFile(r, "README.md", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()
	if err := tt.RightClick("标签 README.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("在另一组里再开一份"); err != nil {
		t.Fatalf("标签菜单：%v，%q", err, tt.Menu())
	}
	tt.Frame()
	tt.Frame()
	m := a.CurrentDoc()
	if m == d || m.Copy != 1 || m.Buf != d.Buf || len(a.Groups()) != 2 {
		t.Fatalf("没开出镜像：copy %d，%d 组", m.Copy, len(a.Groups()))
	}
	mark, ok := tt.Find("同一个文件的第 2 份，改动是同一份")
	if !ok {
		t.Fatalf("镜像的标签上没有标记：%q", tt.Texts())
	}
	if mark.X < 1440/2 {
		t.Errorf("标记 %+v 应在右边那组的标签上", mark)
	}
	if strings.Count(strings.Join(tt.Texts(), "\n"), "同一个文件的第") != 1 {
		t.Errorf("正本也有标记：%q", tt.Texts())
	}
}

// 代码片段：在 Go 文件里敲 fo 弹出补全（fori、forr），Tab 接了它，下一帧认出来进跳位会话：$1 的三处一起选中，
// 状态栏“片段 1/2”；打字三处一起改，Tab 到 $2（片段 2/2），再 Tab 到 $0、会话结束、状态栏没了；Esc 也结束。
// 设置的“片段”页：片段目录一项和按语言分节的表。
func TestSnippetUI(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "x.go"), []byte("package main\n\nfunc main() {\n\t\n}\n"), 0o644)
	a, tt, wait := newUI(t, ws, termState(t, false))
	d := a.OpenFile(a.Repos[0], "x.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	if !d.St.Focused() {
		t.Fatal("编辑器没拿到焦点")
	}
	d.St.GoTo(editor.Pos{Line: 3, Col: 1})
	tt.Frame()
	tt.Type("f")
	tt.Type("o")
	tt.Frame()
	if d.SnipOffer == nil || !tt.HasText("fori") || !tt.HasText("forr") {
		t.Fatalf("敲 fo 没出片段补全：%q", tt.Texts())
	}
	tt.Key(0, wui.KeyTab)
	tt.Frame()
	tt.Frame()
	if d.Snip == nil || !tt.HasText(i18n.Tf("片段 %d/%d", 1, 2)) || len(d.St.Selections()) != 3 {
		t.Fatalf("Tab 接了补全没进跳位：%q，%d 处选中", d.Buf.String(), len(d.St.Selections()))
	}
	tt.Type("k")
	tt.Frame()
	if !strings.Contains(d.Buf.String(), "for k := 0; k < n; k++ {") {
		t.Fatalf("三处一起改：%q", d.Buf.String())
	}
	tt.Key(0, wui.KeyTab)
	tt.Frame()
	if d.St.SelectedText() != "n" || !tt.HasText(i18n.Tf("片段 %d/%d", 2, 2)) {
		t.Fatalf("Tab 到 $2：选中 %q", d.St.SelectedText())
	}
	tt.Key(0, wui.KeyTab)
	tt.Frame()
	if d.Snip != nil || tt.HasText(i18n.Tf("片段 %d/%d", 2, 2)) || d.St.CursorPos() != (editor.Pos{Line: 4, Col: 2}) {
		t.Fatalf("到 $0：会话 %v，光标 %+v", d.Snip != nil, d.St.CursorPos())
	}
	tt.Key(0, wui.KeyTab) // 会话结束了：Tab 照编辑器的，缩进
	tt.Frame()
	if !strings.Contains(d.Buf.String(), "\n\t\t\t\n\t}") {
		t.Fatalf("会话外的 Tab：%q", d.Buf.String())
	}
	a.InsertSnippet(d, snippetOf(t, a, "iferr"))
	tt.Frame()
	if d.Snip == nil {
		t.Fatal("插入 iferr 没开跳位")
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if d.Snip != nil || tt.HasText(i18n.Tf("片段 %d/%d", 1, 1)) {
		t.Fatal("Esc 没结束会话")
	}

	st := wui.NewTester(SettingsView(a), 640, 1400)
	st.SetLocale(i18n.Locale())
	st.Frame()
	mustClick(t, st, "片段")
	for _, s := range []string{"片段目录", "打开片段目录", "重新加载", "Go", "Python", "fori", "for range 循环", "内置"} {
		mustText(t, st, s)
	}
}

// snippetOf 是 Go 能用的、名字是 name 的片段。
func snippetOf(t *testing.T, a *app.App, name string) snippets.Snippet {
	t.Helper()
	for _, sn := range a.Snippets().For("go") {
		if sn.Name == name {
			return sn
		}
	}
	t.Fatalf("没有 go/%s", name)
	return snippets.Snippet{}
}
