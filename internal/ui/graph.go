package ui

import (
	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/graph"
)

// 这个文件画右栏历史每行最左边的提交图：泳道算法在 internal/graph，读历史时就算好了（History.Graph），这里只按行画。
// 每行一个图格（Box 的 Draw，不用 Canvas 位图），撑满行高：上半截（行顶到节点）是上一行连下来的竖线，
// 节点，下半截（节点到行底）是这一行连到下一行的线，弯都在下半截。
// "所有仓库"视图每个仓库那一段用它自己的图（cutGraph），接不到挨着的行的那半截渐隐；搜索结果、文件历史只画点（dotGraph）。

// 图的几何，单位 DIP。
const (
	graphLane   = 14  // 泳道间距
	graphLeft   = 4   // 左边距
	graphRight  = 2   // 右边距
	graphMax    = 6   // 图格最多这么多条泳道宽；再多的照样画，被图格裁掉
	graphNodeY  = 15  // 节点圆心离行顶（一行 30 高的正中）
	graphNodeR  = 3.6 // 节点半径
	graphHalo   = 2   // 节点外面一圈底色描边的宽
	graphRingR  = 7   // HEAD 外面那圈的半径
	graphStroke = 1.8 // 线宽
	graphDash   = 2   // 虚线（"未提交的改动"）一段的长和空
)

// graphWidth 是 lanes 条泳道的图格宽（最多 graphMax 条）。
func graphWidth(lanes int) float32 {
	return graphLeft + float32(min(max(lanes, 1), graphMax))*graphLane + graphRight
}

// graphX 是泳道 lane 的中线离图格左边多远。
func graphX(lane int) float32 {
	return graphLeft + float32(lane)*graphLane + graphLane/2
}

// 泳道的颜色按泳道号循环，都是淡的：钢蓝（强调色那一档）打头，再灰绿、灰沙、灰紫、灰青、灰褐；不用红色。
var (
	lightLanes = [...]wui.Color{wui.Hex("#4e6e8e"), wui.Hex("#7fa393"), wui.Hex("#b79a73"), wui.Hex("#9286a8"),
		wui.Hex("#6f9aa0"), wui.Hex("#a08c78")}
	darkLanes = [...]wui.Color{wui.Hex("#8fa9c6"), wui.Hex("#86b09d"), wui.Hex("#c9ab82"), wui.Hex("#a79cc0"),
		wui.Hex("#84b3b9"), wui.Hex("#b8a38e")}
)

// laneColor 是泳道 lane 的颜色；选中的行（实心的强调色底）上一律 AccentText。
func laneColor(t *wui.Theme, lane int, sel bool) wui.Color {
	switch i := lane % len(lightLanes); {
	case sel:
		return t.AccentText
	case t.Dark:
		return darkLanes[i]
	default:
		return lightLanes[i]
	}
}

// dirtyColor 是"未提交的改动"的虚线圈和连到 HEAD 的虚线：橙色（实心选中的行上 AccentText）。
func dirtyColor(t *wui.Theme, sel bool) wui.Color {
	if sel {
		return t.AccentText
	}
	return t.Warning
}

// edgeColor 是一条线的颜色：拐弯的线算靠右那条泳道的（汇入主线的分支、合并进来的分支），直线就是它自己泳道的。
func edgeColor(t *wui.Theme, e graph.Edge, sel bool) wui.Color {
	return laneColor(t, max(e.From, e.To), sel)
}

// graphBox 是撑满行高的图格，宽按这个仓库读到的最宽的一行。
func graphBox(c *wui.Context, h *app.History) wui.Element {
	return wui.Box(c).Width(graphWidth(h.GraphW)).Shrink(0)
}

// histHead 是右栏历史的 HEAD 和它上面未提交的改动数：平时是这个仓库的，看着一个工作树的历史时是那个工作树的
// （改动数没算出来是 -1），看着一个标签时是标签的提交（改动数 -1）。
func histHead(r *app.Repo) (head string, dirty int) {
	if r.Hist.RefKind != app.RefNone {
		return r.RefHead(), r.RefDirty()
	}
	return r.Extra.Head, r.Status.Count
}

