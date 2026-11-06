package app

import "github.com/holthq/holt/internal/docs"

// HunkPos 是 Diff 标签（和替换预览、比较）里光标所在（inside）或光标之前的那处改动、一共几处：标签条的上 / 下一处、底栏的"第 i / n 处改动"。
// cur 照 mygo 的 CurrentHunk（按光标，不是按视口顶：mygo 没给视口的位置），在第一处之前是 -1；不是 Diff、还没建好或没有改动时 n = 0。
func HunkPos(d *docs.Doc) (cur, n int, inside bool) {
	if d == nil || !d.IsDiffView() || d.DiffSt == nil {
		return -1, 0, false
	}
	if n = len(d.DiffSt.Hunks()); n == 0 {
		return -1, 0, false
	}
	cur, inside = d.DiffSt.CurrentHunk()
	return cur, n, inside
}

// hunkTarget 是 GoToHunk(d, dir) 要去的那一处，到头了是 -1：下一处是 cur+1；上一处在一处里面时是 cur-1，在两处之间是 cur（光标之前那处）。
func hunkTarget(d *docs.Doc, dir int) int {
	cur, n, inside := HunkPos(d)
	i := cur + 1
	if dir < 0 {
		i = cur
		if inside {
			i = cur - 1
		}
	}
	if n == 0 || i < 0 || i >= n {
		return -1
	}
	return i
}

// CanGoToHunk 是标签条上的上 / 下一处改动能不能点（到头灰掉）。
func CanGoToHunk(d *docs.Doc, dir int) bool { return hunkTarget(d, dir) >= 0 }

// GoToHunk 把 Diff 标签的光标挪到上一处（dir < 0）或下一处改动的开头，滚到视图中间（mygo DiffState.GoToHunk）。
// 和 J / K 一样（那两个是 mygo 编辑器自己接的键，这里给标签条按钮和命令面板）。
func (a *App) GoToHunk(d *docs.Doc, dir int) {
	if i := hunkTarget(d, dir); i >= 0 {
		d.DiffSt.GoToHunk(i)
	}
}
