// Package files 是文件树模型：用只读的 git ls-files 列出仓库里的文件（已跟踪的，
// 加上没被 .gitignore 忽略的未跟踪文件），按目录组成一棵树；目录可以展开收起，
// 也可以按名字筛选。只在主线程上用，读文件列表（git.LsFiles）由调用方放到后台。
package files

import (
	"bytes"
	"context"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/fuzzy"
	"github.com/lemoeade/grove/internal/git"
)

// FilterCap 是筛选结果最多显示的行数。
const FilterCap = 500

// WalkCap 是普通文件夹（不是 Git 仓库）最多列出的文件数，再多的不列。
const WalkCap = 50000

// Node 是树里的一个目录。
type Node struct {
	Name  string
	Path  string   // 仓库内路径，根为空
	Dirs  []*Node  // 子目录，按名字排序
	Files []string // 直接子文件的名字，按名字排序
	// Ignored 是整个被 .gitignore 忽略的目录（"显示被忽略的文件"开着时）：里面的不在树里，
	// 展开时一层一层读磁盘（Tree.ReadLazy）。
	Ignored bool

	paths []string // Files 的完整路径（仓库内），筛选用；第一次筛选时算，Files 变了清掉（见 filePaths）
}

// Tree 是一个仓库的文件树和它的展开状态。
type Tree struct {
	Root    *Node    // 整棵树；nil 为还没读到
	Paths   []string // 所有文件（ls-files 的顺序）；快速打开和全文搜索用，只整个换掉、不改里面的
	Count   int      // 文件数
	Loaded  bool
	Loading bool
	Err     error
	Gen     int // 每次重读递增，旧的结果作废

	Open    map[string]bool // 展开着的目录（仓库内路径）
	Version int             // 树或展开状态变了就加一，调用方据此重算行

	// Extra 是应用里新建、git ls-files 列不出来的：空目录（值为 true）和被 .gitignore 忽略的文件；
	// 文件不多的仓库里后台找到的空目录也在这里（SetEmpty、AddEmpty）。
	// 每次读文件列表时（WithExtra）磁盘上还在的并进树里，不在了的丢掉；不保存。
	Extra map[string]bool
	// found 是 Extra 里后台找到的那些空目录：再找一次（SetEmpty）时不空了、被忽略了的从 Extra 和树里去掉；
	// 应用里新建的不在这里（只在磁盘上没了时去掉）。
	found map[string]bool

	// Ignored 是"显示被忽略的文件"开着时并进树里的被忽略的文件和整个被忽略的目录（仓库内路径 → 是不是目录）。
	// 它们不在 Paths 里、不算进 Count，快速打开和全文搜索都不含；每次 Done 换掉。
	Ignored map[string]bool
	// Lazy 是展开过的被忽略目录、指向目录的符号链接里的一层（ReadLazy 读的，目录在前、名字后面带 /，按名字排），
	// 最多 FilterCap 项，lazyMore 是那个目录还有几项没列，lazyLinks 是这些层里读到的符号链接（同 Links）。
	// 每次 Done 清掉，收起时清掉它和它下面的，展开着的下次画行时重读。
	Lazy      map[string][]string
	lazyMore  map[string]int
	lazyLinks map[string]Link

	// Subs 是父仓库里的子模块（仓库内路径 → git submodule status 的第一位，见 git.Submodule.State）：
	// 树里是一行文件（ls-files 列的是 gitlink），不展开。SetSubs 换掉。
	Subs map[string]byte

	// Links 是树里的符号链接（仓库内路径 → 目标）：已跟踪的看索引里的 mode，未跟踪的和增量刷新新加的才看磁盘
	// （ReadLinks），不对整棵树逐个看。在树里是文件；指向目录的（Link.Expands）画成目录行，展开时跟着链接一层层读
	// （同被忽略的目录，见 ReadLazy），里面的行 Linked。每次 Done 清掉（调用方换上读到的），Apply、Moved、Removed 跟着加删。
	Links map[string]Link
}

// MaxLinkDepth 是一串指向目录的链接最多穿过几层：路径上已经有这么多层链接目录时，再遇到的链接目录不展开（Link.Deep）。
const MaxLinkDepth = 8

// Link 是一个符号链接：Target 是链接里写的（os.Readlink，相对的就是相对的），Broken 是它指向的不存在，
// Dir 是指向一个目录。Up 是指向的真目录就是链接所在的目录或在它上面（展开就绕回自己），Deep 是穿过的链接目录
// 已经有 MaxLinkDepth 层了：这两种不展开，同断了的画成文件行。
type Link struct {
	Target      string
	Broken, Dir bool
	Up, Deep    bool
}

// Expands 是 l 画成可以展开的目录行：指向目录、没断、不绕回来、不太深。
func (l Link) Expands() bool { return l.Dir && !l.Broken && !l.Up && !l.Deep }

// ReadLinks 看 cands（仓库内路径）里哪些是符号链接：每个 readlink 一次（不是链接的报错跳过），是的再 stat 一次看断没断、
// 是不是目录（目录再看绕不绕回来，见 loops）。只读磁盘，在后台调；root 是仓库根的磁盘路径。一个都不是为 nil。
func ReadLinks(root string, cands []string) map[string]Link {
	var links map[string]Link
	for _, p := range cands {
		l, ok := readLink(filepath.Join(root, filepath.FromSlash(p)))
		if !ok {
			continue
		}
		if links == nil {
			links = map[string]Link{}
		}
		links[p] = l
	}
	return links
}

// readLink 读磁盘路径 abs 的符号链接，不是链接为 false。
func readLink(abs string) (Link, bool) {
	target, err := os.Readlink(abs)
	if err != nil {
		return Link{}, false
	}
	l := Link{Target: target}
	fi, err := os.Stat(abs)
	l.Broken = err != nil
	if err == nil && fi.IsDir() {
		l.Dir, l.Up = true, loops(abs)
	}
	return l, true
}