// dirtyLine 是"未提交的改动"连到 HEAD 的那条虚线：走 HEAD 的泳道，一直画到 HEAD 那一行（hi）的节点上。
// HEAD 上面的行（开着"所有分支"时别的分支更新的提交）在这条泳道上得都空着——没节点、没线——才画；
// 没改动、HEAD 没读到、泳道被占了都是 lane = -1。
func dirtyLine(r *app.Repo) (lane, hi int) {
	h := &r.Hist
	head, n := histHead(r)
	if hi = h.HeadIndex(head); hi < 0 || n <= 0 {
		return -1, -1
	}
	lane = h.Graph[hi].Lane
	for _, row := range h.Graph[:hi] {
		if row.Lane == lane {
			return -1, hi
		}
		for _, e := range row.Edges {
			if e.From == lane || e.To == lane {
				return -1, hi
			}
		}
	}
	return lane, hi
}

// dashedLine 在图格里泳道 lane 上从 y0 到 y1 画一段虚线。
func dashedLine(p *wui.Painter, r wui.Rect, lane int, y0, y1 float32, col wui.Color) {
	x := r.X + graphX(lane)
	var path wui.Path
	path.MoveTo(x, y0).LineTo(x, y1).Cap(wui.ButtCap).Dash(0, graphDash)
	p.StrokePath(&path, graphStroke, col)
}

// graphLine 画图格里的一段直线：fade < 0 从上往下淡入（上半截接不到上一行），> 0 淡出（下半截接不到下一行），0 实色。
func graphLine(p *wui.Painter, x0, y0, x1, y1 float32, col wui.Color, fade int) {
	if fade == 0 {
		p.Line(x0, y0, x1, y1, graphStroke, col)
		return
	}
	var path wui.Path
	path.MoveTo(x0, y0).LineTo(x1, y1)
	strokeFade(p, &path, col, fade)
}

// strokeFade 描图格里的一条线，fade 同 graphLine。渐隐只占线的前 / 后一段、行边上那一截全透明：
// 两段的线在行边上碰头时中间留一道空，不会看着像一条被捏细的线。
func strokeFade(p *wui.Painter, path *wui.Path, col wui.Color, fade int) {
	switch {
	case fade < 0:
		p.StrokePathGradient(path, graphStroke, wui.LinearGradient{From: col.Alpha(0), To: col, Angle: 180, Start: 0.35, End: 1})
	case fade > 0:
		p.StrokePathGradient(path, graphStroke, wui.LinearGradient{From: col, To: col.Alpha(0), Angle: 180, Start: 0, End: 0.6})
	default:
		p.StrokePath(path, graphStroke, col)
	}
}

// graphNode 画节点：实心点，外面一圈 2 px 的底色描边（和穿过的线分开）；合并提交空心；HEAD 外面再一圈。
func graphNode(p *wui.Painter, x, y float32, col wui.Color, merge, head bool) {
	bg := p.Theme().Background
	var halo wui.Path
	halo.Circle(x, y, graphNodeR+graphHalo/2)
	p.StrokePath(&halo, graphHalo, bg)
	var dot wui.Path
	if merge {
		dot.Circle(x, y, graphNodeR-graphStroke/2)
		p.FillPath(&dot, bg)
		p.StrokePath(&dot, graphStroke, col)
	} else {
		dot.Circle(x, y, graphNodeR)
		p.FillPath(&dot, col)
	}
	if head {
		var ring wui.Path
		ring.Circle(x, y, graphRingR-graphStroke/2)
		p.StrokePath(&ring, graphStroke, col)
	}
}

// graphCut 是一行图格和右栏平时不一样的地方（"所有仓库"视图）：w 是图格宽（几个仓库里最宽的，行和行对齐）；
// fadeUp / fadeDown 是上 / 下半截接不到挨着的行（上 / 下一个提交行是别的仓库的，或这个仓库挨着的提交排到了别处），
// 那半截的线渐隐——段与段之间不连。
type graphCut struct {
	w                float32
	fadeUp, fadeDown bool
}

// commitGraph 画提交行 ci 的图格。head 是 HEAD 的哈希；dirty 是 dirtyLine 的结果，这一行在 HEAD 上面或就是 HEAD 时
// 画那条虚线（HEAD 这行只画上半截）。
func commitGraph(c *wui.Context, h *app.History, ci int, head string, dirty [2]int, sel bool) {
	cutGraph(c, h, ci, head, dirty, sel, graphCut{w: graphWidth(h.GraphW)})
}

