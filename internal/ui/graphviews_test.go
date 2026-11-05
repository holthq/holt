package ui

import (
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 搜索结果画圆点；"所有仓库"不画提交图，画仓库圆点。

// twoRepos 建两个提交时间交错的临时仓库（都是一条直线的历史）：
//
//	alpha：甲 初始（0 点）→ 甲 登录页（2）→ 甲 登录校验（3）→ 甲 修文档（5）→ 甲 登录按钮（6）
//	beta：乙 初始（1）→ 乙 登录（4）
//
// "所有仓库"按时间合并成 登录按钮、修文档、乙 登录、登录校验、登录页、乙 初始、甲 初始。
func twoRepos(t *testing.T) (alpha, beta *testutil.Repo, ws string) {
	t.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alpha, beta = testutil.NewRepo(t, "alpha"), testutil.NewRepo(t, "beta")
	commit := func(r *testutil.Repo, i int, msg string) {
		r.Write("f.txt", msg+"\n")
		r.CommitAt(msg, base.Add(time.Duration(i)*time.Hour))
	}
	commit(alpha, 0, "甲 初始")
	commit(beta, 1, "乙 初始")
	commit(alpha, 2, "甲 登录页")
	commit(alpha, 3, "甲 登录校验")
	commit(beta, 4, "乙 登录")
	commit(alpha, 5, "甲 修文档")
	commit(alpha, 6, "甲 登录按钮")
	return alpha, beta, testutil.Workspace(t, alpha.Root, beta.Root)
}

// graphPixels 是读 4 倍渲染像素的几个小工具：按标题找行、按短哈希找内容左边、读一个点、和颜色比。
type graphPixels struct {
	t  *testing.T
	tt *wui.Tester
}

const graphScale = 4

func (g graphPixels) find(s string) wui.Rect {
	g.t.Helper()
	rc, ok := g.tt.Find(s)
	if !ok {
		g.t.Fatalf("找不到 %q；texts=%q", s, g.tt.Texts())
	}
	return rc
}

func (g graphPixels) at(x, y float32) color.RGBA {
	return g.tt.Image().RGBAAt(int(x*graphScale), int(y*graphScale))
}

// count 数 (x0, y0)–(x1, y1) 这块里颜色接近 col 的像素。
func (g graphPixels) count(x0, y0, x1, y1 float32, col color.RGBA) int {
	img, n := g.tt.Image(), 0
	for y := int(y0 * graphScale); y < int(y1*graphScale); y++ {
		for x := int(x0 * graphScale); x < int(x1*graphScale); x++ {
			if nearRGBA(img.RGBAAt(x, y), col) {
				n++
			}
		}
	}
	return n
}

func nearRGBA(a, b color.RGBA) bool {
	d := func(x, y uint8) bool { return x-y <= 10 || y-x <= 10 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
}

// commitHash 是仓库 r 里标题为 subject 的提交（读到的历史里没有就失败）。
func commitHash(t *testing.T, r *app.Repo, subject string) string {
	t.Helper()
	for _, c := range r.Hist.Commits {
		if c.Subject == subject {
			return c.Short
		}
	}
	t.Fatalf("%s 的历史里没有 %q", r.Name, subject)
	return ""
}

// allCommits 是"所有仓库"视图里提交行的标题，按显示顺序。
func allCommits(a *app.App) []string {
	var out []string
	for _, row := range a.AllHistRows() {
		if row.Kind == app.AllCommit {
			out = append(out, row.Commit().Subject)
		}
	}
	return out
}

// "所有仓库"：不画提交图，每行左边一个仓库圆点（颜色按仓库顺序轮换）；AllHistLinks 照旧算同一个仓库里挨着的两个提交。
func TestAllHistGraphRows(t *testing.T) {
	alpha, _, ws := twoRepos(t)
	alpha.Write("f.txt", "改\n") // 有一段"未提交的改动"，下面才有"提交"段头
	a, tt, wait := newUI(t, ws, "")
	tt.SetScale(graphScale)
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("状态", func() bool { return idle(a.Repos[0])() && idle(a.Repos[1])() })
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	a.SetHistAll(true)
	order := []string{"甲 登录按钮", "甲 修文档", "乙 登录", "甲 登录校验", "甲 登录页", "乙 初始", "甲 初始"}
	wait("所有仓库的历史", func() bool {
		tt.Frame()
		return a.AllHistDone() && !a.AllHistLoading() && slices.Equal(allCommits(a), order)
	})
	tt.Frame()
	for _, r := range a.Repos {
		if !a.HistGraph(r) {
			t.Fatalf("%s 没有提交图", r.Name)
		}
	}
	if dir := os.Getenv("GRAPH_PNG"); dir != "" {
		savePNG(t, filepath.Join(dir, "all-graph.png"), tt.Image())
	}

	// 连不连：同一个仓库、CI 差 1
	type link struct{ up, down bool }
	want := map[string]link{
		"甲 登录按钮": {false, true}, "甲 修文档": {true, false}, "乙 登录": {false, false},
		"甲 登录校验": {false, true}, "甲 登录页": {true, false}, "乙 初始": {false, false}, "甲 初始": {false, false},
	}
	rows := a.AllHistRows()
	for i, row := range rows {
		if row.Kind != app.AllCommit {
			continue
		}
		up, down := a.AllHistLinks(rows, i)
		if got, w := (link{up, down}), want[row.Commit().Subject]; got != w {
			t.Errorf("%s 连线 %+v，想要 %+v", row.Commit().Subject, got, w)
		}
	}

	// 没有提交图：每行左边一个仓库圆点，颜色按仓库顺序轮换（alpha 第一个、beta 第二个）
	g := graphPixels{t, tt}
	if a.HistGraph(a.Repos[0]) && histGraphW(a, a.Repos[0]) == 0 {
		t.Fatal("当前仓库视图应有提交图")
	}
	th := lightTheme()
	repoOf := func(subject string) *app.Repo {
		name := "alpha"
		if strings.HasPrefix(subject, "乙") {
			name = "beta"
		}
		return a.Repos[slices.IndexFunc(a.Repos, func(r *app.Repo) bool { return r.Name == name })]
	}
	for i, s := range order {
		rc := g.find(repoOf(s).Name + " " + s)
		y := rc.Y + histRowH/2
		if i == 0 {
			y += histDayH // 第一个提交上面是日期段头
		}
		want := rgba(repoColor(a, th, repoOf(s)))
		if c := g.at(rc.X+8+histDotD/2, y); !nearRGBA(c, want) {
			t.Errorf("%s 的仓库圆点是 %v，应接近 %v", s, c, want)
		}
	}
	if dir := os.Getenv("GRAPH_PNG"); dir != "" {
		savePNG(t, filepath.Join(dir, "all-dots.png"), tt.Image())
	}
}

// 搜索结果（当前仓库和所有仓库）：每个提交一个点；相邻两行在完整历史里是父子才连短线，不相邻不连。
func TestSearchGraphLinks(t *testing.T) {
	_, _, ws := twoRepos(t)
	a, tt, wait := newUI(t, ws, "")
	tt.SetScale(graphScale)
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("状态", func() bool { return idle(a.Repos[0])() && idle(a.Repos[1])() })
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	ai := slices.IndexFunc(a.Repos, func(r *app.Repo) bool { return r.Name == "alpha" })
	a.Selected = ai
	r := a.Repos[ai]
	subjects := func() []string {
		var out []string
		for _, c := range r.Hist.Commits {
			out = append(out, c.Subject)
		}
		return out
	}
	tt.Frame()
	wait("alpha 的历史", func() bool { tt.Frame(); return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 5 })
	g := graphPixels{t, tt}
	mustClick(t, tt, "搜索历史")
	tt.Type("登录")
	tt.Frame()
	wait("搜索", func() bool {
		tt.Frame()
		return r.Hist.Loaded && !r.Hist.Loading && slices.Equal(subjects(), []string{"甲 登录按钮", "甲 登录校验", "甲 登录页"})
	})
	tt.Frame()
	if a.HistGraph(r) || !a.HistDots(r) {
		t.Fatalf("搜索时 HistGraph=%v HistDots=%v，应只画点", a.HistGraph(r), a.HistDots(r))
	}
	// 登录按钮的父提交是修文档（没搜出来）→ 和登录校验不连；登录校验的父提交是登录页 → 连
	for ci, w := range [][2]bool{{false, false}, {false, true}, {true, false}} {
		if up, down := r.Hist.DotLinks(ci); up != w[0] || down != w[1] {
			t.Errorf("第 %d 个（%s）连线 %v %v，想要 %v", ci, r.Hist.Commits[ci].Subject, up, down, w)
		}
	}
	// 一条直线的历史，提交图本来就一条泳道宽：搜出来的点和原来的节点在同一列
	if w := histGraphW(a, r); w != graphWidth(1) {
		t.Errorf("搜索时图格宽 %.1f，应为一条泳道宽 %.1f", w, graphWidth(1))
	}
	x0 := g.find("甲 登录校验").X
	accent := rgba(laneColor(lightTheme(), 0, false))
	row := func(s string) wui.Rect {
		rc := g.find(s)
		if s == subjects()[0] {
			rc.Y += histDayH // 第一个提交上面是日期段头
		}
		return rc
	}
	for _, s := range subjects() {
		if c := g.at(x0+graphX(0), row(s).Y+graphNodeY); !nearRGBA(c, accent) {
			t.Errorf("%s 的点是 %v，应接近强调色", s, c)
		}
	}
	// 两个点之间：登录校验 → 登录页 有线，登录按钮 → 登录校验 没有
	gapHas := func(above, below string) bool {
		y0, y1 := row(above).Y+graphNodeY+graphRingR+1, row(below).Y+graphNodeY-graphRingR-1
		return g.count(x0+graphX(0)-1, y0, x0+graphX(0)+1, y1, accent) > int((y1-y0)*graphScale)
	}
	if !gapHas("甲 登录校验", "甲 登录页") {
		t.Error("登录校验和登录页是父子，点之间没连线")
	}
	if gapHas("甲 登录按钮", "甲 登录校验") {
		t.Error("登录按钮和登录校验不是父子，点之间连了线")
	}
	if dir := os.Getenv("GRAPH_PNG"); dir != "" {
		savePNG(t, filepath.Join(dir, "search-dots.png"), tt.Image())
	}

	// 所有仓库也一样：乙 登录夹在中间，和两边都不连；登录校验 → 登录页 连
	a.SetHistAll(true)
	order := []string{"甲 登录按钮", "乙 登录", "甲 登录校验", "甲 登录页"}
	wait("所有仓库的搜索结果", func() bool {
		tt.Frame()
		return a.AllHistDone() && !a.AllHistLoading() && slices.Equal(allCommits(a), order)
	})
	tt.Frame()
	type link struct{ up, down bool }
	want := map[string]link{"甲 登录按钮": {false, false}, "乙 登录": {false, false}, "甲 登录校验": {false, true}, "甲 登录页": {true, false}}
	rows := a.AllHistRows()
	for i, rw := range rows {
		if rw.Kind != app.AllCommit {
			continue
		}
		up, down := a.AllHistLinks(rows, i)
		if got, w := (link{up, down}), want[rw.Commit().Subject]; got != w {
			t.Errorf("所有仓库：%s 连线 %+v，想要 %+v", rw.Commit().Subject, got, w)
		}
	}
}
