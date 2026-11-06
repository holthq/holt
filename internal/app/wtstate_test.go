package app

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

func wt(name string, main bool) Worktree {
	return Worktree{Worktree: git.Worktree{Path: "/w/" + name, Branch: name, Head: "h-" + name, Main: main}}
}

// 七种状态怎么算：agent 的压过改动，有改动压过可合并，领先 0、落后 ≥ 1 的是已合并，目录没了的也算已合并；
// 刚建、还没提交的（领先、落后都是 0）不算已合并。
func TestWtStateOf(t *testing.T) {
	clean := &WtInfo{}
	merged := &WtInfo{Behind: 1}
	ahead := &WtInfo{Ahead: 2}
	dirty := &WtInfo{Dirty: 3, Ahead: 2}
	gone := wt("gone", false)
	gone.Prunable = true
	detached := wt("det", false)
	detached.Branch = ""
	for _, c := range []struct {
		name string
		ag   AgentKind
		w    Worktree
		info *WtInfo
		want WtState
	}{
		{"等你压过改动", AgentWaiting, wt("a", false), dirty, WtWaiting},
		{"运行中", AgentRunning, wt("a", false), ahead, WtRunning},
		{"可合并", AgentNone, wt("a", false), ahead, WtMergeable},
		{"空闲时干净领先也算可合并", AgentIdle, wt("a", false), ahead, WtMergeable},
		{"有改动", AgentNone, wt("a", false), dirty, WtDirty},
		{"空闲", AgentIdle, wt("a", false), clean, WtIdle},
		{"还没算", AgentNone, wt("a", false), nil, WtPlain},
		{"已合并", AgentNone, wt("a", false), merged, WtMerged},
		{"刚建的不算已合并", AgentNone, wt("a", false), clean, WtPlain},
		{"可清理并进已合并", AgentNone, gone, nil, WtMerged},
		{"分离 HEAD 不算已合并", AgentNone, detached, merged, WtPlain},
		{"主工作树不会可合并", AgentNone, wt("main", true), ahead, WtPlain},
		{"主工作树有改动", AgentNone, wt("main", true), dirty, WtDirty},
	} {
		if got := WtStateOf(c.ag, c.w, c.info); got != c.want {
			t.Errorf("%s：%v，要 %v", c.name, got, c.want)
		}
	}
}

// 组里按 等你 → 运行中 → 可合并 → 有改动 → 空闲 → 没有 agent → 已合并 排，主工作树仍在最前，同一档保持原来的顺序。
func TestSortWts(t *testing.T) {
	st := map[string]WtState{"m": WtPlain, "a": WtMerged, "b": WtDirty, "c": WtWaiting, "d": WtMergeable, "e": WtDirty, "f": WtRunning, "g": WtIdle}
	var in []Worktree
	for _, n := range []string{"a", "b", "m", "c", "d", "e", "f", "g"} {
		in = append(in, wt(n, n == "m"))
	}
	out := SortWts(in, func(w Worktree) WtState { return st[w.Branch] })
	var got []string
	for _, w := range out {
		got = append(got, w.Branch)
	}
	if want := []string{"m", "c", "f", "d", "b", "e", "g", "a"}; !slices.Equal(got, want) {
		t.Errorf("排序 %v，要 %v", got, want)
	}
	if in[0].Branch != "a" {
		t.Error("SortWts 改了传进来的切片")
	}
}

