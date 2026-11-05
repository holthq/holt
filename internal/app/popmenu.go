package app

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// 文件树的右键菜单是 Grove 自己画的（issue #48）：mygo 的 PopoverMenu 不能按住右键拖到项上松开、子菜单没有悬停延迟
// 和安全三角、灰着的项说不了原因、点菜单外面那一下还会落到底下的行上。这里是它的状态机，不碰界面：
// 项由建菜单的函数经 MenuSink 收集，位置、命中、计时都按窗口坐标（DIP）算，界面（internal/ui/gmenu.go）
// 每帧把指针、按键、时间喂进来，再照着 PopMenu 画。测试直接喂事件（popmenu_test.go），Tester 按不住右键也测得到。

// MenuSink 是建菜单的函数往里加项的地方：mygo 的 *ui.Menu（系统菜单、PopoverMenu，经 internal/ui 的适配器）
// 和 Grove 自己画的菜单（PopMenu）都实现它，menus.go 的那些建菜单的函数两边共用。
type MenuSink interface {
	Item(label string) MenuItem
	Separator()
	Submenu(label string, build func(MenuSink)) MenuItem
}

// MenuItem 是 MenuSink 加出来的一项，方法链式设置，Chosen 报这一回是不是选了它（和 mygo 的一样：
// 收集项的那一回总是 false，选了以后再跑一遍建菜单的函数，选中的那项报 true）。
type MenuItem interface {
	Disabled(bool) MenuItem
	Checked(bool) MenuItem
	Shortcut(ui.Modifiers, ui.Key) MenuItem
	// Why 是灰着的原因（空是没灰），同时设上 Disabled。Grove 画的菜单悬停时用提示说；系统菜单没有悬停说明，
	// 只能括在标签后面（internal/ui 的 withWhy）。
	Why(string) MenuItem
	Chosen() bool
}

// MenuEntry 是收集到的一项。
type MenuEntry struct {
	Label    string
	Sep      bool
	Disabled bool
	Checked  bool
	Why      string // 灰着的原因，悬停时提示
	Keys     string // 快捷键（⇧⌘C 这样），右对齐、淡色
	Sub      []MenuEntry
	HasSub   bool
	seq      int
}

// 量菜单的几个尺寸（pt），界面照这些画：外边距 4、项高 24、分隔线占 9、圆角 6（界面里用）。
const (
	MenuPad      = 4
	MenuItemH    = 24
	MenuSepH     = 9
	MenuMargin   = 6  // 菜单离窗口边至少这么远
	MenuTextL    = 26 // 字左边留的（勾在里面）
	MenuTextR    = 12
	MenuKeysGap  = 28 // 标签和快捷键之间至少空这么多
	MenuArrowW   = 14 // 子菜单的箭头占的
	MenuMinW     = 160
	MenuSubDelay = 200 * time.Millisecond // 指针停在有子菜单的项上多久才开
	MenuTipDelay = 500 * time.Millisecond // 灰着的项停多久出原因
	MenuHoldMin  = 150 * time.Millisecond // 右键按下后过了这么久（或动过）再松开，才算拖到项上松开选它
	menuMoveSlop = 4
)

// MenuRect 是窗口里的一块（DIP）。
type MenuRect struct{ X, Y, W, H float32 }

func (r MenuRect) has(x, y float32) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

// MenuLevel 是开着的一层：根菜单是第 0 层，往下是一层层子菜单。
type MenuLevel struct {
	Items  []MenuEntry
	Rect   MenuRect // 面板（含外边距）
	Scroll float32  // 窗口都放不下时往下滚了多少
	Full   float32  // 不滚时整份的高
	Hot    int      // 高亮的项，-1 是没有
}

// ItemY 是第 i 项在面板里（不算滚动）的顶。
func (l *MenuLevel) ItemY(i int) float32 {
	y := float32(MenuPad)
	for _, it := range l.Items[:i] {
		y += entryH(it)
	}
	return y
}

func entryH(it MenuEntry) float32 {
	if it.Sep {
		return MenuSepH
	}
	return MenuItemH
}

