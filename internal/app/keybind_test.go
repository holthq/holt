package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
)

// resetKeys 让测试结束时命令表回到默认键（命令表是整个进程的）。
func resetKeys(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		keyFile, keyErrors, keyBroken = nil, nil, false
		applyKeybindings(nil)
	})
}

func mustFind(t *testing.T, id string) *Command {
	t.Helper()
	c := Find(id)
	if c == nil {
		t.Fatalf("没有命令 %s", id)
	}
	return c
}

// 快捷键的写法：cmd+shift+p、⇧⌘P、Command+Shift+P 都行，大小写随意；写回去是规范写法，读回来一样。
func TestKeyParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		mods ui.Modifiers
		key  ui.Key
	}{
		{"cmd+shift+p", ui.Super | ui.Shift, ui.KeyP},
		{"⇧⌘P", ui.Super | ui.Shift, ui.KeyP},
		{" Command+Shift+P ", ui.Super | ui.Shift, ui.KeyP},
		{"⌘+⇧+p", ui.Super | ui.Shift, ui.KeyP},
		{"ctrl+alt+F12", ui.Ctrl | ui.Alt, ui.KeyF12},
		{"option+opt+⌃+1", ui.Alt | ui.Ctrl, ui.Key1},
		{"cmd+,", ui.Super, ui.KeyComma},
		{"cmd+bracketleft", ui.Super, ui.KeyBracketLeft},
		{"esc", 0, ui.KeyEscape},
		{"shift+enter", ui.Shift, ui.KeyEnter},
		{"", 0, 0},
	} {
		mods, key, err := ParseKeys(tc.in)
		if err != nil || mods != tc.mods || key != tc.key {
			t.Errorf("ParseKeys(%q) = %v %v %v，应是 %v %v", tc.in, mods, key, err, tc.mods, tc.key)
		}
	}
	for _, bad := range []string{"cmd+", "cmd+shift", "⌘", "hyper+p", "f13", "f01", "cmd+pp", "p+cmd", "cmd+p+q"} {
		if _, _, err := ParseKeys(bad); err == nil {
			t.Errorf("ParseKeys(%q) 该报错", bad)
		}
	}

	if s := FormatKeys(ui.Shift|ui.Super|ui.Alt|ui.Ctrl, ui.KeyP); s != "cmd+ctrl+alt+shift+p" {
		t.Errorf("FormatKeys = %q", s)
	}
	if s := FormatKeys(ui.Super, 0); s != "" {
		t.Errorf("没有键该是空串：%q", s)
	}
	keys := []ui.Key{ui.KeyF1, ui.KeyF12}
	for k := ui.KeyA; k <= ui.KeyZ; k++ {
		keys = append(keys, k)
	}
	for k := ui.Key0; k <= ui.Key9; k++ {
		keys = append(keys, k)
	}
	for k := range keyWords {
		keys = append(keys, k)
	}
	for _, k := range keys {
		for _, m := range []ui.Modifiers{0, ui.Super, ui.Ctrl | ui.Shift, ui.Super | ui.Alt | ui.Shift} {
			s := FormatKeys(m, k)
			if m2, k2, err := ParseKeys(s); err != nil || m2 != m || k2 != k {
				t.Errorf("%q 读回来是 %v %v %v，应是 %v %v", s, m2, k2, err, m, k)
			}
		}
		if !Recordable(k) {
			t.Errorf("%v 该能录", k)
		}
	}
	if Recordable(ui.KeyUnknown) {
		t.Error("认不出的键不该能录")
	}
	if s := KeysOf(ui.Super|ui.Shift, ui.KeyP); s != "⇧⌘P" {
		t.Errorf("KeysOf = %q", s)
	}
}

