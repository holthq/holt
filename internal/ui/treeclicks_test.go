package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// treeKit 是文件树点击 / 右键测试共用的：两个仓库都展开、读完，几个找行、点行的小工具。
type treeKit struct {
	t    *testing.T
	a    *app.App
	tt   *wui.Tester
	wait func(string, func() bool)
}

func newTreeKit(t *testing.T, roots ...string) *treeKit {
	a, tt, wait := newUI(t, testutil.Workspace(t, roots...), "")
	a.ShowSidebar(workspace.ModeFiles)
	for _, r := range a.Repos {
		if !r.Expanded {
			a.ToggleRepo(r)
		}
		a.RefreshRepo(r, app.WhyManual)
		a.EnsureFiles(r)
	}
	wait("仓库的文件", func() bool {
		for _, r := range a.Repos {
			if !r.Files.Loaded {
				return false
			}
		}
		return loaded(a)()
	})
	tt.Frame()
	return &treeKit{t, a, tt, wait}
}

func (k *treeKit) repo(name string) *app.Repo {
	for _, r := range k.a.Repos {
		if r.Name == name {
			return r
		}
	}
	k.t.Fatalf("没有仓库 %s", name)
	return nil
}

// sel 是光标那行的仓库内路径（仓库行是 "仓库"，没有是 "-"）。
func (k *treeKit) sel() string {
	row, ok := k.a.TreeRowAt(k.a.FileSel)
	switch {
	case !ok:
		return "-"
	case row.Root:
		return "仓库"
	}
	return row.Path
}

// click 关掉标签（标签和面包屑里也有文件名，比侧栏先画）、和上一次点隔开（不算双击），再点 name。
func (k *treeKit) click(mods wui.Modifiers, name string) {
	k.t.Helper()
	k.a.Exec("file.closeAll")
	k.tt.Frame()
	k.tt.Advance(time.Second)
	if err := k.tt.ClickWith(mods, name); err != nil {
		k.t.Fatalf("点 %q: %v", name, err)
	}
	k.tt.Frame()
	k.tt.Frame()
}

// doubleClick 在 name 那行上快快地点两下（找的时候标签都关着）。
func (k *treeKit) doubleClick(name string) {
	k.t.Helper()
	k.a.Exec("file.closeAll")
	k.tt.Frame()
	k.tt.Advance(time.Second)
	b, ok := k.tt.Find(name)
	if !ok {
		k.t.Fatalf("找不到 %q", name)
	}
	k.tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
	k.tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
	k.tt.Frame()
	k.tt.Frame()
}

func (k *treeKit) menu(name string, item ...string) {
	k.t.Helper()
	k.a.Exec("file.closeAll")
	k.tt.Frame()
	mustRightClick(k.t, k.tt, name, item...)
}

// blank 是文件树最后一行下面的空白处的一点（last 是最后一行的文字）。
func (k *treeKit) blank(last string) (float32, float32) {
	k.t.Helper()
	b, ok := k.tt.Find(last)
	if !ok {
		k.t.Fatalf("找不到 %q", last)
	}
	return b.X, b.Y + 200
}