// PopMenu 是 Grove 自己画的右键菜单（现在只有文件树用）。零值是关着的。
type PopMenu struct {
	// Owner 是菜单对着的东西（文件树行的 FileKey 之类）：那一行画 1 pt 描边。
	Owner any
	build func(MenuSink)
	open  bool
	// Measure 量一段字有多宽（界面设成按菜单的字号量；测试里给个假的）。
	Measure func(s string) float32
	winW    float32
	winH    float32
	ax, ay  float32 // 打开时指针的位置

	root   []MenuEntry
	path   []int // 开着的子菜单：path[i] 是第 i 层里开着子菜单的那项
	levels []MenuLevel

	// 按住右键打开的：这次按下松开之前，松开在项上就选它（按住拖到项上松开）
	held    bool
	openAt  time.Time
	moved   bool
	downLvl int // 菜单里左键按下的那层 / 项（松开在同一项上才选）
	downIdx int

	px, py   float32 // 指针上一次的位置
	hasPtr   bool
	pending  bool // 等着开 / 换的子菜单
	pendLvl  int
	pendIdx  int
	pendAt   time.Time
	hotSince time.Time // 现在高亮的项从什么时候起（提示的计时）
	byKey    bool      // 最近一次是按键挪的高亮：不出提示

	choice    int // 选中的项（seq），0 是没选
	choiceLbl string
}

// Open 在窗口的 (x, y) 打开菜单，build 建它的项；held 是右键按下打开的（还按着）。
func (m *PopMenu) Open(owner any, build func(MenuSink), x, y, winW, winH float32, held bool, now time.Time) {
	*m = PopMenu{Owner: owner, build: build, open: true, Measure: m.Measure, winW: winW, winH: winH, ax: x, ay: y,
		held: held, openAt: now, downLvl: -1, downIdx: -1, px: x, py: y, hasPtr: true, hotSince: now}
	m.Refresh(winW, winH)
}

// IsOpen 报菜单开着。
func (m *PopMenu) IsOpen() bool { return m != nil && m.open }

// Close 关掉菜单（不选）。
func (m *PopMenu) Close() {
	if m.open {
		*m = PopMenu{Measure: m.Measure}
	}
}

// Levels 是开着的各层（界面照着画）。
func (m *PopMenu) Levels() []MenuLevel { return m.levels }

// OpenPath 是开着子菜单的那几项（第 i 层的第 path[i] 项）。
func (m *PopMenu) OpenPath() []int { return m.path }

// Refresh 重新跑一遍建菜单的函数收集项（每帧调：项的灰不灰、勾不勾跟着状态变），再按窗口大小摆好各层。
func (m *PopMenu) Refresh(winW, winH float32) {
	if !m.open {
		return
	}
	m.winW, m.winH = winW, winH
	s := &menuCollect{}
	m.build(s)
	m.root = tidy(s.items)
	m.layout()
}

// tidy 去掉头尾和连着的分隔线（建菜单的函数按组加分隔线，有的组整组没有）。
func tidy(items []MenuEntry) []MenuEntry {
	out := items[:0:0]
	for _, it := range items {
		if it.Sep && (len(out) == 0 || out[len(out)-1].Sep) {
			continue
		}
		if it.HasSub {
			it.Sub = tidy(it.Sub)
		}
		out = append(out, it)
	}
	for len(out) > 0 && out[len(out)-1].Sep {
		out = out[:len(out)-1]
	}
	return out
}

func (m *PopMenu) measure(s string) float32 {
	if m.Measure != nil {
		return m.Measure(s)
	}
	return float32(utf8.RuneCountInString(s)) * 7
}

// width 是一层的宽：最长的标签 + 快捷键 + 两边留的。
func (m *PopMenu) width(items []MenuEntry) float32 {
	var lab, keys float32
	sub := false
	for _, it := range items {
		if it.Sep {
			continue
		}
		lab = max(lab, m.measure(it.Label))
		if it.Keys != "" {
			keys = max(keys, m.measure(it.Keys))
		}
		sub = sub || it.HasSub
	}
	w := MenuPad*2 + MenuTextL + lab + MenuTextR
	if keys > 0 {
		w += MenuKeysGap + keys
	}
	if sub {
		w += MenuArrowW
	}
	return max(w, MenuMinW)
}

