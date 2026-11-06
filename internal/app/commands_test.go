package app

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

var update = flag.Bool("update", false, "重新生成 docs/shortcuts.md")

// TestShortcutConflicts：同一个上下文里没有两个命令同一快捷键；全局的快捷键在任何上下文里都不能
// 再被别的命令用（全局的在窗口根上，会和焦点里的抢）；菜单里的快捷键唯一；id 唯一；都有名字和分组。
func TestShortcutConflicts(t *testing.T) {
	type combo struct {
		mods ui.Modifiers
		key  ui.Key
	}
	ids := map[string]bool{}
	byCtx := map[Context]map[combo]string{}
	accels := map[string]string{}
	for _, c := range Commands {
		if ids[c.ID] {
			t.Errorf("命令 id %q 重复", c.ID)
		}
		ids[c.ID] = true
		if c.Title == "" {
			t.Errorf("命令 %s 没有名字", c.ID)
		}
		if c.Group == "" && c.Context == Global && c.Run != nil {
			t.Errorf("全局命令 %s 没有菜单分组", c.ID)
		}
		if c.Group != "" && c.Run == nil {
			t.Errorf("菜单里的命令 %s 没有 Run", c.ID)
		}
		if c.Key == 0 {
			if c.Mods != 0 {
				t.Errorf("命令 %s 有修饰键没有键", c.ID)
			}
			continue
		}
		if c.Keys() == "" || (c.Group != "" && c.Accel() == "") {
			t.Errorf("命令 %s 的快捷键写不出来：%q %q", c.ID, c.Keys(), c.Accel())
		}
		k := combo{c.Mods, c.Key}
		if byCtx[c.Context] == nil {
			byCtx[c.Context] = map[combo]string{}
		}
		if other, ok := byCtx[c.Context][k]; ok {
			t.Errorf("%s 里 %s 同时给了 %s 和 %s", c.Context, c.Keys(), other, c.ID)
		}
		byCtx[c.Context][k] = c.ID
		if c.Group != "" {
			if other, ok := accels[c.Accel()]; ok {
				t.Errorf("菜单快捷键 %s 同时给了 %s 和 %s", c.Accel(), other, c.ID)
			}
			accels[c.Accel()] = c.ID
		}
	}
	index := map[string]int{}
	for i, c := range Commands {
		index[c.ID] = i
	}
	for ctx, m := range byCtx {
		if ctx == Global {
			continue
		}
		for k, id := range m {
			g, ok := byCtx[Global][k]
			// 终端的键可以和全局同键（⌘W 关终端 / 关标签、⌘K 清屏 / git 命令），但得排在前面，RunKey 才先给终端
			if ok && !(ctx == Terminal && index[id] < index[g]) {
				t.Errorf("%s 的 %s 和全局命令 %s 同一快捷键", ctx, id, g)
			}
		}
	}
	// 几个容易写错的显示写法
	for id, want := range map[string]string{
		"file.saveAll": "⌥⌘S", "nav.forward": "⌃⇧-", "diff.unified": "⌥⌘L", "repo.3": "⌘3", "commit": "⌘↩",
	} {
		if got := Find(id).Keys(); got != want {
			t.Errorf("%s 显示为 %q，应为 %q", id, got, want)
		}
	}
	for id, want := range map[string]string{"view.right": "Cmd+Alt+B", "nav.forward": "Ctrl+Shift+-", "repo.1": "Cmd+1"} {
		if got := Find(id).Accel(); got != want {
			t.Errorf("%s 的菜单快捷键为 %q，应为 %q", id, got, want)
		}
	}
	if got := Tip("刷新", "git.refresh"); got != "刷新（⌘R）" {
		t.Errorf("Tip = %q", got)
	}
}

// TestShortcutDoc：docs/shortcuts.md 和注册表一致（-update 重新生成）。
func TestShortcutDoc(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "shortcuts.md")
	want := ShortcutDoc()
	if *update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("docs/shortcuts.md 和命令注册表不一致；跑 go test ./internal/app -run TestShortcutDoc -update 重新生成")
	}
}