// loops 是指向目录的链接 abs 解析出来的真目录就是链接所在的目录（解析过的），或者包含它：展开就绕回自己
// （internal/self -> .、a/up -> ..）。解析不了的也算，不展开。
func loops(abs string) bool {
	to, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return true
	}
	at, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return true
	}
	sep := string(filepath.Separator)
	return at == to || strings.HasPrefix(at+sep, strings.TrimSuffix(to, sep)+sep)
}

// Link 是 p 的符号链接：树里的（Links），或展开了的被忽略目录、链接目录里读到的。
func (t *Tree) Link(p string) (Link, bool) {
	if l, ok := t.Links[p]; ok {
		return l, true
	}
	l, ok := t.lazyLinks[p]
	return l, ok
}

// DirLink 是 p 是画成目录行、可以展开的符号链接（Link.Expands）。
func (t *Tree) DirLink(p string) bool {
	l, ok := t.Link(p)
	return ok && l.Expands()
}

// throughLink 是 p 自己或它上面的哪一级是可以展开的链接目录：p 是穿过链接的路径，或者就是这样的链接。
func (t *Tree) throughLink(p string) bool {
	if len(t.Links) == 0 {
		return false
	}
	for q := p; q != ""; q = DirOf(q) {
		if t.DirLink(q) {
			return true
		}
	}
	return false
}

// InLink 是 p 在指向目录的符号链接里面（路径穿过链接，Row.Linked）。
func (t *Tree) InLink(p string) bool { return p != "" && t.throughLink(DirOf(p)) }

// OutsideLinks 是往 p 那里新建、粘贴时真放进去的目录：p 是链接目录或在它里面时是最外面那个链接所在的目录
// （不往链接指的地方写），否则就是 p。
func (t *Tree) OutsideLinks(p string) string {
	if !t.throughLink(p) {
		return p
	}
	parts := strings.Split(p, "/")
	for i := range parts {
		if q := strings.Join(parts[:i+1], "/"); t.DirLink(q) {
			return DirOf(q)
		}
	}
	return p
}

// linkDepth 是 p 上面（不算 p）有几层链接目录。
func (t *Tree) linkDepth(p string) int {
	n := 0
	for q := DirOf(p); q != ""; q = DirOf(q) {
		if t.DirLink(q) {
			n++
		}
	}
	return n
}

// Row 是文件树的一行：一个目录或一个文件。
type Row struct {
	Path  string // 仓库内路径
	Name  string
	Depth int
	Dir   bool
	Open  bool // 目录：展开着
	Code  byte // 文件的 git 状态字母（M / A / ? …），0 为没改
	Mark  bool // 目录：里面有改动的文件

	Ignored bool // 被 .gitignore 忽略的（灰显）
	Linked  bool // 在指向目录的符号链接里面（路径穿过链接）：没有 git 状态，只能打开、复制路径、在访达中显示
	More    int  // 不是文件：被忽略的目录、链接目录太大，还有这么多项没列（这一行是说明）
	Sub     byte // 子模块：git submodule status 的第一位（' ' 正常、'-' 未初始化…），0 为不是子模块

	// Chain 是合并行（CompactRows）的那串目录，从上到下、最后一个就是 Path；不是合并行为 nil。
	// 只有一个子目录、没有文件的目录和它的子目录合成一行，显示成 "internal / ui"。
	Chain []string

	// Hits 是筛选的行（Match）里命中的字在 Path 里的字节下标（升序），别的行为 nil。
	Hits []int
}

// Label 是这一行显示的名字：合并行是各段用 " / " 连起来（"internal / ui"）。
func (r Row) Label() string {
	if len(r.Chain) == 0 {
		return r.Name
	}
	names := make([]string, len(r.Chain))
	for i, p := range r.Chain {
		names[i] = BaseName(p)
	}
	return strings.Join(names, " / ")
}

// FindLabel 是键盘找字（列表里直接打字跳到开头对得上的行）比的名字：Label 每段去掉开头的点
// （.gitignore 打 git 就到）。
func (r Row) FindLabel() string {
	if len(r.Chain) == 0 {
		return strings.TrimLeft(r.Name, ".")
	}
	names := make([]string, len(r.Chain))
	for i, p := range r.Chain {
		names[i] = strings.TrimLeft(BaseName(p), ".")
	}
	return strings.Join(names, " / ")
}

// Holds 是这一行就是目录 p，或 p 是合并行里的一段。
func (r Row) Holds(p string) bool { return r.Path == p || slices.Contains(r.Chain, p) }

// Start 开始一次重读，返回这次的编号；Done 时编号对不上的结果丢掉。
func (t *Tree) Start() int {
	t.Gen++
	t.Loading = true
	return t.Gen
}

// Done 放进读到的文件列表（dirs 是另外要有的空目录，见 WithExtra；ignored 是 git.LsIgnored 列的被忽略的，
// 目录名后面带 /，不显示被忽略的文件时为 nil）；gen 不是最近一次 Start 的就什么都不做并返回 false。
func (t *Tree) Done(gen int, paths []string, err error, ignored []string, dirs ...string) bool {
	if gen != t.Gen {
		return false
	}
	t.Loading, t.Loaded, t.Err = false, true, err
	t.Ignored, t.Lazy, t.lazyMore, t.lazyLinks, t.Links = nil, nil, nil, nil, nil
	if err != nil {
		t.Root, t.Paths, t.Count = nil, nil, 0
		t.Version++
		return true
	}
	all, empty := paths, dirs
	var igDirs []string
	if len(ignored) > 0 {
		listed := make(map[string]bool, len(paths))
		for _, p := range paths {
			listed[p] = true
		}
		t.Ignored = map[string]bool{}
		all = slices.Clone(paths)
		for _, p := range ignored {
			if d, ok := strings.CutSuffix(p, "/"); ok {
				if _, seen := t.Ignored[d]; d != "" && !seen {
					t.Ignored[d] = true
					igDirs = append(igDirs, d)
				}
			} else if _, seen := t.Ignored[p]; p != "" && !listed[p] && !seen {
				t.Ignored[p] = false
				all = append(all, p)
			}
		}
		empty = append(slices.Clone(dirs), igDirs...)
	}
	t.Root, t.Paths, t.Count = Build(all, empty...), paths, len(paths)
	for _, d := range igDirs {
		if n := t.Dir(d); n != nil && len(n.Dirs)+len(n.Files) == 0 {
			n.Ignored = true
		}
	}
	t.Version++
	return true
}

