package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// 历史里展开的提交：文件行右边是 +/−（是 0 的一边不画），二进制写"二进制"，详情里"N 个文件 · +A −D"。
func TestHistNumstat(t *testing.T) {
	repo := testutil.NewRepo(t, "num")
	repo.Write("a.txt", "1\n2\n3\n")
	repo.Write("logo.bin", "\x00\x01")
	repo.Commit("初始提交")
	repo.Write("a.txt", "1\n二\n3\n4\n")
	repo.Write("new.txt", "n1\nn2\nn3\n")
	repo.Write("logo.bin", "\x00\x02")
	repo.Commit("改三个文件")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	wait("历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 2 })
	mustClickRow(t, tt, "改三个文件")
	wait("展开", func() bool { return r.Hist.FilesFor != "" && !r.Hist.FilesLoading })
	tt.Frame()
	mustText(t, tt, "3 个文件 · +5 −1")
	for _, s := range []string{"+2", "−1", "+3", "二进制"} {
		if !shows(tt, s) {
			t.Errorf("没有 %q: %q", s, tt.Texts())
		}
	}
	if shows(tt, "−0") || shows(tt, "+0") {
		t.Errorf("是 0 的一边不该画: %q", tt.Texts())
	}
	accessCheck(t, tt, "带行数的提交文件")
}

// Git 栏里的贮藏点开看文件：点一下展开（箭头转过来、下面是文件和行数）、再点收起；箭头和右键"查看改动 / 收起"也行；
// 点里面的文件在预览 Diff 里看，选中停在文件上；未跟踪的文件也在。
func TestStashFileRows(t *testing.T) {
	repo := testutil.NewRepo(t, "stash")
	repo.Write("a.txt", "1\n2\n")
	repo.Commit("初始提交")
	repo.Write("a.txt", "1\n二\n3\n")
	repo.Write("新文件.txt", "n\n")
	repo.Git("stash", "push", "-q", "-u", "-m", "改了两个")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	showGroups(a)
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.ToggleGitRepo(r)
	tt.Frame()
	mustClickRow(t, tt, "展开 贮藏")
	mustText(t, tt, "改了两个")

	mustClickRow(t, tt, "改了两个")
	wait("读贮藏的文件", func() bool { return r.StashFilesFor != "" })
	tt.Frame()
	for _, s := range []string{"a.txt", "新文件.txt", "+2", "−1", "收起 stash@{0}"} {
		if !shows(tt, s) {
			t.Errorf("展开后没有 %q: %q", s, tt.Texts())
		}
	}
	rows := a.GitRows()
	if a.GitSel < 0 || rows[a.GitSel].Kind != app.RowStash {
		t.Errorf("点贮藏应选中它: %d", a.GitSel)
	}
	accessCheck(t, tt, "展开的贮藏")

	mustClickRow(t, tt, "新文件.txt")
	tt.Frame()
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || d.Change.Kind != git.InCommit || d.Change.Path != "新文件.txt" {
		t.Fatalf("点贮藏里的文件应看它的 Diff: %+v", d)
	}
	if rows := a.GitRows(); a.GitSel < 0 || rows[a.GitSel].Kind != app.RowStashFile || rows[a.GitSel].Change.Path != "新文件.txt" {
		t.Errorf("选中应在贮藏里的文件上: %d", a.GitSel)
	}
	wait("读 Diff", func() bool { return !d.Loading })
	if string(d.Raw[1]) != "n\n" {
		t.Errorf("未跟踪文件的内容 %q", d.Raw[1])
	}

	// 再点贮藏这一行：收起，选中回到它上面；它已经选中了再点也是展开 / 收起
	mustClickRow(t, tt, "改了两个")
	if r.StashOpen != "" || shows(tt, "新文件.txt") {
		t.Fatalf("再点应收起: %q", r.StashOpen)
	}
	if rows := a.GitRows(); a.GitSel < 0 || rows[a.GitSel].Kind != app.RowStash {
		t.Errorf("收起后选中应在贮藏上: %d", a.GitSel)
	}
	mustClickRow(t, tt, "改了两个")
	wait("读贮藏的文件", func() bool { return r.StashFilesFor != "" })
	mustClickRow(t, tt, "改了两个")
	if r.StashOpen != "" {
		t.Fatalf("选中的贮藏再点应收起: %q", r.StashOpen)
	}
	// 箭头、右键
	mustClickRow(t, tt, "展开 stash@{0}")
	if r.StashOpen == "" {
		t.Fatal("点箭头应展开")
	}
	mustRightClickRow(t, tt, "改了两个", "收起")
	if r.StashOpen != "" {
		t.Fatal("右键收起没收")
	}
	mustRightClickRow(t, tt, "改了两个", "查看改动")
	if r.StashOpen == "" {
		t.Fatal("右键查看改动没展开")
	}
}

// 比较两个提交：右键 C"标记为比较起点"、右键 A"与 C 比较"→ 胶囊和比较段（早的在前），文件行带行数；
// 点文件开比较的 Diff（标签头写两个短哈希）；点标题收起、再点展开；× 清掉。有提交图和没有都画得出来。
func TestCompareRows(t *testing.T) {
	repo := testutil.NewRepo(t, "cmp")
	t0 := time.Now().Add(-3 * time.Hour)
	repo.Write("a.txt", "1\n2\n")
	repo.Write("b.txt", "b\n")
	repo.CommitAt("提交甲", t0)
	repo.Write("a.txt", "1\n二\n")
	repo.CommitAt("提交乙", t0.Add(time.Hour))
	repo.Write("a.txt", "1\n二\n3\n")
	repo.Write("c.txt", "c\n")
	repo.Git("rm", "-q", "b.txt")
	repo.CommitAt("提交丙", t0.Add(2*time.Hour)) // 起点标在晚的上，比较时早的在前
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	h := &r.Hist
	wait("历史", func() bool { return h.Loaded && !h.Loading && len(h.Commits) == 3 })
	tt.Frame()
	if !a.HistGraph(r) {
		t.Fatal("默认应画提交图")
	}
	cC, cA := h.Commits[0], h.Commits[2]

	mustRightClickRow(t, tt, "提交丙", "标记为比较起点")
	if h.CompareFrom == nil || h.CompareFrom.Hash != cC.Hash {
		t.Fatalf("起点 %+v", h.CompareFrom)
	}
	if err := rightClickRow(tt, "提交甲"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(t, tt, "与 "+cC.Short+" 比较"); err != nil {
		t.Fatalf("没有与 %s 比较: %v；菜单=%q", cC.Short, err, shownMenu(t, tt))
	}
	tt.Frame()
	wait("比较", func() bool { return h.Compare != nil && !h.Compare.Loading })
	tt.Frame()
	title := "比较 " + cA.Short + " → " + cC.Short + "（3 个文件，+3 −2）"
	mustText(t, tt, "比较："+cA.Short+" → "+cC.Short+" · 3 个文件")
	mustText(t, tt, title)
	mustText(t, tt, "c.txt")
	accessCheck(t, tt, "比较两个提交（有提交图）")

	mustClickRow(t, tt, "c.txt")
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || !d.Change.Range || d.Change.Path != "c.txt" {
		t.Fatalf("点比较里的文件应开它的 Diff: %+v", d)
	}
	wait("Diff", func() bool { return !d.Loading })
	tt.Frame()
	mustText(t, tt, cA.Short+" ↔ "+cC.Short)

	tt.Advance(time.Second)
	mustClickRow(t, tt, title)
	if h.Compare.Open || shows(tt, "c.txt") {
		t.Fatalf("点标题应收起: open %v", h.Compare.Open)
	}
	tt.Advance(time.Second)
	mustClickRow(t, tt, title)
	if !h.Compare.Open || !shows(tt, "c.txt") {
		t.Fatal("再点标题应展开")
	}
	// 右键标题是 Holt 菜单
	if err := rightClickRow(tt, title); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.Menu() != nil || !a.Menu.IsOpen() {
		t.Fatalf("比较行的右键该是 Holt 菜单：系统菜单 %q", tt.Menu())
	}
	if err := chooseMenu(t, tt, "收起"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if h.Compare.Open {
		t.Fatal("右键收起没收")
	}
	tt.Advance(time.Second)
	mustClickRow(t, tt, title)

	a.ToggleHistGraph()
	tt.Frame()
	mustText(t, tt, title)
	accessCheck(t, tt, "比较两个提交（没有提交图）")

	mustClickRow(t, tt, "不再比较这两个提交")
	if h.Compare != nil || shows(tt, title) {
		t.Error("× 应清掉比较")
	}
}

// Git 栏多选：点一行预览；⌘点加行、取消都不预览；右键没选中的一行只对它；右键选中的一行是多选菜单
// （复制路径、暂存 N 项）；回车把选中的都开成固定的 Diff；Esc 收回到当前标签那一行。
func TestGitMultiSelect(t *testing.T) {
	_, _, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := newUI(t, ws, "")
	var copied string
	keep := copyText
	copyText = func(s string) { copied = s }
	t.Cleanup(func() { copyText = keep })
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("git status", loaded(a))
	tt.Frame()
	alpha, beta := a.Repos[0], a.Repos[1]
	chosen := func() []string {
		var out []string
		for _, cr := range a.ChosenChanges() {
			out = append(out, cr.Change.Path)
		}
		return out
	}
	click := func(mods wui.Modifiers, name string) {
		t.Helper()
		if err := clickRowWith(tt, mods, name); err != nil {
			t.Fatalf("点 %q: %v", name, err)
		}
		tt.Frame()
	}

	click(0, "notes.md") // 标题栏的面包屑里会有当前文件名：先点的那个以后不再按名字点
	if d := a.CurrentDoc(); d == nil || d.Change.Path != "notes.md" || d.Pinned {
		t.Fatalf("点一下应预览 notes.md: %+v", d)
	}
	click(wui.Cmd, "beta.go")
	click(wui.Cmd, "alpha.txt")
	if got := chosen(); !slices.Equal(got, []string{"alpha.txt", "notes.md", "beta.go"}) || len(a.Docs) != 1 || a.CurrentDoc().Change.Path != "notes.md" {
		t.Fatalf("⌘点两行后选中 %q、%d 个标签", got, len(a.Docs))
	}
	click(wui.Cmd, "beta.go")
	if got := chosen(); !slices.Equal(got, []string{"alpha.txt", "notes.md"}) || len(a.Docs) != 1 {
		t.Fatalf("⌘点取消后选中 %q", got)
	}
	accessCheck(t, tt, "Git 栏多选")

	// 右键没选中的一行：它自己的菜单，选中不变
	if err := rightClickRow(tt, "beta.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m := shownMenu(t, tt); !slices.Contains(m, "暂存") || slices.Contains(m, "暂存 2 项") {
		t.Errorf("没选中的那行的菜单 %q", m)
	}
	closeMenu(t, tt)
	tt.Frame()
	if got := chosen(); len(got) != 2 {
		t.Fatalf("右键没选中的那行改了选中: %q", got)
	}
	mustRightClickRow(t, tt, "alpha.txt", "复制 2 个仓库内路径")
	if copied != "alpha.txt\nnotes.md" {
		t.Errorf("复制 %q", copied)
	}

	// 回车：两个都开成固定的 Diff，多选还在
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	pinned := 0
	for _, d := range a.Docs {
		if d.Kind == docs.Diff && d.Pinned {
			pinned++
		}
	}
	if pinned != 2 || len(chosen()) != 2 {
		t.Fatalf("回车后 %d 个固定的 Diff、选中 %q", pinned, chosen())
	}

	// 右键选中的一行"暂存 2 项"：两个都暂存
	mustRightClickRow(t, tt, "alpha.txt", "暂存 2 项")
	wait("暂存", func() bool { return idle(alpha)() && alpha.Status.Staged == 2 })
	if alpha.Status.Unstaged+alpha.Status.Untracked != 0 || beta.Status.Staged != 0 {
		t.Errorf("暂存后 alpha %+v beta %+v", alpha.Status, beta.Status)
	}

	// Esc：不再多选
	tt.Frame()
	click(0, "beta.go")
	click(wui.Cmd, "notes.md")
	if a.GitChosen.Len() != 2 {
		t.Fatalf("再选两行: %d", a.GitChosen.Len())
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if rows := a.GitRows(); a.GitChosen.Len() != 1 || a.GitSel < 0 || rows[a.GitSel].Change.Path != "beta.go" {
		t.Errorf("Esc 后选着 %d 行（sel %d）", a.GitChosen.Len(), a.GitSel)
	}
}

// 分支菜单的"标签"子菜单："v1 · 刚刚"，选了右栏看它的历史：胶囊"标签：v1"、仓库名后面"v1（标签）"，
// 点胶囊上的 × 回到自己的历史；没有标签时是一行灰的"没有标签"。
func TestTagsMenu(t *testing.T) {
	repo := testutil.NewRepo(t, "tags")
	repo.Write("a.txt", "1\n")
	repo.Commit("打标签的提交")
	repo.Git("tag", "v1")
	repo.Write("a.txt", "2\n")
	repo.Commit("后来的提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	h := &r.Hist
	loaded := func() bool { return h.Loaded && !h.Loading }
	tt.Frame()

	mustClickRow(t, tt, "分支 main")
	if m := shownMenu(t, tt); !slices.Contains(m, "标签") || slices.Index(m, "标签") > slices.Index(m, "新建分支…") {
		t.Fatalf("分支菜单：%v", m)
	}
	if err := chooseMenu(t, tt, "标签", "v1 · "+git.Ago(r.Tags[0].Date)); err != nil {
		t.Fatal(err)
	}
	wait("标签的历史", func() bool { return loaded() && h.RefKind == app.RefTag && len(h.Commits) == 1 })
	tt.Frame()
	for _, s := range []string{"标签：v1", "v1（标签）", "打标签的提交"} {
		if !shows(tt, s) {
			t.Errorf("没有 %q: %q", s, tt.Texts())
		}
	}
	if shows(tt, "后来的提交") {
		t.Errorf("标签后面的提交不该在: %q", tt.Texts())
	}
	accessCheck(t, tt, "看标签的历史")

	mustClickRow(t, tt, "回到这个仓库自己的历史")
	wait("回到自己的历史", func() bool { return loaded() && h.RefKind == app.RefNone && len(h.Commits) == 2 })
	tt.Frame()
	if shows(tt, "标签：v1") {
		t.Errorf("× 之后胶囊还在: %q", tt.Texts())
	}

	repo.Git("tag", "-d", "v1")
	a.RefreshRepo(r, app.WhyManual)
	wait("标签删了", func() bool { return idle(r)() && len(r.Tags) == 0 })
	mustClickRow(t, tt, "分支 main")
	if err := chooseMenu(t, tt, "标签", "没有标签"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("没有标签时：%v", err)
	}
	closeMenu(t, tt)
}

// 仓库菜单的"远程"子菜单：每个远程"origin  地址"（复制地址、提示一句）和"抓取 origin"（git fetch --prune origin），
// 推送地址不一样时多一行灰的"推送：地址"；没有远程是一行灰的"没有远程"。
func TestRemotesMenu(t *testing.T) {
	up := testutil.NewRepo(t, "up")
	up.Write("a.txt", "1\n")
	up.Commit("上游的提交")
	repo := testutil.NewRepo(t, "local")
	repo.Write("b.txt", "1\n")
	repo.Commit("本地的提交")
	repo.Git("remote", "add", "origin", up.Root)
	var copied string
	keep := copyText
	copyText = func(s string) { copied = s }
	t.Cleanup(func() { copyText = keep })
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", func() bool { return idle(r)() && len(r.Extra.Remotes) == 1 })
	tt.Frame()
	row := r.Name + "（仓库）"
	open := func() {
		t.Helper()
		if err := rightClickRow(tt, row); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(shownMenu(t, tt), "更多") {
			t.Fatalf("仓库菜单：%v", shownMenu(t, tt))
		}
	}

	open()
	if err := chooseMenu(t, tt, "更多", "远程", "origin  "+up.Root); err != nil {
		t.Fatal(err)
	}
	if copied != up.Root || a.Toast != "已复制 origin 的地址" && !tt.HasText("已复制 origin 的地址") {
		t.Errorf("复制地址：%q，提示 %q", copied, a.Toast)
	}
	open()
	if err := chooseMenu(t, tt, "更多", "远程", "推送："+up.Root); err == nil || !strings.Contains(err.Error(), "no item") {
		t.Errorf("推送地址一样时不该有那一行：%v", err)
	}
	closeMenu(t, tt)
	open()
	if err := chooseMenu(t, tt, "更多", "远程", "抓取 origin"); err != nil {
		t.Fatal(err)
	}
	wait("抓取", func() bool {
		return idle(r)() && slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Remote && b.Name == "origin/main" })
	})
	if !slices.ContainsFunc(a.Output, func(o app.LogEntry) bool { return o.Title == "$ git fetch --prune origin" && !o.Failed }) {
		t.Errorf("输出栏：%+v", a.Output)
	}

	push := t.TempDir()
	repo.Git("remote", "set-url", "--push", "origin", push)
	a.RefreshRepo(r, app.WhyManual)
	wait("推送地址", func() bool { return idle(r)() && len(r.Extra.Remotes) == 1 && r.Extra.Remotes[0].Push == push })
	open()
	if err := chooseMenu(t, tt, "更多", "远程", "推送："+push); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("推送地址：%v", err)
	}
	closeMenu(t, tt)

	repo.Git("remote", "remove", "origin")
	a.RefreshRepo(r, app.WhyManual)
	wait("没有远程", func() bool { return idle(r)() && len(r.Extra.Remotes) == 0 })
	open()
	if err := chooseMenu(t, tt, "更多", "远程", "没有远程"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("没有远程：%v", err)
	}
	closeMenu(t, tt)
}

// 未暂存和未跟踪合成一组：只有未跟踪的文件时组名仍是"改动"，没有丢弃按钮（git restore . 不动未跟踪的），+ 照样有；
// 有已跟踪的改动后才出丢弃。贮藏的组头不写"贮藏"两个字（只有图标和条数，读屏照旧）。
func TestUnstagedGroupWithUntracked(t *testing.T) {
	repo := testutil.NewRepo(t, "mix")
	repo.Write("a.txt", "一\n")
	repo.Commit("初始")
	repo.Write("b.txt", "藏\n")
	repo.Git("add", "b.txt")
	repo.Git("stash", "-q")
	repo.Write("new.txt", "新\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	showGroups(a)
	a.St.BottomBar = false
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.Exec("view.git")
	tt.Frame()
	hoverOn(t, tt, "收起 改动")
	mustText(t, tt, "暂存所有未暂存的改动")
	if tt.HasText("未跟踪") || tt.HasText("丢弃所有未暂存的改动…") {
		t.Fatalf("只有未跟踪的：组名应是改动、没有丢弃: %q", tt.Texts())
	}
	// 贮藏组头：读屏有"贮藏"，画出来的字里只有条数
	mustText(t, tt, "展开 贮藏")
	// 和只有图标的"未暂存"组头一样：一行的提示和图标的 Label 各一个；以前还多一个画出来的"贮藏"
	if n := countText(tt, "贮藏"); n != 2 {
		t.Errorf("贮藏组头应只有图标：%d 个\"贮藏\" %q", n, tt.Texts())
	}
	repo.Write("a.txt", "二\n")
	a.RefreshRepo(r, app.WhyManual)
	wait("改了 a.txt", func() bool { return idle(r)() && r.Status.Unstaged == 1 })
	tt.Frame()
	mustText(t, tt, "丢弃所有未暂存的改动…")
	rows := a.GitRows()
	var got []string
	for _, cr := range rows {
		switch cr.Kind {
		case app.RowCommit:
			got = append(got, "✎")
		case app.RowGroup:
			got = append(got, "="+cr.Title+strconv.Itoa(cr.Count))
		case app.RowChange:
			got = append(got, cr.Change.Path)
		case app.RowStashes:
			got = append(got, "#stash")
		}
	}
	if s := strings.Join(got, " "); s != "✎ =改动2 a.txt new.txt #stash" {
		t.Errorf("行：%s", s)
	}
}

// TestGitRowsHoltMenu：Git 栏的变更行、贮藏里的文件、未跟踪目录行和历史的标题行、比较行的右键是 Holt 画的菜单；
// 行上装了 menuPress 以后单击（预览）、双击（固定）、点目录行展开照旧；已暂存和未暂存的同一个文件是两份菜单。
func TestGitRowsHoltMenu(t *testing.T) {
	repo := testutil.NewRepo(t, "rows")
	repo.Write("a.txt", "1\n")
	repo.Write("s.txt", "s\n")
	repo.Commit("初始")
	repo.Write("s.txt", "s2\n")
	repo.Git("add", "s.txt")
	repo.Write("s.txt", "s3\n") // 已暂存、未暂存各一行
	repo.Write("st.txt", "藏\n")
	repo.Git("stash", "push", "-q", "-u", "-m", "藏一个")
	repo.Write("a.txt", "改\n")
	repo.Write("s.txt", "s2\n")
	repo.Git("add", "s.txt")
	repo.Write("s.txt", "s3\n")
	repo.Write("dir/x.txt", "x\n")
	repo.Write("dir/y.txt", "y\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	showGroups(a)
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	wait("历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading })
	tt.Frame()

	grove := func(what string, want ...string) {
		t.Helper()
		tt.Frame()
		if tt.Menu() != nil {
			t.Fatalf("%s：开的是系统菜单 %q", what, tt.Menu())
		}
		if !a.Menu.IsOpen() {
			t.Fatalf("%s：没开 Holt 菜单", what)
		}
		m := shownMenu(t, tt)
		for _, w := range want {
			if !slices.Contains(m, w) {
				t.Errorf("%s：菜单里没有 %q：%q", what, w, m)
			}
		}
		closeMenu(t, tt)
		tt.Frame()
	}
	at := func(name string) (float32, float32) {
		t.Helper()
		x, y, err := rowCenter(tt, name)
		if err != nil {
			t.Fatalf("%v；texts=%q", err, tt.Texts())
		}
		return x, y
	}

	// 已暂存、未暂存的 s.txt 各是一份：右键前面那行，收起它那一组再右键剩下的那行
	tt.RightClickAt(at("s.txt"))
	tt.Frame()
	first, firstMenu := a.Menu.Owner, shownMenu(t, tt)
	grove("前面那行 s.txt")
	group := "收起 已暂存"
	if slices.Contains(firstMenu, "暂存") {
		group = "收起 改动"
	}
	mustClickRow(t, tt, group)
	tt.RightClickAt(at("s.txt"))
	tt.Frame()
	if a.Menu.Owner == first || slices.Equal(shownMenu(t, tt), firstMenu) {
		t.Errorf("已暂存、未暂存的 s.txt 是同一份菜单：%v %q", first, firstMenu)
	}
	grove("后面那行 s.txt")
	mustClickRow(t, tt, strings.Replace(group, "收起", "展开", 1))

	// 单击预览、双击固定照旧
	tt.Advance(time.Second)
	tt.ClickAt(at("a.txt"))
	tt.Frame()
	if d := a.CurrentDoc(); d == nil || d.Change.Path != "a.txt" || d.Pinned {
		t.Fatalf("单击变更行应预览 a.txt：%+v", d)
	}
	tt.Advance(time.Second)
	x, y := at("a.txt")
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	tt.Frame()
	if d := a.CurrentDoc(); d == nil || d.Change.Path != "a.txt" || !d.Pinned {
		t.Fatalf("双击变更行应固定 a.txt：%+v", d)
	}

	// 多选：右键选中的一行是选中的全部
	tt.Advance(time.Second)
	x, y = at("s.txt")
	tt.ClickAtWith(wui.Cmd, x, y)
	tt.Frame()
	if n := len(a.ChosenChanges()); n != 2 {
		t.Fatalf("⌘点后选中 %d 行", n)
	}
	tt.RightClickAt(at("a.txt"))
	grove("多选", "查看 2 个文件的 diff", "复制 2 个仓库内路径")
	tt.RightClickAt(at("dir/"))
	grove("没选中的目录行", "展开")

	// 未跟踪目录行：单击展开，右键是 Holt 菜单（收起在里面）
	tt.Key(0, wui.KeyEscape)
	tt.Advance(time.Second)
	tt.Frame()
	mustClickRow(t, tt, "dir/")
	if !r.DirOpen["dir/"] {
		t.Fatalf("单击未跟踪目录应展开：%v", r.DirOpen)
	}
	if err := rightClickRow(tt, "dir/"); err != nil {
		t.Fatal(err)
	}
	grove("未跟踪目录", "收起")

	// 贮藏里的文件
	mustClickRow(t, tt, "展开 贮藏")
	mustClickRow(t, tt, "藏一个")
	wait("读贮藏的文件", func() bool { return r.StashFilesFor != "" })
	tt.Frame()
	if err := rightClickRow(tt, "st.txt"); err != nil {
		t.Fatal(err)
	}
	grove("贮藏里的文件", "只应用这个文件")

	// 历史的标题行：提交图开关
	graph := !a.St.HistNoGraph
	mustRightClickRow(t, tt, "历史", "提交图")
	if !a.St.HistNoGraph != !graph {
		t.Fatal("右键历史标题行的提交图没切换")
	}
	a.ToggleHistGraph()
	tt.Frame()
}

// 变更行放不下时路径先缩，文件名不截断；文件名比整行还长时才截断，行尾的状态字母还在行里。
func TestChangeNameFirst(t *testing.T) {
	repo := testutil.NewRepo(t, "names")
	long := strings.Repeat("很长的名字", 12) + ".vue"
	deep := "src/views/achievementCollection/components/forms/AdminConfig.vue"
	repo.Write(deep, "a\n")
	repo.Write("lib/"+long, "a\n")
	repo.Commit("初始提交")
	repo.Write(deep, "b\n")
	repo.Write("lib/"+long, "b\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Unstaged == 2 })

	width := func(side float32) wui.Rect {
		t.Helper()
		a.St.SidebarW = side
		tt.Frame()
		tt.Frame()
		b, ok := tt.Find("AdminConfig.vue")
		if !ok {
			t.Fatalf("没有文件名: %q", tt.Texts())
		}
		return b
	}
	wide, narrow := width(640), width(240)
	if wide.W != narrow.W {
		t.Errorf("路径还能缩时文件名被压了：宽 %.1f 窄 %.1f", wide.W, narrow.W)
	}
	name, ok := tt.Find(long)
	if !ok {
		t.Fatalf("没有长文件名: %q", tt.Texts())
	}
	letter, ok := tt.Find("M")
	if !ok || letter.X+letter.W > 240 {
		t.Errorf("行尾的状态字母不在行里: %v %v", letter, ok)
	}
	if name.X+name.W > letter.X {
		t.Errorf("长文件名没截断，压到了状态字母: 名 %v 字母 %v", name, letter)
	}
}

// 变更行的状态字母在行尾，悬停时让给按钮，文件名不动；提交里的文件字母排在行数右边。
func TestChangeLetterAtEnd(t *testing.T) {
	repo := testutil.NewRepo(t, "letter")
	repo.Write("src/a.txt", "1\n")
	repo.Commit("初始提交")
	repo.Write("src/a.txt", "1\n2\n")
	repo.Commit("加一行")
	repo.Write("src/a.txt", "1\n2\n3\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Unstaged == 1 })
	tt.Move(0, 0)
	tt.Frame()

	name, ok := tt.Find("a.txt")
	if !ok {
		t.Fatalf("没有变更行: %q", tt.Texts())
	}
	letter, ok := tt.Find("M")
	if !ok {
		t.Fatalf("没有状态字母: %q", tt.Texts())
	}
	if letter.X <= name.X+name.W {
		t.Errorf("字母不在文件名右边: 名 %v 字母 %v", name, letter)
	}
	hoverOn(t, tt, "a.txt")
	if _, ok := tt.Find("M"); ok {
		t.Errorf("悬停时字母没让开: %q", tt.Texts())
	}
	if now, _ := tt.Find("a.txt"); now != name {
		t.Errorf("悬停时文件名动了: %v → %v", name, now)
	}

	repo.Git("checkout", "src/a.txt") // 工作区干净了，界面上的 M 只剩提交文件行那个
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Unstaged == 0 })
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	wait("历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 2 })
	mustClickRow(t, tt, "加一行")
	wait("展开", func() bool { return r.Hist.FilesFor != "" && !r.Hist.FilesLoading })
	tt.Move(0, 0)
	tt.Frame()
	tt.Frame()
	plus, ok := tt.Find("+1")
	if !ok {
		t.Fatalf("没有行数: %q", tt.Texts())
	}
	if m, ok := tt.Find("M"); !ok || m.X < plus.X+plus.W || m.Y >= plus.Y+plus.H || plus.Y >= m.Y+m.H {
		t.Errorf("提交文件行的字母不在行数右边: 行数 %v 字母 %v %v", plus, m, ok)
	}
}
