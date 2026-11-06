package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/files"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是文件树里的拖放：选中的文件 / 目录拖到别的目录行（或仓库行，即仓库根目录）上移动；
// 从访达拖到目录行上的复制进来（本来就在这个仓库里的是移动）；访达的拖到窗口别处的在编辑器里打开（OpenArgs）。
// 移动和复制都写磁盘，只在松手之后做，目录、多项、有没保存的标签时先问。
// 文件树的行还能拖到编辑器的 group 上（在那里打开，OpenDropped）和终端上（贴绝对路径，PastePaths），不写磁盘、不问。

// TreeDrag 是文件树里拖着的东西：Repo 里的仓库内路径（选中的目录里面的不重复算）。
// 选中的行跨了仓库时 Repo 是 nil，放到别的目录上不动（编辑器、终端照样接）。Rows 是 Paths 那几行。
type TreeDrag struct {
	Repo  *Repo
	Paths []string
	Rows  []TreeRow
}

// TreeDragOf 是从 row 拖起来带着的：row 在选中里就是选中的全部（仓库行不算），不然只是它。
func (a *App) TreeDragOf(row TreeRow) TreeDrag {
	d := TreeDrag{Repo: row.Repo}
	for _, r := range outermost(a.MenuRows(row)) {
		if r.Root {
			continue
		}
		if r.Repo != row.Repo {
			d.Repo = nil
		}
		d.Paths = append(d.Paths, r.Path)
		d.Rows = append(d.Rows, r)
	}
	return d
}

// Linked 是拖着的里面有链接目录里面的行：它们不能移动（在链接指向的地方），按着 ⌥ 复制出来可以。
func (d TreeDrag) Linked() bool { return HasLinked(d.Rows) }

// FileRows 是拖着的里面的文件行（目录、指向目录的链接、子模块不算），按顺序：拖到编辑器上打开的就是它们。
func (d TreeDrag) FileRows() []TreeRow {
	var out []TreeRow
	for _, r := range d.Rows {
		if !r.Dir && r.Sub == 0 && r.Note == "" && !r.Edit {
			out = append(out, r)
		}
	}
	return out
}

// HasFiles 是拖着的里面有文件行（FileRows 不空）。
func (d TreeDrag) HasFiles() bool { return len(d.FileRows()) > 0 }

// OpenDropped 是文件树的行拖到编辑器组 g 上松手：里面的文件按顺序在 g 里打开成固定标签，最后一个当前；
// 已经开在别的组的搬过来。目录、子模块不算（全是这些时什么都不做）；不问。
func (a *App) OpenDropped(d TreeDrag, g *Group) {
	rows := d.FileRows()
	if len(rows) == 0 || a.Editors().leaf(g) == nil {
		return
	}
	for _, row := range rows {
		a.FocusGroup(g)
		if doc := a.OpenFile(row.Repo, row.Path, true); a.GroupOf(doc) != g {
			a.MoveDocTo(doc, g, -1)
		}
	}
	a.UseRepo(rows[len(rows)-1].Repo)
	a.FocusDoc = true
}

// ShellText 是拖到终端上贴进去的：每一行的绝对路径（ShellQuote 过）用空格连起来，末尾一个空格，好接着敲。
func (d TreeDrag) ShellText() string {
	paths := make([]string, len(d.Rows))
	for i, r := range d.Rows {
		paths[i] = filepath.Join(r.Repo.Root, filepath.FromSlash(r.Path))
	}
	return shellJoin(paths)
}

// shellJoin 是 paths 在 shell 里的写法：每个 ShellQuote 过、空格连起来、末尾一个空格。
func shellJoin(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(ShellQuote(p))
		b.WriteByte(' ')
	}
	return b.String()
}

// ShellQuote 把 s 写成 shell 的一个参数：只有字母、数字和 _@%+=:,./- 的原样，别的（空格、引号、$、中文……）
// 整个用单引号包，里面的单引号先收掉引号、写一个反斜杠转义的单引号、再开引号。
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !shellSafe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)
}