// layout 按 path 摆好各层：根菜单左上角在指针处，下面放不下就往上开，往上也放不下就贴着窗口，窗口本身都放不下才滚
// （issue #6 的规矩）；右边放不下往左开。子菜单的第一项和开它的那项对齐，右边放不下开到左边。
func (m *PopMenu) layout() {
	old := m.levels
	m.levels = m.levels[:0:0]
	items := m.root
	for lv := 0; ; lv++ {
		l := MenuLevel{Items: items, Hot: -1}
		if lv < len(old) && len(old[lv].Items) == len(items) {
			l.Hot, l.Scroll = old[lv].Hot, old[lv].Scroll
		}
		h := float32(MenuPad * 2)
		for _, it := range items {
			h += entryH(it)
		}
		l.Full = h
		w := m.width(items)
		var x, y float32
		if lv == 0 {
			x, y = m.ax, m.ay
			if x+w > m.winW-MenuMargin {
				x = m.ax - w
			}
			if y+h > m.winH-MenuMargin {
				y = m.ay - h
			}
		} else {
			p := m.levels[lv-1]
			at := m.path[lv-1]
			x = p.Rect.X + p.Rect.W - MenuPad/2
			if x+w > m.winW-MenuMargin {
				x = p.Rect.X - w + MenuPad/2
			}
			y = p.Rect.Y + p.ItemY(at) - p.Scroll - MenuPad
			if y+h > m.winH-MenuMargin {
				y = m.winH - MenuMargin - h
			}
		}
		x = max(MenuMargin, min(x, m.winW-MenuMargin-w))
		if maxH := m.winH - 2*MenuMargin; h > maxH {
			h = max(maxH, MenuItemH+2*MenuPad)
		}
		y = max(MenuMargin, min(y, m.winH-MenuMargin-h))
		l.Rect = MenuRect{x, y, w, h}
		l.Scroll = max(0, min(l.Scroll, l.Full-h))
		if l.Hot >= len(items) {
			l.Hot = -1
		}
		m.levels = append(m.levels, l)
		if lv >= len(m.path) {
			break
		}
		at := m.path[lv]
		if at < 0 || at >= len(items) || !items[at].HasSub || items[at].Disabled {
			m.path = m.path[:lv]
			break
		}
		items = items[at].Sub
	}
}

// hit 是窗口的 (x, y) 落在哪层的哪项上：lv -1 是不在任何一层上，idx -1 是在面板上但不在能指的项上（外边距、分隔线）。
func (m *PopMenu) hit(x, y float32) (lv, idx int) {
	for lv := len(m.levels) - 1; lv >= 0; lv-- {
		l := &m.levels[lv]
		if !l.Rect.has(x, y) {
			continue
		}
		yy := y - l.Rect.Y + l.Scroll
		top := float32(MenuPad)
		for i, it := range l.Items {
			h := entryH(it)
			if yy >= top && yy < top+h {
				if it.Sep {
					return lv, -1
				}
				return lv, i
			}
			top += h
		}
		return lv, -1
	}
	return -1, -1
}

// Contains 报窗口的 (x, y) 在菜单的某一层上。
func (m *PopMenu) Contains(x, y float32) bool {
	lv, _ := m.hit(x, y)
	return lv >= 0
}

func (m *PopMenu) setHot(lv, idx int, now time.Time) {
	if m.levels[lv].Hot != idx {
		m.levels[lv].Hot = idx
		m.hotSince = now
	}
}

// towardSub 报指针从 (px, py) 挪到 (x, y) 是不是朝着第 lv 层开着的子菜单去（安全三角：上一个点和子菜单靠近这边的
// 上下两个角围成的三角形里）。
func (m *PopMenu) towardSub(lv int, x, y float32) bool {
	if lv+1 >= len(m.levels) || !m.hasPtr {
		return false
	}
	s := m.levels[lv+1].Rect
	edge := s.X
	if s.X < m.levels[lv].Rect.X { // 子菜单开在左边
		edge = s.X + s.W
	}
	return inTri(x, y, m.px, m.py, edge, s.Y-MenuItemH/2, edge, s.Y+s.H+MenuItemH/2)
}