// lazyDir 是 p 是不是被忽略的目录、可以展开的链接目录或在它们里面（里面的东西靠 ReadLazy 读）。
func (t *Tree) lazyDir(p string) bool {
	if len(t.Ignored)+len(t.Links) == 0 {
		return false
	}
	for q := p; q != ""; q = DirOf(q) {
		if t.DirLink(q) {
			return true
		}
		if t.Ignored[q] { // 被忽略的目录（文件是 false）
			n := t.Dir(q)
			return n != nil && n.Ignored
		}
	}
	return false
}

// ReadLazy 读展开着、还没读过的被忽略目录和链接目录（一层，os.ReadDir；root 是仓库根的磁盘路径），返回读了没有。
// 一层目录读得很快，在主线程上画行之前调；最多列 FilterCap 项，再多的记个数。从上往下读，里面那层要等外面那层
// 读过、列着它才读（收起着的、上次留下的展开状态不读）。读到的符号链接记进 lazyLinks（ReadLinks 那样看一遍，
// 穿过的链接目录已经有 MaxLinkDepth 层的标 Deep），可以展开的当目录列。
func (t *Tree) ReadLazy(root string) bool {
	if len(t.Ignored)+len(t.Links) == 0 {
		return false
	}
	var todo []string
	for p, on := range t.Open {
		if _, done := t.Lazy[p]; !done && on && t.lazyDir(p) {
			todo = append(todo, p)
		}
	}
	if len(todo) == 0 {
		return false
	}
	slices.Sort(todo) // 外面的在里面的前面
	read := false
	for _, p := range todo {
		if up := DirOf(p); t.lazyDir(up) && !slices.Contains(t.Lazy[up], BaseName(p)+"/") {
			continue
		}
		ents, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(p)))
		var ds, fs []string
		for _, e := range ents {
			switch {
			case e.Name() == ".DS_Store":
			case e.IsDir():
				ds = append(ds, e.Name())
			case e.Type()&os.ModeSymlink != 0:
				q := JoinPath(p, e.Name())
				l, ok := readLink(filepath.Join(root, filepath.FromSlash(q)))
				if !ok {
					fs = append(fs, e.Name())
					break
				}
				l.Deep = l.Dir && t.linkDepth(q) >= MaxLinkDepth
				if t.lazyLinks == nil {
					t.lazyLinks = map[string]Link{}
				}
				t.lazyLinks[q] = l
				if l.Expands() {
					ds = append(ds, e.Name())
				} else {
					fs = append(fs, e.Name())
				}
			default:
				fs = append(fs, e.Name())
			}
		}
		slices.SortFunc(ds, LessName)
		slices.SortFunc(fs, LessName)
		list := make([]string, 0, min(len(ds)+len(fs), FilterCap))
		for _, d := range ds {
			list = append(list, d+"/")
		}
		list = append(list, fs...)
		if t.Lazy == nil {
			t.Lazy, t.lazyMore = map[string][]string{}, map[string]int{}
		}
		if len(list) > FilterCap {
			t.lazyMore[p] = len(list) - FilterCap
			list = list[:FilterCap]
		}
		t.Lazy[p] = list
		read = true
	}
	if read {
		t.Version++
	}
	return read
}