// PastePaths 是文件树的行拖到终端的一格 p 上松手：ShellText 粘进去（不回车），键盘给这一格。
// shell 退出了（或还没起来）不接，界面那边不登记放的地方。
func (a *App) PastePaths(r *Repo, p *TermPane, d TreeDrag) {
	if p.Term == nil || p.Exited || len(d.Rows) == 0 {
		return
	}
	p.Term.Paste(d.ShellText())
	a.FocusPane(r, p)
	a.FocusTermTab(r.tabOf(p))
}

// placeName 是目标目录在提示里的写法：仓库根是"仓库根目录"。
func placeName(dir string) string {
	if dir == "" {
		return i18n.T("仓库根目录")
	}
	return dir
}

// MoveTree 把 d 移到 r 的目录 dir（"" 是仓库根）下面。别的仓库的不动（给个提示）；本来就在 dir 里的跳过；
// 拖进自己或自己里面的整个不动。一个干净的文件直接移，目录、多项、有改了没保存的标签先问。
func (a *App) MoveTree(d TreeDrag, r *Repo, dir string) {
	if len(d.Paths) == 0 || r == nil {
		return
	}
	if d.Repo != r {
		a.Toast = i18n.T("只能在同一个仓库里移动")
		return
	}
	if d.Linked() {
		a.Toast = i18n.T("链接目录里面的不能移动，按着 ⌥ 拖是复制")
		return
	}
	var items []string
	isDir, dirty := false, 0
	for _, p := range d.Paths {
		switch {
		case files.Under(dir, p):
			return // 拖进它自己或它里面
		case files.DirOf(p) == dir:
			continue // 本来就在这儿
		}
		items = append(items, p)
		if fi, err := os.Lstat(filepath.Join(r.Root, filepath.FromSlash(p))); err == nil && fi.IsDir() {
			isDir = true // 被忽略的目录里读到的一层不在 Files 的树里，看磁盘
		}
		dirty += a.dirtyUnder(r, p)
	}
	if len(items) == 0 {
		return
	}
	move := func() { a.moveAll(r, items, dir) }
	if len(items) == 1 && !isDir && dirty == 0 {
		move()
		return
	}
	title := i18n.Tf("把 %s 移到 %s？", path.Base(items[0]), placeName(dir))
	if len(items) > 1 {
		title = i18n.Tn("把这 %d 项移到 %s？", len(items), placeName(dir))
	}
	msg := i18n.T("git 会看成删除 + 新文件；已暂存的要重新暂存。")
	if dirty > 0 {
		msg += i18n.Tn("\n有 %d 个打开的标签改了没保存，它们跟着换到新的路径。", dirty)
	}
	a.Ask(title, msg, i18n.T("移动"), move)
}

// DropTree 是文件树里拖着的 d 在 r 的目录 dir（"" 是仓库根）上松手：别的仓库来的复制过来（先问），
// 按着 ⌥（alt）在同一个仓库里也是复制（访达的规矩，不问），别的是移动（MoveTree）。选中的跨了仓库的不动。
func (a *App) DropTree(d TreeDrag, r *Repo, dir string, alt bool) {
	switch {
	case len(d.Paths) == 0 || r == nil:
	case d.Repo == nil:
		a.Toast = i18n.T("选中的在好几个仓库里，不能一起拖")
	case DropCopies(d, r, alt):
		a.CopyTree(d, r, dir)
	default:
		a.MoveTree(d, r, dir)
	}
}

// DropCopies 是 d 在 r 上松手（按没按着 ⌥ 是 alt）会不会是复制：别的仓库来的、或按着 ⌥。
func DropCopies(d TreeDrag, r *Repo, alt bool) bool {
	return d.Repo != nil && (d.Repo != r || alt)
}

// DropInside 是 d 放进 r 的 dir 是不是放进了它自己或它里面（按磁盘上的路径看，嵌套在别的仓库里的仓库也算），
// 移动和复制都不接。
func DropInside(d TreeDrag, r *Repo, dir string) bool {
	if d.Repo == nil || r == nil {
		return false
	}
	to := filepath.ToSlash(filepath.Join(r.Root, filepath.FromSlash(dir)))
	return slices.ContainsFunc(d.Paths, func(p string) bool {
		return files.Under(to, filepath.ToSlash(filepath.Join(d.Repo.Root, filepath.FromSlash(p))))
	})
}