// cutGraph 是带着 cut 的 commitGraph。
func cutGraph(c *wui.Context, h *app.History, ci int, head string, dirty [2]int, sel bool, cut graphCut) {
	row := h.Graph[ci]
	var prev []graph.Edge
	if ci > 0 {
		prev = h.Graph[ci-1].Edges
	}
	cm := h.Commits[ci]
	isHead, merge := cm.Hash == head && head != "", len(cm.Parents) > 1
	dash := dirty[0] >= 0 && dirty[0] <= graphMax && ci <= dirty[1]
	up, down := 0, 0
	if cut.fadeUp {
		up = -1
	}
	if cut.fadeDown {
		down = 1
	}
	wui.Box(c).Width(cut.w).Shrink(0).Draw(func(p *wui.Painter, r wui.Rect) {
		t := p.Theme()
		ny, bottom := r.Y+graphNodeY, r.Y+r.H
		gap := float32(graphNodeR)
		if isHead {
			gap = graphRingR
		}
		p.Clip(r, 0, func() {
			// 上半截：上一行的每条线落到这一行的泳道，竖着下来；落到节点的到节点边上为止
			var done [graphMax + 2]bool
			for _, e := range prev {
				if e.To > graphMax || done[e.To] {
					continue
				}
				done[e.To] = true
				y1 := ny
				if e.To == row.Lane {
					y1 = ny - gap
				}
				graphLine(p, r.X+graphX(e.To), r.Y, r.X+graphX(e.To), y1, laneColor(t, e.To, sel), up)
			}
			if dash && ci == dirty[1] {
				dashedLine(p, r, dirty[0], r.Y, ny-gap, dirtyColor(t, sel))
			} else if dash {
				dashedLine(p, r, dirty[0], r.Y, bottom, dirtyColor(t, sel))
			}
			// 下半截：直的是竖线；拐弯的从节点（或这条泳道的中线）出发、到下一行那条泳道的行底，一段 S 形三次曲线
			for _, e := range row.Edges {
				if min(e.From, e.To) > graphMax {
					continue
				}
				x0, x1, y0 := r.X+graphX(e.From), r.X+graphX(e.To), ny
				if e.From == row.Lane {
					y0 = ny + gap
				}
				col := edgeColor(t, e, sel)
				if e.From == e.To {
					graphLine(p, x0, y0, x1, bottom, col, down)
					continue
				}
				hh := (bottom - y0) / 2
				var path wui.Path
				path.MoveTo(x0, y0).CubeTo(x0, y0+hh, x1, bottom-hh, x1, bottom)
				if max(e.From, e.To) >= graphMax && down == 0 {
					// 伸出图格的线往外渐隐（极少：同时开着超过 graphMax 条泳道）
					angle := float32(90)
					if e.To < e.From {
						angle = 270
					}
					p.StrokePathGradient(&path, graphStroke, wui.LinearGradient{From: col, To: col.Alpha(0), Angle: angle})
					continue
				}
				strokeFade(p, &path, col, down)
			}
			if row.Lane <= graphMax {
				graphNode(p, r.X+graphX(row.Lane), ny, laneColor(t, row.Lane, sel), merge, isHead)
			}
		})
	})
}

// dotGraph 是搜索结果、文件历史一行的图格：提交不连续，不画泳道，只在泳道 0 上一个点（颜色也是泳道 0 的；
// 合并提交空心、HEAD 外圈）。up / down 是和上 / 下一个提交在完整历史里是父子（app.DotLink），是就从点连到行顶 / 行底。
func dotGraph(c *wui.Context, cm git.Commit, head string, up, down bool, w float32, sel bool) {
	isHead, merge := cm.Hash == head && head != "", len(cm.Parents) > 1
	wui.Box(c).Width(w).Shrink(0).Draw(func(p *wui.Painter, r wui.Rect) {
		col := laneColor(p.Theme(), 0, sel)
		x, ny := r.X+graphX(0), r.Y+graphNodeY
		gap := float32(graphNodeR)
		if isHead {
			gap = graphRingR
		}
		p.Clip(r, 0, func() {
			if up {
				p.Line(x, r.Y, x, ny-gap, graphStroke, col)
			}
			if down {
				p.Line(x, ny+gap, x, r.Y+r.H, graphStroke, col)
			}
			graphNode(p, x, ny, col, merge, isHead)
		})
	})
}