// DirOf 是仓库内路径的目录部分，顶层为空。
func DirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// JoinPath 拼仓库内路径。
func JoinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// LessName 按名字排序：先不分大小写，再分，和 Finder 差不多。
func LessName(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// Build 把路径列表组成一棵树；empty 是另外要有的（空）目录。
func Build(paths []string, empty ...string) *Node {
	root := &Node{}
	dirs := map[string]*Node{"": root}
	var get func(p string) *Node
	get = func(p string) *Node {
		if n, ok := dirs[p]; ok {
			return n
		}
		parent := get(DirOf(p))
		n := &Node{Name: p[strings.LastIndexByte(p, '/')+1:], Path: p}
		parent.Dirs = append(parent.Dirs, n)
		dirs[p] = n
		return n
	}
	for _, p := range paths {
		n := get(DirOf(p))
		n.Files = append(n.Files, p[strings.LastIndexByte(p, '/')+1:])
	}
	for _, d := range empty {
		get(d)
	}
	for _, n := range dirs {
		slices.SortFunc(n.Dirs, func(a, b *Node) int { return LessName(a.Name, b.Name) })
		slices.SortFunc(n.Files, LessName)
	}
	return root
}

// Marks 是 git 状态里每个文件的状态字母，和所有含改动文件的目录。
func Marks(changes []git.Change) (codes map[string]byte, marks map[string]bool) {
	codes = make(map[string]byte, len(changes))
	marks = make(map[string]bool)
	for _, ch := range changes {
		if _, ok := codes[ch.Path]; !ok {
			codes[ch.Path] = ch.Code
		}
		for d := DirOf(ch.Path); d != ""; d = DirOf(d) {
			if marks[d] {
				break
			}
			marks[d] = true
		}
	}
	return codes, marks
}

// Rows 把树按展开状态摊平追加到 rows 后面，最外层的深度是 depth。被忽略的目录里是 ReadLazy 读到的那层。
func (t *Tree) Rows(depth int, codes map[string]byte, marks map[string]bool, rows []Row) []Row {
	if t.Root == nil {
		return rows
	}
	return t.treeRows(t.Root, depth, &rowOpts{codes: codes, marks: marks}, rows)
}

// CompactRows 和 Rows 一样，但只有一个子目录、没有文件的目录和它的子目录合成一行（Row.Chain），一直合到有文件或
// 多个子目录的那层；合并的几层只算一层深度。被忽略的目录、只有一个被忽略子目录的目录不合；keep（不为空时）自己一行，
// 不和上下合（新建、重命名的输入框对着它）。
func (t *Tree) CompactRows(depth int, keep string, codes map[string]byte, marks map[string]bool, rows []Row) []Row {
	if t.Root == nil {
		return rows
	}
	return t.treeRows(t.Root, depth, &rowOpts{codes: codes, marks: marks, compact: true, keep: keep}, rows)
}

// rowOpts 是摊平时的选项，见 Rows / CompactRows。
type rowOpts struct {
	codes   map[string]byte
	marks   map[string]bool
	compact bool
	keep    string
}

// merges 是目录 d 和它唯一的子目录合成一行。
func (o *rowOpts) merges(d *Node) bool {
	return o.compact && !d.Ignored && len(d.Files) == 0 && len(d.Dirs) == 1 && !d.Dirs[0].Ignored &&
		(o.keep == "" || d.Path != o.keep && d.Dirs[0].Path != o.keep)
}

func (t *Tree) treeRows(n *Node, depth int, o *rowOpts, rows []Row) []Row {
	links := t.dirLinks(n)
	for _, d := range n.Dirs {
		for len(links) > 0 && LessName(links[0], d.Name) < 0 {
			rows = t.linkRow(JoinPath(n.Path, links[0]), links[0], depth, o, rows)
			links = links[1:]
		}
		var chain []string
		for o.merges(d) {
			if chain == nil {
				chain = []string{d.Path}
			}
			d = d.Dirs[0]
			chain = append(chain, d.Path)
		}
		isOpen := t.Open[d.Path]
		for _, p := range chain {
			isOpen = isOpen && t.Open[p] // 合并行的每一层都展开着才算展开
		}
		rows = append(rows, Row{Path: d.Path, Name: d.Name, Depth: depth, Dir: true, Open: isOpen, Mark: o.marks[d.Path] && !d.Ignored, Ignored: d.Ignored, Chain: chain})
		switch {
		case isOpen && d.Ignored:
			rows = t.lazyRows(d.Path, depth+1, true, false, rows)
		case isOpen:
			rows = t.treeRows(d, depth+1, o, rows)
		}
	}
	for _, name := range links {
		rows = t.linkRow(JoinPath(n.Path, name), name, depth, o, rows)
	}
	for _, name := range n.Files {
		p := JoinPath(n.Path, name)
		if t.DirLink(p) {
			continue
		}
		if _, ig := t.Ignored[p]; ig {
			rows = append(rows, Row{Path: p, Name: name, Depth: depth, Ignored: true})
		} else {
			rows = append(rows, Row{Path: p, Name: name, Depth: depth, Code: o.codes[p], Sub: t.Subs[p]})
		}
	}
	return rows
}

// dirLinks 是目录 n 里画成目录行的符号链接（Link.Expands），按名字的顺序；没有为 nil。
func (t *Tree) dirLinks(n *Node) []string {
	if len(t.Links) == 0 {
		return nil
	}
	var names []string
	for _, name := range n.Files {
		if t.DirLink(JoinPath(n.Path, name)) {
			names = append(names, name)
		}
	}
	return names
}

// linkRow 是树里指向目录的链接 p 那行（目录行，git 状态字母照文件那样给），展开着时接着是读到的那层。
func (t *Tree) linkRow(p, name string, depth int, o *rowOpts, rows []Row) []Row {
	open := t.Open[p]
	rows = append(rows, Row{Path: p, Name: name, Depth: depth, Dir: true, Open: open, Code: o.codes[p]})
	if open {
		rows = t.lazyRows(p, depth+1, false, true, rows)
	}
	return rows
}

// lazyRows 是被忽略的目录、链接目录 p 里读到的那层（展开着的子目录接着往下），太多时最后一行说还有几项。
// ig 是这些行灰显（被忽略的），linked 是在链接目录里面（里面的子目录是链接时往下都是）。
func (t *Tree) lazyRows(p string, depth int, ig, linked bool, rows []Row) []Row {
	for _, e := range t.Lazy[p] {
		if name, ok := strings.CutSuffix(e, "/"); ok {
			q := JoinPath(p, name)
			open := t.Open[q]
			rows = append(rows, Row{Path: q, Name: name, Depth: depth, Dir: true, Open: open, Ignored: ig, Linked: linked})
			if open {
				rows = t.lazyRows(q, depth+1, ig, linked || t.DirLink(q), rows)
			}
			continue
		}
		rows = append(rows, Row{Path: JoinPath(p, e), Name: e, Depth: depth, Ignored: ig, Linked: linked})
	}
	if n := t.lazyMore[p]; n > 0 {
		rows = append(rows, Row{Path: p, Depth: depth, More: n})
	}
	return rows
}

// Scored 是筛选匹配到的一个文件：Path 是仓库内路径，Score 是模糊匹配的分（越大越好）。
// 只有分，显示的那行用 HitRow 再造（几万个匹配里只显示前 FilterCap 个，不给每个都造行、记命中的字）。
type Scored struct {
	Path  string
	Score int32
	name  int32 // 文件名在 Path 里开始的地方
	lazy  bool  // 在被忽略的目录里、展开时才读的那几层（Lazy）
}

// Match 把仓库内路径模糊匹配 pat 的文件（fuzzy.Pattern.ScorePath：字按顺序出现就算，命中在文件名里的分高）
// 按树的顺序一个个交给 yield。被忽略的目录里只匹配展开过、已经读到的那几层，链接目录里的不匹配。
// 不排、不截、不留：调用方把所有仓库的放在一起挑分最好的 FilterCap 个，再用 HitRow 造行。
func (t *Tree) Match(pat fuzzy.Pattern, yield func(Scored)) {
	if t.Root == nil {
		return
	}
	t.matchNode(t.Root, pat, yield)
	for _, d := range slices.Sorted(maps.Keys(t.Lazy)) {
		if t.throughLink(d) {
			continue
		}
		for _, name := range t.Lazy[d] {
			if strings.HasSuffix(name, "/") {
				continue
			}
			p := JoinPath(d, name)
			at := len(p) - len(name)
			if sc, ok := pat.ScorePath(p, at); ok {
				yield(Scored{p, int32(sc), int32(at), true})
			}
		}
	}
}

func (t *Tree) matchNode(n *Node, pat fuzzy.Pattern, yield func(Scored)) {
	for _, d := range n.Dirs {
		t.matchNode(d, pat, yield)
	}
	name := 0 // 文件名在完整路径里开始的地方
	if n.Path != "" {
		name = len(n.Path) + 1
	}
	for _, p := range n.filePaths() {
		if sc, ok := pat.ScorePath(p, name); ok && !t.DirLink(p) {
			yield(Scored{p, int32(sc), int32(name), false})
		}
	}
}

// HitRow 是 Match 匹配到的 s 显示的那行（深度 depth，Hits 是命中的字）。被忽略的文件（Ignored 标着的、Lazy 里的）
// 灰显、没有状态字母。
func (t *Tree) HitRow(pat fuzzy.Pattern, s Scored, depth int, codes map[string]byte) Row {
	_, hits, _ := pat.MatchPath(s.Path, int(s.name))
	row := Row{Path: s.Path, Name: s.Path[s.name:], Depth: depth, Ignored: s.lazy, Hits: hits}
	if !s.lazy {
		row.Sub = t.Subs[s.Path]
		if _, ig := t.Ignored[s.Path]; ig {
			row.Ignored = true
		} else {
			row.Code = codes[s.Path]
		}
	}
	return row
}

// filePaths 是 n.Files 的完整路径（筛选每敲一个字都要比一遍所有文件，不每次都拼）。
func (n *Node) filePaths() []string {
	if n.paths == nil && len(n.Files) > 0 {
		n.paths = make([]string, len(n.Files))
		for i, name := range n.Files {
			n.paths[i] = JoinPath(n.Path, name)
		}
	}
	return n.paths
}

// Toggle 展开或收起一个目录。
func (t *Tree) Toggle(p string) {
	if t.Open == nil {
		t.Open = map[string]bool{}
	}
	if t.Open[p] {
		delete(t.Open, p)
		t.dropLazy(p)
	} else {
		t.Open[p] = true
	}
	t.Version++
}

// dropLazy 去掉 p 和它下面读过的那几层（Lazy）和那几层里的链接（p 自己的链接不动）：再展开时重读。
func (t *Tree) dropLazy(p string) {
	maps.DeleteFunc(t.Lazy, func(k string, _ []string) bool { return Under(k, p) })
	maps.DeleteFunc(t.lazyMore, func(k string, _ int) bool { return Under(k, p) })
	maps.DeleteFunc(t.lazyLinks, func(k string, _ Link) bool { return k != p && Under(k, p) })
}

// ToggleRow 展开或收起目录行 row：合并行收起时只收最深那段，展开时链上每一层都展开
// （以后中间那层多出文件、链拆开了，拆出来的几层还展开着，不会突然收起一截）。
func (t *Tree) ToggleRow(row Row) {
	if len(row.Chain) == 0 || row.Open {
		t.SetOpen(row.Path, !row.Open)
		return
	}
	for _, p := range row.Chain {
		t.SetOpen(p, true)
	}
}

// SetSubs 换上父仓库的子模块（路径 → 状态），和原来的一样时不动。
func (t *Tree) SetSubs(subs map[string]byte) {
	if maps.Equal(t.Subs, subs) {
		return
	}
	t.Subs = subs
	t.Version++
}

// SetOpen 展开（on）或收起一个目录。
func (t *Tree) SetOpen(p string, on bool) {
	if t.Open[p] != on {
		t.Toggle(p)
	}
}

// CollapseAll 收起所有目录。
func (t *Tree) CollapseAll() {
	if len(t.Open) == 0 {
		return
	}
	t.Open, t.Lazy, t.lazyMore, t.lazyLinks = nil, nil, nil, nil
	t.Version++
}

// Reveal 展开一个文件所在的各级目录。
func (t *Tree) Reveal(p string) {
	for d := DirOf(p); d != ""; d = DirOf(d) {
		t.SetOpen(d, true)
	}
}

// Has 是树里有没有这个文件。
func (t *Tree) Has(p string) bool {
	n := t.Dir(DirOf(p))
	return n != nil && slices.Contains(n.Files, p[strings.LastIndexByte(p, '/')+1:])
}

// Apply 是增量刷新：added 里的文件加进树、removed 里的从树里去掉（仓库内路径；已经在、已经不在的跳过）。
// 新文件的目录一层层建；去掉文件后空了的目录也去掉（和整棵重读一样），Extra 里的空目录留着。
// Paths 换成一份新的切片（快速打开、全文搜索手里的旧切片不变）；Extra、Ignored、Lazy、Subs 不动。
// links 是 ReadLinks 读到的符号链接（新加的文件、成了链接的文件），树里有的放进 Links；unlinked 里的
// 不再是链接了，从 Links 里去掉；去掉的文件也从 Links 里去掉。返回树变了没有（只是链接变了也算）。
func (t *Tree) Apply(added, removed, unlinked []string, links map[string]Link) bool {
	if t.Root == nil {
		return false
	}
	gone := map[string]bool{}
	for _, p := range removed {
		n := t.Dir(DirOf(p))
		if n == nil {
			continue
		}
		i := slices.Index(n.Files, BaseName(p))
		if i < 0 {
			continue
		}
		n.Files, n.paths = slices.Delete(n.Files, i, i+1), nil
		gone[p] = true
		delete(t.Links, p)
		t.dropLazy(p)
		t.prune(DirOf(p))
	}
	var add []string
	for _, p := range added {
		if p == "" || strings.HasSuffix(p, "/") || t.Has(p) {
			continue
		}
		n := t.mkdirs(DirOf(p))
		name := BaseName(p)
		i, _ := slices.BinarySearchFunc(n.Files, name, LessName)
		n.Files, n.paths = slices.Insert(n.Files, i, name), nil
		add = append(add, p)
	}
	relinked := false // Links 变了
	for p, l := range links {
		if old, ok := t.Links[p]; (!ok || old != l) && t.Has(p) {
			if t.Links == nil {
				t.Links = map[string]Link{}
			}
			t.Links[p], relinked = l, true
			t.dropLazy(p)
		}
	}
	for _, p := range unlinked {
		if _, ok := t.Links[p]; ok {
			delete(t.Links, p)
			t.dropLazy(p)
			relinked = true
		}
	}
	if len(gone)+len(add) == 0 { // 只是链接变了：Paths 不用换
		if relinked {
			t.Version++
		}
		return relinked
	}
	paths := make([]string, 0, len(t.Paths)-len(gone)+len(add))
	for _, p := range t.Paths {
		if !gone[p] {
			paths = append(paths, p)
		}
	}
	t.Paths = append(paths, add...)
	t.Count = len(t.Paths)
	t.Version++
	return true
}

// prune 从 dir 往上去掉空了的目录，Extra 里的空目录和被忽略的目录留着。
func (t *Tree) prune(dir string) {
	for ; dir != ""; dir = DirOf(dir) {
		n := t.Dir(dir)
		if n == nil || len(n.Files)+len(n.Dirs) > 0 || n.Ignored || t.Extra[dir] {
			return
		}
		parent := t.Dir(DirOf(dir))
		parent.Dirs = slices.DeleteFunc(parent.Dirs, func(d *Node) bool { return d == n })
	}
}

// mkdirs 是目录 dir 的节点，没有的各级目录按名字的顺序插进去。
func (t *Tree) mkdirs(dir string) *Node {
	n := t.Root
	if dir == "" {
		return n
	}
	for _, part := range strings.Split(dir, "/") {
		i, ok := slices.BinarySearchFunc(n.Dirs, part, func(d *Node, name string) int { return LessName(d.Name, name) })
		if !ok {
			n.Dirs = slices.Insert(n.Dirs, i, &Node{Name: part, Path: JoinPath(n.Path, part)})
		}
		n = n.Dirs[i]
	}
	return n
}

// hasKey 是 m 里有 k。
func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

// BaseName 是仓库内路径的最后一段。
func BaseName(p string) string { return p[strings.LastIndexByte(p, '/')+1:] }

// Delta 是 Diff 比出来的磁盘和树不一样的地方（仓库内路径，各自排好序、不重复）。
type Delta struct {
	Added    []string // 磁盘上有、树里没有的文件（调用方问 git 是不是被忽略）
	Removed  []string // 树里有、磁盘上没了的文件
	NewDirs  []string // 磁盘上有、树里没有的目录（里面有 git 列得出的文件就得整棵重读）
	Relinked []string // 树里的文件在磁盘上是符号链接、Links 里没有（普通文件换成了链接），或者提示的文件本来就是链接：调用方 ReadLinks
	Unlinked []string // Links 里的文件在磁盘上不是符号链接了（链接换成了普通文件）
	Full     bool     // 树里的目录在磁盘上没了（或成了文件）、读不了：调用方整棵重读
}

// Diff 比磁盘和树，给增量刷新用：dirs 里的每个目录连同它下面在树里的目录（只进树里有的；新的、被忽略的不进），
// 和 paths 里的每个文件（Lstat）。树里的文件只看还在不在、是不是符号链接，不看内容；链接指的东西有了没了（断没断）不看。
// 不列 .DS_Store。只读磁盘和树，在后台调：调用方保证这期间没有 Apply（它在主线程上、读完之后才调），
// Links 是调用方的副本；root 是仓库根的磁盘路径。
func (t *Tree) Diff(root string, dirs, paths []string) Delta {
	var d Delta
	if t.Root == nil {
		return Delta{Full: true}
	}
	disk := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	var walked map[string]map[string]fs.FileMode // 树里有链接时：走过的目录 → 磁盘上的名字和类型（最后看 Links 里哪些不是链接了）
	if len(t.Links) > 0 {
		walked = map[string]map[string]fs.FileMode{}
	}
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		if slices.Contains(n.Files, "") {
			return true // 未跟踪的嵌套仓库（ls-files 里是一条 "x/"）：git 不往里列，这里也不看
		}
		ents, err := os.ReadDir(disk(n.Path))
		if err != nil {
			return false
		}
		seen := make(map[string]fs.FileMode, len(ents)) // 磁盘上的名字 → 类型（目录、符号链接……）
		for _, e := range ents {
			if e.Name() != ".DS_Store" {
				seen[e.Name()] = e.Type()
			}
		}
		if walked != nil {
			walked[n.Path] = seen
		}
		known := make(map[string]bool, len(n.Files)+len(n.Dirs))
		for _, f := range n.Files {
			known[f] = true
			switch typ, ok := seen[f]; {
			case !ok:
				d.Removed = append(d.Removed, JoinPath(n.Path, f))
			case typ&fs.ModeSymlink != 0:
				if p := JoinPath(n.Path, f); !hasKey(t.Links, p) {
					d.Relinked = append(d.Relinked, p)
				}
			}
		}
		for _, sub := range n.Dirs {
			known[sub.Name] = true
			if typ, ok := seen[sub.Name]; !ok || !typ.IsDir() {
				return false
			}
			if !sub.Ignored && !walk(sub) {
				return false
			}
		}
		for name, typ := range seen {
			if known[name] {
				continue
			}
			if p := JoinPath(n.Path, name); typ.IsDir() {
				d.NewDirs = append(d.NewDirs, p)
			} else {
				d.Added = append(d.Added, p)
			}
		}
		return true
	}
	for _, dir := range dirs {
		fi, err := os.Lstat(disk(dir))
		switch n := t.Dir(dir); {
		case n == nil && err == nil && fi.IsDir():
			d.NewDirs = append(d.NewDirs, dir)
		case n == nil:
			// 不在树里的目录没了（被忽略的、空的）：树不用动
		case err != nil || !fi.IsDir() || !walk(n):
			return Delta{Full: true}
		}
	}
	for _, p := range paths {
		fi, err := os.Lstat(disk(p))
		switch has := t.Has(p); {
		case err == nil && !has && !fi.IsDir():
			d.Added = append(d.Added, p)
		case err != nil && has:
			d.Removed = append(d.Removed, p)
		case err == nil && has && fi.Mode()&fs.ModeSymlink != 0:
			d.Relinked = append(d.Relinked, p) // 提示的链接重读一次：ln -sf 换了指向的也认得出
		case err == nil && has && hasKey(t.Links, p):
			d.Unlinked = append(d.Unlinked, p)
		}
	}
	for p := range t.Links {
		if seen := walked[DirOf(p)]; seen != nil {
			if typ, ok := seen[BaseName(p)]; ok && typ&fs.ModeSymlink == 0 {
				d.Unlinked = append(d.Unlinked, p)
			}
		}
	}
	for _, l := range []*[]string{&d.Added, &d.Removed, &d.NewDirs, &d.Relinked, &d.Unlinked} {
		slices.Sort(*l)
		*l = slices.Compact(*l)
	}
	return d
}

