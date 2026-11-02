package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是文件树右键菜单和键盘的小件：复制 / 粘贴 / 创建副本（⌘C / ⌘V / ⌘D，都是复制不是移动，
// 跨仓库也行，重名的加" 副本"），添加到 .gitignore，展开有改动的目录、收起其他仓库，行的悬停提示。
// 写磁盘的都只在点了菜单、按了键之后。

// CopyFiles 把文件放到系统剪贴板（访达里能粘贴）；测试里换掉，不碰真的剪贴板。
var CopyFiles = func(paths []string) {
	if d, err := transfer.FileData(paths...); err == nil {
		mygo.Clipboard.Write(d)
	}
}

// CopyChosen 是文件树里的 ⌘C：复制选中的文件 / 目录（CopyRows）。
func (a *App) CopyChosen() {
	if a.Edit == nil {
		a.CopyRows(a.ChosenRows())
	}
}

// CopyRows 记下 rows（仓库行不算，选了目录又选了它里面的只记目录）的绝对路径等着粘贴，系统剪贴板里放它们的文件 URL。
func (a *App) CopyRows(rows []TreeRow) {
	var paths []string
	for _, row := range outermost(rows) {
		if !row.Root {
			paths = append(paths, filepath.Join(row.Repo.Root, filepath.FromSlash(row.Path)))
		}
	}
	if len(paths) == 0 {
		return
	}
	a.TreeClip = paths
	CopyFiles(paths)
}

// PasteChosen 是文件树里的 ⌘V：粘贴到光标那行（PasteDir），没有光标时粘贴到当前仓库的根。
func (a *App) PasteChosen() {
	if a.Edit != nil {
		return
	}
	if row, ok := a.TreeRowAt(a.FileSel); ok && row.Note == "" && !row.Edit {
		a.PasteInto(row.Repo, PasteDir(row))
	} else if r := a.Current(); r != nil {
		a.PasteInto(r, "")
	}
}

// PasteDir 是粘贴到 row 时放进哪个目录：目录行是它里面（合并行是最深那段），文件是它所在的目录，仓库行是仓库根。
// 指向目录的链接和它里面的不往链接指的地方放：放在最外面那个链接所在的目录（files.Tree.OutsideLinks）。
func PasteDir(row TreeRow) string {
	switch {
	case row.Root:
		return ""
	case row.Dir:
		return row.Repo.Files.OutsideLinks(row.Path)
	}
	return row.Repo.Files.OutsideLinks(files.DirOf(row.Path))
}

// PasteInto 把 ⌘C 记下的复制到 r 的 dir 下，重名的换成" 副本"（freeName）。
func (a *App) PasteInto(r *Repo, dir string) {
	if len(a.TreeClip) > 0 && r != nil {
		a.copyAll(r, slices.Clone(a.TreeClip), dir, true)
	}
}

// DuplicateChosen 是文件树里的 ⌘D：选中的各自在旁边复制一份（DuplicateRows）。
func (a *App) DuplicateChosen() {
	if a.Edit == nil {
		a.DuplicateRows(a.ChosenRows())
	}
}

// DuplicateRows 把 rows（仓库行不算）各自在它所在的目录里复制一份："main.go" → "main 副本.go"。
// 有链接目录里面的行时不做（说一声）。
func (a *App) DuplicateRows(rows []TreeRow) {
	if HasLinked(rows) {
		a.linkedToast()
		return
	}
	type place struct {
		r   *Repo
		dir string
	}
	var order []place
	srcs := map[place][]string{}
	for _, row := range outermost(rows) {
		if row.Root {
			continue
		}
		p := place{row.Repo, files.DirOf(row.Path)}
		if _, ok := srcs[p]; !ok {
			order = append(order, p)
		}
		srcs[p] = append(srcs[p], filepath.Join(row.Repo.Root, filepath.FromSlash(row.Path)))
	}
	for _, p := range order {
		a.copyAll(p.r, srcs[p], p.dir, true)
	}
}

// freeName 是把 src 复制进目录 dir 时用的名字：没重名就是原名，重名了是 "main 副本.go"、"main 副本 2.go"……
// （目录和点开头的文件不拆扩展名："build 副本"、".env 副本"）。
func freeName(dir, src string) string {
	name := filepath.Base(src)
	if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
		return name
	}
	base, ext := name, ""
	if fi, err := os.Stat(src); err == nil && !fi.IsDir() {
		if e := filepath.Ext(name); e != "" && e != name {
			base, ext = strings.TrimSuffix(name, e), e
		}
	}
	for i := 1; i < 1000; i++ {
		n := i18n.Tf("%s 副本", base)
		if i > 1 {
			n = i18n.Tf("%s 副本 %d", base, i)
		}
		if _, err := os.Lstat(filepath.Join(dir, n+ext)); err != nil {
			return n + ext
		}
	}
	return name // 一千个副本都有了：照原名复制，报"已经有了"
}

