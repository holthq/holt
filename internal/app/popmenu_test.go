package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 测 Grove 自己画的右键菜单的状态机（issue #48）：Tester 按不住右键，按住拖、松开、延迟、安全三角都在这里直接喂事件测。

type popKit struct {
	t      *testing.T
	m      PopMenu
	now    time.Time
	chosen []string
}

// 菜单：打开 | 灰着（说了原因）| - | 更多 ▸（甲、乙）| 复制（⌘C）| 删除
func newPopKit(t *testing.T) *popKit {
	k := &popKit{t: t, now: time.Unix(1000, 0)}
	k.m.Measure = func(s string) float32 { return float32(len(s)) * 7 }
	return k
}

func (k *popKit) build(m MenuSink) {
	if m.Item("Open").Chosen() {
		k.chosen = append(k.chosen, "Open")
	}
	m.Item("Gray").Why("no reason")
	m.Separator()
	m.Submenu("More", func(m MenuSink) {
		if m.Item("Alpha").Chosen() {
			k.chosen = append(k.chosen, "Alpha")
		}
		if m.Item("Beta").Chosen() {
			k.chosen = append(k.chosen, "Beta")
		}
	})
	if m.Item("Copy").Shortcut(ui.Super, ui.KeyC).Chosen() {
		k.chosen = append(k.chosen, "Copy")
	}
	if m.Item("Delete").Chosen() {
		k.chosen = append(k.chosen, "Delete")
	}
	m.Separator()
}

func (k *popKit) open(held bool) {
	k.m.Open("row", k.build, 100, 100, 1000, 800, held, k.now)
}

func (k *popKit) wait(d time.Duration) {
	k.now = k.now.Add(d)
	k.m.Tick(k.now)
}

// center 是第 lv 层第 i 项的中心（窗口坐标）。
func (k *popKit) center(lv, i int) (float32, float32) {
	l := k.m.Levels()[lv]
	return l.Rect.X + l.Rect.W/2, l.Rect.Y + l.ItemY(i) - l.Scroll + MenuItemH/2
}

func (k *popKit) moveTo(lv, i int) {
	x, y := k.center(lv, i)
	k.m.Move(x, y, k.now)
}

func (k *popKit) run() {
	if k.m.Chosen() {
		k.m.RunChoice()
	}
}

func TestPopMenuItems(t *testing.T) {
	k := newPopKit(t)
	k.open(false)
	l := k.m.Levels()
	if len(l) != 1 {
		t.Fatalf("开了 %d 层", len(l))
	}
	it := l[0].Items
	if len(it) != 6 || it[2].Sep != true || it[len(it)-1].Sep {
		t.Fatalf("项不对（尾巴上的分隔线该去掉）：%+v", it)
	}
	if !it[1].Disabled || it[1].Why != "no reason" {
		t.Fatalf("Why 该设上灰和原因：%+v", it[1])
	}
	if it[4].Keys != KeysOf(ui.Super, ui.KeyC) || it[4].Keys == "" {
		t.Fatalf("快捷键：%q", it[4].Keys)
	}
	if !it[3].HasSub || len(it[3].Sub) != 2 {
		t.Fatalf("子菜单：%+v", it[3])
	}
	r := l[0].Rect
	if r.X != 100 || r.Y != 100 || r.H != MenuPad*2+5*MenuItemH+MenuSepH {
		t.Fatalf("位置 / 高：%+v", r)
	}
}

// 按住右键拖到项上、过了 150 ms 松开：选它。
func TestPopMenuHoldDragRelease(t *testing.T) {
	k := newPopKit(t)
	k.open(true)
	k.now = k.now.Add(300 * time.Millisecond)
	k.moveTo(0, 4)
	x, y := k.center(0, 4)
	k.m.Up(x, y, 1, k.now)
	k.run()
	if len(k.chosen) != 1 || k.chosen[0] != "Copy" || k.m.IsOpen() {
		t.Fatalf("拖到复制上松开：选了 %q，开着 %v", k.chosen, k.m.IsOpen())
	}
}