// 盖上去的键：同域同键后来的赢、被顶掉的没有键并记下是谁；不同域（文件树、终端）不算冲突；
// 键写空是去掉；看不懂的条目报出来、其余照常；重新 apply 从默认起算。
func TestKeybindingsApply(t *testing.T) {
	resetKeys(t)
	applyKeybindings(nil)
	pal, gitCmd, push := mustFind(t, "palette.commands"), mustFind(t, "git.command"), mustFind(t, "git.push")
	treeCopy, termClear := mustFind(t, "tree.copy"), mustFind(t, "term.clear")
	rev := keyRev

	if c := KeyConflict(pal, ui.Super, ui.KeyK); c != gitCmd {
		t.Errorf("⌘K 该和 git.command 冲突：%v", c)
	}
	if c := KeyConflict(pal, ui.Super, ui.KeyC); c != nil && c.Context == FileTree {
		t.Errorf("文件树的 ⌘C 不该算冲突：%s", c.ID)
	}

	errs := applyKeybindings([]Keybinding{
		{"palette.commands", "cmd+k"},
		{"nope.command", "cmd+1"},
		{"git.push", "hyper+x"},
		{"file.quickOpen", ""},
		{"git.push", "cmd+shift+f9"},
		{"git.fetch", "cmd+shift+f9"},
	})
	if len(errs) != 2 || !strings.Contains(errs[0], "nope.command") || !strings.Contains(errs[1], "hyper+x") {
		t.Errorf("看不懂的条目：%q", errs)
	}
	if keyRev != rev+1 {
		t.Errorf("apply 一次 keyRev 该加一：%d → %d", rev, keyRev)
	}
	if pal.Mods != ui.Super || pal.Key != ui.KeyK || !pal.Custom || pal.Shadowed != "" {
		t.Errorf("palette.commands：%v %v custom=%v", pal.Mods, pal.Key, pal.Custom)
	}
	if gitCmd.Key != 0 || gitCmd.Shadowed != "palette.commands" || gitCmd.Custom {
		t.Errorf("git.command 该被顶掉：%v %q", gitCmd.Key, gitCmd.Shadowed)
	}
	if termClear.Key != ui.KeyK || termClear.Shadowed != "" {
		t.Errorf("终端的 ⌘K 不是一个域，不该动：%v %q", termClear.Key, termClear.Shadowed)
	}
	if q := mustFind(t, "file.quickOpen"); q.Key != 0 || !q.Custom || q.Shadowed != "" {
		t.Errorf("键写空该去掉：%v custom=%v", q.Key, q.Custom)
	}
	fetch := mustFind(t, "git.fetch")
	if fetch.Key != ui.KeyF9 || push.Key != 0 || push.Shadowed != "git.fetch" || !push.Custom {
		t.Errorf("同一个键两条，后写的赢：fetch %v，push %v %q", fetch.Key, push.Key, push.Shadowed)
	}
	if DefaultKeys(pal) != "⇧⌘P" || DefaultKeys(gitCmd) != "⌘K" {
		t.Errorf("默认键：%q %q", DefaultKeys(pal), DefaultKeys(gitCmd))
	}
	if m, k, ok := KeyOf("palette.commands"); !ok || m != ui.Super || k != ui.KeyK {
		t.Errorf("KeyOf：%v %v %v", m, k, ok)
	}
	if _, _, ok := KeyOf("git.command"); ok {
		t.Error("被顶掉的命令 KeyOf 该没有键")
	}

	// 全局的键和文件树的 ⌘C 不在一个域
	applyKeybindings([]Keybinding{{"git.push", "cmd+c"}})
	if treeCopy.Key != ui.KeyC || treeCopy.Shadowed != "" {
		t.Errorf("文件树的 ⌘C 不该被全局的顶掉：%v %q", treeCopy.Key, treeCopy.Shadowed)
	}

	// 同一条命令写了几次，靠后的算
	applyKeybindings([]Keybinding{{"git.push", "cmd+shift+f7"}, {"git.push", "cmd+shift+f8"}})
	if push.Key != ui.KeyF8 {
		t.Errorf("同一条命令靠后的算：%v", push.Key)
	}

	applyKeybindings(nil)
	if pal.Key != ui.KeyP || pal.Mods != ui.Super|ui.Shift || pal.Custom || gitCmd.Key != ui.KeyK || gitCmd.Shadowed != "" {
		t.Errorf("重新 apply 该回到默认：%v %v / %v %q", pal.Mods, pal.Key, gitCmd.Key, gitCmd.Shadowed)
	}
}