// 收起的组头只放等你 / 运行中 / 可合并里最需要你的那一档。
func TestWtTop(t *testing.T) {
	for _, c := range []struct {
		in   []WtState
		want WtState
		ok   bool
	}{
		{nil, WtMerged, false},
		{[]WtState{WtDirty, WtIdle, WtMerged}, WtDirty, false},
		{[]WtState{WtDirty, WtMergeable}, WtMergeable, true},
		{[]WtState{WtMergeable, WtRunning, WtPlain}, WtRunning, true},
		{[]WtState{WtRunning, WtWaiting}, WtWaiting, true},
	} {
		got, ok := WtTop(c.in)
		if ok != c.ok || ok && got != c.want {
			t.Errorf("WtTop(%v) = %v %v，要 %v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// 展开的工作树列的是 main...分支：主分支后来多出的改动不算；"需要你"条在有可合并的工作树时出现，
// 上一个 / 下一个在它们之间跳、展开并选中；合并完（领先 0）就从条上消失。
func TestWtExpandAndNeedsYou(t *testing.T) {
	m := testutil.NewRepo(t, "proj")
	m.Write("a.txt", "一\n")
	m.Commit("初始")
	side := filepath.Dir(m.Root)
	featDir, fixDir := filepath.Join(side, "proj-feat"), filepath.Join(side, "proj-fix")
	m.Git("worktree", "add", "-q", "-b", "feat", featDir)
	m.Git("worktree", "add", "-q", "-b", "fix", fixDir)
	m.In(featDir).Write("feat.txt", "分支\n")
	m.In(featDir).Commit("分支的提交")
	m.In(fixDir).Write("fix.txt", "修\n")
	m.In(fixDir).Commit("修的提交")
	m.Write("main.txt", "主分支后来的\n") // 主分支在分出去之后又多了一个提交
	m.Commit("主分支的提交")

	a, wait := newTestApp(t, wsFile(t, "需要你", m.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyOpen)
	wait("读状态", idle(r))
	if len(a.NeedsYou()) != 0 {
		t.Fatalf("没算过状态时不该认出可合并：%+v", a.NeedsYou())
	}
	a.ToggleWorktrees(r)
	wait("算工作树状态", func() bool { return r.WtInfo != nil && !r.WtLoading })
	ns := a.NeedsYou()
	if len(ns) != 2 || ns[0].State != WtMergeable || ns[1].State != WtMergeable {
		t.Fatalf("两个都该可合并：%+v", ns)
	}

	// 收起组：有可合并的也不再自动展开，只剩组头；但状态照样算（"需要你"条、组头的符号还在），重读后 WtInfo 也还在
	a.ToggleWorktrees(r)
	a.SyncWtAuto()
	if r.WtShown() || !r.WtTracked() {
		t.Fatalf("收起后应不列行、照样算状态：shown=%v tracked=%v", r.WtShown(), r.WtTracked())
	}
	if slices.ContainsFunc(a.GitRows(), func(cr ChangeRow) bool { return cr.Kind == RowWorktree }) {
		t.Fatal("收起的组不该列工作树")
	}
	if top, ok := a.WtGroupTop(r); !ok || top != WtMergeable || len(a.NeedsYou()) != 2 {
		t.Fatalf("收着时组头符号 %v %v、需要你 %d 条", top, ok, len(a.NeedsYou()))
	}
	a.RefreshRepo(r, WhyManual)
	wait("重读", func() bool { return idle(r)() && !r.WtLoading })
	a.SyncWtAuto()
	if r.WtInfo == nil || len(a.NeedsYou()) != 2 || r.WtShown() {
		t.Fatalf("收着重读后应照样算出可合并：%+v", a.NeedsYou())
	}

	// 下一个：跳到第一个，展开、选中
	a.StepNeedsYou(1)
	first := ns[0].Wt
	if !a.WtExpanded(r, first) {
		t.Fatalf("下一个后应展开 %s", first.Branch)
	}
	rows := a.GitRows()
	if a.GitSel < 0 || rows[a.GitSel].Kind != RowWorktree || rows[a.GitSel].Wt.Path != first.Path {
		t.Fatalf("下一个后应选中 %s：%d", first.Branch, a.GitSel)
	}
	wait("读 main...分支", func() bool { return !a.WtExp.Loading })
	want := map[string]string{"feat": "feat.txt", "fix": "fix.txt"}[first.Branch]
	if x := a.WtExp; x.Err != nil || x.Commits != 1 || len(x.Files) != 1 || x.Files[0].Path != want {
		t.Fatalf("main...%s 应只有 %s：%+v", first.Branch, want, x)
	}
	rows = a.GitRows()
	i := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowWtFile })
	if i < 0 || rows[i-1].Kind != RowWtSummary || rows[i].Change.Path != want || !rows[i].Change.Range {
		t.Fatalf("展开的工作树下面应是摘要和 %s：%+v", want, rows)
	}
	if slices.ContainsFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowWtFile && cr.Change.Path == "main.txt" }) {
		t.Error("主分支后来的改动不该算进 main...分支")
	}

	// 下一个、再下一个：在两个之间转圈；上一个往回
	a.StepNeedsYou(1)
	if !a.WtExpanded(r, ns[1].Wt) {
		t.Errorf("再下一个应展开 %s", ns[1].Wt.Branch)
	}
	a.StepNeedsYou(1)
	if !a.WtExpanded(r, first) {
		t.Errorf("转回第一个")
	}
	a.StepNeedsYou(-1)
	if !a.WtExpanded(r, ns[1].Wt) {
		t.Errorf("上一个应回到 %s", ns[1].Wt.Branch)
	}

	// 合并 feat：确认框点了才合，合完 feat 领先 0，从条上消失、变成已合并
	feat := ns[slices.IndexFunc(ns, func(n NeedYou) bool { return n.Wt.Branch == "feat" })].Wt
	if why := MergeWorktreeWhy(r, feat); why != "" {
		t.Fatalf("feat 应能合并：%s", why)
	}
	a.MergeWorktree(r, feat)
	if a.Confirm == nil || !a.ConfirmOpen {
		t.Fatal("合并前应先问")
	}
	a.Confirm.Fn()
	wait("合并完", func() bool {
		info := r.WtInfo[feat.Path]
		return idle(r)() && r.Running() == nil && info != nil && info.Ahead == 0 && !r.WtLoading
	})
	ns = a.NeedsYou()
	if len(ns) != 1 || ns[0].Wt.Branch != "fix" {
		t.Errorf("合并后条上只剩 fix：%+v", ns)
	}
	if s := a.WtState(r, feat); s != WtMerged {
		t.Errorf("合并后 feat 应是已合并：%v", s)
	}
}