// 右键按下马上松开（没动）：菜单留着；之后点一项才选。
func TestPopMenuReleaseTooSoon(t *testing.T) {
	k := newPopKit(t)
	k.open(true)
	k.now = k.now.Add(50 * time.Millisecond)
	k.m.Up(100, 100, 1, k.now) // 落在第一项上，但太快
	k.run()
	if len(k.chosen) != 0 || !k.m.IsOpen() {
		t.Fatalf("刚按下就松开：选了 %q，开着 %v", k.chosen, k.m.IsOpen())
	}
	// 动过就算没到 150 ms 也选
	k.open(true)
	k.moveTo(0, 0)
	x, y := k.center(0, 0)
	k.m.Up(x, y, 1, k.now)
	k.run()
	if len(k.chosen) != 1 || k.chosen[0] != "Open" {
		t.Fatalf("动过再松开：选了 %q", k.chosen)
	}
	// 之后的普通点击
	k.chosen = nil
	k.open(true)
	k.m.Up(100, 100, 1, k.now)
	x, y = k.center(0, 5)
	k.m.Move(x, y, k.now)
	if !k.m.Down(x, y, 0, k.now) {
		t.Fatal("菜单里的按下该吞掉")
	}
	k.m.Up(x, y, 0, k.now)
	k.run()
	if len(k.chosen) != 1 || k.chosen[0] != "Delete" {
		t.Fatalf("点删除：选了 %q", k.chosen)
	}
}

// 灰着的项、分隔线松开不选，菜单留着；灰项停 500 ms 出原因。
func TestPopMenuDisabled(t *testing.T) {
	k := newPopKit(t)
	k.open(false)
	k.moveTo(0, 1)
	x, y := k.center(0, 1)
	k.m.Down(x, y, 0, k.now)
	k.m.Up(x, y, 0, k.now)
	k.run()
	if len(k.chosen) != 0 || !k.m.IsOpen() {
		t.Fatalf("点灰项：选了 %q，开着 %v", k.chosen, k.m.IsOpen())
	}
	if _, _, _, ok := k.m.Tip(k.now); ok {
		t.Fatal("还没停够就出了原因")
	}
	if w := k.m.Tick(k.now); w <= 0 || w > MenuTipDelay {
		t.Fatalf("该等提示：%v", w)
	}
	k.wait(MenuTipDelay)
	lv, i, why, ok := k.m.Tip(k.now)
	if !ok || lv != 0 || i != 1 || why != "no reason" {
		t.Fatalf("停够了该出原因：%d %d %q %v", lv, i, why, ok)
	}
	k.moveTo(0, 0)
	if _, _, _, ok := k.m.Tip(k.now); ok {
		t.Fatal("挪到别的项上原因该收掉")
	}
}

// 菜单外面按下：关掉、吞掉。
func TestPopMenuOutsideSwallowed(t *testing.T) {
	k := newPopKit(t)
	k.open(false)
	if !k.m.Down(10, 10, 0, k.now) {
		t.Fatal("外面的按下该吞掉")
	}
	if k.m.IsOpen() {
		t.Fatal("外面按下该关掉菜单")
	}
	if k.m.Down(10, 10, 0, k.now) {
		t.Fatal("关着时不吞")
	}
}

func TestPopMenuEscScrollBlur(t *testing.T) {
	k := newPopKit(t)
	k.open(false)
	if !k.m.Key(ui.KeyEscape, "", k.now) || k.m.IsOpen() {
		t.Fatal("Esc 该关掉")
	}
	k.open(false)
	k.m.Scroll(10, 10, 20)
	if k.m.IsOpen() {
		t.Fatal("在菜单外面滚（滚文件树）该关掉")
	}
	k.open(false)
	k.m.Close()
	if k.m.IsOpen() {
		t.Fatal("Close")
	}
}

