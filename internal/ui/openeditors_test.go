package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 打开的编辑器：默认没有；视图菜单打开后两组三个文档各一行、按组分开、灰字是仓库 / 目录；点行换组和标签，
// 悬停行上的 × 关掉它（那一格和标签条是同一个 tabClose：有改动没悬停是一个点），全部保存、收起、关闭全部（两组都关）；
// 再关掉开关就没了。
func TestOpenEditors(t *testing.T) {
	r0 := testutil.NewRepo(t, "oe")
	r0.Write("a.txt", "a\n")
	r0.Write("sub/b.go", "package sub\n")
	r0.Write("c.md", "# c\n")
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.ShowSidebar(workspace.ModeFiles)
	a.OpenFile(r, "a.txt", true)
	a.OpenFile(r, "sub/b.go", true)
	a.OpenFileSide(r, "c.md")
	wait("三个文件读进来", func() bool {
		tt.Frame()
		return !slices.ContainsFunc(a.AllDocs(), func(d *docs.Doc) bool { return d.Buf == nil })
	})
	if len(a.Groups()) != 2 {
		t.Fatalf("应该两组：%d", len(a.Groups()))
	}
	row := func(name string) string { return i18n.Tf("打开的编辑器 %s", name) }
	if shows(tt, "打开的编辑器") || shows(tt, row("a.txt")) {
		t.Fatal("默认不该有打开的编辑器")
	}
	a.Exec("view.openEditors")
	tt.Frame()
	tt.Frame()
	if !a.St.Settings.ShowOpenEditors || !shows(tt, "打开的编辑器") {
		t.Fatalf("视图菜单打开以后该有：%q", tt.Texts())
	}
	for _, s := range []string{"第 1 组", "第 2 组", row("a.txt"), row("b.go"), row("c.md"), r.Name + "/sub"} {
		if !shows(tt, s) {
			t.Errorf("没有 %q；texts=%q", s, tt.Texts())
		}
	}
	above(t, tt, "第 1 组", row("a.txt"))
	above(t, tt, row("b.go"), "第 2 组")
	above(t, tt, "第 2 组", row("c.md"))
	above(t, tt, row("c.md"), "仓库")

	// 键盘在第二组（c.md）；点 a.txt 那一行回到第一组的 a.txt，键盘给编辑器
	gs := a.Groups()
	if a.Grp != gs[1] {
		t.Fatal("在侧边打开以后键盘该在第二组")
	}
	mustClick(t, tt, row("a.txt"))
	if a.Grp != gs[0] || a.CurrentDoc() == nil || a.CurrentDoc().Rel != "a.txt" {
		t.Fatalf("点行没换到第一组的 a.txt：%v", a.CurrentDoc())
	}
	tt.Frame()
	tt.Type("x")
	tt.Frame()
	if a.DirtyDocs() != 1 {
		t.Fatalf("在编辑器里打了字该有一个没保存的：%d", a.DirtyDocs())
	}
	// 悬停 b.go 那一行，× 关掉它（标签条上它不是当前标签，× 只有这一个）
	hoverOn(t, tt, row("b.go"))
	mustClick(t, tt, i18n.Tf("关闭 %s", "b.go"))
	if slices.ContainsFunc(a.AllDocs(), func(d *docs.Doc) bool { return d.Rel == "sub/b.go" }) {
		t.Fatal("× 没关掉 b.go")
	}
	tt.Frame()
	if shows(tt, row("b.go")) {
		t.Error("关掉以后行还在")
	}

	mustClick(t, tt, app.Tip(i18n.T("全部保存"), "file.saveAll"))
	if got, _ := os.ReadFile(filepath.Join(r0.Root, "a.txt")); !strings.Contains(string(got), "x") || a.DirtyDocs() != 0 {
		t.Fatalf("全部保存没写盘：%q，还有 %d 个没保存", got, a.DirtyDocs())
	}

	mustClick(t, tt, i18n.Tf("收起 %s", "打开的编辑器"))
	if !a.St.OpenEditorsFolded || shows(tt, row("a.txt")) {
		t.Fatal("收起以后行还在")
	}
	if p := a.St.Panels(); !p.OpenEditorsFolded {
		t.Error("收起没进布局（Panels）")
	}
	mustClick(t, tt, i18n.Tf("展开 %s", "打开的编辑器"))
	if !shows(tt, row("a.txt")) {
		t.Fatal("展开以后没有行")
	}

	mustClick(t, tt, "关闭全部标签（所有组）") // 两组都关（⇧⌘W 只关有键盘的那组）
	if n := len(a.AllDocs()); n != 0 {
		t.Fatalf("关闭全部以后还有 %d 个标签", n)
	}
	if shows(tt, row("a.txt")) || shows(tt, "第 1 组") {
		t.Error("关闭全部以后行还在")
	}

	a.Exec("view.openEditors")
	tt.Frame()
	if a.St.Settings.ShowOpenEditors || shows(tt, "打开的编辑器") {
		t.Error("再关掉开关以后还在")
	}
}