// MaxEmptyScan 是后台整棵找空目录的仓库最多有几个文件：再多的整棵 find 一遍太久，改成展开到哪一层读哪一层
// （app 的 lazyEmpty）。是变量只为测试调小。
var MaxEmptyScan = 20000

// FindEmpty 是 root（磁盘路径）下的空目录（find -type d -empty，不进 .git），仓库内路径、排好序；
// in 不空时只在这几个目录（仓库内路径）里找。读不了的目录跳过（find 退出码不是 0，读到的照用）。
// 子模块、嵌套仓库里的也会列出来，调用方去掉。只读磁盘，在后台调；ctx 取消了返回 nil。
func FindEmpty(ctx context.Context, root string, in ...string) []string {
	starts := []string{"."}
	if len(in) > 0 {
		starts = make([]string, len(in))
		for i, d := range in {
			starts[i] = "./" + d // 不让 - 开头的名字被当成 find 的选项
		}
	}
	return findEmpty(ctx, root, starts, nil)
}

// MaxEmptySkip 是 FindEmptySkip 最多不进的目录数：每个是 find 的一个 -path，太多了每一项都比一遍反而慢；
// 浅的先算，多出来的照样进去找（调用方本来就再去掉被忽略的）。
const MaxEmptySkip = 500

// FindEmptySkip 和 FindEmpty 一样整棵找，但不进 skip 里的目录（仓库内路径，末尾的 / 可有可无），它们自己也不列：
// 被忽略的 node_modules 那样的目录里的空目录反正不显示，整棵走一遍却要几秒（回测：一个大仓库 6.9 s → 0.2 s）。
func FindEmptySkip(ctx context.Context, root string, skip []string) []string {
	skip = slices.Clone(skip)
	if len(skip) > MaxEmptySkip {
		slices.SortStableFunc(skip, func(a, b string) int {
			return strings.Count(strings.TrimSuffix(a, "/"), "/") - strings.Count(strings.TrimSuffix(b, "/"), "/")
		})
		skip = skip[:MaxEmptySkip]
	}
	return findEmpty(ctx, root, []string{"."}, skip)
}