// 子菜单：停 200 ms 才开；点它马上开。
func TestPopMenuSubDelay(t *testing.T) {
	k := newPopKit(t)
	k.open(false)
	k.moveTo(0, 3)
	if len(k.m.Levels()) != 1 {
		t.Fatal("刚指上就开了子菜单")
	}
	k.wait(100 * time.Millisecond)
	if len(k.m.Levels()) != 1 {
		t.Fatal("100 ms 就开了")
	}
	k.wait(100 * time.Millisecond)
	if len(k.m.Levels()) != 2 {
		t.Fatal("200 ms 该开了")
	}
	sub := k.m.Levels()[1].Rect
	par := k.m.Levels()[0]
	if sub.X < par.Rect.X+par.Rect.W-MenuPad || sub.Y != par.Rect.Y+par.ItemY(3)-MenuPad {
		t.Fatalf("子菜单该在右边、和开它的项对齐：%+v，父 %+v", sub, par.Rect)
	}
	// 子菜单里选一项
	k.moveTo(1, 1)
	x, y := k.center(1, 1)
	k.m.Down(x, y, 0, k.now)
	k.m.Up(x, y, 0, k.now)
	k.run()
	if len(k.chosen) != 1 || k.chosen[0] != "Beta" {
		t.Fatalf("选子菜单里的乙：%q", k.chosen)
	}
	// 点马上开
	k.open(false)
	k.moveTo(0, 3)
	x, y = k.center(0, 3)
	k.m.Down(x, y, 0, k.now)
	if len(k.m.Levels()) != 2 {
		t.Fatal("点有子菜单的项该马上开")
	}
	// 指到别的项上，过了延迟子菜单关掉
	k.moveTo(0, 0)
	k.wait(MenuSubDelay)
	if len(k.m.Levels()) != 1 {
		t.Fatal("指到别的项停够了，子菜单该关")
	}
}

// 安全三角：从开着子菜单的项斜着往子菜单走，路过下面的"复制"不关子菜单；停在"复制"上过了延迟才换。
func TestPopMenuSafetyTriangle(t *testing.T) {
	k := newPopKit(t)
	k.open(false)
	k.moveTo(0, 3)
	k.wait(MenuSubDelay)
	if len(k.m.Levels()) != 2 {
		t.Fatal("子菜单没开")
	}
	par := k.m.Levels()[0]
	sub := k.m.Levels()[1].Rect
	// 从"更多"右边一点往子菜单的下半走，经过"复制"那一行的右端
	sx, sy := par.Rect.X+par.Rect.W-30, par.Rect.Y+par.ItemY(3)+MenuItemH/2
	k.m.Move(sx, sy, k.now)
	cx, cy := par.Rect.X+par.Rect.W-10, par.Rect.Y+par.ItemY(4)+MenuItemH/2
	if cx >= sub.X {
		t.Fatalf("测不到：复制的点 %v 已经在子菜单 %v 里", cx, sub.X)
	}
	k.now = k.now.Add(20 * time.Millisecond)
	k.m.Move(cx, cy, k.now)
	k.m.Tick(k.now)
	if len(k.m.Levels()) != 2 || k.m.Levels()[0].Hot != 3 {
		t.Fatalf("朝子菜单走路过复制，子菜单该留着、更多还高亮：%d 层，高亮 %d", len(k.m.Levels()), k.m.Levels()[0].Hot)
	}
	k.now = k.now.Add(20 * time.Millisecond)
	k.m.Move(sub.X+5, sub.Y+sub.H/2, k.now)
	if len(k.m.Levels()) != 2 || k.m.Levels()[1].Hot < 0 {
		t.Fatal("到了子菜单该高亮里面的项")
	}
	// 没有三角：直着往下走到复制，过了延迟子菜单关
	k.open(false)
	k.moveTo(0, 3)
	k.wait(MenuSubDelay)
	x, y := k.center(0, 3)
	k.m.Move(x-40, y, k.now) // 往左挪：背着子菜单
	x, y = k.center(0, 4)
	k.m.Move(x-40, y, k.now)
	if k.m.Levels()[0].Hot != 4 {
		t.Fatal("背着子菜单走，该马上高亮复制")
	}
	k.wait(MenuSubDelay)
	if len(k.m.Levels()) != 1 {
		t.Fatal("停在复制上过了延迟，子菜单该关")
	}
}

