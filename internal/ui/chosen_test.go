package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 文件树多选，三个临时仓库跨着选：点一行预览；⌘点加一行、再 ⌘点取消都不预览；右键选中的一行是多选菜单
// （复制路径一行一个、在 Finder 中显示、看有改动的 diff），右键没选中的一行只对它、原来的多选不变；回车把选中的文件都固定打开；
// F2 只对一项；新建放在光标行的目录；⇧点选一段（跨仓库连仓库行，这时 ⌘⌫ 不动）；⌘⌫ 只问一次、废纸篓收到全部、选了目录又选了它里面的只移目录；
// ⌘A 全选；筛选时筛掉的不算、清掉筛选又回来；移除仓库清掉选中。
func TestTreeMultiSelect(t *testing.T) {
	alpha, _, _, ws := testutil.ThreeRepos(t)
	alpha.Write("docs/x.md", "x\n")
	alpha.Write("docs/y.md", "y\n")
	a, tt, wait := newUI(t, ws, "")
	trashDir := t.TempDir()
	var trashed, revealed []string
	a.Trash = func(p string) error {
		trashed = append(trashed, p)
		return os.Rename(p, filepath.Join(trashDir, strconv.Itoa(len(trashed))+"-"+filepath.Base(p)))
	}
	a.InFinder = func(p string) { revealed = append(revealed, p) }
	var copied string
	keep := copyText
	copyText = func(s string) { copied = s }
	t.Cleanup(func() { copyText = keep })

	a.ShowSidebar(workspace.ModeFiles)
	for _, r := range a.Repos {
		if !r.Expanded {
			a.ToggleRepo(r)
		}
		a.RefreshRepo(r, app.WhyManual)
		a.EnsureFiles(r)
	}
	ra, rb, rc := a.Repos[0], a.Repos[1], a.Repos[2]
	wait("三个仓库的文件", func() bool {
		return loaded(a)() && ra.Files.Loaded && tt.HasText("notes.md") && tt.HasText("beta.go") && tt.HasText("gamma.md")
	})
	a.ToggleDir(ra, "docs")
	wait("docs 展开", func() bool { return tt.HasText("x.md") })

	chosen := func() []string {
		var out []string
		for _, row := range a.ChosenRows() {
			out = append(out, row.Repo.Name+":"+row.Path)
		}
		return out
	}
	expect := func(what string, want ...string) {
		t.Helper()
		if got := chosen(); !slices.Equal(got, want) {
			t.Fatalf("%s：选中 %q，想要 %q", what, got, want)
		}
	}
	// 标签和标题栏的面包屑里也有文件名（比侧栏先画）：点树里的名字之前先把标签都关掉
	click := func(mods wui.Modifiers, name string) {
		t.Helper()
		a.Exec("file.closeAll")
		tt.Frame()
		tt.Advance(time.Second) // 和上一次点隔开，不算双击
		if err := tt.ClickWith(mods, name); err != nil {
			t.Fatalf("点 %q: %v", name, err)
		}
		tt.Frame()
	}
	menu := func(name string, item string) {
		t.Helper()
		a.Exec("file.closeAll")
		tt.Frame()
		mustRightClick(t, tt, name, item)
	}
	treeKey := func(mods wui.Modifiers, key wui.Key) {
		a.FocusTree = true
		tt.Frame()
		tt.Key(mods, key)
		tt.Frame()
	}
	fileDoc := func(rel string) *docs.Doc {
		for _, d := range a.Docs {
			if d.Kind == docs.File && d.Rel == rel {
				return d
			}
		}
		return nil
	}

	// 点一行：只选它、预览
	click(0, "alpha.txt")
	expect("点 alpha.txt", "alpha:alpha.txt")
	if d := a.CurrentDoc(); d == nil || d.Rel != "alpha.txt" || d.Pinned {
		t.Fatalf("点一下应该预览 alpha.txt：%+v", d)
	}
	// ⌘点加上别的仓库的行，再 ⌘点取消一行：都不预览
	click(wui.Cmd, "beta.go")
	click(wui.Cmd, "gamma.md")
	expect("⌘点加两行", "alpha:alpha.txt", "beta:beta.go", "gamma:gamma.md")
	click(wui.Cmd, "beta.go")
	expect("⌘点取消", "alpha:alpha.txt", "gamma:gamma.md")
	if len(a.Docs) != 0 {
		t.Fatalf("⌘点不该打开文件：%d 个标签", len(a.Docs))
	}

	// 右键选中的一行：菜单作用于选中的两行
	menu("gamma.md", "复制 2 个仓库内路径")
	if copied != "alpha.txt\ngamma.md" {
		t.Fatalf("复制仓库内路径 %q", copied)
	}
	menu("gamma.md", "复制 2 个路径")
	if want := filepath.Join(ra.Root, "alpha.txt") + "\n" + filepath.Join(rc.Root, "gamma.md"); copied != want {
		t.Fatalf("复制路径 %q，想要 %q", copied, want)
	}
	menu("gamma.md", "在 Finder 中显示")
	if want := []string{filepath.Join(ra.Root, "alpha.txt"), filepath.Join(rc.Root, "gamma.md")}; !slices.Equal(revealed, want) {
		t.Fatalf("在 Finder 中显示了 %q", revealed)
	}
	menu("gamma.md", "查看 1 个文件的 diff") // gamma.md 没改过
	wait("alpha.txt 的 diff", func() bool {
		d := a.CurrentDoc()
		return len(a.Docs) == 1 && d != nil && d.Kind == docs.Diff && d.Rel == "alpha.txt"
	})
	// 右键没选中的一行：菜单只对它，原来的多选留着（VS Code 也这样）
	before := chosen()
	menu("notes.md", "复制仓库内路径")
	if copied != "notes.md" {
		t.Fatalf("右键没选中的行复制了 %q", copied)
	}
	if got := chosen(); !slices.Equal(got, before) {
		t.Fatalf("右键没选中的行改了选中：%q → %q", before, got)
	}
	// 用默认应用打开：经 a.ShowPath（不真的打开），打不开的是应用自己的提示
	var shown []string
	a.ShowPath = func(p string) error { shown = append(shown, p); return nil }
	menu("notes.md", "用默认应用打开")
	if want := []string{filepath.Join(ra.Root, "notes.md")}; !slices.Equal(shown, want) {
		t.Fatalf("用默认应用打开了 %q", shown)
	}
	a.ShowPath = func(string) error { return errors.New("没有能打开它的应用") }
	menu("notes.md", "用默认应用打开")
	if !a.AlertOpen || a.AlertTitle != "打不开文件" {
		t.Fatalf("打不开：alert=%v %q", a.AlertOpen, a.AlertTitle)
	}
	a.AlertOpen = false
	if len(a.Docs) != 0 {
		t.Fatal("右键不该预览")
	}

	// 回车：选中的文件都固定打开；F2 选了两项时不重命名
	click(0, "alpha.txt")
	click(wui.Cmd, "gamma.md")
	expect("再选两行", "alpha:alpha.txt", "gamma:gamma.md")
	treeKey(0, wui.KeyEnter)
	wait("两个都固定打开", func() bool {
		d1, d2 := fileDoc("alpha.txt"), fileDoc("gamma.md")
		return d1 != nil && d2 != nil && d1.Pinned && d2.Pinned
	})
	treeKey(0, wui.KeyF2)
	if a.Edit != nil {
		t.Fatal("选了两项时 F2 不该重命名")
	}

	// ⇧点一段：跨仓库连仓库行一起；有仓库行时 ⌘⌫ 不动
	click(0, "notes.md")
	click(wui.Shift, "beta.go")
	expect("⇧点一段", "alpha:notes.md", "beta:", "beta:beta.go")
	treeKey(wui.Cmd, wui.KeyBackspace)
	if a.Confirm != nil {
		t.Fatalf("选中有仓库行时不该问：%q", a.Confirm.Title)
	}

	// 新建文件放在光标那行（最后点的）的目录
	click(0, "beta.go")
	click(wui.Cmd, "x.md")
	expect("beta.go 加 x.md", "alpha:docs/x.md", "beta:beta.go")
	treeKey(wui.Cmd, wui.KeyN)
	if e := a.Edit; e == nil || e.Repo != ra || e.Dir != "docs" {
		t.Fatalf("⌘N 应该建在 alpha 的 docs 里：%+v", e)
	}
	a.CancelEdit()

	// ⌘⌫：只问一次；docs 和它里面的 x.md、y.md 只移 docs。
	// 取消新建之后选着的是 docs：单击它是它自己接的（列表不知道），收起它；列表的锚点还在 x.md 上、收起之后没了，
	// ⇧点要从 docs 选起（TreeListChanged）
	if !a.FileChosen.Has(app.FileKey{Root: ra.Root, Path: "docs"}) {
		t.Fatalf("取消新建之后该选着 docs：%q", chosen())
	}
	click(0, "docs")
	if tt.HasText("x.md") {
		t.Fatal("单击选着的 docs 该收起")
	}
	a.ToggleDir(ra, "docs")
	wait("docs 展开", func() bool { return tt.HasText("x.md") })
	click(wui.Shift, "alpha.txt")
	expect("⇧点从 docs 选起", "alpha:docs", "alpha:docs/x.md", "alpha:docs/y.md", "alpha:alpha.txt")
	click(wui.Cmd, "gamma.md")
	expect("docs 到 alpha.txt 加 gamma.md", "alpha:docs", "alpha:docs/x.md", "alpha:docs/y.md", "alpha:alpha.txt", "gamma:gamma.md")
	treeKey(wui.Cmd, wui.KeyBackspace)
	wait("确认框", func() bool { return tt.HasText("把 3 项移到废纸篓？") })
	if len(trashed) != 0 {
		t.Fatal("没确认就挪了")
	}
	mustClick(t, tt, "移到废纸篓")
	wait("挪走", func() bool {
		return len(trashed) == 3 && !hasFile(ra, "alpha.txt") && !hasFile(ra, "docs/x.md") && !hasFile(rc, "gamma.md")
	})
	if want := []string{filepath.Join(ra.Root, "docs"), filepath.Join(ra.Root, "alpha.txt"), filepath.Join(rc.Root, "gamma.md")}; !slices.Equal(trashed, want) {
		t.Fatalf("挪的是 %q，想要 %q", trashed, want)
	}
	if fileDoc("alpha.txt") != nil || fileDoc("gamma.md") != nil {
		t.Fatal("挪走的文件的标签（没改过）该关掉")
	}

	// ⌘A：看得见的行全选（说明行不算）
	treeKey(wui.Cmd, wui.KeyA)
	n := 0
	for _, row := range a.TreeRows() {
		if row.Note == "" {
			n++
		}
	}
	if got := len(a.ChosenRows()); got != n || n < 5 {
		t.Fatalf("⌘A 选了 %d 行，看得见的有 %d 行", got, n)
	}

	// 筛选：筛掉的不算，清掉筛选又回来
	a.SetFilter("beta")
	expect("筛 beta", "beta:", "beta:beta.go")
	a.SetFilter("")
	if got := len(a.ChosenRows()); got != n {
		t.Fatalf("清掉筛选后选中 %d 行，想要 %d", got, n)
	}

	// 移除仓库：选中清掉
	a.RemoveRepo(rb)
	if a.FileChosen.Len() != 0 {
		t.Fatalf("移除仓库后还选着 %d 行", a.FileChosen.Len())
	}
}
