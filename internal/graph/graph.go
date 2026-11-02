// Package graph 给右栏历史算提交图：每个提交的节点在哪条泳道、这一行到下一行之间有哪些线。
//
// 提交按显示顺序一个个喂进来（git log --date-order：父提交总排在它所有的子提交后面），只往前算，
// 状态全在 Lanes 里——分页读历史时下一页拿上一页的 Lanes 接着喂，和一次喂完结果一样。
//
// 画法的约定：一行的上半截（行顶到节点）全是竖线，所有的弯都在下半截（节点到行底）。
// 所以好几条泳道等同一个提交时（它有好几个子提交），汇入的弯得画在上一行：Next 发现时回头改上一行的 Edges。
//
// 开着"所有分支"时 HEAD 上面可能有别的分支更新的提交：Reserve 把泳道 0 留给 HEAD，当前分支的第一父链总在最左边。
package graph

import "slices"

// Edge 是一行到下一行之间的一条线：从这一行的 From 泳道到下一行的 To 泳道；From == To 是直着穿过去的。
type Edge struct{ From, To int }

// Row 是一行的图。
type Row struct {
	Lane  int    // 节点在哪条泳道
	Edges []Edge // 这一行到下一行之间所有的线（含穿过去的），按 From、To 排
}

// Width 是这一行用到几条泳道（节点和线里最大的泳道号 + 1）。
func (r Row) Width() int {
	w := r.Lane + 1
	for _, e := range r.Edges {
		w = max(w, e.From+1, e.To+1)
	}
	return w
}

// Lanes 是算到一半的状态：每条泳道正在等哪个提交（"" 是空位），和上一行的线。零值就能用。
type Lanes struct {
	active   []string
	prev     []Edge // 上一次 Next 返回的 Edges（同一块底层数组）：汇入时改它
	reserved string // Reserve 给泳道 0 留着的提交，还没来；来了就清掉
}

// Reserve 把泳道 0 留给 hash（HEAD）：在它来之前，泳道 0 上没有节点、也没有穿过去的线，free 不把它给别的分支；
// 别的提交的父提交是它也不连进泳道 0，各自在自己的泳道上等，到它那一行再汇入（和平常好几条泳道等同一个提交一样）。
// 它来了就放在泳道 0，之后和没留过一样。要在喂第一个提交之前调；留着的状态在 Lanes 里，分页接着喂也算数。
// hash 一直没来（不在读到的范围里）泳道 0 就一直空着，别的分支从泳道 1 起。
func (l *Lanes) Reserve(hash string) {
	if hash == "" || len(l.active) > 0 {
		return
	}
	l.active, l.reserved = []string{hash}, hash
}

// Next 算下一个提交那一行。
//
//   - 节点放在第一条等着 hash 的泳道；没有泳道等它（分支的顶）就放第一个空位，没有空位就加一条。
//     别的也在等 hash 的泳道在上一行弯过来汇入——改的是上一次返回的 Row.Edges 的元素，调用方存的那份跟着变——然后空出来。
//   - 第一个父提交接着用节点这条泳道；左边已经有泳道在等它就弯过去、空出这条
//     （右边的泳道在等就不管，两条一起等到父提交那一行再汇入左边这条，主线不往右跳）。
//   - 别的父提交（合并进来的）有泳道在等就连过去，没有就占第一个空位（或加一条）。
//   - 别的泳道直着穿过去。末尾的空位裁掉，中间的留着给后面的分支用。
//
// 父提交一直没出现（还没读到，或不在读的范围里）的泳道就一直等着，线画到最后一行底下。
func (l *Lanes) Next(hash string, parents []string) Row {
	lane := -1
	for i, h := range l.active {
		if h != hash {
			continue
		}
		if lane < 0 {
			lane = i
			continue
		}
		for k := range l.prev {
			if l.prev[k].To == i {
				l.prev[k].To = lane
			}
		}
		l.active[i] = ""
	}
	if lane < 0 {
		lane = l.free()
	}
	l.active[lane] = ""
	if hash == l.reserved {
		l.reserved = "" // 留着的泳道 0 兑现了（它在泳道 0 上等，上面的循环先找到它）
	}

	var edges []Edge
	opened := map[int]bool{} // 这一行给合并进来的父提交开的泳道（线从节点连过去，不是穿过去）
	if len(parents) > 0 {
		if k := l.find(parents[0]); k >= 0 && k < lane {
			edges = append(edges, Edge{lane, k})
		} else {
			l.active[lane] = parents[0]
		}
		for _, p := range parents[1:] {
			k := l.find(p)
			if k < 0 {
				k = l.free()
				l.active[k] = p
				opened[k] = true
			}
			edges = append(edges, Edge{lane, k})
		}
	}
	for j, h := range l.active {
		if h != "" && !opened[j] && !l.held(j) {
			edges = append(edges, Edge{j, j})
		}
	}
	slices.SortFunc(edges, func(a, b Edge) int {
		if a.From != b.From {
			return a.From - b.From
		}
		return a.To - b.To
	})
	edges = slices.Compact(edges) // 同一个父提交写了两遍
	for len(l.active) > 0 && l.active[len(l.active)-1] == "" {
		l.active = l.active[:len(l.active)-1]
	}
	l.prev = edges
	return Row{Lane: lane, Edges: edges}
}

// find 返回在等 hash 的第一条泳道，没有是 -1。留着还没兑现的泳道 0 不算（它不是真的线）。
func (l *Lanes) find(hash string) int {
	for i, h := range l.active {
		if h == hash && !l.held(i) {
			return i
		}
	}
	return -1
}

// held 是泳道 i 是 Reserve 留着、还没兑现的泳道 0。
func (l *Lanes) held(i int) bool { return i == 0 && l.reserved != "" }

// free 返回第一个空位，没有就加一条。
func (l *Lanes) free() int {
	if i := slices.Index(l.active, ""); i >= 0 {
		return i
	}
	l.active = append(l.active, "")
	return len(l.active) - 1
}