// keybindings.json：注释和多余的逗号能读；写回去是一条一行的普通 JSON，和默认一样的删掉、去掉写空键；
// 读不了的文件不写；重置全部先问；写的时候不留临时文件；在编辑器里保存了它就重读。
func TestKeybindingsFile(t *testing.T) {
	resetKeys(t)
	a, _ := newTestApp(t, t.TempDir())
	p := a.KeybindingsPath()
	if p != filepath.Join(a.StateDir, "keybindings.json") {
		t.Fatalf("KeybindingsPath = %q", p)
	}
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pal, push := mustFind(t, "palette.commands"), mustFind(t, "git.push")

	a.LoadKeybindings() // 没有文件：全默认
	if len(KeyErrors()) != 0 || KeysCustomized() || pal.Key != ui.KeyP {
		t.Fatalf("没有文件该是全默认：%q", KeyErrors())
	}

	write("// 我的快捷键\n[\n  {\"command\": \"palette.commands\", \"key\": \"cmd+shift+f6\"}, /* 面板 */\n  {\"command\": \"nope\", \"key\": \"cmd+1\"},\n]\n")
	a.LoadKeybindings()
	if pal.Key != ui.KeyF6 || len(KeyErrors()) != 1 || !KeysCustomized() {
		t.Fatalf("带注释的文件：%v，看不懂的 %q", pal.Key, KeyErrors())
	}
	if s := keyErrorsToast(1); !strings.Contains(s, "1") {
		t.Errorf("提示：%q", s)
	}

	// 改一条：看不懂的条目原样留着，新的接在后面
	a.SetKeybinding(push, ui.Super|ui.Shift, ui.KeyF5)
	want := "[\n  {\"command\": \"palette.commands\", \"key\": \"cmd+shift+f6\"},\n  {\"command\": \"nope\", \"key\": \"cmd+1\"},\n  {\"command\": \"git.push\", \"key\": \"cmd+shift+f5\"}\n]\n"
	if got := read(); got != want {
		t.Errorf("写回的文件：\n%s\n应是：\n%s", got, want)
	}
	if push.Key != ui.KeyF5 || !push.Custom {
		t.Errorf("写了要马上生效：%v", push.Key)
	}
	// 和默认一样：删掉那条；去掉键：写空
	a.SetKeybinding(pal, ui.Super|ui.Shift, ui.KeyP)
	a.SetKeybinding(push, 0, 0)
	want = "[\n  {\"command\": \"nope\", \"key\": \"cmd+1\"},\n  {\"command\": \"git.push\", \"key\": \"\"}\n]\n"
	if got := read(); got != want {
		t.Errorf("改回默认、去掉键以后：\n%s", got)
	}
	if pal.Custom || pal.Key != ui.KeyP || push.Key != 0 || !push.Custom {
		t.Errorf("palette %v custom=%v，push %v", pal.Key, pal.Custom, push.Key)
	}
	a.ResetKeybinding(push)
	if got := read(); got != "[\n  {\"command\": \"nope\", \"key\": \"cmd+1\"}\n]\n" || push.Custom || push.Key == 0 {
		t.Errorf("重置一条：\n%s", got)
	}

	// 重置全部：先问，确定了写成空数组
	a.ResetAllKeybindings()
	if a.Confirm == nil || !a.ConfirmOpen {
		t.Fatal("重置全部该先问")
	}
	if strings.Contains(read(), "[]") {
		t.Fatal("没确定就写了")
	}
	a.Confirm.Fn()
	if got := read(); got != "[]\n" || KeysCustomized() || len(KeyErrors()) != 0 {
		t.Errorf("重置全部以后：%q", got)
	}
	a.Confirm, a.ConfirmOpen = nil, false
	a.ResetAllKeybindings()
	if a.Confirm != nil {
		t.Error("没有自定义时重置全部不该问")
	}

	// 读不了的文件：全默认，设置页不往里写
	write("{\"command\": ")
	a.LoadKeybindings()
	if !keyBroken || len(KeyErrors()) != 1 || pal.Key != ui.KeyP {
		t.Fatalf("读不了的文件：broken=%v %q", keyBroken, KeyErrors())
	}
	a.SetKeybinding(push, ui.Super|ui.Shift, ui.KeyF5)
	if got := read(); got != "{\"command\": " || !a.AlertOpen || push.Key == ui.KeyF5 {
		t.Errorf("读不了的文件不该写：%q alert=%v", got, a.AlertOpen)
	}
	a.AlertOpen = false

	// 在编辑器里保存了它：重读，有看不懂的说一声；别的文件不管
	write("[{\"command\": \"git.push\", \"key\": \"cmd+shift+f4\"}, {\"command\": \"x\", \"key\": \"y\"}]")
	a.Toast = ""
	a.keybindingsSaved(&docs.Doc{Abs: filepath.Join(a.StateDir, "other.json")})
	if push.Key == ui.KeyF4 {
		t.Fatal("别的文件保存了不该重读")
	}
	a.keybindingsSaved(&docs.Doc{Abs: p})
	if push.Key != ui.KeyF4 || keyBroken || a.Toast == "" {
		t.Errorf("保存了 keybindings.json 该重读：%v toast=%q", push.Key, a.Toast)
	}

	es, _ := os.ReadDir(a.StateDir)
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".keybindings.json.") {
			t.Errorf("留下了临时文件 %s", e.Name())
		}
	}

	// 没有文件时“打开 keybindings.json”先写一个空数组
	os.Remove(p)
	a.OpenKeybindings()
	if got := read(); got != "[]\n" {
		t.Errorf("新建的 keybindings.json：%q", got)
	}
}