// CanIgnore 是 rows 里有没有能"添加到 .gitignore"的：Git 仓库里没被忽略的文件 / 目录（仓库行、子模块不算）。
func CanIgnore(rows []TreeRow) bool {
	return slices.ContainsFunc(rows, ignorable)
}

func ignorable(row TreeRow) bool {
	return !row.Root && !row.Ignored && !row.Linked && row.Sub == 0 && row.Note == "" && !row.Repo.Plain
}

// IgnoreRows 是"添加到 .gitignore"：把 rows 里能加的（ignorable）的仓库内路径加到各自仓库根的 .gitignore 末尾，
// 一行一个、以 / 开头只认这一个（目录后面带 /），没有 .gitignore 就建一个；然后重读文件树和状态。
// 已经提交过的文件加了也照样被跟踪（git 的规矩），这里不替它取消跟踪。
func (a *App) IgnoreRows(rows []TreeRow) {
	var order []*Repo
	lines := map[*Repo][]string{}
	for _, row := range outermost(rows) {
		if !ignorable(row) {
			continue
		}
		if _, ok := lines[row.Repo]; !ok {
			order = append(order, row.Repo)
		}
		lines[row.Repo] = append(lines[row.Repo], IgnoreLine(row.Path, row.Dir && !row.DirLink())) // 链接在 git 眼里是文件
	}
	for _, r := range order {
		what := strings.Join(lines[r], " ")
		if err := appendIgnore(r.Root, lines[r]); err != nil {
			a.Log(r, i18n.Tf("添加到 .gitignore：%s", what), err.Error(), true)
			a.Fail(i18n.T("没能写 .gitignore"), err)
			continue
		}
		a.Log(r, i18n.Tf("添加到 .gitignore：%s", what), "", false)
		a.LoadFiles(r) // 被忽略的行重新算
		a.RefreshRepo(r, WhyOp)
	}
}

