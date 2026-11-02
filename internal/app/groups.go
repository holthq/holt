package app

import (
	"cmp"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是编辑器组（分屏）：主区能左右 / 上下分成几组、可以嵌套，每组自己一条标签条。
// 有键盘的那组（Grp）的标签就放在 a.Docs / a.DocSel 里，"当前标签"的地方照旧走它们；
// 别的组的标签放在组自己那里，键盘换组时两边对调（swapTo）。一个标签同时只在一组里：
// 拖标签、⌘\ 都是搬过去，不复制。组里最后一个标签关掉（或搬走），这组就收起，兄弟占满。

// Group 是一个编辑器组。
type Group struct {
	docs []*docs.Doc // 没有键盘时的标签；有键盘时它们在 a.Docs
	sel  int         // 没有键盘时的当前标签

	TabStrip ui.ScrollState // 标签条的横向滚动（界面按它画底边的细滚动条）
	TabShown *docs.Doc      // 标签条上一次滚进视图的当前标签：当前标签换了就再滚一次
	W, H     float32        // 界面上一帧排出来的大小，分屏前看分不分得开（0 = 没排过，算分得开）
	HadFocus bool           // 上一帧键盘在不在这组里：键盘进来的那一帧把它当成有键盘的组
}

// EdNode 是编辑器组的拆分树：叶子是一组（G），否则是左右（Row）或上下分的两半，A 占 Ratio。
type EdNode struct {
	G     *Group
	Row   bool
	Ratio float32
	A, B  *EdNode
}

// Side 是拖着的标签在一组的哪一块：中间是搬进这组，四边是在那一侧分出新的一组。
type Side int

const (
	SideNone Side = iota
	SideCenter
	SideLeft
	SideRight
	SideTop
	SideBottom
)

// 分出来的一组最少多大（pt）：一组不到两倍宽 / 高就不让左右 / 上下分。
const (
	MinGroupW = 160
	MinGroupH = 120
)

// DropSide 是指针在 w×h 的一组里 (x, y) 处时落在哪一块：离哪条边不到 1/4 就是那边（两边都近取更近的），
// 否则是中间；在外面是 SideNone。
func DropSide(x, y, w, h float32) Side {
	if w <= 0 || h <= 0 || x < 0 || y < 0 || x > w || y > h {
		return SideNone
	}
	fx, fy := x/w, y/h
	best, near := SideCenter, float32(0.25)
	for _, e := range [...]struct {
		s Side
		d float32
	}{{SideLeft, fx}, {SideRight, 1 - fx}, {SideTop, fy}, {SideBottom, 1 - fy}} {
		if e.d < near {
			best, near = e.s, e.d
		}
	}
	return best
}

// DropRect 是落点预览盖住的那一块（相对组的左上角）：中间盖整组，四边盖新组会占的那一半。
func DropRect(s Side, w, h float32) (x, y, rw, rh float32) {
	switch s {
	case SideLeft:
		return 0, 0, w / 2, h
	case SideRight:
		return w / 2, 0, w / 2, h
	case SideTop:
		return 0, 0, w, h / 2
	case SideBottom:
		return 0, h / 2, w, h / 2
	}
	return 0, 0, w, h
}

// Editors 是编辑器组的拆分树的根；只有一组时是那一组的叶子。
func (a *App) Editors() *EdNode {
	if a.Ed == nil {
		a.Grp = &Group{sel: -1}
		a.Ed = &EdNode{G: a.Grp}
	}
	return a.Ed
}

// Split 是不是分了屏（不止一组）。
func (a *App) Split() bool { return a.Editors().G == nil }

// Groups 是所有组，从左到右、从上到下。
func (a *App) Groups() []*Group {
	var out []*Group
	var walk func(n *EdNode)
	walk = func(n *EdNode) {
		if n.G != nil {
			out = append(out, n.G)
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(a.Editors())
	return out
}

// GroupDocs 是组 g 的标签和它的当前标签（下标，-1 为没有）。
func (a *App) GroupDocs(g *Group) ([]*docs.Doc, int) {
	if g == a.Grp {
		return a.Docs, a.DocSel
	}
	return g.docs, g.sel
}

func (a *App) setGroupDocs(g *Group, ds []*docs.Doc, sel int) {
	if g == a.Grp {
		a.Docs, a.DocSel = ds, sel
		return
	}
	g.docs, g.sel = ds, sel
}

// AllDocs 是所有组的标签，按组的顺序；只有一组时就是 a.Docs（要边走边关的拷一份再走）。
func (a *App) AllDocs() []*docs.Doc {
	if !a.Split() {
		return a.Docs
	}
	var out []*docs.Doc
	for _, g := range a.Groups() {
		ds, _ := a.GroupDocs(g)
		out = append(out, ds...)
	}
	return out
}

// GroupOf 是标签 d 所在的组；不在任何组里为 nil。
func (a *App) GroupOf(d *docs.Doc) *Group {
	for _, g := range a.Groups() {
		if ds, _ := a.GroupDocs(g); slices.Contains(ds, d) {
			return g
		}
	}
	return nil
}

// locate 是 k 那个标签在哪一组的第几个；没开着为 nil, -1。
func (a *App) locate(k docs.Key) (*Group, int) {
	for _, g := range a.Groups() {
		ds, _ := a.GroupDocs(g)
		if i := slices.IndexFunc(ds, func(d *docs.Doc) bool { return d.Key() == k }); i >= 0 {
			return g, i
		}
	}
	return nil, -1
}

// swapTo 把键盘换到组 g：现在这组的标签收回它自己那里，g 的放进 a.Docs / a.DocSel。
func (a *App) swapTo(g *Group) {
	a.Editors()
	if g == a.Grp {
		return
	}
	if d := a.CurrentDoc(); d != nil {
		d.Rest.Woke = true // 歇着的编辑器每帧把键盘拿回去：叫醒它，键盘才换得到别的组
	}
	a.Grp.docs, a.Grp.sel = a.Docs, a.DocSel
	a.Docs, a.DocSel = g.docs, g.sel
	g.docs, g.sel = nil, 0
	a.Grp = g
}

// FocusGroup 把键盘给组 g（点了它的标签或里面的编辑器）：‹ › 记一步，Git 栏的选中跟着它的当前标签。
func (a *App) FocusGroup(g *Group) {
	if g == a.Grp || a.Editors().leaf(g) == nil {
		return
	}
	a.markNav()
	a.swapTo(g)
	if d := a.CurrentDoc(); d != nil {
		a.visit(d.Key())
	}
	a.syncChangeSel()
}

// toGroup 是程序里换到组 g（‹ ›、打开已经开在别组的文件）：换了组就让那组的编辑器拿键盘。
func (a *App) toGroup(g *Group) {
	if g != a.Grp {
		a.swapTo(g)
		a.FocusDoc = true
	}
}

// SelectIn 切到组 g 的第 i 个标签（点标签）：别的组的就把键盘也给那组的编辑器。
func (a *App) SelectIn(g *Group, i int) {
	if g != a.Grp {
		a.FocusGroup(g)
		a.FocusDoc = true
	}
	a.SelectDoc(i)
}

// takeOut 把 d 从组 g 里拿掉（组空了也不收）：关的是当前标签就换到它后面那个，最后一个就换到前面那个。
func (a *App) takeOut(g *Group, d *docs.Doc) {
	ds, sel := a.GroupDocs(g)
	i := slices.Index(ds, d)
	if i < 0 {
		return
	}
	ds = slices.Delete(ds, i, i+1)
	if i < sel || sel >= len(ds) {
		sel--
	}
	a.setGroupDocs(g, ds, sel)
}

// collapse 收起空了的组 g：兄弟占满它的位置；键盘在 g 上就给占过来那一半的第一组。只剩一组时不收。
func (a *App) collapse(g *Group) {
	s := a.Editors().cut(g)
	if s == nil {
		return
	}
	if g == a.Grp {
		a.swapTo(s.first())
		a.FocusDoc = true
	}
	a.SaveState()
}

// MoveDocTo 把标签 d 搬到组 g 的第 at 个（at < 0 放最后）：键盘跟过去，d 成了那组的当前标签、固定下来
// （别和那组的预览标签抢位置）；原来那组空了就收起。
func (a *App) MoveDocTo(d *docs.Doc, g *Group, at int) {
	from := a.GroupOf(d)
	if from == nil || a.Editors().leaf(g) == nil {
		return
	}
	a.markNav()
	a.takeOut(from, d)
	a.swapTo(g)
	if at < 0 || at > len(a.Docs) {
		at = len(a.Docs)
	}
	a.Docs = slices.Insert(a.Docs, at, d)
	a.DocSel = at
	d.Pinned = true
	if from != g {
		if ds, _ := a.GroupDocs(from); len(ds) == 0 {
			a.collapse(from)
		}
	}
	a.FocusDoc = true
	a.visit(d.Key())
	a.syncChangeSel()
	a.SaveState()
}

// DropOnTab 是把标签 v 拖到标签 over 上松开：同一组里换到 over 的位置（当前标签不变），
// 别的组的就搬过来插在 over 前面。
func (a *App) DropOnTab(v, over *docs.Doc) {
	g, from := a.GroupOf(over), a.GroupOf(v)
	if g == nil || from == nil || v == over {
		return
	}
	ds, sel := a.GroupDocs(g)
	if from != g {
		a.MoveDocTo(v, g, slices.Index(ds, over))
		return
	}
	var cur *docs.Doc
	if sel >= 0 {
		cur = ds[sel]
	}
	i, j := slices.Index(ds, v), slices.Index(ds, over)
	ds = slices.Insert(slices.Delete(ds, i, i+1), j, v)
	a.setGroupDocs(g, ds, slices.Index(ds, cur))
	a.SaveState()
}

// roomFor 是这组分不分得开：左右分要两倍 MinGroupW 宽，上下分要两倍 MinGroupH 高。
func (g *Group) roomFor(row bool) bool {
	if row {
		return g.W == 0 || g.W >= 2*MinGroupW
	}
	return g.H == 0 || g.H >= 2*MinGroupH
}

// CanDrop 是把标签 d 拖到组 g 的 s 那一块松开动不动（界面按它画不画落点预览）：拖回自己那组中间不动；
// 自己那组只有它一个时不分；组太小不分。
func (a *App) CanDrop(d *docs.Doc, g *Group, s Side) bool {
	from := a.GroupOf(d)
	switch {
	case from == nil || g == nil || s == SideNone:
		return false
	case s == SideCenter:
		return from != g
	}
	if ds, _ := a.GroupDocs(g); from == g && len(ds) == 1 {
		return false
	}
	return g.roomFor(s == SideLeft || s == SideRight)
}

// DropDoc 是标签 d 在组 g 的 s 那一块松开：中间搬进这组当最后一个标签，四边在那一侧分出只有它的新组。
func (a *App) DropDoc(d *docs.Doc, g *Group, s Side) {
	if !a.CanDrop(d, g, s) {
		return
	}
	if s == SideCenter {
		a.MoveDocTo(d, g, -1)
		return
	}
	a.splitOff(d, g, s)
}

// splitOff 把组 g 的叶子分成两半，新的一半在 s 那一侧，把 d 搬进去。
func (a *App) splitOff(d *docs.Doc, g *Group, s Side) {
	n := a.Editors().leaf(g)
	old, nn := *n, &EdNode{G: &Group{sel: -1}}
	*n = EdNode{Row: s == SideLeft || s == SideRight, Ratio: 0.5, A: &old, B: nn}
	if s == SideLeft || s == SideTop {
		n.A, n.B = nn, &old
	}
	a.MoveDocTo(d, nn.G, -1)
}

// SplitEditor 是 ⌘\ / ⌥⌘\：当前标签搬到右边 / 下面新分出来的一组。没有标签不动；这组只有它一个、
// 或者再分就太小了，提示一句不动。
func (a *App) SplitEditor(row bool) {
	d := a.CurrentDoc()
	switch {
	case d == nil:
	case len(a.Docs) < 2:
		a.Toast = i18n.T("这一组只有一个标签，分不出新的一组")
	case !a.Grp.roomFor(row):
		a.Toast = i18n.T("这一组太小，分不开了")
	case row:
		a.splitOff(d, a.Grp, SideRight)
	default:
		a.splitOff(d, a.Grp, SideBottom)
	}
}

// CanSplit 是 d 能不能往 s 那边分出一组（标签的"拆分"子菜单灰不灰）：和拖到自己那组的那一边一样，
// 那组得还有别的标签，那个方向放得下两组。
func (a *App) CanSplit(d *docs.Doc, s Side) bool {
	return s >= SideLeft && a.CanDrop(d, a.GroupOf(d), s)
}

// SplitDoc 把 d 搬进它那组 s 边新分出的一组，键盘跟过去（"拆分"子菜单）；分不了就不动。
func (a *App) SplitDoc(d *docs.Doc, s Side) {
	if a.CanSplit(d, s) {
		a.splitOff(d, a.GroupOf(d), s)
	}
}

// sideOf 是"在侧边打开"去的那组：g 在 Groups() 顺序里的下一组；g 是最后一组就是它自己（已经在侧边了，
// 接着开进来，不再一路往右分）；只有一组时是 nil（要向右分出一组）。
func (a *App) sideOf(g *Group) *Group {
	gs := a.Groups()
	switch i := slices.Index(gs, g); {
	case len(gs) < 2 || i < 0:
		return nil
	case i+1 < len(gs):
		return gs[i+1]
	}
	return g
}

// OpenFileSide 是"在侧边打开"（文件树右键、⌥回车、⌥点）：rel 开成固定标签，进侧边的组（sideOf：键盘所在组的
// 下一组；它是最后一组就是它自己），只有一组时向右分出一组（太小就提示、开在这组），键盘跟过去；一个标签都没有时就是打开。
// 已经开在别的组（或侧边的组）的只是跳过去；开在这组的搬过去，这组只有它一个就只提示。
func (a *App) OpenFileSide(r *Repo, rel string) *docs.Doc {
	if len(a.AllDocs()) == 0 {
		return a.OpenFile(r, rel, true)
	}
	d, _ := a.openSide(r, rel, a.Grp, a.sideOf(a.Grp))
	return d
}

// openSide 把 rel 开进 from 旁边的 side 组（nil 就从 from 向右分出一组），返回文件和之后的侧边组。
func (a *App) openSide(r *Repo, rel string, from, side *Group) (*docs.Doc, *Group) {
	g, i := a.locate(docs.NewFile(r.Root, rel).Key())
	switch {
	case g != nil && (g != from || g == side):
		return a.OpenFile(r, rel, true), side
	case g == from:
		if ds, _ := a.GroupDocs(from); len(ds) < 2 {
			a.Toast = i18n.T("这一组只有一个标签，分不出新的一组")
			return ds[i], side
		}
	}
	if side == nil && !from.roomFor(true) {
		a.Toast = i18n.T("这一组太小，分不开了")
		return a.OpenFile(r, rel, true), nil
	}
	d := a.OpenFile(r, rel, true)
	if side == nil {
		a.splitOff(d, from, SideRight)
		return d, a.GroupOf(d)
	}
	if a.GroupOf(d) != side {
		a.MoveDocTo(d, side, -1)
	}
	a.FocusDoc = true
	return d, side
}

// MergeGroups 把所有组合成一组（视图菜单"合并所有编辑器组"）：标签按组的顺序接起来，当前标签不变。
func (a *App) MergeGroups() {
	if !a.Split() {
		return
	}
	cur, all := a.CurrentDoc(), a.AllDocs()
	for _, g := range a.Groups() {
		g.docs = nil
	}
	a.Ed = &EdNode{G: a.Grp}
	a.Docs, a.DocSel = all, slices.Index(all, cur)
	a.SaveState()
}

// leaf 是组 g 所在的叶子，没有为 nil。
func (n *EdNode) leaf(g *Group) *EdNode {
	if n.G != nil {
		if n.G == g {
			return n
		}
		return nil
	}
	if l := n.A.leaf(g); l != nil {
		return l
	}
	return n.B.leaf(g)
}

// cut 把组 g 从树里拿掉，另一半占满两半的位置，返回占过来的那一半；g 是唯一的一组时返回 nil。
func (n *EdNode) cut(g *Group) *EdNode {
	if n.G != nil {
		return nil
	}
	for _, kid := range [2]struct{ me, other *EdNode }{{n.A, n.B}, {n.B, n.A}} {
		if kid.me.G == g {
			*n = *kid.other
			return n
		}
	}
	if s := n.A.cut(g); s != nil {
		return s
	}
	return n.B.cut(g)
}

// first 是这一半里第一组。
func (n *EdNode) first() *Group {
	for n.G == nil {
		n = n.A
	}
	return n.G
}

// layout 是存进会话的拆分树：叶子记这组的文件标签在会话 Tabs 里的下标（idx 里查，不记的没有）。只有一组时为 nil。
func (a *App) layout(idx map[*docs.Doc]int) *workspace.Layout {
	if !a.Split() {
		return nil
	}
	var walk func(n *EdNode) *workspace.Layout
	walk = func(n *EdNode) *workspace.Layout {
		if n.G == nil {
			return &workspace.Layout{Row: n.Row, Ratio: n.Ratio, A: walk(n.A), B: walk(n.B)}
		}
		l := &workspace.Layout{Focus: n.G == a.Grp}
		ds, sel := a.GroupDocs(n.G)
		for i, d := range ds {
			if t, ok := idx[d]; ok {
				if i == sel {
					l.Sel = len(l.Tabs)
				}
				l.Tabs = append(l.Tabs, t)
			}
		}
		return l
	}
	return walk(a.Editors())
}

// restoreLayout 按会话里的拆分树把重开的标签（opened[i] 是会话 Tabs[i] 重开的，没开成为 nil）分回各组：
// 一个标签只进第一个记着它的组，没有标签的组不要，树里没记的标签放进有键盘的组。分出来不到两组就照旧一组。
func (a *App) restoreLayout(l *workspace.Layout, opened []*docs.Doc) {
	if l == nil || l.A == nil && l.B == nil {
		return
	}
	used := map[*docs.Doc]bool{}
	var focus *Group
	var build func(l *workspace.Layout) *EdNode
	build = func(l *workspace.Layout) *EdNode {
		if l == nil {
			return nil
		}
		if l.A != nil || l.B != nil {
			x, y := build(l.A), build(l.B)
			if x == nil || y == nil {
				return cmp.Or(x, y)
			}
			r := l.Ratio
			if r <= 0 || r >= 1 {
				r = 0.5
			}
			return &EdNode{Row: l.Row, Ratio: min(max(r, 0.12), 0.88), A: x, B: y}
		}
		g := &Group{sel: -1}
		for k, i := range l.Tabs {
			if i < 0 || i >= len(opened) || opened[i] == nil || used[opened[i]] {
				continue
			}
			used[opened[i]] = true
			if k == l.Sel {
				g.sel = len(g.docs)
			}
			g.docs = append(g.docs, opened[i])
		}
		if len(g.docs) == 0 {
			return nil
		}
		g.sel = max(g.sel, 0)
		if l.Focus || focus == nil {
			focus = g
		}
		return &EdNode{G: g}
	}
	root := build(l)
	if root == nil || root.G != nil {
		return
	}
	for _, d := range a.Docs {
		if !used[d] {
			focus.docs = append(focus.docs, d)
		}
	}
	a.Ed, a.Grp = root, focus
	a.Docs, a.DocSel = focus.docs, focus.sel
	focus.docs, focus.sel = nil, 0
}