// CopyTree 把 d 复制到 r 的目录 dir 下，同名的换个名字（"副本"，见 freeName），复制好的选中第一个。
// 别的仓库来的先问（说清楚是复制、原来的不动），同一个仓库里（⌥ 拖）不问。拖进它自己或它里面的整个不动。
func (a *App) CopyTree(d TreeDrag, r *Repo, dir string) {
	if len(d.Paths) == 0 || d.Repo == nil || r == nil || DropInside(d, r, dir) {
		return
	}
	srcs := make([]string, len(d.Paths))
	dirty := 0
	for i, p := range d.Paths {
		srcs[i] = filepath.Join(d.Repo.Root, filepath.FromSlash(p))
		dirty += a.dirtyUnder(d.Repo, p)
	}
	run := func() { a.copyAll(r, srcs, dir, true) }
	if d.Repo == r {
		run()
		return
	}
	place := r.Name
	if dir != "" {
		place += "/" + dir
	}
	title := i18n.Tf("把 %s 复制到 %s？", path.Base(d.Paths[0]), place)
	if len(srcs) > 1 {
		title = i18n.Tn("把 %d 项复制到 %s？", len(srcs), place)
	}
	msg := i18n.Tf("是复制：%s 里原来的不动。", d.Repo.Name)
	if dirty > 0 {
		msg += i18n.Tn("\n有 %d 个打开的标签改了没保存，复制的是磁盘上的。", dirty)
	}
	a.Ask(title, msg, i18n.T("复制"), run)
}

// TreeDwell 是拖着在收起的目录行 / 仓库行上停多久展开它。
const TreeDwell = 500 * time.Millisecond

// treeDwell 是拖着停在 repo 的哪一行上（仓库行 path 是 ""，合并行 chain 是那串目录）、从 since 起；
// seen 是这一帧还在它上面，due 是停够了、行画完之后展开（画行的时候改树，这一帧的行会被重算的换掉）。
type treeDwell struct {
	repo  *Repo
	path  string
	chain []string
	since time.Time
	seen  bool
	due   bool
}

// DwellTree 是这一帧拖着（文件树里拖的、访达拖进来的）停在 row（目录行或仓库行）上：换了一行重新计时，
// 同一行停满 TreeDwell 而它还收着就在行画完之后展开（EndTreeDrop：仓库行展开仓库，合并行整条链都展开），挪走了不收回。
// 返回还要等多久再来一帧（0 是不用等：已经展开着或停够了）。
func (a *App) DwellTree(row TreeRow, now time.Time) time.Duration {
	if !row.Root && !row.Dir {
		return 0
	}
	p, open := row.Path, row.Open
	if row.Root {
		p, open = "", row.Repo.Expanded
	}
	w := &a.dwell
	if w.repo != row.Repo || w.path != p {
		*w = treeDwell{repo: row.Repo, path: p, chain: row.Chain, since: now}
	}
	w.seen = true
	if open {
		return 0
	}
	left := TreeDwell - now.Sub(w.since)
	w.due = left <= 0
	return max(left, 0)
}

// EndTreeDrop 在文件树的行都画完之后：停够了的那行展开（返回 true：要再画一帧）；这一帧没停在哪一行上就忘掉停在哪儿；
// 松手会不会复制、是不是放不下留给下一帧画光标。
func (a *App) EndTreeDrop() bool {
	w := &a.dwell
	opened := w.seen && w.due
	switch {
	case !w.seen:
		*w = treeDwell{}
	case w.due && w.path == "":
		if !w.repo.Expanded {
			a.ToggleRepo(w.repo)
		}
	case w.due:
		for _, q := range w.chain {
			w.repo.Files.SetOpen(q, true)
		}
		w.repo.Files.SetOpen(w.path, true)
	}
	w.seen, w.due = false, false
	a.TreeCopying, a.TreeDropCopy = a.TreeDropCopy, false
	a.TreeNoDrop, a.TreeDropNone = a.TreeDropNone, false
	return opened
}

