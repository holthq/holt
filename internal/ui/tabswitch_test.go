package ui

import (
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/testutil"
)

// 编辑器标签切换（#19）：三个仓库各开一个文件，⇧⌘] / ⇧⌘[ 和 ⌥⌘→ / ⌥⌘← 按标签条顺序走、到头绕回；
// ⌃Tab 回到上一个用过的标签（连按两次来回），⌃⇧Tab 走到最久没用的；键盘在终端里时 ⇧⌘] / ⌥⌘→ 切的是终端标签。
func TestEditorTabSwitch(t *testing.T) {
	alpha, beta, gamma, ws := testutil.ThreeRepos(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	wait("读完状态", loaded(a))
	for _, f := range []struct {
		root, rel string
	}{{alpha.Root, "alpha.txt"}, {beta.Root, "beta.go"}, {gamma.Root, "gamma.md"}} {
		a.OpenFile(a.RepoFor(f.root), f.rel, true)
		wait("读文件 "+f.rel, func() bool { d := a.CurrentDoc(); return d != nil && d.Rel == f.rel && !d.Loading })
	}
	if len(a.Docs) != 3 || a.DocSel != 2 {
		t.Fatalf("开了 %d 个标签、当前第 %d 个，应为 3 个、第 2 个", len(a.Docs), a.DocSel)
	}
	a.TermFocused = false
	tt.Frame()
	press := func(mods wui.Modifiers, key wui.Key, want int, what string) {
		t.Helper()
		tt.Key(mods, key)
		tt.Frame()
		if a.DocSel != want {
			t.Fatalf("%s 之后当前是第 %d 个标签（%s），应为第 %d 个", what, a.DocSel, a.CurrentDoc().Rel, want)
		}
	}
	press(wui.Cmd|wui.Shift, wui.KeyBracketRight, 0, "⇧⌘]（绕回）")
	press(wui.Cmd|wui.Shift, wui.KeyBracketRight, 1, "⇧⌘]")
	press(wui.Cmd|wui.Shift, wui.KeyBracketLeft, 0, "⇧⌘[")
	press(wui.Cmd|wui.Shift, wui.KeyBracketLeft, 2, "⇧⌘[（绕回）")
	press(wui.Cmd|wui.Alt, wui.KeyRight, 0, "⌥⌘→（绕回）")
	press(wui.Cmd|wui.Alt, wui.KeyLeft, 2, "⌥⌘←（绕回）")

	// 最近用过的：2 ← 0 ← 2 …，现在是 2，上一个用过的是 0
	press(wui.Ctrl, wui.KeyTab, 0, "⌃Tab")
	press(wui.Ctrl, wui.KeyTab, 2, "再按 ⌃Tab（来回）")
	a.SelectDoc(1)
	tt.Frame()
	press(wui.Ctrl, wui.KeyTab, 2, "切到 1 之后 ⌃Tab")
	press(wui.Ctrl|wui.Shift, wui.KeyTab, 0, "⌃⇧Tab（最久没用的）")

	// 键盘在终端里：⇧⌘] / ⌥⌘→ 切终端标签，编辑器标签不动
	r := a.TermRepo()
	a.Exec("view.bottom")
	readyPane(t, a, tt)
	tt.Key(wui.Ctrl|wui.Shift, wui.KeyBackquote) // 再开一个终端标签
	tt.Frame()
	if len(r.Terms) != 2 || r.TermSel != 1 {
		t.Fatalf("⌃⇧` 之后 %d 个终端标签、当前第 %d 个", len(r.Terms), r.TermSel)
	}
	readyPane(t, a, tt)
	before := a.DocSel
	tt.Key(wui.Cmd|wui.Shift, wui.KeyBracketRight)
	wait("⇧⌘] 切终端标签", func() bool { return r.TermSel == 0 })
	readyPane(t, a, tt)
	tt.Key(wui.Cmd|wui.Alt, wui.KeyRight)
	wait("⌥⌘→ 切终端标签", func() bool { return r.TermSel == 1 })
	if a.DocSel != before {
		t.Fatalf("键盘在终端里时编辑器标签从第 %d 个变成了第 %d 个", before, a.DocSel)
	}
}
