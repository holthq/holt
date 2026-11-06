package app

import (
	"math"
	"time"

	ui "github.com/egoist/mygo/ui"
)

// SideScroll 是一个侧栏列表的滚动（界面的 sideList 用）：列表 TrackScroll 着 S；鼠标滚轮（不是触控板的 precise）
// 由界面接过来，一格走一个行高、往累计的目标滑过去（Wheel、Step）；Drawn 是上一帧画的时候的偏移，
// 和这一帧的 S.Y 一比就知道行比上一帧挪了多少（吸顶栈用上一帧的位置，靠它挪到这一帧）。
//
// 滑动是自己按帧算的、每帧写 S.Y，不用 ScrollState.ScrollTo：ScrollTo 的滑动每帧在布局前把偏移拽回滑动的路上，
// 半路上 ListState.ScrollTo / ScrollIntoView（定位当前文件、点吸顶栈、键盘挪光标）就被吞了。自己写的话，
// 下一帧建的时候 S.Y 不是上一帧写的那个就知道别的挪过了，滑动让给它（Step 停下）；差不到一行的是列表量到新行后
// 修正的偏移（行没动），滑动跟着平移、接着走。
type SideScroll struct {
	S     ui.ScrollState
	Drawn float32 // 上一帧画的时候的 S.Y

	gliding  bool
	from, to float32   // 这一段从哪滑到哪
	start    time.Time // 这一段从什么时候起
	wrote    float32   // 上一帧写进 S.Y 的
	rowH     float32   // 这一段按多高一行算的
}

// 滚轮的手感：一格（mygo 的一行，40 DIP）走一个行高；系统加速后一次事件好几行，照走，最多 WheelMaxRows 行；
// 一段滑 WheelGlide。wheelEndGap：mygo 的列表把离底 0.5 DIP 以内的偏移当成到底、拉回底上，从底往上的滑动至少要离开这么多。
const (
	WheelLine    = 40 // mygo 给非 precise 滚轮的一行是多少 DIP
	WheelMaxRows = 6
	WheelGlide   = 160 * time.Millisecond
	wheelEndGap  = 1
)

// WheelRows 把一次滚轮事件的 dy（DIP，mygo 一行 40）换成走几行：一行走一行，加速后的 n 行走 n 行、最多 WheelMaxRows；
// 不到一行的（Tester、细分的滚轮）照比例。
func WheelRows(dy float32) float32 {
	r := math.Min(math.Abs(float64(dy))/WheelLine, WheelMaxRows)
	if dy < 0 {
		r = -r
	}
	return float32(r)
}

// Gliding 报滚轮的滑动还在路上。
func (s *SideScroll) Gliding() bool { return s.gliding }

// Wheel 是滚轮的一格（dy 是 mygo 给的 DIP，rowH 一行多高，now 现在）：滑着的时候同方向接着累加（连续几格接成一段，
// 从现在的位置、现在的速度接着走），反方向从现在的位置重来（立刻转向）；目标夹在 0..MaxY 里。
// still 时（系统"减弱动态效果"）直接到位。报 false 是这一格走不动（已经到头，或列表不能滚）。
func (s *SideScroll) Wheel(dy, rowH float32, now time.Time, still bool) bool {
	if s.S.MaxY <= 0 {
		return false
	}
	step := WheelRows(dy) * rowH
	cur := s.S.Y
	from := cur
	if s.gliding && (s.to-cur)*step > 0 {
		from = s.to // 同方向还没走完：接着累加
	}
	to := min(max(from+step, 0), s.S.MaxY)
	if to == cur {
		s.gliding = false
		return false
	}
	if still {
		s.gliding = false
		s.S.Y = to
		return true
	}
	s.gliding, s.from, s.to, s.start, s.wrote, s.rowH = true, cur, to, now, cur, rowH
	return true
}

// Step 在建列表之前推进滑动到 now，写进 S.Y；报还要不要下一帧。上一帧写的 S.Y 被别的改了一行以上（列表自己滚到一行）
// 就停下，让给它；不到一行的跟着平移。
func (s *SideScroll) Step(now time.Time) bool {
	if !s.gliding {
		return false
	}
	if d := s.S.Y - s.wrote; d != 0 {
		if d >= s.rowH || d <= -s.rowH {
			s.gliding = false
			return false
		}
		s.from += d
		s.to = min(max(s.to+d, 0), s.S.MaxY)
	}
	t := max(float64(now.Sub(s.start)), 0) / float64(WheelGlide) // 帧的时间可能比事件的早一点
	if t >= 1 {
		s.S.Y, s.gliding = s.to, false
		s.wrote = s.to
		return false
	}
	// 三次方的缓出：起步快（接着上一段的速度，不一格一停），收得软
	e := 1 - math.Pow(1-t, 3)
	s.S.Y = s.from + (s.to-s.from)*float32(e)
	if s.to < s.from {
		s.S.Y = max(min(s.S.Y, s.S.MaxY-wheelEndGap), s.to) // 从底往上的第一帧走得太少会被拉回底上，这一格就没了
	}
	s.wrote = s.S.Y
	return true
}
