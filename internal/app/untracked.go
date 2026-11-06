package app

import (
	"errors"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是 Git 栏里的未跟踪目录：git status 不带 -uall 时把整个未跟踪的目录折叠成一项（"harness/"，
// Change.Dir），计数照它；读完状态后另外列出里面的文件（git.ListUntrackedDirs → Status.DirFiles，RefreshRepo 里和分支、
// 标签一起跑）。目录行点一下、← →、回车展开 / 收起，不开 Diff；展开后里面的文件一项一行（一层扁平，不分树），就是普通的
// 未跟踪文件：看 Diff、暂存、删除、多选都照走。

// dirWait 是状态读完、分支那几样也读完以后，最多再等列未跟踪目录多久：等不到就先贴上状态（目录先用上一次列的），列完再补。
const dirWait = 50 * time.Millisecond

// dirLists 是在后台列的未跟踪目录（git.ListUntrackedDirs 的结果）。
type dirLists struct {
	files map[string][]string
	more  map[string]bool
	err   error
}

// listDirs 在后台列 st 里的未跟踪目录。
func listDirs(r *Repo, st git.Status) dirLists {
	var dl dirLists
	dl.files, dl.more, dl.err = git.ListUntrackedDirs(r.ctx, r.Root, st)
	if dl.err != nil && !errors.Is(dl.err, git.ErrCanceled) {
		log.Printf("列 %s 的未跟踪目录: %v", r.Root, dl.err)
	}
	return dl
}

// waitDirs 等 dirs（nil 是没有要列的）最多 dirWait：等到了返回结果，没等到返回还要等的 late。
func waitDirs(dirs chan dirLists) (dl dirLists, late chan dirLists) {
	if dirs == nil {
		return dl, nil
	}
	select {
	case dl = <-dirs:
		return dl, nil
	case <-time.After(dirWait):
		return dl, dirs
	}
}

// lateDirs 是这次贴上去的状态里的未跟踪目录还没列完：列完了、状态还是这一次的（LoadedAt 没变）就补上。
func (a *App) lateDirs(r *Repo, late chan dirLists) {
	at, up := r.LoadedAt, a.Update
	go func() {
		dl := <-late
		up(func() {
			if r.gone() || r.LoadedAt != at || dl.err != nil {
				return
			}
			r.Status.DirFiles, r.Status.DirMore = dl.files, dl.more
			a.syncChangeSel()
		})
	}()
}

// keepDirs 是新状态 st 里的未跟踪目录先用 old 里列过的（同一个目录），这次的还没列完时不闪成"…"。
func keepDirs(old, st git.Status) (files map[string][]string, more map[string]bool) {
	for _, ch := range st.Changes {
		fs, ok := old.DirFiles[ch.Path]
		if !ch.Dir || !ok {
			continue
		}
		if files == nil {
			files = map[string][]string{}
		}
		files[ch.Path] = fs
		if old.DirMore[ch.Path] {
			if more == nil {
				more = map[string]bool{}
			}
			more[ch.Path] = true
		}
	}
	return files, more
}

// appendDirFiles 加上展开的未跟踪目录 dir 里的文件：一项一行；还没列到是"读取中…"，超了上限最后一行说明。
func appendDirFiles(rows []ChangeRow, r *Repo, dir string) []ChangeRow {
	files, ok := r.Status.DirFiles[dir]
	if !ok {
		return append(rows, ChangeRow{Kind: RowNote, Repo: r, InDir: dir, Title: i18n.T("读取中…")})
	}
	for _, p := range files {
		rows = append(rows, ChangeRow{Kind: RowChange, Repo: r, InDir: dir, Change: git.Change{Kind: git.Untracked, Path: p, Code: '?'}})
	}
	if r.Status.DirMore[dir] {
		rows = append(rows, ChangeRow{Kind: RowNote, Repo: r, InDir: dir, Title: i18n.T("还有更多，整个目录请用目录行暂存或删除")})
	}
	return rows
}

// ToggleUntrackedDir 展开 / 收起 Git 栏里仓库 r 的未跟踪目录 dir（点目录行、回车、右键），选中停在它那一行。
func (a *App) ToggleUntrackedDir(r *Repo, dir string) { a.setDirOpen(r, dir, !r.DirOpen[dir]) }

func (a *App) setDirOpen(r *Repo, dir string, open bool) {
	if open {
		if r.DirOpen == nil {
			r.DirOpen = map[string]bool{}
		}
		r.DirOpen[dir] = true
	} else {
		delete(r.DirOpen, dir)
	}
	a.stopOnDir(r, dir)
}

// stopOnDir 让 Git 栏的选中停在仓库 r 的未跟踪目录 dir 那一行；换了标签就跟着标签走（同停在贮藏上）。
func (a *App) stopOnDir(r *Repo, dir string) {
	a.gitWt, a.gitStash, a.gitMulti = "", "", false
	a.gitDir, a.gitDirDoc = ChangeKey{r.Root, RowChange, git.Untracked, dir}, a.CurrentDoc()
	a.syncChangeSel()
}

// FoldGitDir 是 Git 栏里的 ← →：选中停在未跟踪目录上时收起 / 展开它；停在展开的目录里的文件上时 ← 回到目录那行。
func (a *App) FoldGitDir(open bool) {
	rows := a.GitRows()
	if a.GitSel < 0 || a.GitSel >= len(rows) {
		return
	}
	switch cr := rows[a.GitSel]; {
	case cr.Kind == RowChange && cr.Change.Dir:
		a.setDirOpen(cr.Repo, cr.Change.Path, open)
	case cr.InDir != "" && !open:
		a.stopOnDir(cr.Repo, cr.InDir)
	}
}

// untrackedDirOf 是 path 在仓库的哪个未跟踪目录里（那项的 Path），不在为空。
func (r *Repo) untrackedDirOf(path string) string {
	for _, ch := range r.Status.Changes {
		if ch.Dir && len(path) > len(ch.Path) && strings.HasPrefix(path, ch.Path) {
			return ch.Path
		}
	}
	return ""
}

// HasChange 是仓库现在还有 k 这一组里 path 这项改动：Status.Changes 里有，或是一个未跟踪目录里的文件
// （列过的按 DirFiles，超了上限、还没列到的在目录里就算）。
func (r *Repo) HasChange(k git.Kind, path string) bool {
	if slices.ContainsFunc(r.Status.Changes, func(ch git.Change) bool { return ch.Kind == k && ch.Path == path }) {
		return true
	}
	dir := ""
	if k == git.Untracked {
		dir = r.untrackedDirOf(path)
	}
	if dir == "" {
		return false
	}
	fs, ok := r.Status.DirFiles[dir]
	_, listed := slices.BinarySearch(fs, path)
	return !ok || listed || r.Status.DirMore[dir]
}

// ChangeOf 是文件 rel 的未提交改动：未暂存的优先，其次已暂存、未跟踪；在未跟踪目录里的是它自己一项未跟踪。没有时 ok 为假。
func (r *Repo) ChangeOf(rel string) (ch git.Change, ok bool) {
	for _, c := range r.Status.Changes {
		if c.Path == rel && (!ok || c.Kind == git.Unstaged) {
			ch, ok = c, true
		}
	}
	if !ok && r.HasChange(git.Untracked, rel) {
		ch, ok = git.Change{Kind: git.Untracked, Path: rel, Code: '?'}, true
	}
	return ch, ok
}
