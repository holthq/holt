package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/files"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/workspace"
)

// 这个文件是文件树里的文件操作：新建文件 / 文件夹、行内重命名、移到废纸篓。都只在用户点了菜单或
// 按了快捷键、再在输入框里按回车（或在确认框里点了按钮）之后才做；移到废纸篓先确认。做完马上重读
// 文件列表和 git 状态（监听也会报，这里先读让界面立刻跟上），并在输出栏记一条。

// EditKind 是文件树里正在输入的名字是干什么用的。
type EditKind int

const (
	EditNewFile EditKind = iota + 1
	EditNewDir
	EditRename
)

// TreeEdit 是文件树里正在输入的名字：新建时是插在目标目录下面第一行的输入框，重命名时是那一行本身。
// 回车提交，Esc 或焦点离开取消。
type TreeEdit struct {
	Repo  *Repo
	Kind  EditKind
	Dir   string // 新建：放在哪个目录下（仓库内路径，空为仓库根）
	Path  string // 重命名：原来的路径
	IsDir bool   // 新建文件夹，或重命名的是目录
	Name  string // 输入框里的字
	Err   string // 名字不行的原因，显示在输入框下面

	Started bool // 界面已经把焦点给了输入框、选好了要改的那段
	Focused bool // 输入框拿到过焦点（之后失去焦点就取消）
}

// Label 是输入框的无障碍名字。
func (e *TreeEdit) Label() string {
	switch e.Kind {
	case EditNewFile:
		return i18n.T("新文件名")
	case EditNewDir:
		return i18n.T("新文件夹名")
	}
	return i18n.T("新名字")
}

// Placeholder 是空输入框里的提示。
func (e *TreeEdit) Placeholder() string {
	if e.Kind == EditRename {
		return ""
	}
	return i18n.T("可以带子目录，如 docs/a.md")
}

// Stem 是重命名时一开始选中的那段：文件选扩展名之前，目录选全部（rune 计）。
func (e *TreeEdit) Stem() int {
	name := e.Name
	if !e.IsDir {
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			name = name[:i]
		}
	}
	return utf8.RuneCountInString(name)
}

// FileOpTarget 是 ⌘N / ⇧⌘N 把新东西放在哪：文件树有焦点时按选中的那行（目录就是它，文件是它所在的目录，
// 仓库行是仓库根；见 PasteDir），否则是当前标签的文件所在的目录，再不然是当前仓库的根。不往指向目录的链接里放
// （files.Tree.OutsideLinks）。
func (a *App) FileOpTarget() (*Repo, string) {
	if a.TreeFocused {
		if row, ok := a.TreeRowAt(a.FileSel); ok && row.Note == "" && !row.Edit {
			return row.Repo, PasteDir(row)
		}
	}
	if d := a.CurrentDoc(); d != nil && d.Kind == docs.File && d.Root != "" {
		if r := a.repoByRoot(d.Root); r != nil {
			return r, r.Files.OutsideLinks(files.DirOf(d.Rel))
		}
	}
	return a.Current(), ""
}

func (a *App) repoByRoot(root string) *Repo {
	for _, r := range a.Repos {
		if r.Root == root {
			return r
		}
	}
	return nil
}

// NewHere 是 ⌘N / ⇧⌘N：在 FileOpTarget 那里新建文件（folder 为 true 时是文件夹）。
func (a *App) NewHere(folder bool) {
	r, dir := a.FileOpTarget()
	a.StartNew(r, dir, folder)
}

// StartNew 在文件树里 dir 下面插一行输入框，输入名字回车后新建文件或文件夹。
func (a *App) StartNew(r *Repo, dir string, folder bool) {
	if r == nil {
		return
	}
	kind := EditNewFile
	if folder {
		kind = EditNewDir
	}
	a.startEdit(&TreeEdit{Repo: r, Kind: kind, Dir: dir, IsDir: folder})
}

// StartRename 把文件树里 p 那一行换成输入框，改名字回车后重命名。
func (a *App) StartRename(r *Repo, p string, isDir bool) {
	if r == nil || p == "" {
		return
	}
	a.startEdit(&TreeEdit{Repo: r, Kind: EditRename, Path: p, IsDir: isDir, Name: path.Base(p)})
}

func (a *App) startEdit(e *TreeEdit) {
	r := e.Repo
	a.Edit = e
	if a.Filter != "" {
		a.SetFilter("")
	}
	if !r.Expanded {
		r.Expanded = true
	}
	a.EnsureFiles(r)
	target := e.Dir
	if e.Kind == EditRename {
		target = files.DirOf(e.Path)
	}
	if target != "" {
		r.Files.Reveal(target)
		r.Files.SetOpen(target, true)
	}
	a.tree.ver++
	a.selectEditRow()
	a.ShowSidebar(workspace.ModeFiles)
}