// 键盘：↓ 跳过灰项和分隔线，→ 开子菜单、← 关，回车选，打字母跳到它开头的项。
func TestPopMenuKeys(t *testing.T) {
	k := newPopKit(t)
	k.open(false)
	k.m.Key(ui.KeyDown, "", k.now)
	if k.m.Levels()[0].Hot != 0 {
		t.Fatalf("第一次 ↓ 到第一项：%d", k.m.Levels()[0].Hot)
	}
	k.m.Key(ui.KeyDown, "", k.now)
	if k.m.Levels()[0].Hot != 3 {
		t.Fatalf("↓ 该跳过灰项和分隔线到更多：%d", k.m.Levels()[0].Hot)
	}
	k.m.Key(ui.KeyRight, "", k.now)
	if len(k.m.Levels()) != 2 || k.m.Levels()[1].Hot != 0 {
		t.Fatal("→ 开子菜单、高亮第一项")
	}
	k.m.Key(ui.KeyDown, "", k.now)
	if k.m.Levels()[1].Hot != 1 {
		t.Fatal("子菜单里 ↓")
	}
	k.m.Key(ui.KeyLeft, "", k.now)
	if len(k.m.Levels()) != 1 || k.m.Levels()[0].Hot != 3 {
		t.Fatal("← 关回上一层、更多还高亮")
	}
	k.m.Key(ui.KeyUp, "", k.now)
	if k.m.Levels()[0].Hot != 0 {
		t.Fatal("↑ 跳过分隔线和灰项回到打开")
	}
	k.m.Key(ui.KeyUp, "", k.now)
	if k.m.Levels()[0].Hot != 5 {
		t.Fatal("↑ 转到最后")
	}
	k.m.Key(0, "c", k.now)
	if k.m.Levels()[0].Hot != 4 {
		t.Fatal("打 c 跳到复制")
	}
	k.m.Key(ui.KeyEnter, "", k.now)
	k.run()
	if len(k.chosen) != 1 || k.chosen[0] != "Copy" {
		t.Fatalf("回车选复制：%q", k.chosen)
	}
}

// 下面放不下往上开，右边放不下往左开；窗口都放不下才滚，滚到底最后一项整个露出来。
func TestPopMenuFlipAndScroll(t *testing.T) {
	k := newPopKit(t)
	k.m.Open("row", k.build, 990, 790, 1000, 800, false, k.now)
	r := k.m.Levels()[0].Rect
	if r.Y+r.H > 800-MenuMargin+0.5 || r.Y >= 790 || r.X+r.W > 1000-MenuMargin+0.5 || r.X >= 990 {
		t.Fatalf("该往上、往左开：%+v", r)
	}
	if r.Y != 790-r.H || r.X != 990-r.W {
		t.Fatalf("往上开时底边在指针处：%+v", r)
	}
	k.m.Open("row", k.build, 10, 40, 1000, 100, false, k.now)
	l := k.m.Levels()[0]
	if l.Rect.H > 100-2*MenuMargin || l.Full <= l.Rect.H {
		t.Fatalf("窗口放不下时该能滚：%+v", l)
	}
	k.m.Scroll(l.Rect.X+5, l.Rect.Y+5, 1000)
	l = k.m.Levels()[0]
	if !k.m.IsOpen() || l.Scroll != l.Full-l.Rect.H {
		t.Fatalf("在菜单上滚该滚它：%+v", l)
	}
}
