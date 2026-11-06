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

// 菜单开着时在别处右键：关掉这份，在右键的那一块（登记过的、Z 最大的）开一份新的，也是按着右键开的；
// 左键和别的键点外面只关、被吞掉；右键落在没登记的地方只关。
func TestPopMenuRightClickElsewhere(t *testing.T) {
	k := newPopKit(t)
	var other []string
	otherBuild := func(m MenuSink) {
		if m.Item("Other").Chosen() {
			other = append(other, "Other")
		}
	}
	// 列表（空白处，也是裁剪）0..300 × 0..600；一行 0..300 × 200..220；钉住的一行盖在 0..300 × 0..20
	frame := func() {
		k.m.AddTarget(MenuRect{0, 200, 300, 20}, 1, "row2", otherBuild)
		k.m.AddTarget(MenuRect{0, 0, 300, 600}, 0, "blank", k.build)
		k.m.AddTarget(MenuRect{0, 0, 300, 20}, 3, "pin", k.build)
		k.m.AddTarget(MenuRect{0, 590, 300, 40}, 1, "half", k.build) // 一半露在列表外面
		k.m.EndFrame()
	}
	frame()
	k.open(true)
	k.now = k.now.Add(time.Second)
	k.m.Up(100, 100, 1, k.now) // 刚开的那一下松开（太快，菜单留着）

	// 右键另一行：新菜单对着它、在指针的横坐标、那一行底下展开
	if !k.m.Down(10, 210, 1, k.now) {
		t.Fatal("右键外面也要吞掉（新菜单接着这一下的拖和松开）")
	}
	if !k.m.IsOpen() || k.m.Owner != "row2" {
		t.Fatalf("右键别的行该在那里开新菜单：open=%v owner=%v", k.m.IsOpen(), k.m.Owner)
	}
	if r := k.m.Levels()[0].Rect; r.X != 10 || r.Y != 220 {
		t.Fatalf("新菜单该开在那一行底下：%+v", r)
	}
	// 新菜单是按着右键开的：拖到项上过一会儿松开选它
	k.now = k.now.Add(300 * time.Millisecond)
	k.moveTo(0, 0)
	x, y := k.center(0, 0)
	k.m.Up(x, y, 1, k.now)
	k.run()
	if len(other) != 1 {
		t.Fatalf("新菜单按住拖到项上松开该选它：%v", other)
	}

	// Z 大的盖住小的：钉住的那行在空白处上面
	frame()
	k.open(false)
	k.m.Down(50, 10, 1, k.now)
	if k.m.Owner != "pin" {
		t.Fatalf("钉住的行该盖住底下：%v", k.m.Owner)
	}
	// 空白处
	k.m.Down(50, 400, 1, k.now)
	if k.m.Owner != "blank" {
		t.Fatalf("右键空白处该开空白处的菜单：%v", k.m.Owner)
	}
	// 露在列表外面的那半行不算
	k.m.Down(50, 620, 1, k.now)
	if k.m.IsOpen() {
		t.Fatalf("右键列表外面（没登记的地方）只关：%v", k.m.Owner)
	}

	// 左键、中键点在别的行上：只关、吞掉，不开新的
	for _, b := range []int{0, 2} {
		k.open(false)
		if !k.m.Down(10, 210, b, k.now) || k.m.IsOpen() {
			t.Fatalf("键 %d 点外面该关掉并吞掉：open=%v", b, k.m.IsOpen())
		}
	}
}

// ⇧F10 / 菜单键打开的：第一个能选的项高亮着，↓ 跳过灰的和分隔线，回车选。
func TestPopMenuOpenByKey(t *testing.T) {
	k := newPopKit(t)
	k.m.OpenByKey("row", k.build, 100, 120, 1000, 800, k.now)
	l := k.m.Levels()
	if len(l) != 1 || l[0].Hot != 0 {
		t.Fatalf("键盘开的第一项该高亮：%+v", l)
	}
	if r := l[0].Rect; r.X != 100 || r.Y != 120 {
		t.Fatalf("该开在给的位置：%+v", r)
	}
	if _, _, _, ok := k.m.Tip(k.now.Add(time.Second)); ok {
		t.Fatal("键盘挪的高亮不出提示")
	}
	k.m.Key(ui.KeyDown, "", k.now) // 跳过 Gray、分隔线
	if h := k.m.Levels()[0].Hot; h != 3 {
		t.Fatalf("↓ 该跳到 More（3）：%d", h)
	}
	k.m.Key(ui.KeyDown, "", k.now)
	k.m.Key(ui.KeyEnter, "", k.now)
	k.run()
	if len(k.chosen) != 1 || k.chosen[0] != "Copy" {
		t.Fatalf("回车该选 Copy：%v", k.chosen)
	}
	// 菜单关了，登记的地方还在
	k.m.AddTarget(MenuRect{0, 0, 10, 10}, 0, "blank", k.build)
	k.m.EndFrame()
	k.m.Close()
	if _, ok := k.m.TargetAt(5, 5); !ok {
		t.Fatal("关菜单不该清掉登记的地方")
	}
}

// 右键一行（OpenRow）：菜单在指针的横坐标、这一行底下展开，不盖住它；靠窗口底下放不下时翻到这一行上面。
// 太高的（列表空白处）和指针不在行里的照旧按指针处开。
func TestPopMenuOpenRow(t *testing.T) {
	k := newPopKit(t)
	row := MenuRect{X: 0, Y: 90, W: 300, H: 24}
	k.m.OpenRow("row", k.build, 100, 100, row, 1000, 800, true, k.now)
	r := k.m.Levels()[0].Rect
	if r.X != 100 || r.Y != row.Y+row.H {
		t.Fatalf("该在行底下（y=%v）展开：%+v", row.Y+row.H, r)
	}
	low := MenuRect{X: 0, Y: 760, W: 300, H: 24}
	k.m.OpenRow("row", k.build, 100, 770, low, 1000, 800, true, k.now)
	r = k.m.Levels()[0].Rect
	if r.Y+r.H != low.Y {
		t.Fatalf("靠底下该翻到行上面（底边 %v）：%+v", low.Y, r)
	}
	k.m.OpenRow("blank", k.build, 100, 100, MenuRect{Y: 0, W: 300, H: 600}, 1000, 800, true, k.now)
	if r := k.m.Levels()[0].Rect; r.Y != 100 {
		t.Fatalf("空白处按指针处开：%+v", r)
	}
	// 菜单开着时右键另一行（登记的目标）：新菜单也在那一行底下
	k.m.AddTarget(MenuRect{X: 0, Y: 300, W: 300, H: 24}, 1, "other", k.build)
	k.m.EndFrame()
	k.m.Down(50, 310, 1, k.now)
	if r := k.m.Levels()[0].Rect; !k.m.IsOpen() || r.Y != 324 {
		t.Fatalf("右键别的行：新菜单该在它底下（324）：%+v", r)
	}
}