// moveAll 依次把 items 移到 dir 下（rename，打开着的标签跟着换路径），第一个失败就停下报错；
// 移好的展开目标目录、选中第一个。
func (a *App) moveAll(r *Repo, items []string, dir string) {
	var moved []string
	for _, p := range items {
		to := files.JoinPath(dir, path.Base(p))
		if err := a.rename(r, p, to); err != nil {
			a.Log(r, i18n.Tf("移动 %s → %s", p, to), err.Error(), true)
			a.Fail(i18n.T("没能移动"), err)
			break
		}
		moved = append(moved, to)
	}
	from := items[:len(moved)]
	a.pushUndo(FileOp{Repo: r, Kind: OpRename, From: slices.Clone(from), To: moved, Label: moveLabel(from, dir)})
	a.landed(r, dir, moved)
}

// landed 是移动 / 复制进 dir 的 rels 落地之后：展开 dir，文件列表重读好了选中第一个，刷新 git 状态。
func (a *App) landed(r *Repo, dir string, rels []string) {
	if len(rels) == 0 {
		return
	}
	if dir != "" {
		r.Files.SetOpen(dir, true)
	}
	a.tree.ver++
	a.revealLater = &revealReq{repo: r, rel: rels[0]}
	a.LoadFiles(r)
	a.RefreshRepo(r, WhyFileOp)
}

// DropFiles 是从访达拖到 r 的目录 dir（"" 是仓库根）上放下的 paths：本来就在这个仓库里的是移动（MoveTree），
// 别处的（别的仓库也算）先问再复制进来。混着的（同一个访达窗口拖不出来）只复制外面的。
func (a *App) DropFiles(paths []string, r *Repo, dir string) {
	var inside, outside []string
	for _, p := range paths {
		p = filepath.Clean(p)
		if rr, rel := a.linkRepo(p); rr == r && rel != "" {
			inside = append(inside, rel)
		} else {
			outside = append(outside, p)
		}
	}
	if len(outside) == 0 {
		a.MoveTree(TreeDrag{Repo: r, Paths: inside}, r, dir)
		return
	}
	names := make([]string, 0, 6)
	for i, p := range outside {
		if i == 5 {
			names = append(names, "  "+i18n.Tf("…还有 %d 个", len(outside)-5))
			break
		}
		names = append(names, "  "+filepath.Base(p))
	}
	a.Ask(i18n.Tn("把 %d 项复制到 %s？", len(outside), placeName(dir)), strings.Join(names, "\n"), i18n.T("复制"), func() {
		a.copyAll(r, outside, dir, false)
	})
}

// copyAll 在后台把 srcs 依次复制到 r 的 dir 下（文件保留权限位，符号链接还是链接，目录整个复制），同名的已经有了就算失败
// （dup 时换个名字：粘贴、创建副本，见 freeName）；第一个失败就停下报错。复制好的记进 Files.Extra（ls-files 列出来之前也看得见）。
func (a *App) copyAll(r *Repo, srcs []string, dir string, dup bool) {
	root := r.Root
	up := a.Update
	go func() {
		var done []string
		var isDir []bool
		var err error
		for _, src := range srcs {
			name := filepath.Base(src)
			if dup {
				name = freeName(filepath.Join(root, filepath.FromSlash(dir)), src)
			}
			rel := files.JoinPath(dir, name)
			var d bool
			if d, err = copyIn(src, filepath.Join(root, filepath.FromSlash(rel))); err != nil {
				err = fmt.Errorf("%s：%w", filepath.Base(src), err)
				break
			}
			done, isDir = append(done, rel), append(isDir, d)
		}
		up(func() {
			if r.Files.Extra == nil {
				r.Files.Extra = map[string]bool{}
			}
			for i, rel := range done {
				a.Log(r, i18n.Tf("复制 %s → %s", srcs[i], rel), "", false)
				r.Files.Extra[rel] = isDir[i]
			}
			if err != nil {
				a.Log(r, i18n.Tf("复制 %s → %s", srcs[len(done)], placeName(dir)), err.Error(), true)
				a.Fail(i18n.T("没能复制"), err)
			}
			a.pushUndo(FileOp{Repo: r, Kind: OpCreate, To: done, Label: copyLabel(srcs[:len(done)], dir)})
			a.landed(r, dir, done)
		})
	}()
}