// findEmpty 跑 find：starts 下的空目录，不进 .git 和 skip 里的。
func findEmpty(ctx context.Context, root string, starts, skip []string) []string {
	args := append(starts, "(", "-name", ".git")
	for _, d := range skip {
		if d = strings.TrimSuffix(d, "/"); d != "" {
			args = append(args, "-o", "-path", "./"+globEscape.Replace(d))
		}
	}
	cmd := exec.CommandContext(ctx, "find", append(args, ")", "-prune", "-o", "-type", "d", "-empty", "-print0")...)
	cmd.Dir = root
	out, _ := cmd.Output()
	if ctx.Err() != nil {
		return nil
	}
	var dirs []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if d, ok := strings.CutPrefix(string(p), "./"); ok && d != "" {
			dirs = append(dirs, d)
		}
	}
	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// globEscape 让 find -path 按字面比：目录名里的 [ * ? \ 不当通配符（[id] 那样的目录）。
var globEscape = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)

// SetEmpty 放进整棵找到的空目录 dirs（FindEmpty 再去掉被忽略的；gen 是那次读文件列表的编号，
// 不是最近一次 Start 的不放）：记进 Extra（之后整棵重读时 WithExtra 留着它们）、树里建出来；
// 上一次找到、这次没有的（不空了、被忽略了）从 Extra 去掉，树里空着的那几层也去掉。返回树变了没有。
func (t *Tree) SetEmpty(gen int, dirs []string) bool {
	if gen != t.Gen || t.Loading || t.Root == nil {
		return false
	}
	old := t.found
	t.found = nil
	changed := t.addEmpty(dirs)
	for d := range old {
		if t.found[d] || !t.Extra[d] {
			continue
		}
		delete(t.Extra, d)
		if n := t.Dir(d); n != nil && len(n.Files)+len(n.Dirs) == 0 && !n.Ignored {
			t.prune(d)
			changed = true
		}
	}
	if changed {
		t.Version++
	}
	return changed
}

