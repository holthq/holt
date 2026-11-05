package app

import "time"

// TreeTipDelay 是指针在文件树的一行上停多久才出那行的悬停提示（大小 · 改动时间 · 状态），和 VS Code 差不多。
const TreeTipDelay = 500 * time.Millisecond

// TreeTip 记文件树行的悬停提示：指针从什么时候起停在哪一行。有"暖启动"（VS Code、macOS 都这样）：
// 一条提示出来以后，指针在行间移动，新的一行的提示直接出、不再等；指针离开所有行、或者按下 / 右键收掉以后又要等满 TreeTipDelay。
// 按下（点击、拖、右键）之后这一行不再出提示，直到指针离开它。
type TreeTip struct {
	key   FileKey
	on    bool // 这一帧（到现在为止）指针在某一行上
	since time.Time
	gone  bool // 这一行的提示被按下收掉了
	warm  bool // 刚出过提示：换到别的行直接出
}

// Hover 报指针这一帧停在 key 那行上，返回还要等多久才出提示：≤ 0 是该出了；被按下收掉的返回很大的数。
func (t *TreeTip) Hover(key FileKey, now time.Time) time.Duration {
	if !t.on || t.key != key {
		t.key, t.on, t.since, t.gone = key, true, now, false
		if t.warm {
			t.since = now.Add(-TreeTipDelay)
		}
	}
	if t.gone {
		return time.Hour
	}
	wait := TreeTipDelay - now.Sub(t.since)
	if wait <= 0 {
		t.warm = true
	}
	return wait
}

// Leave 是指针不在任何一行上了（界面画完列表、这一帧没有行报悬停时调）。
func (t *TreeTip) Leave() { t.on, t.gone, t.warm = false, false, false }

// Dismiss 收掉指针下面那行的提示（按下、点击、拖、右键），指针离开那行之前不再出；下一行也重新等。
func (t *TreeTip) Dismiss() {
	if t.on {
		t.gone = true
	}
	t.warm = false
}