func inTri(x, y, ax, ay, bx, by, cx, cy float32) bool {
	d1 := (x-bx)*(ay-by) - (ax-bx)*(y-by)
	d2 := (x-cx)*(by-cy) - (bx-cx)*(y-cy)
	d3 := (x-ax)*(cy-ay) - (cx-ax)*(y-ay)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

// Move 是指针挪到窗口的 (x, y)（按着右键拖的、没按的都是）。
func (m *PopMenu) Move(x, y float32, now time.Time) {
	if !m.open {
		return
	}
	if m.hasPtr && (abs32(x-m.ax) > menuMoveSlop || abs32(y-m.ay) > menuMoveSlop) {
		m.moved = true
	}
	m.byKey = false
	lv, idx := m.hit(x, y)
	defer func() { m.px, m.py, m.hasPtr = x, y, true }()
	if lv < 0 {
		// 出了菜单：开着的子菜单留着，只是没开子菜单的那层不再高亮
		last := len(m.levels) - 1
		m.setHot(last, -1, now)
		m.pending = false
		return
	}
	// 深一层的面板上指着：上面各层只高亮开着子菜单的那项
	if lv < len(m.path) && idx == m.path[lv] {
		m.setHot(lv, idx, now)
		m.pending = false
		m.trim(lv + 1)
		return
	}
	if lv < len(m.path) && m.towardSub(lv, x, y) {
		// 朝子菜单走着，路过别的项：先不换，停下来过了延迟才换
		m.pending, m.pendLvl, m.pendIdx, m.pendAt = true, lv, idx, now
		return
	}
	m.setHot(lv, idx, now)
	m.trim(lv + 1)
	if idx < 0 {
		m.pending = false
		return
	}
	if lv < len(m.path) || m.levels[lv].Items[idx].HasSub && !m.levels[lv].Items[idx].Disabled {
		if !m.pending || m.pendLvl != lv || m.pendIdx != idx {
			m.pending, m.pendLvl, m.pendIdx, m.pendAt = true, lv, idx, now
		}
		return
	}
	m.pending = false
}

// trim 把 lv 层往下、不是开着的那些层的高亮清掉（指针回到上层时，下层不高亮）。
func (m *PopMenu) trim(lv int) {
	for i := lv; i < len(m.levels); i++ {
		m.levels[i].Hot = -1
	}
}

// Tick 跑到时的计时（子菜单的悬停延迟），返回还要多久再叫它（界面拿去 c.After）；0 是没有在等的。
func (m *PopMenu) Tick(now time.Time) time.Duration {
	if !m.open {
		return 0
	}
	var wait time.Duration
	if m.pending {
		if d := MenuSubDelay - now.Sub(m.pendAt); d > 0 {
			wait = d
		} else {
			m.pending = false
			lv, idx := m.pendLvl, m.pendIdx
			if lv < len(m.levels) {
				m.setHot(lv, idx, now)
				m.openSub(lv, idx)
			}
		}
	}
	if _, _, ok := m.tipItem(); ok && !m.byKey {
		if d := MenuTipDelay - now.Sub(m.hotSince); d > 0 && (wait == 0 || d < wait) {
			wait = d
		}
	}
	return wait
}

// openSub 让第 lv 层只开第 idx 项的子菜单（idx 不是有子菜单的项就是关掉那层下面的）。
func (m *PopMenu) openSub(lv, idx int) {
	m.path = m.path[:min(lv, len(m.path))]
	if idx >= 0 && idx < len(m.levels[lv].Items) {
		it := m.levels[lv].Items[idx]
		if it.HasSub && !it.Disabled && len(it.Sub) > 0 {
			m.path = append(m.path, idx)
		}
	}
	m.layout()
}

// tipItem 是现在高亮着、灰着又说了原因的那项。
func (m *PopMenu) tipItem() (lv, idx int, ok bool) {
	for lv := len(m.levels) - 1; lv >= 0; lv-- {
		l := &m.levels[lv]
		if l.Hot >= 0 && (lv >= len(m.path) || l.Hot != m.path[lv]) {
			it := l.Items[l.Hot]
			return lv, l.Hot, it.Disabled && it.Why != ""
		}
	}
	return -1, -1, false
}

// Tip 是该出的灰项原因：第 lv 层第 idx 项，指针停在上面满 MenuTipDelay 了。
func (m *PopMenu) Tip(now time.Time) (lv, idx int, why string, ok bool) {
	lv, idx, ok = m.tipItem()
	if !ok || m.byKey || now.Sub(m.hotSince) < MenuTipDelay {
		return -1, -1, "", false
	}
	return lv, idx, m.levels[lv].Items[idx].Why, true
}

// Down 是指针在窗口的 (x, y) 按下：菜单外面的关掉菜单、吞掉这一下（不落到底下）；返回 true 都是吞了。
func (m *PopMenu) Down(x, y float32, button int, now time.Time) bool {
	if !m.open {
		return false
	}
	lv, idx := m.hit(x, y)
	if lv < 0 {
		m.Close()
		return true
	}
	m.held = false
	m.downLvl, m.downIdx = lv, idx
	if button == 0 && idx >= 0 && m.levels[lv].Items[idx].HasSub {
		m.setHot(lv, idx, now)
		m.pending = false
		m.openSub(lv, idx) // 点有子菜单的项马上开，不等
	}
	return true
}

// Up 是指针在窗口的 (x, y) 松开。右键按下打开菜单的那一下：过了 MenuHoldMin 或者动过再松在项上就选它，
// 刚按下就松开（没动）菜单留着。之后在菜单里点：按下和松开在同一项上才选。
func (m *PopMenu) Up(x, y float32, button int, now time.Time) {
	if !m.open {
		return
	}
	lv, idx := m.hit(x, y)
	if m.held {
		m.held = false
		if now.Sub(m.openAt) < MenuHoldMin && !m.moved {
			return
		}
		m.activate(lv, idx, now)
		return
	}
	dl, di := m.downLvl, m.downIdx
	m.downLvl, m.downIdx = -1, -1
	if lv >= 0 && lv == dl && idx == di {
		m.activate(lv, idx, now)
	}
}

// activate 选第 lv 层第 idx 项：有子菜单的开它，灰着的、分隔线不理。
func (m *PopMenu) activate(lv, idx int, now time.Time) {
	if lv < 0 || idx < 0 || lv >= len(m.levels) || idx >= len(m.levels[lv].Items) {
		return
	}
	it := m.levels[lv].Items[idx]
	if it.Sep || it.Disabled {
		return
	}
	if it.HasSub {
		m.setHot(lv, idx, now)
		m.pending = false
		m.openSub(lv, idx)
		if lv+1 < len(m.levels) && m.byKey {
			m.levels[lv+1].Hot = next(m.levels[lv+1].Items, -1, 1)
		}
		return
	}
	build := m.build
	m.Close()
	m.choice, m.choiceLbl, m.build = it.seq, it.Label, build // 留给 RunChoice 再跑一遍
}

// Chosen 报刚选了一项（还没执行）：界面这一帧调 RunChoice。
func (m *PopMenu) Chosen() bool { return m.choice != 0 }

// RunChoice 再跑一遍建菜单的函数，选中的那项的 Chosen 报 true（和 mygo 的菜单一样），执行它。
func (m *PopMenu) RunChoice() {
	if m.choice == 0 || m.build == nil {
		return
	}
	s := &menuCollect{choose: m.choice, label: m.choiceLbl}
	build := m.build
	*m = PopMenu{Measure: m.Measure}
	build(s)
}

// Scroll 是在窗口的 (x, y) 滚动 dy：在窗口都放不下、能滚的那层上滚它；别的地方关掉菜单（滚文件树也关）。
func (m *PopMenu) Scroll(x, y, dy float32) {
	if !m.open {
		return
	}
	lv, _ := m.hit(x, y)
	if lv < 0 || m.levels[lv].Full <= m.levels[lv].Rect.H {
		m.Close()
		return
	}
	l := &m.levels[lv]
	l.Scroll = max(0, min(l.Scroll+dy, l.Full-l.Rect.H))
	m.path = m.path[:min(lv, len(m.path))]
	m.layout()
}

// focusLevel 是按键作用的那层：最深的有高亮的一层，都没有是最深的那层。
func (m *PopMenu) focusLevel() int {
	for lv := len(m.levels) - 1; lv >= 0; lv-- {
		if m.levels[lv].Hot >= 0 && (lv >= len(m.path) || m.levels[lv].Hot != m.path[lv] || lv == len(m.levels)-1) {
			return lv
		}
	}
	return len(m.levels) - 1
}

// next 从 i 往 dir 方向找下一个能选的项（跳过分隔线和灰着的），转圈；没有返回 -1。
func next(items []MenuEntry, i, dir int) int {
	n := len(items)
	for k := 1; k <= n; k++ {
		j := ((i+dir*k)%n + n) % n
		if i < 0 && dir < 0 {
			j = ((n-k)%n + n) % n
		}
		if !items[j].Sep && !items[j].Disabled {
			return j
		}
	}
	return -1
}

// Key 是菜单开着时按的键：↑↓ 挪高亮（跳过分隔线和灰着的），→ 开子菜单、← 关回上一层，回车 / 空格选，Esc 关；
// text 是打的字，跳到下一个以它开头的项。返回 true 是菜单接了。
func (m *PopMenu) Key(key ui.Key, text string, now time.Time) bool {
	if !m.open {
		return false
	}
	lv := m.focusLevel()
	l := &m.levels[lv]
	switch key {
	case ui.KeyEscape:
		m.Close()
		return true
	case ui.KeyDown, ui.KeyUp:
		dir := 1
		if key == ui.KeyUp {
			dir = -1
		}
		if j := next(l.Items, l.Hot, dir); j >= 0 {
			m.byKey = true
			m.setHot(lv, j, now)
			m.pending = false
			m.path = m.path[:min(lv, len(m.path))]
			m.layout()
		}
		return true
	case ui.KeyRight:
		if l.Hot >= 0 && l.Items[l.Hot].HasSub {
			m.byKey = true
			m.activate(lv, l.Hot, now)
		}
		return true
	case ui.KeyLeft:
		if lv > 0 {
			m.byKey = true
			m.path = m.path[:lv-1]
			m.levels[lv-1].Hot = m.pathAt(lv - 1)
			m.layout()
		}
		return true
	case ui.KeyEnter, ui.KeySpace:
		if l.Hot >= 0 {
			m.byKey = true
			m.activate(lv, l.Hot, now)
		}
		return true
	}
	r, _ := utf8.DecodeRuneInString(text)
	if text != "" && unicode.IsPrint(r) && !unicode.IsSpace(r) {
		lower := unicode.ToLower(r)
		n := len(l.Items)
		for k := 1; k <= n; k++ {
			j := ((l.Hot+k)%n + n) % n
			it := l.Items[j]
			f, _ := utf8.DecodeRuneInString(strings.TrimSpace(it.Label))
			if !it.Sep && !it.Disabled && unicode.ToLower(f) == lower {
				m.byKey = true
				m.setHot(lv, j, now)
				m.path = m.path[:min(lv, len(m.path))]
				m.layout()
				break
			}
		}
		return true
	}
	return false
}

func (m *PopMenu) pathAt(lv int) int {
	if lv < len(m.levels) {
		return m.levels[lv].Hot
	}
	return -1
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// menuCollect 是 PopMenu 的 MenuSink：收集项（choose 为 0），或者再跑一遍时报选中的那项。
type menuCollect struct {
	items  []MenuEntry
	seq    *int
	n      int
	choose int
	label  string
}

type popItem struct {
	s *menuCollect
	i int // 在 s.items 里的下标
}

func (s *menuCollect) add(e MenuEntry) MenuItem {
	if s.seq == nil {
		s.seq = &s.n
	}
	*s.seq++
	e.seq = *s.seq
	s.items = append(s.items, e)
	return popItem{s, len(s.items) - 1}
}

func (s *menuCollect) Item(label string) MenuItem { return s.add(MenuEntry{Label: label}) }
func (s *menuCollect) Separator()                 { s.add(MenuEntry{Sep: true}) }
func (s *menuCollect) Submenu(label string, build func(MenuSink)) MenuItem {
	it := s.add(MenuEntry{Label: label, HasSub: true}).(popItem)
	if s.seq == nil {
		s.seq = &s.n
	}
	sub := &menuCollect{seq: s.seq, choose: s.choose, label: s.label}
	build(sub)
	s.items[it.i].Sub = sub.items
	return it
}

func (p popItem) e() *MenuEntry { return &p.s.items[p.i] }

func (p popItem) Disabled(d bool) MenuItem { p.e().Disabled = d; return p }
func (p popItem) Checked(on bool) MenuItem { p.e().Checked = on; return p }
func (p popItem) Shortcut(mods ui.Modifiers, key ui.Key) MenuItem {
	p.e().Keys = KeysOf(mods, key)
	return p
}
func (p popItem) Why(why string) MenuItem {
	e := p.e()
	e.Why, e.Disabled = why, why != ""
	return p
}
func (p popItem) Chosen() bool {
	e := p.e()
	return p.s.choose != 0 && e.seq == p.s.choose && e.Label == p.s.label && !e.Disabled && !e.HasSub
}