// AddEmpty 放进增量刷新时冒出来的空目录 dirs（已经去掉被忽略的）：同 SetEmpty，只加不去。
func (t *Tree) AddEmpty(dirs []string) bool {
	if t.Root == nil || !t.addEmpty(dirs) {
		return false
	}
	t.Version++
	return true
}

// addEmpty 把 dirs 里能放的记进 Extra 和 found、树里没有的建出来；返回建了没有。
func (t *Tree) addEmpty(dirs []string) bool {
	made := false
	for _, d := range dirs {
		if d == "" || !t.emptyOK(d) {
			continue
		}
		if t.Extra == nil {
			t.Extra = map[string]bool{}
		}
		if t.found == nil {
			t.found = map[string]bool{}
		}
		t.Extra[d], t.found[d] = true, true
		if t.Dir(d) == nil {
			t.mkdirs(d)
			made = true
		}
	}
	return made
}

// emptyOK 是找到的空目录 d 能不能放进树：一路往上（含它自己）不是被忽略的目录、链接目录、文件（子模块是一行文件）、
// 未跟踪的嵌套仓库（ls-files 里是一条 "x/"）。
func (t *Tree) emptyOK(d string) bool {
	for q := d; q != ""; q = DirOf(q) {
		if t.Ignored[q] || t.DirLink(q) || t.Has(q) {
			return false
		}
		if n := t.Dir(q); n != nil && slices.Contains(n.Files, "") {
			return false
		}
	}
	return true
}