// copyIn 把 src 复制成 dst（dst 不能已经有）：文件按内容复制、保留权限位；符号链接复制成一样的链接
// （链接里写的原样写，相对的还是相对的，不跟进去）；目录自己一层层复制（copyDir）。返回 src 是不是目录（链接不算）。
func copyIn(src, dst string) (bool, error) {
	fi, err := os.Lstat(src)
	if err != nil {
		return false, friendly(err)
	}
	if _, err := os.Lstat(dst); err == nil {
		return fi.IsDir(), fmt.Errorf(i18n.T("已经有 %s 了"), filepath.Base(dst))
	}
	if fi.IsDir() {
		if rel, err := filepath.Rel(src, dst); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true, errors.New(i18n.T("不能复制到它自己里面"))
		}
		return true, copyDir(src, dst, fi.Mode().Perm())
	}
	return false, copyNode(src, dst, fi)
}

// copyDir 把目录 src 复制成 dst：建目录（最后换成 src 的权限位 perm），里面的目录接着往下，文件和链接见 copyNode。
// os.CopyFS 遇到里面的符号链接直接报错，所以自己走。第一个出错就停下（已经复制的留着）。
func copyDir(src, dst string, perm fs.FileMode) error {
	if err := os.Mkdir(dst, 0o700); err != nil {
		return friendly(err)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		return friendly(err)
	}
	for _, e := range ents {
		fi, err := e.Info() // 不跟链接
		if err != nil {
			return friendly(err)
		}
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if fi.IsDir() {
			err = copyDir(from, to, fi.Mode().Perm())
		} else {
			err = copyNode(from, to, fi)
		}
		if err != nil {
			return err
		}
	}
	return friendly(os.Chmod(dst, perm))
}

