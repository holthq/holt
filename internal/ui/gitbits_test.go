package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
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

// 未暂存和未跟踪合成一组：只有未跟踪的文件时组名仍是"未暂存"，没有丢弃按钮（git restore . 不动未跟踪的），+ 照样有；
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
	mustText(t, tt, "收起 未暂存")
	mustText(t, tt, "暂存所有未暂存的改动")
	if tt.HasText("未跟踪") || tt.HasText("丢弃所有未暂存的改动…") {
		t.Fatalf("只有未跟踪的：组名应是未暂存、没有丢弃: %q", tt.Texts())
	}
	// 贮藏组头：读屏有"贮藏"，画出来的字里只有条数
	mustText(t, tt, "展开 贮藏")
	// 和只有图标的"未暂存"组头一样：一行的提示和图标的 Label 各一个；以前还多一个画出来的"贮藏"
	if n := countText(tt, "贮藏"); n != 2 || countText(tt, "未暂存") != 2 {
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
	if s := strings.Join(got, " "); s != "✎ =未暂存2 a.txt new.txt #stash" {
		t.Errorf("行：%s", s)
	}
}