// WithExtra 把 extra（Tree.Extra 的副本）里磁盘上还在的并进读到的文件列表：paths 里没有的文件加到后面，
// 目录另外返回；kept 是还在的那些，调用方用它换掉 Tree.Extra。只读磁盘，可以在后台调用。
func WithExtra(root string, paths []string, extra map[string]bool) (out, dirs []string, kept map[string]bool) {
	if len(extra) == 0 {
		return paths, nil, nil
	}
	kept = map[string]bool{}
	var want []string // 还在的文件
	for p, dir := range extra {
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || fi.IsDir() != dir {
			continue
		}
		kept[p] = dir
		if dir {
			dirs = append(dirs, p)
		} else {
			want = append(want, p)
		}
	}
	if len(want) > 0 {
		listed := map[string]bool{}
		for _, p := range paths {
			if slices.Contains(want, p) {
				listed[p] = true
			}
		}
		out = slices.Clone(paths)
		for _, p := range want {
			if !listed[p] {
				out = append(out, p)
			}
		}
		return out, dirs, kept
	}
	return paths, dirs, kept
}

// Moved 是 from 改名成了 to（文件或目录）：展开状态、Extra 和 Links 里 from 和它下面的路径跟着换，
// 读过的被忽略目录、链接目录那几层去掉（展开着的画行时重读）。
func (t *Tree) Moved(from, to string) {
	t.dropLazy(from)
	t.Open = movePaths(t.Open, from, to)
	t.Extra = movePaths(t.Extra, from, to)
	t.found = movePaths(t.found, from, to)
	t.Links = movePaths(t.Links, from, to)
	t.Version++
}

// Removed 是 p（文件或目录）不在了：展开状态、Extra、Links 和读过的那几层里它和它下面的路径都去掉。
func (t *Tree) Removed(p string) {
	t.dropLazy(p)
	maps.DeleteFunc(t.lazyLinks, func(k string, _ Link) bool { return k == p })
	for _, m := range []map[string]bool{t.Open, t.Extra, t.found} {
		maps.DeleteFunc(m, func(k string, _ bool) bool { return Under(k, p) })
	}
	maps.DeleteFunc(t.Links, func(k string, _ Link) bool { return Under(k, p) })
	t.Version++
}

// Walk 列出普通文件夹 root（不是 Git 仓库，没有 ls-files 可用）里的文件和目录，仓库内路径、按名字的顺序；
// links 是其中的符号链接（读目录时就知道，不另外看；不跟进去，指向目录的也算文件）。
// 不进 .git、node_modules 和点开头的目录，不列 .DS_Store；读不了的子目录跳过；最多 WalkCap 个文件。
// 只读磁盘，可以在后台调用；ctx 取消了就停下返回它的错。
func Walk(ctx context.Context, root string) (paths, dirs, links []string, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p == root {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || strings.HasPrefix(name, ".") || err != nil {
				return filepath.SkipDir
			}
			dirs = append(dirs, rel)
			return nil
		}
		if err != nil || d.Name() == ".DS_Store" {
			return nil
		}
		if len(paths) >= WalkCap {
			return filepath.SkipAll
		}
		paths = append(paths, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			links = append(links, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return paths, dirs, links, nil
}

// Under 是 p 是 dir 本身或在它下面。
func Under(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}

func movePaths[V any](m map[string]V, from, to string) map[string]V {
	moved := map[string]V{}
	for k, v := range m {
		if Under(k, from) {
			delete(m, k)
			moved[to+k[len(from):]] = v
		}
	}
	for k, v := range moved {
		m[k] = v
	}
	return m
}

// Dir 是仓库内路径 p 的目录节点（空为根），没有或树还没读到为 nil。
func (t *Tree) Dir(p string) *Node {
	n := t.Root
	if n == nil || p == "" {
		return n
	}
	for _, part := range strings.Split(p, "/") {
		i := slices.IndexFunc(n.Dirs, func(d *Node) bool { return d.Name == part })
		if i < 0 {
			return nil
		}
		n = n.Dirs[i]
	}
	return n
}