// copyNode 把不是目录的 src（fi 是它的 Lstat）复制成 dst：链接照写一个，普通文件按内容复制、保留权限位，
// 别的（管道、套接字…）不复制、报错。
func copyNode(src, dst string, fi fs.FileInfo) error {
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return friendly(err)
		}
		return friendly(os.Symlink(target, dst))
	case !fi.Mode().IsRegular():
		return errors.New(i18n.Tf("%s 不是普通文件，复制不了", filepath.Base(src)))
	}
	in, err := os.Open(src)
	if err != nil {
		return friendly(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return friendly(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// treeDoubleClick 是认双击的间隔（和系统默认的一样）。
const treeDoubleClick = 500 * time.Millisecond

// treeClick 是文件树里选中的行自己接到的一次点击：选中的行可以拖（Drag），按下归它，列表收不到这次点击、
// 也就不选、不报 Changed；列表建好之后 ApplyTreeClick 照列表的规矩补上。key / at 是上一次单击的行和时间：
// 第一下点在没选中的行上是列表接的，第二下那行已经选中、归它自己，系统数的双击在两个元素之间重新数，
// 这里按时间认出来。只有文件行认双击（固定打开）；目录 / 仓库行每一下单击都展开 / 收起（VS Code、Zed 也是），双击就是切两次。
// dbl / dblAt 是上一次双击的行和时间：行自己数的双击在第二下按下时报，松开时还报一次单击，那次不算。
// anchor 是 ⇧点从哪行选起，own 是它是这里记的：行自己接的单击 / ⌘点列表不知道（mygo 没有设锚点的接口），
// 列表的锚点还在它上一次自己接的那行，那行可能已经收起看不见了；列表下一次自己接到没按 ⇧ 的改动时不再是。
type treeClick struct {
	pending     bool
	row         int
	mods        ui.Modifiers
	double, alt bool
	now         time.Time
	key         FileKey
	at          time.Time
	dbl         FileKey
	dblAt       time.Time
	anchor      FileKey
	own         bool
	// press 是鼠标按下、还没松开的那行（PressTreeRow），pressing 是还按着，pressAlt 是按下时按着 ⌥，
	// dragged 是按着拖到了别的行（拖着选），松开时就不打开。
	press    FileKey
	pressRow int
	pressAlt bool
	pressing bool
	dragged  bool
	// eat：松开时已经照单击做了的那行；那次松开 mygo 还会在紧接着的那一帧报成这一行的一次单击（TreeRowClicked），不再做一遍。
	// 只管那一帧（ApplyTreeClick 每帧末尾清掉），按住多久再松开都只做一次。
	eat FileKey
}

// PressTreeRow 是鼠标在第 i 行（没选中的文件 / 目录行，没按 ⌘ ⇧）上按下：选中这一帧就挪过去（VS Code 也是按下选中），
// 提示收起；打开文件 / 展开目录等松开（ReleaseTreePress）。alt 是按着 ⌥（松开时在侧边打开），now 是按下的时间。
func (a *App) PressTreeRow(i int, alt bool, now time.Time) {
	row, ok := a.TreeRowAt(i)
	if !ok || row.Note != "" || row.Edit {
		return
	}
	a.FocusTree = true
	a.chooseOnly(i)
	a.FileSel = i
	tc := &a.treeClick
	tc.anchor, tc.own = row.Key(), true
	tc.press, tc.pressRow, tc.pressAlt, tc.pressing, tc.dragged = row.Key(), i, alt, true, false
	tc.key, tc.at = row.Key(), now // 第二下按在它上面时（那时它已经选中、归它自己）认成双击
	a.TreeTip.Dismiss()
}

// TreePressed 是按下还没松开、也没拖走的那行（PressTreeRow 记的），没有时第二个值为假。界面拿它画"按下"的底色。
func (a *App) TreePressed() (FileKey, bool) {
	tc := &a.treeClick
	return tc.press, tc.pressing && !tc.dragged
}

// DragTreePress 是按着拖到了第 j 行（-1 是不在任何一行上）：拖到别的行就是拖着选，选中从按下的那行到 j。
func (a *App) DragTreePress(j int) {
	tc := &a.treeClick
	if !tc.pressing || j < 0 || j == tc.pressRow && !tc.dragged {
		return
	}
	tc.dragged = true
	a.extendFrom(j)
}

// ReleaseTreePress 是按下的那次松开了：没拖走就照单击做按下的那行（文件预览 / 切到它的标签，⌥ 在侧边打开，目录展开 / 收起）。
func (a *App) ReleaseTreePress() {
	tc := &a.treeClick
	if !tc.pressing {
		return
	}
	tc.pressing = false
	row, ok := a.TreeRowAt(a.FileSel)
	if tc.dragged || !ok || row.Key() != tc.press {
		return
	}
	tc.eat = row.Key()
	a.FileHover = a.FileSel // 松开时指针在它上面（ClickTreeRow 拿这个分点击和按键）
	a.ClickTreeRow(false, tc.pressAlt)
}

// TreeRowClicked 记下第 i 行（选中、可拖的那种）自己接到的点击：mods 是按着的键，clicked 是这一帧松开了一次
// （el.Clicked），double 是它自己数到的双击（第二下按下时报，el.DoubleClicked），now 是这一帧的时间。
// 目录 / 仓库行不认双击：只看松开的那次，按下时报的双击不算（不然第二下切两次）。
func (a *App) TreeRowClicked(i int, mods ui.Modifiers, clicked, double bool, now time.Time) {
	tc := &a.treeClick
	row, ok := a.TreeRowAt(i)
	if ok && row.Dir {
		if !clicked {
			return
		}
		double = false
	}
	if ok && !double && mods&^ui.Alt == 0 && row.Key() == tc.eat {
		tc.eat = FileKey{} // PressTreeRow / ReleaseTreePress 已经做了
		return
	}
	tc.pending, tc.row, tc.mods, tc.double, tc.now = true, i, mods&^ui.Alt, double, now
	tc.alt = mods&ui.Alt != 0
}

// NoteTreeClick 记下列表接的一次单击（第 i 行，没按 ⌘ ⇧），后面一下点在它自己上时认成双击。
func (a *App) NoteTreeClick(i int, now time.Time) {
	if row, ok := a.TreeRowAt(i); ok {
		a.treeClick.key, a.treeClick.at = row.Key(), now
	}
}

// TreeListChanged 是列表自己改了选中（点击、按键、拖着选），mods 是按着的键：没按 ⇧ 的，列表的锚点又对了；
// 按着 ⇧、锚点是这里记的，就按这里的锚点重选（列表是从它自己那个旧锚点选的）。
func (a *App) TreeListChanged(mods ui.Modifiers) {
	switch tc := &a.treeClick; {
	case mods&ui.Shift == 0:
		tc.own = false
	case tc.own:
		a.extendFrom(a.FileSel)
	}
}

// extendFrom 把选中换成从 treeClick.anchor 到第 i 行的一段（说明行、输入框不算），光标在 i；
// 锚点那行不在了（收起了、点空白处清掉了）只选 i，和列表没有锚点时一样。
func (a *App) extendFrom(i int) {
	rows := a.TreeRows()
	if i < 0 || i >= len(rows) {
		return
	}
	j := slices.IndexFunc(rows, func(row TreeRow) bool { return row.Key() == a.treeClick.anchor })
	if j < 0 {
		j = i
	}
	a.FileChosen.Clear()
	for _, row := range rows[min(i, j) : max(i, j)+1] {
		if row.Note == "" && !row.Edit {
			a.FileChosen.Add(row.Key())
		}
	}
	a.FileSel = i
}

// ApplyTreeClick 在列表建好之后照列表的规矩做记下的点击：⇧点从锚点选到它，⌘点把它从选中里去掉，
// 单击把多选收成只选它、照点没选中的行那样做（目录展开 / 收起，文件切到它的标签或重新预览，⌥点在侧边打开），
// 双击文件固定打开（同列表的 Submitted）；目录 / 仓库行不认双击，每一下单击都展开 / 收起。
// 每帧在文件树的行画完之后调：顺带清掉 ReleaseTreePress 记的 eat（只管松开后紧接着的那一帧）。
func (a *App) ApplyTreeClick() {
	tc := &a.treeClick
	tc.eat = FileKey{}
	if !tc.pending {
		return
	}
	tc.pending = false
	row, ok := a.TreeRowAt(tc.row)
	if !ok || row.Note != "" || row.Edit {
		return
	}
	a.FocusTree = true
	switch {
	case tc.mods&ui.Shift != 0 && tc.own:
		a.extendFrom(tc.row)
		return
	case tc.mods&ui.Shift != 0:
		a.FileList.ExtendTo(tc.row)
		return
	case tc.mods&ui.Cmd != 0:
		a.FileChosen.Remove(row.Key())
		a.FileSel = tc.row
		tc.anchor, tc.own = row.Key(), true
		return
	}
	double := !row.Dir && (tc.double || tc.key == row.Key() && tc.now.Sub(tc.at) < treeDoubleClick)
	switch {
	case row.Dir:
		tc.key, tc.dbl = FileKey{}, FileKey{}
	case !double && tc.dbl == row.Key() && tc.now.Sub(tc.dblAt) < treeDoubleClick:
		tc.dbl = FileKey{}
		return // 双击第二下松开时的那次单击
	case double:
		tc.key = FileKey{}
		tc.dbl, tc.dblAt = row.Key(), tc.now
	default:
		tc.key, tc.at = row.Key(), tc.now
	}
	collapse := a.FileChosen.Len() > 1
	if collapse {
		a.chooseOnly(tc.row)
	}
	a.FileSel = tc.row
	tc.anchor, tc.own = row.Key(), true
	if double {
		a.ClickTreeRow(true, false)
	} else {
		a.ClickTreeRow(false, tc.alt)
	}
}