// TestMenuStructure：菜单栏按 应用 / 文件 / 编辑 / 视图 / Git / 仓库 / 窗口 排；仓库菜单显示仓库名。
func TestMenuStructure(t *testing.T) {
	a := &App{Repos: []*Repo{NewRepo("/x/alpha"), NewRepo("/x/beta")}}
	a.St.Sidebar = true
	items := a.BuildMenu().Items()
	var labels []string
	for i, it := range items {
		if i == 0 { // 应用菜单：标题是应用名，第一项是自己的"关于 Holt"（不是系统的关于面板），然后检查更新、分隔线、设置…
			labels = append(labels, "appMenu")
			if !slices.ContainsFunc(it.Submenu, func(x *mygo.MenuItem) bool { return x.ID == "app.settings" && x.Accelerator == "Cmd+," }) {
				t.Errorf("应用菜单里没有 设置…（⌘,）：%+v", it.Submenu)
			}
			if sub := it.Submenu; len(sub) < 4 || sub[0].ID != "app.about" || sub[1].ID != "app.checkUpdate" || sub[2].Type != mygo.MenuItemSeparator || sub[3].ID != "app.settings" {
				t.Errorf("应用菜单开头不是 关于 / 检查更新 / 分隔线 / 设置…：%+v", sub)
			}
			if slices.ContainsFunc(it.Submenu, func(x *mygo.MenuItem) bool { return x.Role == mygo.RoleAbout }) {
				t.Errorf("应用菜单里还有系统的关于面板")
			}
			continue
		}
		if it.Role != "" {
			labels = append(labels, string(it.Role))
		} else {
			labels = append(labels, it.Label)
		}
	}
	want := []string{"appMenu", "文件", "editMenu", "视图", "终端", "Git", "仓库", "windowMenu"}
	if len(labels) != len(want) {
		t.Fatalf("菜单 = %q", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("菜单 = %q，应为 %q", labels, want)
		}
	}
	var repo []string
	for _, it := range items[6].Submenu {
		if it.Label != "" {
			repo = append(repo, it.Label+"|"+it.Accelerator)
		}
	}
	if len(repo) != 5 || repo[0] != "alpha|Cmd+1" || repo[1] != "beta|Cmd+2" || repo[2] != "添加仓库…|Cmd+Shift+O" || repo[3] != "克隆仓库…|" {
		t.Errorf("仓库菜单 = %q", repo)
	}
	sig := a.menuSignature()
	a.St.Sidebar = false
	if a.menuSignature() == sig {
		t.Error("侧栏开关变了，菜单签名没变")
	}
}

// TestRunKey：同一个键按当前标签分派。⌥⌘F 在 Diff 标签是全文 diff，文件标签上落到编辑器的替换
// （菜单项走同一条路，菜单的快捷键先于焦点里的编辑器接到按键）；没标签时什么都不做。
func TestRunKey(t *testing.T) {
	a := &App{}
	full := Find("diff.full")
	if a.RunKey(full.Mods, full.Key) {
		t.Error("没有标签时 ⌥⌘F 不该执行任何命令")
	}
	a.Docs = []*docs.Doc{{Kind: docs.Diff}}
	a.menuRun(full)
	if !a.St.FullDiff {
		t.Error("Diff 标签上的菜单项没切全文 diff")
	}
	// 文件标签（还没读好，没有编辑器）：两个命令都不能用
	a.Docs = []*docs.Doc{{Kind: docs.File}}
	a.menuRun(full)
	if !a.St.FullDiff || a.CanRun(Find("edit.replace")) {
		t.Error("文件标签上 ⌥⌘F 不该切全文 diff；没读好的文件不能替换")
	}
}