// 编辑器自带的命令：改了键，新键交给编辑器、mygo 的旧默认键拿走；窗口上的命令占了编辑器的默认键，
// 编辑器让出来、那条编辑器命令没有键；Diff 的 J / K 跟着 diff.nextHunk / prevHunk。
func TestEditorBindingsOverride(t *testing.T) {
	resetKeys(t)
	a := &App{} // 没有状态目录：SetKeybinding 只改内存里的
	applyKeybindings(nil)
	tc, sn := mustFind(t, "editor.toggleComment"), mustFind(t, "editor.selectNext")
	has := func(bs []editor.Binding, b editor.Binding) bool { return slices.Contains(bs, b) }

	if tc.Key != 0 || tc.Hint != "⌘/" || ShownKeys(tc) != "⌘/" || len(EditorBindings()) != 0 {
		t.Fatalf("默认：key %v hint %q，EditorBindings %v", tc.Key, tc.Hint, EditorBindings())
	}
	if m, k, ok := strokeOf(tc); !ok || m != ui.Super || k != ui.KeySlash {
		t.Errorf("编辑器命令占着它显示的默认键：%v %v %v", m, k, ok)
	}
	ds := DiffBindings()
	if len(ds) < 2 || ds[0] != (editor.Binding{Key: ui.KeyJ, Command: editor.NextHunk}) || ds[1] != (editor.Binding{Key: ui.KeyK, Command: editor.PreviousHunk}) {
		t.Errorf("Diff 默认是 J / K：%v", ds)
	}

	a.SetKeybinding(tc, ui.Super|ui.Shift, ui.Key7)
	bs := EditorBindings()
	if tc.Key != ui.Key7 || tc.Hint != "" || ShownKeys(tc) != "⇧⌘7" ||
		!has(bs, editor.Binding{Mods: ui.Super | ui.Shift, Key: ui.Key7, Command: editor.ToggleComment}) ||
		!has(bs, editor.Binding{Mods: ui.Super, Key: ui.KeySlash}) {
		t.Errorf("改了切换行注释：key %v hint %q，EditorBindings %v", tc.Key, tc.Hint, bs)
	}
	if !has(DiffBindings(), editor.Binding{Mods: ui.Super | ui.Shift, Key: ui.Key7, Command: editor.ToggleComment}) {
		t.Error("Diff 里也该用改过的键")
	}
	if KeyMatches(tc, "cmd+/") || !KeyMatches(tc, "⇧⌘7") || !KeyMatches(tc, "toggle comment") && !KeyMatches(tc, "togglecomment") {
		t.Errorf("搜索按现在的键：%q", ShownKeys(tc))
	}
	// 改回默认键：删掉那条
	a.SetKeybinding(tc, ui.Super, ui.KeySlash)
	if tc.Custom || tc.Hint != "⌘/" || len(keyFile) != 0 || len(EditorBindings()) != 0 {
		t.Errorf("改回 ⌘/：custom=%v hint %q file %v", tc.Custom, tc.Hint, keyFile)
	}

	// 全局命令拿了 ⌘D：加选下一个让出来，编辑器不再接 ⌘D
	a.SetKeybinding(mustFind(t, "git.push"), ui.Super, ui.KeyD)
	if sn.Hint != "" || sn.Shadowed != "git.push" || ShownKeys(sn) != "" || !has(EditorBindings(), editor.Binding{Mods: ui.Super, Key: ui.KeyD}) {
		t.Errorf("加选下一个：hint %q shadowed %q，EditorBindings %v", sn.Hint, sn.Shadowed, EditorBindings())
	}
	if c := KeyConflict(mustFind(t, "git.fetch"), ui.Super, ui.KeyD); c == nil || c.ID != "git.push" {
		t.Errorf("⌘D 现在是 git.push 的：%v", c)
	}

	// 跳改动换成 N / P
	applyKeybindings([]Keybinding{{"diff.nextHunk", "n"}, {"diff.prevHunk", "p"}})
	ds = DiffBindings()
	if len(ds) < 2 || ds[0] != (editor.Binding{Key: ui.KeyN, Command: editor.NextHunk}) || ds[1] != (editor.Binding{Key: ui.KeyP, Command: editor.PreviousHunk}) {
		t.Errorf("Diff 跳改动换成 N / P：%v", ds)
	}
}