// IgnoreLine 是仓库内路径 rel 在 .gitignore 里的一行："/a/b.txt"、目录 "/build/"；
// 通配符和反斜杠前面加 \，末尾的空格也加（不然 git 会去掉）。
func IgnoreLine(rel string, dir bool) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, c := range rel {
		if strings.ContainsRune(`*?[\`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	s := b.String()
	if dir {
		return s + "/"
	}
	if strings.HasSuffix(s, " ") {
		s = s[:len(s)-1] + `\ `
	}
	return s
}

// appendIgnore 把 lines 加到 root/.gitignore 末尾（原来最后一行没换行先补一个）。
func appendIgnore(root string, lines []string) error {
	p := filepath.Join(root, ".gitignore")
	old, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return friendly(err)
	}
	var b []byte
	if len(old) > 0 && old[len(old)-1] != '\n' {
		b = append(b, '\n')
	}
	for _, l := range lines {
		b = append(append(b, l...), '\n')
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return friendly(err)
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return friendly(err)
	}
	return nil
}

// CollapseOtherRepos 是仓库菜单的"收起其他仓库"：别的仓库都收起（它们里面展开的目录记着），r 展开。
func (a *App) CollapseOtherRepos(r *Repo) {
	for _, o := range a.Repos {
		o.Expanded = o == r
	}
	a.tree.ver++
	a.EnsureFiles(r)
}

// ExpandChanged 是仓库菜单的"展开有改动的目录"：展开 r，把有改动的文件所在的各级目录都展开（别的不动）。
func (a *App) ExpandChanged(r *Repo) {
	if !r.Expanded {
		r.Expanded = true
		a.EnsureFiles(r)
	}
	for _, ch := range r.Status.Changes {
		if ch.Kind != git.InCommit {
			r.Files.Reveal(strings.TrimSuffix(ch.Path, "/"))
		}
	}
	a.tree.ver++
}

// ExpandAllChanged 是文件树空白处的"展开所有仓库有改动的目录"：有改动的仓库都 ExpandChanged，没改动的不动。
func (a *App) ExpandAllChanged() {
	for _, r := range a.Repos {
		if HasChanges(r) {
			a.ExpandChanged(r)
		}
	}
}

// HasChanges 是 r 的工作区、暂存区有没有改动（仓库菜单里"展开有改动的目录"灰不灰）。
func HasChanges(r *Repo) bool {
	for _, ch := range r.Status.Changes {
		if ch.Kind != git.InCommit {
			return true
		}
	}
	return false
}

// rowTip 是缓存的一行悬停提示：仓库读完状态的时间、文件树的版本都没变就还能用。
type rowTip struct {
	at   time.Time
	ver  int
	text string
}

// RowTip 是文件树里一行悬停时的提示：文件是"大小 · 修改时间 · 状态"（第一次悬停时 stat，仓库刷新之后重算），
// 目录是"N 个文件 · 共 X · 最近改动 Y"（文件数按读到的树数，后两段后台算，见 dirTip）；被忽略的说被忽略。
// 仓库行、说明行、子模块为空（它们有自己的）。
func (a *App) RowTip(row TreeRow) string {
	if row.Root || row.Note != "" || row.Edit || row.Sub != 0 {
		return ""
	}
	r, k := row.Repo, row.Key()
	if _, isLink := r.Files.Link(row.Path); row.Dir && !isLink && !row.Ignored {
		if n := r.Files.Dir(row.Path); n != nil && !n.Ignored {
			return a.dirTip(r, row.Path, n) // 自己有缓存：算好了要换掉"正在算…"
		}
	}
	if t, ok := a.rowTips[k]; ok && t.at.Equal(r.LoadedAt) && t.ver == r.Files.Version {
		return t.text
	}
	if a.rowTips == nil || len(a.rowTips) > 500 {
		a.rowTips = map[FileKey]rowTip{}
	}
	text := rowTipText(r, row)
	a.rowTips[k] = rowTip{r.LoadedAt, r.Files.Version, text}
	return text
}

func rowTipText(r *Repo, row TreeRow) string {
	l, isLink := r.Files.Link(row.Path)
	if row.Dir && !isLink {
		if row.Ignored {
			return i18n.T("被 .gitignore 忽略")
		}
		n := r.Files.Dir(row.Path)
		if n == nil {
			return ""
		}
		return i18n.Tn("%d 个文件", filesUnder(n))
	}
	var parts []string
	if isLink {
		parts = append(parts, LinkTip(l))
	}
	if fi, err := os.Stat(filepath.Join(r.Root, filepath.FromSlash(row.Path))); err == nil && !fi.IsDir() {
		parts = append(parts, docs.HumanSize(fi.Size()), git.Ago(fi.ModTime()))
	}
	switch {
	case row.Ignored:
		parts = append(parts, i18n.T("被 .gitignore 忽略"))
	case r.Plain || row.Linked: // 链接目录里面的没有 git 状态
	default:
		var st []string
		for _, ch := range r.Status.Changes {
			if ch.Kind == git.InCommit || ch.Path != row.Path {
				continue
			}
			switch ch.Kind {
			case git.Staged:
				st = append(st, i18n.Tf("已暂存：%s", codeWord(ch.Code)))
			case git.Unstaged:
				st = append(st, i18n.Tf("未暂存：%s", codeWord(ch.Code)))
			default: // 没跟踪、冲突
				st = append(st, ch.Kind.String())
			}
		}
		if len(st) == 0 {
			st = append(st, i18n.T("没有改动"))
		}
		parts = append(parts, st...) // 又暂存了又改了的：两样都列
	}
	return strings.Join(parts, " · ")
}

// LinkTip 是符号链接的悬停提示：→ 链接里写的目标（相对的就是相对的），断了的、指向目录的说一声，
// 指向目录却不展开的说为什么（绕回上层、穿过的链接太多）。
func LinkTip(l files.Link) string {
	switch {
	case l.Broken:
		return i18n.Tf("→ %s（目标不存在）", l.Target)
	case l.Up:
		return i18n.Tf("→ %s（指向上层目录，不展开）", l.Target)
	case l.Deep:
		return i18n.Tf("→ %s（嵌套太深，不展开）", l.Target)
	case l.Dir:
		return i18n.Tf("→ %s（目录）", l.Target)
	}
	return i18n.Tf("→ %s", l.Target)
}

// filesUnder 是 n 下面一共有几个文件（各级子目录都算）。
func filesUnder(n *files.Node) int {
	c := len(n.Files)
	for _, d := range n.Dirs {
		c += filesUnder(d)
	}
	return c
}

// codeWord 是 git 状态字母说的事："已修改"、"新文件"……（不用"修改"、"复制"：那是提交框的修改上一次、菜单的复制）
func codeWord(code byte) string {
	switch code {
	case 'M':
		return i18n.T("已修改")
	case 'A':
		return i18n.T("新文件")
	case 'D':
		return i18n.T("已删除")
	case 'R':
		return i18n.T("已重命名")
	case 'C':
		return i18n.T("已复制")
	case 'T':
		return i18n.T("类型变了")
	}
	return string(code)
}

// ChooseAllTree 是文件树里的全选：菜单栏"编辑 › 全选"（⌘A 在窗口里被它接走）落到文件树时，选中看得见的所有行。
func (a *App) ChooseAllTree() {
	for _, row := range a.TreeRows() {
		if row.Note == "" && !row.Edit {
			a.FileChosen.Add(row.Key())
		}
	}
}