// selectEditRow 选中输入框那一行并滚到眼前（文件列表还没读到时读到后再滚）。
func (a *App) selectEditRow() {
	if i := slices.IndexFunc(a.TreeRows(), func(row TreeRow) bool { return row.Edit }); i >= 0 {
		a.setTreeSel(i)
		a.FileList.ScrollIntoView(i)
	}
}

// CancelEdit 收起输入框，什么都不改；选中回到原来那行（新建时是目标目录）。焦点不动（失焦取消时
// 焦点已经去了别处）；按 Esc 取消的由界面把焦点还给文件树。
func (a *App) CancelEdit() {
	e := a.Edit
	if e == nil {
		return
	}
	a.Edit = nil
	a.tree.ver++
	sel, dir := e.Path, e.IsDir
	if e.Kind != EditRename {
		sel, dir = e.Dir, true
	}
	a.setTreeSel(rowOf(a.TreeRows(), e.Repo, sel, dir))
}

// CommitEdit 是在输入框里按回车：名字不行就在输入框下面说为什么、输入框留着；行就新建或重命名。
// 返回做没做成。
func (a *App) CommitEdit() bool {
	e := a.Edit
	if e == nil {
		return false
	}
	name := strings.TrimSpace(e.Name)
	if e.Kind == EditRename && name == path.Base(e.Path) {
		a.CancelEdit() // 没改
		return false
	}
	if err := checkName(name, e.Kind != EditRename); err != nil {
		e.Err = err.Error()
		return false
	}
	r := e.Repo
	var rel string
	var err error
	var op FileOp
	switch e.Kind {
	case EditRename:
		rel = files.JoinPath(files.DirOf(e.Path), name)
		err = a.rename(r, e.Path, rel)
		op = FileOp{Repo: r, Kind: OpRename, From: []string{e.Path}, To: []string{rel},
			Label: i18n.Tf("重命名 %s → %s", path.Base(e.Path), name)}
	default:
		rel = files.JoinPath(e.Dir, strings.Trim(name, "/"))
		top := topNew(r.Root, rel) // 缺的上级目录一起建：撤销时整个拿走
		err = a.create(r, rel, e.IsDir)
		what := i18n.T("新建文件 ")
		if e.IsDir {
			what = i18n.T("新建文件夹 ")
		}
		op = FileOp{Repo: r, Kind: OpCreate, To: []string{top}, Label: what + rel}
	}
	if err != nil {
		e.Err = err.Error()
		return false
	}
	a.pushUndo(op)
	a.Edit = nil
	a.tree.ver++
	a.revealLater = &revealReq{repo: r, rel: rel} // 文件列表重读好了再选中
	a.LoadFiles(r)
	a.RefreshRepo(r, WhyFileOp)
	if e.Kind == EditNewFile {
		a.OpenFile(r, rel, true)
		a.FocusDoc = true // 新文件打开就能写
	} else {
		a.FocusTree = true
	}
	return true
}

// checkName 检查输入的名字：新建时可以带子目录（a/b.txt），重命名只能是一段。
func checkName(name string, slash bool) error {
	if name == "" {
		return errors.New(i18n.T("要有名字"))
	}
	if !slash && strings.Contains(name, "/") {
		return errors.New(i18n.T("名字里不能有 /"))
	}
	if strings.ContainsAny(name, "\x00\\") {
		return errors.New(i18n.T(`名字里不能有 \ 或 NUL`))
	}
	for _, part := range strings.Split(strings.Trim(name, "/"), "/") {
		switch {
		case part == "" || part == "." || part == "..":
			return errors.New(i18n.T("路径里不能有空的、. 或 .. 这样的一段"))
		case strings.EqualFold(part, ".git"):
			return errors.New(i18n.T("不能用 .git 这个名字"))
		case len(part) > 255:
			return errors.New(i18n.T("名字太长了"))
		}
	}
	return nil
}

// create 新建空文件（O_EXCL，已经有了就失败）或文件夹，缺的上级目录一起建；
// 建的目录记进 Extra，空的时候 git ls-files 列不出来也能在树里看到。
func (a *App) create(r *Repo, rel string, dir bool) error {
	abs := filepath.Join(r.Root, filepath.FromSlash(rel))
	if _, err := os.Lstat(abs); err == nil {
		return fmt.Errorf(i18n.T("已经有 %s 了"), path.Base(rel))
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	what := i18n.T("新建文件 ")
	if dir {
		what = i18n.T("新建文件夹 ")
		if err := os.Mkdir(abs, 0o755); err != nil {
			return friendly(err)
		}
	} else {
		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return friendly(err)
		}
		f.Close()
	}
	if r.Files.Extra == nil {
		r.Files.Extra = map[string]bool{}
	}
	r.Files.Extra[rel] = dir
	for d := files.DirOf(rel); d != ""; d = files.DirOf(d) {
		if _, ok := r.Files.Extra[d]; !ok {
			r.Files.Extra[d] = true
		}
	}
	a.Log(r, what+rel, "", false)
	return nil
}