// 点击：目录单击就展开 / 收起，双击只切一次；箭头只切、不改选中；再点选中的文件重新预览，双击固定；
// 点空白处一行也不选、键盘留在树上；Esc 不再多选、光标那行留着。
func TestTreeClicks(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("a.txt", "a\n")
	repo.Write("b.txt", "b\n")
	repo.Write("docs/d.md", "d\n")
	repo.Commit("init")
	k := newTreeKit(t, repo.Root)
	a, tt := k.a, k.tt
	r := k.repo("proj")

	// 目录：单击展开，再单击（这时它已经选中，点击归行自己）收起
	k.click(0, "docs")
	if !r.Files.Open["docs"] || k.sel() != "docs" {
		t.Fatalf("单击目录：展开 %v，选中 %s", r.Files.Open["docs"], k.sel())
	}
	k.click(0, "docs")
	if r.Files.Open["docs"] {
		t.Fatal("再单击已经选中的目录应该收起")
	}
	// 双击目录只切一次：选中着的（行自己数）和没选中的（第一下列表接、第二下行接）都一样
	k.doubleClick("docs")
	if !r.Files.Open["docs"] {
		t.Fatal("双击选中着的目录应该只展开一次")
	}
	k.click(0, "b.txt")
	k.doubleClick("docs")
	if r.Files.Open["docs"] || k.sel() != "docs" {
		t.Fatalf("双击没选中的目录：展开 %v（应该只收起一次），选中 %s", r.Files.Open["docs"], k.sel())
	}

	// 箭头只展开 / 收起：选中不动、键盘不走
	k.click(0, "b.txt")
	if !a.TreeFocused {
		t.Fatal("点了文件树的行，键盘应该在树上")
	}
	k.click(0, "展开 docs")
	if !r.Files.Open["docs"] || k.sel() != "b.txt" || a.FileChosen.Len() != 1 || !a.TreeFocused {
		t.Fatalf("点箭头：展开 %v，选中 %s / %d 项，键盘在树上 %v", r.Files.Open["docs"], k.sel(), a.FileChosen.Len(), a.TreeFocused)
	}

	// 再点选中着的文件：标签关掉了也重新预览出来；双击固定打开
	k.click(0, "b.txt")
	if d := a.CurrentDoc(); d == nil || d.Rel != "b.txt" || d.Pinned {
		t.Fatalf("再点选中的 b.txt 应该重新预览：%+v", d)
	}
	k.doubleClick("b.txt")
	if d := a.CurrentDoc(); d == nil || d.Rel != "b.txt" || !d.Pinned {
		t.Fatalf("双击 b.txt 应该固定打开：%+v", d)
	}

	// 点行下面的空白处：一行也不选，键盘还在树上
	x, y := k.blank("d.md")
	tt.ClickAt(x, y)
	for range 3 {
		tt.Frame()
	}
	if a.FileSel != -1 || a.FileChosen.Len() != 0 || !a.TreeFocused {
		t.Fatalf("点空白处：光标 %d，选中 %d 项，键盘在树上 %v", a.FileSel, a.FileChosen.Len(), a.TreeFocused)
	}
	// 之后的 ⇧点只选那一行（列表的锚点还在 b.txt 上，不从它选起）
	k.click(wui.Shift, "a.txt")
	if rows := a.ChosenRows(); len(rows) != 1 || rows[0].Path != "a.txt" {
		t.Fatalf("点空白处之后 ⇧点 a.txt：选中 %d 项，光标 %s", len(rows), k.sel())
	}

	// Esc：多选收成光标那一行
	k.click(0, "a.txt")
	k.click(wui.Cmd, "b.txt")
	if a.FileChosen.Len() != 2 {
		t.Fatalf("⌘点之后选中 %d 项", a.FileChosen.Len())
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if rows := a.ChosenRows(); len(rows) != 1 || rows[0].Path != k.sel() {
		t.Fatalf("Esc 之后选中 %d 项，光标 %s", len(rows), k.sel())
	}

	// 右键没选中的行：先只选中它，不预览；右键多选里的一行：选中不动，菜单对全部
	a.Exec("file.closeAll")
	tt.Frame()
	if err := tt.RightClick("d.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if k.sel() != "docs/d.md" || a.FileChosen.Len() != 1 || a.CurrentDoc() != nil {
		t.Fatalf("右键 d.md：选中 %s / %d 项，当前标签 %v", k.sel(), a.FileChosen.Len(), a.CurrentDoc())
	}
	closeMenu(t, tt)
	k.click(0, "a.txt")
	k.click(wui.Cmd, "b.txt")
	a.Exec("file.closeAll")
	tt.Frame()
	if err := tt.RightClick("a.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if items := shownMenu(t, tt); a.FileChosen.Len() != 2 || !hasItem(items, "复制 2 项") {
		t.Fatalf("右键多选里的 a.txt：选中 %d 项，菜单 %q", a.FileChosen.Len(), items)
	}
	closeMenu(t, tt)
}

// 右键菜单：文件的暂存 / 取消暂存，没跟踪的只有暂存；添加到 .gitignore（已经忽略的灰着）；
// 复制 / 粘贴 / 创建副本（重名加"副本"，能跨仓库，⌘C ⌘V ⌘D 和菜单栏的拷贝 / 粘贴 / 全选）；
// 仓库菜单的收起其他仓库、展开有改动的目录，空白处的展开所有仓库有改动的目录；悬停提示。
func TestTreeContextMenus(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("a.txt", "a\n")
	repo.Write("b.txt", "b\n")
	repo.Write("docs/d.md", "d\n")
	repo.Commit("init")
	repo.Write("a.txt", "a2\n")          // 未暂存的改动
	repo.Write("new.txt", "n\n")         // 没跟踪
	repo.Write("build/out.bin", "bin\n") // 没跟踪的目录
	other := testutil.NewRepo(t, "other")
	other.Write("o.txt", "o\n")
	other.Commit("init")
	k := newTreeKit(t, repo.Root, other.Root)
	a, tt, wait := k.a, k.tt, k.wait
	rp, ro := k.repo("proj"), k.repo("other")
	var clipped []string
	keep := app.CopyFiles
	app.CopyFiles = func(p []string) { clipped = p }
	t.Cleanup(func() { app.CopyFiles = keep })
	wait("文件树", func() bool { return tt.HasText("new.txt") && tt.HasText("o.txt") })

	// 悬停提示：文件是大小 · 修改时间 · 状态，目录是文件数
	tip := func(path string) string {
		for _, row := range a.TreeRows() {
			if row.Repo == rp && !row.Root && row.Note == "" && row.Path == path {
				return a.RowTip(row)
			}
		}
		t.Fatalf("树里没有 %s", path)
		return ""
	}
	if s := tip("b.txt"); !strings.HasPrefix(s, "2 B · ") || !strings.HasSuffix(s, " · 没有改动") {
		t.Errorf("b.txt 的提示 %q", s)
	}
	if s := tip("a.txt"); !strings.HasPrefix(s, "3 B · ") || !strings.HasSuffix(s, " · 未暂存：已修改") {
		t.Errorf("a.txt 的提示 %q", s)
	}
	if s := tip("new.txt"); !strings.HasSuffix(s, " · 未跟踪") {
		t.Errorf("new.txt 的提示 %q", s)
	}
	// 目录：文件数马上有，大小和最近改动后台算
	if s := tip("docs"); s != "1 个文件 · 正在算…" {
		t.Errorf("docs 的提示 %q", s)
	}
	wait("docs 算好", func() bool { return strings.HasPrefix(tip("docs"), "1 个文件 · 共 ") })

	// 菜单的分组：打开 | 新建 | Git | 访达 | 路径 | 复制粘贴 | 重命名、废纸篓；没跟踪的没有"删除文件…"
	if err := tt.RightClick("new.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	items := shownMenu(t, tt)
	order := []string{"打开", "新建文件…", "暂存（git add）", "添加到 .gitignore", "在 Finder 中显示", "复制路径", "复制", "粘贴", "创建副本", "重命名…"}
	last := -1
	for _, it := range order {
		i := slices.Index(items, it)
		if i <= last {
			t.Fatalf("new.txt 的菜单 %q：%q 不在该在的位置", items, it)
		}
		last = i
	}
	if hasItem(items, "删除文件…") {
		t.Fatalf("文件树里没跟踪的文件不该有删除文件：%q", items)
	}
	if err := chooseMenu(t, tt, "粘贴"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("还没复制过，粘贴应该灰着：%v", err)
	}
	closeMenu(t, tt)

	// 暂存、取消暂存（和 Git 栏一样的命令）
	staged := func(kind git.Kind) func() bool {
		return func() bool {
			return idle(rp)() && slices.ContainsFunc(rp.Status.Changes, func(ch git.Change) bool { return ch.Path == "a.txt" && ch.Kind == kind })
		}
	}
	k.menu("a.txt", "暂存")
	wait("暂存 a.txt", staged(git.Staged))
	k.menu("a.txt", "取消暂存")
	wait("取消暂存 a.txt", staged(git.Unstaged))

	// 添加到 .gitignore：文件、目录各一行，写好了刷新（没开"显示被忽略的文件"时它们从树里没了）
	gi := filepath.Join(rp.Root, ".gitignore")
	k.menu("new.txt", "添加到 .gitignore")
	wait("忽略 new.txt", func() bool { return idle(rp)() && !tt.HasText("new.txt") && tt.HasText(".gitignore") })
	k.menu("build", "添加到 .gitignore")
	wait("忽略 build", func() bool { return idle(rp)() && !tt.HasText("build") })
	if b, _ := os.ReadFile(gi); string(b) != "/new.txt\n/build/\n" {
		t.Fatalf(".gitignore 是 %q", b)
	}
	a.ToggleShowIgnored()
	wait("显示被忽略的", func() bool { return tt.HasText("new.txt") })
	a.Exec("file.closeAll")
	tt.Frame()
	if err := tt.RightClick("new.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(t, tt, "添加到 .gitignore"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("已经被忽略的，添加到 .gitignore 应该灰着：%v", err)
	}
	closeMenu(t, tt)
	a.ToggleShowIgnored()
	wait("不显示被忽略的", func() bool { return !tt.HasText("new.txt") })

	// 创建副本：重名的加"副本"、再加序号
	k.menu("b.txt", "创建副本")
	wait("b 副本.txt", func() bool { return tt.HasText("b 副本.txt") })
	k.menu("b.txt", "创建副本")
	wait("b 副本 2.txt", func() bool { return tt.HasText("b 副本 2.txt") })

	// ⌘C 记下、系统剪贴板放文件；⌘V 粘贴到选中的目录里，菜单栏的粘贴再来一份（重名加副本）
	k.click(0, "b.txt")
	tt.Key(wui.Cmd, wui.KeyC)
	want := []string{filepath.Join(rp.Root, "b.txt")}
	if !slices.Equal(a.TreeClip, want) || !slices.Equal(clipped, want) {
		t.Fatalf("⌘C：记下 %q，剪贴板 %q", a.TreeClip, clipped)
	}
	k.click(0, "docs")
	tt.Key(wui.Cmd, wui.KeyV)
	wait("粘贴到 docs", func() bool { return idle(rp)() && exists(filepath.Join(rp.Root, "docs/b.txt")) })
	tt.Command("paste")
	wait("再粘贴一份", func() bool { return idle(rp)() && exists(filepath.Join(rp.Root, "docs/b 副本.txt")) })
	// 跨仓库：仓库行菜单的粘贴放到仓库根
	k.menu("other（仓库）", "粘贴")
	wait("粘贴到 other", func() bool { return idle(ro)() && exists(filepath.Join(ro.Root, "b.txt")) })

	// 菜单栏"编辑"：拷贝、全选（窗口里 ⌘C ⌘A 走这条）
	k.click(0, "a.txt")
	tt.Command("copy")
	if want := []string{filepath.Join(rp.Root, "a.txt")}; !slices.Equal(a.TreeClip, want) {
		t.Fatalf("拷贝：记下 %q", a.TreeClip)
	}
	tt.Command("selectAll")
	n := 0
	for _, row := range a.TreeRows() {
		if row.Note == "" && !row.Edit {
			n++
		}
	}
	if a.FileChosen.Len() != n {
		t.Fatalf("全选：选中 %d 行，有 %d 行", a.FileChosen.Len(), n)
	}

	// ⌘D：菜单栏里是终端的分屏，键盘在文件树上时 RunKey 落到创建副本
	k.click(0, "o.txt")
	if !a.TreeFocused || !a.RunKey(wui.Cmd, wui.KeyD) {
		t.Fatalf("键盘在树上 %v，⌘D 没分派", a.TreeFocused)
	}
	wait("o 副本.txt", func() bool { return idle(ro)() && exists(filepath.Join(ro.Root, "o 副本.txt")) })

	// 仓库菜单：收起其他仓库；展开有改动的目录
	k.menu("proj（仓库）", "收起其他仓库")
	if !rp.Expanded || ro.Expanded {
		t.Fatalf("收起其他仓库：proj %v，other %v", rp.Expanded, ro.Expanded)
	}
	repo.Write("docs/d.md", "d2\n")
	a.RefreshRepo(rp, app.WhyManual)
	wait("d.md 改了", func() bool {
		return idle(rp)() && slices.ContainsFunc(rp.Status.Changes, func(ch git.Change) bool { return ch.Path == "docs/d.md" })
	})
	a.CollapseAll()
	tt.Frame()
	k.menu("proj（仓库）", "展开有改动的目录")
	if !rp.Files.Open["docs"] {
		t.Fatal("展开有改动的目录：docs 没展开")
	}
	// 空白处：展开所有仓库有改动的目录（other 收着、根上有新文件：只展开仓库）
	a.CollapseAll()
	tt.Frame()
	tt.Frame()
	x, y := k.blank("other（仓库）")
	tt.RightClickAt(x, y)
	tt.Frame()
	if err := chooseMenu(t, tt, "展开所有仓库有改动的目录"); err != nil {
		t.Fatalf("空白处的菜单 %q: %v", shownMenu(t, tt), err)
	}
	tt.Frame()
	if !rp.Files.Open["docs"] || !ro.Expanded {
		t.Fatalf("展开所有仓库有改动的目录：docs %v，other %v", rp.Files.Open["docs"], ro.Expanded)
	}
	// 命令面板里的同名命令：侧栏在 Git 页也切回文件再展开
	a.CollapseAll()
	a.ShowSidebar(workspace.ModeGit)
	a.Exec("tree.expandChangedAll")
	tt.Frame()
	if a.St.SidebarMode != workspace.ModeFiles || !rp.Files.Open["docs"] || !ro.Expanded {
		t.Fatalf("tree.expandChangedAll：侧栏 %s，docs %v，other %v", a.St.SidebarMode, rp.Files.Open["docs"], ro.Expanded)
	}

	// 撤销文件操作：键盘在树上时菜单栏"编辑"的撤销（窗口里 ⌘Z 走这条）、没有菜单栏时的 ⌘Z；
	// 空白处菜单写着下一次要撤的（这里是复制进来的，不点：会进真的废纸篓）
	for _, undo := range []func(){func() { tt.Command("undo") }, func() { tt.Key(wui.Cmd, wui.KeyZ) }} {
		k.click(0, "a.txt")
		a.MoveTree(app.TreeDrag{Repo: rp, Paths: []string{"a.txt"}}, rp, "docs")
		wait("移到 docs", func() bool { return idle(rp)() && exists(filepath.Join(rp.Root, "docs/a.txt")) && a.TreeFocused })
		undo()
		if done := "已撤销：移动 a.txt → docs"; !exists(filepath.Join(rp.Root, "a.txt")) || a.Toast != done && !tt.HasText(done) {
			t.Fatalf("没撤销：toast %q", a.Toast)
		}
		wait("树里回来", func() bool { return idle(rp)() && tt.HasText("a.txt") })
	}
	x, y = k.blank("other（仓库）")
	tt.RightClickAt(x, y)
	tt.Frame()
	if !slices.Contains(shownMenu(t, tt), "撤销：复制 o.txt → 仓库根目录") {
		t.Fatalf("空白处的菜单 %q", shownMenu(t, tt))
	}
	closeMenu(t, tt)
}
