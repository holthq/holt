package ui

import (
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// graphRepo 建一个有分支和合并的临时仓库（时间定死，--date-order 读出来顺序一定）：
//
//	main：初始提交 → 加 README → 修文档 → 合并 feature/login → 合并 feature/search（HEAD）
//	feature/login：从"加 README"分出，登录页 → 登录校验，合并回 main
//	feature/search：从"加 README"分出，搜索框 → 搜索结果，合并回 main
//	topic：从"合并 feature/login"分出，试验：新布局（没合并，只在"所有分支"里看得到，也是最新的）
func graphRepo(t *testing.T) *testutil.Repo {
	t.Helper()
	repo := testutil.NewRepo(t, "graph")
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Hour) }
	commit := func(i int, file, msg string) {
		repo.Write(file, msg+"\n")
		repo.CommitAt(msg, at(i))
	}
	merge := func(i int, branch string) {
		d := at(i).Format(time.RFC3339)
		t.Setenv("GIT_AUTHOR_DATE", d)
		t.Setenv("GIT_COMMITTER_DATE", d)
		repo.Git("merge", "-q", "--no-ff", "-m", "合并 "+branch, branch)
		os.Unsetenv("GIT_AUTHOR_DATE")
		os.Unsetenv("GIT_COMMITTER_DATE")
	}
	commit(0, "a.txt", "初始提交")
	commit(1, "README.md", "加 README")
	repo.Git("switch", "-q", "-c", "feature/login")
	commit(2, "login.txt", "登录页")
	commit(4, "login.txt", "登录校验")
	repo.Git("switch", "-q", "-c", "feature/search", "main")
	commit(3, "search.txt", "搜索框")
	repo.Git("switch", "-q", "main")
	commit(5, "docs.txt", "修文档")
	merge(6, "feature/login")
	repo.Git("switch", "-q", "feature/search")
	commit(7, "search.txt", "搜索结果")
	repo.Git("switch", "-q", "main")
	merge(8, "feature/search")
	repo.Git("switch", "-q", "-c", "topic", "HEAD^")
	commit(9, "layout.txt", "试验：新布局")
	repo.Git("switch", "-q", "main")
	return repo
}