// 快捷键表的分段：每条命令在且只在一段；全局的按菜单组在前，编辑器自带的分组在最后。
func TestKeyGroups(t *testing.T) {
	gs := KeyGroups()
	seen := map[*Command]int{}
	for _, g := range gs {
		if g.Title == "" || len(g.Commands) == 0 {
			t.Errorf("空的一段：%+v", g)
		}
		for _, c := range g.Commands {
			seen[c]++
		}
	}
	for _, c := range Commands {
		if seen[c] != 1 {
			t.Errorf("%s 在 %d 段里", c.ID, seen[c])
		}
	}
	if gs[0].Title != "文件" || !strings.HasPrefix(gs[len(gs)-1].Title, "编辑器 · ") {
		t.Errorf("分段顺序：第一段 %q，最后一段 %q", gs[0].Title, gs[len(gs)-1].Title)
	}
	pal := mustFind(t, "palette.commands")
	for _, q := range []string{"命令面板", "palette.commands", "⇧⌘p", "cmd+shift+p", "command palette"} {
		if !KeyMatches(pal, q) {
			t.Errorf("%q 该搜得到命令面板", q)
		}
	}
	if KeyMatches(pal, "cmd+k") {
		t.Error("cmd+k 不该搜到命令面板")
	}
}

// 菜单栏跟着改过的键：改了的换成新键、被顶掉的不带键；录键时整个菜单栏不带键；改一次、开始录键菜单的签名都变（重建）。
func TestMenuFollowsKeys(t *testing.T) {
	resetKeys(t)
	t.Cleanup(func() { KeyRecording = false })
	applyKeybindings(nil)
	a := &App{Repos: []*Repo{NewRepo("/x/alpha")}}
	var find func(items []*mygo.MenuItem, id string) *mygo.MenuItem
	find = func(items []*mygo.MenuItem, id string) *mygo.MenuItem {
		for _, it := range items {
			if it == nil {
				continue
			}
			if it.ID == id {
				return it
			}
			if x := find(it.Submenu, id); x != nil {
				return x
			}
		}
		return nil
	}
	accel := func(id string) string {
		t.Helper()
		it := find(a.BuildMenu().Items(), id)
		if it == nil {
			t.Fatalf("菜单栏里没有 %s", id)
		}
		return it.Accelerator
	}
	if accel("palette.commands") != "Cmd+Shift+P" || accel("git.command") != "Cmd+K" {
		t.Fatalf("默认：%q %q", accel("palette.commands"), accel("git.command"))
	}
	sig := a.menuSignature()
	applyKeybindings([]Keybinding{{"palette.commands", "cmd+k"}})
	if accel("palette.commands") != "Cmd+K" || accel("git.command") != "" {
		t.Errorf("改了以后：%q %q", accel("palette.commands"), accel("git.command"))
	}
	if a.menuSignature() == sig {
		t.Error("改了快捷键菜单的签名该变")
	}
	sig = a.menuSignature()
	a.SetKeyRecording(true)
	if accel("palette.commands") != "" || accel("app.settings") != "" || a.menuSignature() == sig {
		t.Errorf("录键时菜单栏不该带键：%q %q", accel("palette.commands"), accel("app.settings"))
	}
	a.SetKeyRecording(false)
	if accel("app.settings") != "Cmd+," {
		t.Errorf("录完键回来：%q", accel("app.settings"))
	}
}