// TestEditorCommands：编辑器自带的命令只进命令面板（不进菜单栏、没有窗口快捷键），
// 英文名也搜得到；从面板跑完键盘回到编辑器；只读的标签上改文字的不能用、硬调 Run 也什么都不做；
// Diff 标签上都不能用；跳改动的要有 HEAD 基准；显示的键第一下都不是菜单快捷键。
func TestEditorCommands(t *testing.T) {
	r := testutil.NewRepo(t, "one")
	r.Write("a.go", "package a\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc f() {\n\tif true {\n\t\tfmt.Println(os.Args)\n\t}\n}\n\nfunc g() {\n\tx := 1\n\t_ = x\n}\n")
	r.Commit("初始")
	r.Write("new.go", "package a\n")
	r.Write("big.txt", strings.Repeat("{\n}\n", MaxWholeTextBytes/4+1))
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	t.Cleanup(a.Close)
	repo := a.Repos[0]

	// 注册表：mygo 改名会被抓到；每条都有分组；Key 留 0
	n := 0
	for _, c := range Commands {
		en, ok := strings.CutPrefix(c.ID, "editor.")
		if !ok || c.Section == "" {
			continue
		}
		n++
		if c.Key != 0 || c.Group != "" || c.Context != Editor {
			t.Errorf("%s 应只进命令面板：key=%v group=%q", c.ID, c.Key, c.Group)
		}
		if cmd, ok := editor.CommandNamed(en); !ok && c.ID != "editor.foldImports" || ok && cmd.String() != en {
			t.Errorf("%s 不是 mygo 的命令名", c.ID)
		}
	}
	if n != len(editorTable)+1 {
		t.Errorf("编辑器自带的命令 %d 条，表里 %d 条", n, len(editorTable))
	}
	for _, it := range a.BuildMenu().Items() {
		for _, sub := range it.Submenu {
			if strings.HasPrefix(sub.ID, "editor.") {
				t.Errorf("菜单栏里有 %s", sub.ID)
			}
		}
	}
	// 显示的键：第一下不是菜单快捷键（菜单先接到），⌘K 开头的都不显示；写得出每个键
	menuKeys := map[string]string{}
	for _, c := range Commands {
		if c.Group != "" && c.Key != 0 {
			menuKeys[c.Keys()] = c.ID
		}
	}
	for _, c := range Commands {
		if c.Hint == "" {
			continue
		}
		first, _, _ := strings.Cut(c.Hint, " ")
		if id, ok := menuKeys[first]; ok {
			t.Errorf("%s 显示 %s，第一下是菜单的 %s", c.ID, c.Hint, id)
		}
		if strings.HasSuffix(first, "⌘") || strings.HasSuffix(first, "⇧") || strings.HasSuffix(first, "⌥") || strings.HasSuffix(first, "⌃") {
			t.Errorf("%s 的键写不出来：%q", c.ID, c.Hint)
		}
	}
	for id, want := range map[string]string{
		"editor.fold": "⌥⌘[", "editor.selectNext": "⌘D", "editor.addCursorBelow": "⌥⌘↓", "editor.toggleComment": "⌘/",
		"editor.nextHunk": "⌘F8", "editor.foldAll": "", "editor.redoSelection": "", "editor.findInSelection": "", "editor.findPrevious": "",
	} {
		if got := Find(id).Hint; got != want {
			t.Errorf("%s 显示的键 %q，应为 %q", id, got, want)
		}
	}

	d := a.OpenFile(repo, "a.go", true)
	wait("读 a.go", func() bool { return d.Buf != nil && d.Base != nil })
	text := d.Buf.String()

	// 英文名搜得到，标题上不标字；分组写"编辑器 · 折叠"；从面板跑完键盘回到编辑器
	p := &a.Palette
	a.OpenPalette(">foldAll")
	a.UpdatePalette()
	if len(p.Items) == 0 || p.Items[0].cmd != Find("editor.foldAll") || p.Items[0].Hits != nil || p.Items[0].Detail != "编辑器 · 折叠" {
		t.Fatalf(">foldAll：%+v", p.Items)
	}
	a.FocusDoc = false
	a.AcceptPalette(0)
	if p.Open || !a.FocusDoc || len(d.St.Folded()) < 2 {
		t.Fatalf("全部折叠：open=%v focus=%v folded=%v", p.Open, a.FocusDoc, d.St.Folded())
	}
	a.Exec("editor.unfoldAll")
	if len(d.St.Folded()) != 0 {
		t.Fatalf("全部展开后还折着 %v", d.St.Folded())
	}
	a.Exec("editor.foldImports")
	if got := d.St.Folded(); !slices.Equal(got, []int{2}) {
		t.Fatalf("折叠导入：%v", got)
	}
	a.Exec("editor.unfoldAll")

	// 多光标：在下一行加光标；选中所有相同的（光标在 fmt 上）
	d.St.GoTo(editor.Pos{Line: 9, Col: 3})
	a.Exec("editor.addCursorBelow")
	if got := len(d.St.Selections()); got != 2 {
		t.Fatalf("加光标后 %d 个光标", got)
	}
	d.St.GoTo(editor.Pos{Line: 9, Col: 3})
	a.Exec("editor.selectAllMatches")
	if got := len(d.St.Selections()); got != 2 || d.St.SelectedText() != "fmt\nfmt" {
		t.Fatalf("选中所有 fmt：%d 处 %q", got, d.St.SelectedText())
	}
	// 改文字的：删除行，撤销回去
	d.St.GoTo(editor.Pos{Line: 14, Col: 0})
	a.Exec("editor.deleteLine")
	if d.Buf.String() == text || !d.Modified() {
		t.Fatal("删除行没改文字")
	}
	d.St.Run(editor.Undo)
	if d.Buf.String() != text {
		t.Fatal("撤销没回去")
	}
	if !a.CanRun(Find("editor.nextHunk")) || !a.CanRun(Find("editor.revertHunk")) {
		t.Error("跟踪了的文件有 HEAD 基准，跳改动、还原改动应能用")
	}

	// 没跟踪的文件没有基准：跳改动不能用
	nd := a.OpenFile(repo, "new.go", true)
	wait("读 new.go", func() bool { return nd.Buf != nil })
	if a.CanRun(Find("editor.nextHunk")) || !a.CanRun(Find("editor.deleteLine")) {
		t.Error("没跟踪的文件：跳改动不能用、删除行能用")
	}

	// 超过 MaxWholeTextBytes 的：扫整篇的（全部折叠、选中所有相同的）不能用，别的照样能用
	bd := a.OpenFile(repo, "big.txt", true)
	wait("读 big.txt", func() bool { return bd.Buf != nil })
	if a.CanRun(Find("editor.foldAll")) || a.CanRun(Find("editor.foldLevel2")) || a.CanRun(Find("editor.selectAllMatches")) ||
		!a.CanRun(Find("editor.fold")) || !a.CanRun(Find("editor.selectNext")) {
		t.Error("超过 1 MB 的文件：扫整篇的命令不能用、别的能用")
	}

	// 历史版本只读：改文字的不能用，硬调 Run 文字也不变；选择、折叠照样能用
	head := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	v := a.OpenVersion(repo, git.Change{Kind: git.InCommit, Path: "a.go", Code: 'A', Commit: head})
	wait("读历史版本", func() bool { return !v.Loading && v.Buf != nil })
	vtext := v.Buf.String()
	for _, id := range []string{"editor.deleteLine", "editor.sortLines", "editor.uppercase", "editor.revertHunk"} {
		c := Find(id)
		if a.CanRun(c) {
			t.Errorf("历史版本上 %s 不该能用", id)
		}
		v.St.SetSelections(editor.Selection{Anchor: 0, Head: len(vtext)})
		c.Run(a)
	}
	if v.Buf.String() != vtext {
		t.Fatal("只读的历史版本被改了")
	}
	if !a.CanRun(Find("editor.selectLine")) || !a.CanRun(Find("editor.foldAll")) {
		t.Error("历史版本上选择、折叠应能用")
	}

	// Diff 标签：一条都不能用
	a.OpenDiff(repo, git.Change{Kind: git.InCommit, Path: "a.go", Code: 'A', Commit: head}, true)
	for _, c := range Commands {
		if c.Section != "" && a.CanRun(c) {
			t.Errorf("Diff 标签上 %s 不该能用", c.ID)
		}
	}
}