// 提交图接进历史卡片：每个提交一行图、节点和线的颜色在对的地方（4 倍渲染读像素）、选中变 AccentText、
// 展开的文件行线穿过去；搜索、看文件历史时只画点（graphviews_test.go）；开关记进 state.json；有改动时虚线连到 HEAD。
func TestHistoryGraph(t *testing.T) {
	repo := graphRepo(t)
	stateDir := t.TempDir()
	ws := testutil.Workspace(t, repo.Root)
	a, tt, wait := newUI(t, ws, stateDir)
	const s = 4
	tt.SetScale(s)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	subjects := func() []string {
		var out []string
		for _, c := range r.Hist.Commits {
			out = append(out, c.Subject)
		}
		return out
	}
	waitHist := func(what string, want ...string) {
		t.Helper()
		wait(what, func() bool { return r.Hist.Loaded && !r.Hist.Loading && slices.Equal(subjects(), want) })
		tt.Frame()
	}
	all := []string{"试验：新布局", "合并 feature/search", "搜索结果", "合并 feature/login", "修文档",
		"登录校验", "搜索框", "登录页", "加 README", "初始提交"}
	a.ToggleHistBranches()
	waitHist("所有分支的历史", all...)
	if dir := os.Getenv("GRAPH_PNG"); dir != "" {
		savePNG(t, filepath.Join(dir, "graph.png"), tt.Image())
	}

	// 算法有表测试，这里只看接上了：每个提交一行，HEAD（合并 feature/search）在泳道 0（Reserve），topic 更新、在泳道 1
	if !a.HistGraph(r) || r.Hist.GraphW != 3 {
		t.Fatalf("没有提交图：HistGraph=%v GraphW=%d", a.HistGraph(r), r.Hist.GraphW)
	}
	var lanes []int
	for _, row := range r.Hist.Graph {
		lanes = append(lanes, row.Lane)
	}
	if want := []int{1, 0, 2, 0, 0, 1, 2, 1, 0, 0}; !slices.Equal(lanes, want) {
		t.Fatalf("泳道 %v，想要 %v", lanes, want)
	}

	find := func(s string) wui.Rect {
		t.Helper()
		rc, ok := tt.Find(s)
		if !ok {
			t.Fatalf("找不到 %q；texts=%q", s, tt.Texts())
		}
		return rc
	}
	th := lightTheme()
	lane := func(l int) color.RGBA { return rgba(laneColor(th, l, false)) }
	near := func(a, b color.RGBA) bool {
		d := func(x, y uint8) bool { return x-y <= 10 || y-x <= 10 }
		return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
	}
	// 按标题找到的是整行（行的 Label），图格从行的左边起；一天的第一个提交上面多一个日期段头
	cellX := func() float32 { return find("修文档").X }
	nodeY := func(subject string) float32 {
		t.Helper()
		i := slices.Index(subjects(), subject)
		if i < 0 {
			t.Fatalf("历史里没有 %q", subject)
		}
		y := find(subject).Y + graphNodeY
		if dayStart(r.Hist.Commits, i) {
			y += histDayH
		}
		return y
	}
	at := func(x, y float32) color.RGBA { return tt.Image().RGBAAt(int(x*s), int(y*s)) }
	// count 数 (x0, y0)–(x1, y1) 这块里颜色接近 col 的像素
	count := func(x0, y0, x1, y1 float32, col color.RGBA) int {
		img, n := tt.Image(), 0
		for y := int(y0 * s); y < int(y1*s); y++ {
			for x := int(x0 * s); x < int(x1*s); x++ {
				if near(img.RGBAAt(x, y), col) {
					n++
				}
			}
		}
		return n
	}
	expect := func(what string, got, want color.RGBA) {
		t.Helper()
		if !near(got, want) {
			t.Errorf("%s 是 %v，应接近 %v", what, got, want)
		}
	}
	x0 := cellX()
	gx := func(l int) float32 { return x0 + graphX(l) }
	expect("登录校验的节点（泳道 1，实心）", at(gx(1), nodeY("登录校验")), lane(1))
	expect("修文档的节点（泳道 0，钢蓝）", at(gx(0), nodeY("修文档")), lane(0))
	// 修文档那一行，泳道 1、2 是穿过去的线：节点上面一点读
	expect("修文档行泳道 1 的竖线", at(gx(1), nodeY("修文档")-6), lane(1))
	expect("修文档行泳道 2 的竖线", at(gx(2), nodeY("修文档")-6), lane(2))
	// 合并提交空心：圈上是线的颜色，圆心不是
	ring := float32(graphNodeR - graphStroke/2)
	expect("合并 feature/login 的圈", at(gx(0)+ring, nodeY("合并 feature/login")), lane(0))
	if c := at(gx(0), nodeY("合并 feature/login")); near(c, lane(0)) {
		t.Errorf("合并提交的圆心是 %v，应该空心", c)
	}
	// HEAD 外面一圈（泳道 0）
	expect("HEAD 的外圈", at(gx(0)+graphRingR-graphStroke/2, nodeY("合并 feature/search")), lane(0))
	// 日期段头（第一个提交上面）没有节点
	if n := count(gx(0)-4, find("试验：新布局").Y, gx(0)+4, find("试验：新布局").Y+histDayH-2, lane(1)); n > 0 {
		t.Errorf("日期段头左边有 %d 个泳道 1 颜色的像素", n)
	}

	// 搜索时不画泳道：图格窄成一条泳道宽（只画点）；清空了图回来
	search := func(q string) {
		t.Helper()
		if !tt.HasText("搜索提交") {
			mustClickRow(t, tt, "搜索历史")
		}
		mustClickRow(t, tt, "搜索提交")
		tt.Key(wui.Cmd, wui.KeyA)
		tt.Key(0, wui.KeyBackspace)
		if q != "" {
			tt.Type(q)
		}
		tt.Frame()
	}
	search("登录")
	waitHist("搜索", "登录校验", "登录页")
	if a.HistGraph(r) {
		t.Error("搜索结果也画了提交图")
	}
	if w := histGraphW(a, r); w != graphWidth(1) {
		t.Errorf("搜索时图格宽 %.1f，应为 %.1f", w, graphWidth(1))
	}
	if n := count(x0, find("登录校验").Y, x0+graphWidth(3), find("登录页").Y+find("登录页").H, lane(2)); n > 0 {
		t.Errorf("搜索结果左边有 %d 个泳道 2 颜色的像素", n)
	}
	search("")
	waitHist("清空搜索", all...)
	if !a.HistGraph(r) {
		t.Error("清空搜索后提交图没回来")
	}

	// 看文件历史时不画
	a.ShowFileHistory(r, "README.md", false)
	waitHist("README.md 的历史", "加 README")
	if a.HistGraph(r) {
		t.Error("文件历史也画了提交图")
	}
	a.ClearFileHistory(r)
	waitHist("去掉文件筛选", all...)

	// 选中的行图是 AccentText（实心的选中行；默认的淡底时图不换色）；点一下还展开，文件行上泳道 0、2 穿过去，泳道 1 是这个提交连下去的
	a.St.Settings.SolidSelection = true
	mustClickRow(t, tt, "登录校验")
	wait("展开登录校验", func() bool {
		return r.Hist.Expanded != "" && r.Hist.FilesFor == r.Hist.Expanded && tt.HasText("login.txt")
	})
	tt.Frame()
	expect("选中的登录校验的节点", at(gx(1), nodeY("登录校验")), rgba(th.AccentText))
	fy := find("login.txt").Y + find("login.txt").H/2
	expect("文件行泳道 0", at(gx(0), fy), lane(0))
	expect("文件行泳道 1", at(gx(1), fy), lane(1))
	expect("文件行泳道 2", at(gx(2), fy), lane(2))

	// 有改动：未提交的改动那行 HEAD 的泳道（0）上一个橙色虚线圈，橙色虚线穿过 topic 那行（泳道 0 留给 HEAD，空着）连到 HEAD
	orange := rgba(th.Warning)
	und := find("未提交的改动")
	if n := count(gx(0)-5, und.Y, gx(0)+5, und.Y+und.H, orange); n > 0 {
		t.Errorf("没改动时未提交的改动那行有 %d 个泳道 0 颜色的像素", n)
	}
	repo.Write("a.txt", "改\n")
	a.RefreshRepo(r, app.WhyManual)
	wait("改动", func() bool { return idle(r)() && r.Status.Count > 0 })
	tt.Frame()
	und = find("未提交的改动")
	if n := count(gx(0)-5, und.Y, gx(0)+5, und.Y+und.H, orange); n == 0 {
		t.Error("有改动时未提交的改动那行没有虚线圈")
	}
	if dir := os.Getenv("GRAPH_PNG"); dir != "" {
		savePNG(t, filepath.Join(dir, "graph-dirty.png"), tt.Image())
	}
	top := find("试验：新布局")
	if n := count(gx(0)-2, top.Y, gx(0)+2, top.Y+top.H, orange); n == 0 {
		t.Error("虚线没穿过 topic 那行连到 HEAD")
	}
	if l, hi := dirtyLine(r); l != 0 || hi != 1 {
		t.Errorf("虚线在泳道 %d、连到第 %d 个提交，应为泳道 0、第 1 个（HEAD）", l, hi)
	}

	// 开关在头部的右键菜单里：关了图格没了，记进 state.json，新开的 App 也是关的
	mustRightClickRow(t, tt, "历史", "提交图")
	if !a.St.HistNoGraph || a.HistGraph(r) || histGraphW(a, r) != 0 {
		t.Fatal("点了提交图开关没关掉")
	}
	b, err := app.New(ws, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !b.St.HistNoGraph {
		t.Error("state.json 里没记下关了提交图")
	}
	b.Close()
	mustRightClickRow(t, tt, "历史", "提交图")
	if a.St.HistNoGraph || histGraphW(a, r) != graphWidth(3) {
		t.Error("再点一下提交图没回来")
	}
}