// rename 改名（只改最后一段）：目标已经有了就不改（macOS 的 rename 会覆盖），只改大小写的除外；
// 打开着的标签跟着换路径，没保存的改动留着。
func (a *App) rename(r *Repo, from, to string) error {
	oldAbs := filepath.Join(r.Root, filepath.FromSlash(from))
	newAbs := filepath.Join(r.Root, filepath.FromSlash(to))
	oldFi, err := os.Lstat(oldAbs)
	if err != nil {
		return friendly(err)
	}
	if fi, err := os.Lstat(newAbs); err == nil && !os.SameFile(oldFi, fi) {
		return fmt.Errorf(i18n.T("已经有 %s 了"), path.Base(to))
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return friendly(err)
	}
	a.localMoved(oldAbs, newAbs, oldFi.IsDir())
	for _, d := range a.AllDocs() {
		if d.Kind == docs.File && d.Root == r.Root && files.Under(d.Rel, from) {
			d.Moved(to + d.Rel[len(from):])
		}
	}
	r.Files.Moved(from, to)
	if files.DirOf(from) != files.DirOf(to) { // 文件树里拖到别的目录
		a.Log(r, i18n.Tf("移动 %s → %s", from, to), "", false)
	} else {
		a.Log(r, i18n.Tf("重命名 %s → %s", from, to), "", false)
	}
	return nil
}

// friendly 把文件系统的错误换成一句中文（路径用户已经知道，不再重复）。
func friendly(err error) error {
	switch {
	case errors.Is(err, fs.ErrExist):
		return errors.New(i18n.T("已经有同名的了"))
	case errors.Is(err, fs.ErrNotExist):
		return errors.New(i18n.T("原来的文件已经不在了"))
	case errors.Is(err, fs.ErrPermission):
		return errors.New(i18n.T("没有权限"))
	case errors.Is(err, syscall.ENOTDIR):
		return errors.New(i18n.T("路径里有一段是文件，不是目录"))
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// TrashPath 先确认，再把仓库里的文件或目录移到废纸篓（Finder 里能找回）。没改过的标签跟着关掉，
// 改了没保存的留着。
func (a *App) TrashPath(r *Repo, p string, isDir bool) {
	what, msg := i18n.T("文件"), i18n.T("可以在废纸篓里找回。")
	if isDir {
		what, msg = i18n.T("文件夹"), i18n.Tf("里面的文件会一起移过去，%s", msg)
	}
	if n := a.dirtyUnder(r, p); n > 0 {
		msg += i18n.Tn("\n有 %d 个打开的标签改了没保存，它们会留着。", n)
	}
	a.Ask(i18n.Tf("把%s“%s”移到废纸篓？", what, path.Base(p)), msg, i18n.T("移到废纸篓"), func() { a.trash(r, p) })
}

func (a *App) dirtyUnder(r *Repo, p string) int {
	n := 0
	for _, d := range a.AllDocs() {
		if d.Kind == docs.File && d.Root == r.Root && files.Under(d.Rel, p) && d.Modified() && d.Copy == 0 {
			n++
		}
	}
	return n
}

func (a *App) trash(r *Repo, p string) { a.trashAll([]trashItem{{r, p}}) }

// trashItem 是要移到废纸篓的一项：仓库和仓库内路径。
type trashItem struct {
	repo *Repo
	path string
}

// trashAll 在后台依次把 items 移到废纸篓，遇到移不动的就停下（前面的已经移了）。每项记一条日志，
// 没改过的标签跟着关掉，最后每个碰过的仓库重读一次文件列表和状态。
func (a *App) trashAll(items []trashItem) {
	trash := a.Trash
	up := a.Update
	go func() {
		n := 0
		var err error
		for ; n < len(items); n++ {
			it := items[n]
			if err = trash(filepath.Join(it.repo.Root, filepath.FromSlash(it.path))); err != nil {
				break
			}
		}
		up(func() {
			var repos []*Repo
			for _, it := range items[:n] {
				r, p := it.repo, it.path
				a.Log(r, i18n.Tf("移到废纸篓 %s", p), "", false)
				for _, d := range slices.Clone(a.AllDocs()) {
					if d.Kind == docs.File && d.Root == r.Root && files.Under(d.Rel, p) && !d.Modified() {
						a.RemoveDoc(d)
					}
				}
				r.Files.Removed(p)
				if !slices.Contains(repos, r) {
					repos = append(repos, r)
				}
			}
			if err != nil {
				it := items[n]
				a.Log(it.repo, i18n.Tf("移到废纸篓 %s", it.path), err.Error(), true)
				a.Fail(i18n.T("没能移到废纸篓"), err)
			}
			for _, r := range repos {
				a.LoadFiles(r)
				a.RefreshRepo(r, WhyFileOp)
			}
		})
	}()
}