// 打开的编辑器最多露 9 行，再多在段里滚，下面的“仓库”头不被挤走。
func TestOpenEditorsCap(t *testing.T) {
	r0 := testutil.NewRepo(t, "oecap")
	for i := range 12 {
		r0.Write(fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.ShowSidebar(workspace.ModeFiles)
	for i := range 12 {
		a.OpenFile(r, fmt.Sprintf("f%02d.txt", i), true)
	}
	a.St.Settings.ShowOpenEditors = true
	wait("文件树画出行", func() bool { tt.Frame(); return a.TreeRowH > 0 })
	tt.Frame()
	first, ok1 := tt.Find(i18n.Tf("打开的编辑器 %s", "f00.txt"))
	head, ok2 := tt.Find("仓库")
	if !ok1 || !ok2 || a.TreeRowH <= 0 {
		t.Fatalf("没找到第一行或仓库头（行高 %v）：%q", a.TreeRowH, tt.Texts())
	}
	if h := head.Y - first.Y; h > a.TreeRowH*app.OpenEditorsMax+16 {
		t.Errorf("12 个标签把这一段撑到了 %.0f（行高 %.0f，最多 9 行）", h, a.TreeRowH)
	}
	if h := head.Y - first.Y; h < a.TreeRowH*(app.OpenEditorsMax-1) {
		t.Errorf("这一段只有 %.0f 高，不到 9 行（行高 %.0f）", h, a.TreeRowH)
	}
}

// 写死在文案里的键跟着 keybindings.json 走：快速打开改成 ⌃P，空编辑器区的提示写 ⌃P、不再写 ⌘P；去掉键换成没键的说法，
// 不留空括号。保存改成 ⌃S，改过的文件状态栏“未保存 · ⌃S”、设置文档的横幅和“没应用 · ⌃S”跟着变。
func TestHintsFollowKeys(t *testing.T) {
	r0 := testutil.NewRepo(t, "hints")
	r0.Write("a.txt", "a\n")
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	p := a.KeybindingsPath()
	t.Cleanup(func() {
		os.Remove(p)
		a.LoadKeybindings()
	})
	a.RefreshRepo(a.Repos[0], app.WhyManual)
	wait("git status", idle(a.Repos[0]))
	tt.Frame()
	if !hasPrefixText(tt, "在侧栏里点一个文件或一项变更，或 ⌘P 按名字找") {
		t.Fatalf("默认键的提示：%q", tt.Texts())
	}
	a.SetKeybinding(app.Find("file.quickOpen"), wui.Ctrl, wui.KeyP)
	tt.Frame()
	if !hasPrefixText(tt, "在侧栏里点一个文件或一项变更，或 ⌃P 按名字找") || hasPrefixText(tt, "在侧栏里点一个文件或一项变更，或 ⌘P") {
		t.Fatalf("改成 ⌃P 以后的提示：%q", tt.Texts())
	}
	a.SetKeybinding(app.Find("file.quickOpen"), 0, 0)
	tt.Frame()
	if !hasPrefixText(tt, "在侧栏里点一个文件或一项变更，或用快速打开按名字找") {
		t.Fatalf("去掉键以后的提示：%q", tt.Texts())
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, "（）") || strings.Contains(s, "或  按") {
			t.Errorf("留了空的键：%q", s)
		}
	}

	a.SetKeybinding(app.Find("file.save"), wui.Ctrl, wui.KeyS)
	d := a.OpenFile(a.Repos[0], "a.txt", true)
	wait("a.txt 读进来", func() bool { tt.Frame(); return d.Buf != nil })
	mustClick(t, tt, "编辑 a.txt")
	tt.Type("新")
	tt.Frame()
	if !d.Modified() || !shows(tt, "未保存 · ⌃S") || shows(tt, "未保存 · ⌘S") {
		t.Errorf("改过的文件状态栏没跟着 ⌃S：modified=%v %q", d.Modified(), tt.Texts())
	}
	a.OpenSettingsDoc()
	tt.Frame()
	if d = a.CurrentDoc(); d == nil {
		t.Fatal("设置文档没开")
	}
	if !hasPrefixText(tt, "这是 state.json 里的 settings；⌃S 保存即生效") || hasPrefixText(tt, "这是 state.json 里的 settings；⌘S") {
		t.Errorf("设置文档横幅没跟着 ⌃S：%q", tt.Texts())
	}
	tt.Type(" ")
	tt.Frame()
	if !shows(tt, "没应用 · ⌃S") || shows(tt, "没应用 · ⌘S") {
		t.Errorf("设置文档的状态没跟着 ⌃S：%q", tt.Texts())
	}
	a.SetKeybinding(app.Find("file.save"), 0, 0)
	tt.Frame()
	if !shows(tt, "没应用") || !hasPrefixText(tt, "这是 state.json 里的 settings；保存即生效") {
		t.Errorf("去掉保存键以后该只写“没应用”、横幅不带键：%q", tt.Texts())
	}
}
