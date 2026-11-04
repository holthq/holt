package app

import "time"

// TreeTipDelay 是指针在文件树的一行上停多久才出那行的悬停提示（大小 · 改动时间 · 状态）。
// 比 mygo 全局的 0.6 s 长：在树上上下找文件时不该一路冒提示。别的地方的提示还是 mygo 的。
const TreeTipDelay = time.Second

// TreeTip 记文件树行的悬停提示：指针从什么时候起停在哪一行。和 mygo 的提示不同，没有"热身"：
// 换一行就重新等满 TreeTipDelay，刚有一条提示消失也不会让下一行立刻弹。
// 按下（点击、拖）之后这一行不再出提示，直到指针离开它。
type TreeTip struct {
	key   FileKey
	on    bool // 这一帧（到现在为止）指针在某一行上
	since time.Time
	gone  bool // 这一行的提示被按下收掉了
}

// Hover 报指针这一帧停在 key 那行上，返回还要等多久才出提示：≤ 0 是该出了；被按下收掉的返回很大的数。
func (t *TreeTip) Hover(key FileKey, now time.Time) time.Duration {
	if !t.on || t.key != key {
		t.key, t.on, t.since, t.gone = key, true, now, false
	}
	if t.gone {
		return time.Hour
	}
	return TreeTipDelay - now.Sub(t.since)
}

// Leave 是指针不在任何一行上了（界面画完列表、这一帧没有行报悬停时调）。
func (t *TreeTip) Leave() { t.on, t.gone = false, false }

// Dismiss 收掉指针下面那行的提示（按下、点击、拖），指针离开那行之前不再出。
func (t *TreeTip) Dismiss() {
	if t.on {
		t.gone = true
	}
}