// dotPass 是圆点模式下展开的提交下面文件行的图格：这个提交和下一个连着（down）就一条竖线穿过去。
func dotPass(c *wui.Context, down bool, w float32, sel bool) {
	el := wui.Box(c).Width(w).Shrink(0)
	if !down {
		return
	}
	el.Draw(func(p *wui.Painter, r wui.Rect) {
		x := r.X + graphX(0)
		p.Line(x, r.Y, x, r.Y+r.H, graphStroke, laneColor(p.Theme(), 0, sel))
	})
}

// passGraph 画展开的提交 ci 下面文件行的图格：这个提交连到下一个提交的线竖着穿过去（还在 HEAD 上面的话，虚线也穿过去）。
// w 是图格宽。
func passGraph(c *wui.Context, h *app.History, ci int, dirty [2]int, sel bool, w float32) {
	edges := h.Graph[ci].Edges
	dash := dirty[0] >= 0 && dirty[0] <= graphMax && ci < dirty[1]
	wui.Box(c).Width(w).Shrink(0).Draw(func(p *wui.Painter, r wui.Rect) {
		t := p.Theme()
		p.Clip(r, 0, func() {
			if dash {
				dashedLine(p, r, dirty[0], r.Y, r.Y+r.H, dirtyColor(t, sel))
			}
			var done [graphMax + 2]bool
			for _, e := range edges {
				if e.To > graphMax || done[e.To] {
					continue
				}
				done[e.To] = true
				x := r.X + graphX(e.To)
				p.Line(x, r.Y, x, r.Y+r.H, graphStroke, laneColor(t, e.To, sel))
			}
		})
	})
}

// gapGraph 是比较段那几行的图格：同宽的空白，"未提交的改动"连到 HEAD 的虚线照样穿过去。
func gapGraph(c *wui.Context, h *app.History, dirty [2]int, sel bool) {
	el := graphBox(c, h)
	if dirty[0] < 0 || dirty[0] > graphMax {
		return
	}
	el.Draw(func(p *wui.Painter, r wui.Rect) {
		p.Clip(r, 0, func() { dashedLine(p, r, dirty[0], r.Y, r.Y+r.H, dirtyColor(p.Theme(), sel)) })
	})
}

// uncommittedGraph 画"未提交的改动"那一行的图格：有改动、HEAD 读到了，就在 HEAD 的泳道上画一个虚线空心节点，
// dirtyLine 连得上再一条虚线到行底（下面的行接着画到 HEAD）。没改动只占宽度、和下面的提交对齐。
func uncommittedGraph(c *wui.Context, r *app.Repo, dirty [2]int, sel bool) {
	h := &r.Hist
	lane := -1
	if head, n := histHead(r); n > 0 {
		if i := h.HeadIndex(head); i >= 0 {
			lane = h.Graph[i].Lane
		}
	}
	line := dirty[0] >= 0
	graphBox(c, h).Draw(func(p *wui.Painter, rc wui.Rect) {
		if lane < 0 || lane > graphMax {
			return
		}
		col := dirtyColor(p.Theme(), sel)
		x, y := rc.X+graphX(lane), rc.Y+rc.H/2
		p.Clip(rc, 0, func() {
			var path wui.Path
			path.Circle(x, y, graphNodeR-graphStroke/2)
			if line {
				path.MoveTo(x, y+graphNodeR).LineTo(x, rc.Y+rc.H)
			}
			path.Cap(wui.ButtCap).Dash(0, graphDash)
			p.StrokePath(&path, graphStroke, col)
		})
	})
}

// headGraph 是提交 ci 上面日期段头的图格：上一个提交连下来的线竖着穿过去（还在 HEAD 上面的话，"未提交"的虚线也穿过去）。
func headGraph(c *wui.Context, h *app.History, ci int, dirty [2]int, w float32) {
	if ci > 0 {
		passGraph(c, h, ci-1, dirty, false, w)
		return
	}
	el := wui.Box(c).Width(w).Shrink(0)
	if dirty[0] < 0 || dirty[0] > graphMax {
		return
	}
	el.Draw(func(p *wui.Painter, r wui.Rect) {
		p.Clip(r, 0, func() { dashedLine(p, r, dirty[0], r.Y, r.Y+r.H, dirtyColor(p.Theme(), false)) })
	})
}
