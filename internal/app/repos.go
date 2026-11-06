package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/workspace"
)

// 这个文件是仓库列表的增删和排序：添加仓库、从列表移除、把移除的放回来、拖仓库行 / 上移下移。
// 增删只改应用自己的 state.json；用户的 .code-workspace 只有排顺序（MoveRepo，松手或点了菜单之后）会写，
// 也只换 folders 里那几项的先后。仓库本身不碰。

// AddRepoDir 把一个目录加进列表：目录在哪个 Git 仓库里就加哪个仓库的根
// （只读的 rev-parse），不在仓库里就把它当普通文件夹加进来；已经在列表里的就只是选中它。
func (a *App) AddRepoDir(dir string) error {
	root, err := git.Toplevel(dir)
	plain := err != nil && git.NotRepo(dir)
	if err != nil && !plain {
		return err
	}
	if plain {
		root = filepath.Clean(dir)
	}
	if r := a.RepoFor(root); r != nil {
		a.SelectRepo(slices.Index(a.Repos, r))
		return nil
	}
	r := a.newRepo(root)
	at := len(a.Repos)
	if a.WsPath != "" && slices.Contains(a.WsRoots, root) {
		at = a.wsSlot(root) // 移除过的工作区仓库放回它那组里（两组各排各的，CanMoveRepo）
	}
	a.Repos = slices.Insert(a.Repos, at, r)
	if i := slices.Index(a.St.Hidden, root); i >= 0 {
		a.St.Hidden = slices.Delete(a.St.Hidden, i, i+1)
	} else if !slices.Contains(a.WsRoots, root) && !slices.Contains(a.St.Added, root) {
		a.St.Added = append(a.St.Added, root)
	}
	a.SaveState()
	a.RefreshRepo(r, WhyOpen)
	a.watchRepo(r)
	a.wtReposChanged()
	a.tree.ver++
	a.SelectRepo(at)
	if plain {
		a.Toast = i18n.Tf("已添加 %s（不是 Git 仓库）", r.Name)
	}
	return nil
}

// RemoveRepo 把仓库从列表拿掉并关掉它的终端：工作区里的记成隐藏，
// 自己加的从 added 里删掉。不动仓库本身，也不关它的标签。还有 git 写命令在跑或排队时先问（移除会中断它们）。
func (a *App) RemoveRepo(r *Repo) {
	if !slices.Contains(a.Repos, r) {
		return
	}
	if r.running != nil || len(r.Queue) > 0 {
		n := len(r.Queue)
		if r.running != nil {
			n++
		}
		a.Ask(i18n.Tf("从列表移除 %s？", r.Name), i18n.Tn("它还有 %d 条 git 操作在跑或排队，移除会中断它们。", n), i18n.T("中断并移除"), func() { a.removeRepo(r) })
		return
	}
	a.removeRepo(r)
}

func (a *App) removeRepo(r *Repo) {
	i := slices.Index(a.Repos, r)
	if i < 0 {
		return
	}
	for _, d := range a.AllDocs() { // 编辑器区的终端标签跟着关（别的标签留着）
		if d.Kind == docs.Term && d.Root == r.Root {
			a.RemoveDoc(d)
		}
	}
	a.killTerms(r) // 后台的会话也关掉（关窗口、换工作区时只断开）
	r.close()
	a.unwatch(r)
	a.Repos = slices.Delete(a.Repos, i, i+1)
	a.dropUndo(r)
	a.dropProblems(r.Root)
	a.FileChosen.Clear()
	a.GitChosen.Clear()
	if j := slices.Index(a.St.Added, r.Root); j >= 0 {
		a.St.Added = slices.Delete(a.St.Added, j, j+1)
	} else if !slices.Contains(a.St.Hidden, r.Root) {
		a.St.Hidden = append(a.St.Hidden, r.Root)
	}
	a.SaveState()
	switch {
	case len(a.Repos) == 0:
		a.Selected = 0
	case a.Selected > i:
		a.Selected--
	case a.Selected >= len(a.Repos):
		a.Selected = len(a.Repos) - 1
	}
	a.tree.ver++
	a.wtReposChanged()
}

// ShowAllRepos 把移除过的工作区仓库放回来，按工作区文件里的顺序排在前面。
func (a *App) ShowAllRepos() {
	if len(a.St.Hidden) == 0 {
		return
	}
	a.St.Hidden = nil
	a.SaveState()
	cur := a.Current()
	ordered := make([]*Repo, 0, len(a.WsRoots)+len(a.Repos))
	for _, root := range a.WsRoots {
		r := a.RepoFor(root)
		if r == nil {
			r = a.newRepo(root)
			a.RefreshRepo(r, WhyOpen)
			a.watchRepo(r)
		}
		ordered = append(ordered, r)
	}
	for _, r := range a.Repos {
		if !slices.Contains(ordered, r) {
			ordered = append(ordered, r)
		}
	}
	a.Repos = ordered
	if i := slices.Index(a.Repos, cur); i >= 0 {
		a.Selected = i
	}
	a.wtReposChanged()
	a.tree.ver++
}

// InWsFile 是 r 属于工作区自己（.code-workspace 的 folders 里的、打开的文件夹下面找到的），不是"添加仓库"加进来的。
func (a *App) InWsFile(r *Repo) bool { return a.WsPath != "" && slices.Contains(a.WsRoots, r.Root) }

// CanMoveRepo 是仓库 r 能不能挪到 a.Repos 的下标 to：只在同一组里挪（工作区文件里的在前、"添加仓库"加的在后，
// 打开工作区时就是这样排的，各排各的）。
func (a *App) CanMoveRepo(r *Repo, to int) bool {
	i := slices.Index(a.Repos, r)
	return i >= 0 && to >= 0 && to < len(a.Repos) && to != i && a.InWsFile(a.Repos[to]) == a.InWsFile(r)
}

// RepoDrag 是文件树里拖着的仓库行（调顺序）：只有别的仓库行接它（和 TreeDrag 分开，目录行不接）。
type RepoDrag struct{ Repo *Repo }

// wsSlot 是工作区文件里的 root 放回列表时的下标：照文件里的先后插在工作区那组里。
func (a *App) wsSlot(root string) int {
	k, at := slices.Index(a.WsRoots, root), 0
	for n, x := range a.Repos {
		if j := slices.Index(a.WsRoots, x.Root); j >= 0 && j < k {
			at = n + 1
		}
	}
	return at
}

// RepoNear 是把仓库 r 插到仓库 target 前面（after 为假）/ 后面时 r 的新下标；-1 是不认得。
func (a *App) RepoNear(r, target *Repo, after bool) int {
	i, j := slices.Index(a.Repos, r), slices.Index(a.Repos, target)
	if i < 0 || j < 0 || r == target {
		return -1
	}
	if after {
		j++
	}
	if i < j {
		j-- // 先拿出来再插
	}
	return j
}

// CanMoveRepoNear 是把 r 拖到 target 的上半（after 为假）/ 下半松手会不会挪：同一组里、挪了位置才算（画插入线）。
func (a *App) CanMoveRepoNear(r, target *Repo, after bool) bool {
	return a.CanMoveRepo(r, a.RepoNear(r, target, after))
}

// RepoMove 是拖仓库行松手、仓库菜单的上移 / 下移要做的挪动：把 Repo 挪到下标 To。画列表的时候只记下（MoveReq），
// 列表画完了再挪（MoveRepo 改了顺序，文件树的行就重建了）。
type RepoMove struct {
	Repo *Repo
	To   int
}

// MoveRepo 把仓库 r 挪到 a.Repos 的下标 to（CanMoveRepo）：文件树、Git 栏、历史的仓库顺序都跟着。
// 工作区文件里的那组写回 .code-workspace 的 folders 顺序（workspace.Reorder：只换那几项，别的原样；写不了就退回去），
// "添加仓库"加的那组记进会话（added 的先后）；打开的是文件夹时它下面找到的那组只改内存。会话里按根路径记的（展开、标签……）不受影响。
func (a *App) MoveRepo(r *Repo, to int) {
	if !a.CanMoveRepo(r, to) {
		return
	}
	cur, old := a.Current(), slices.Clone(a.Repos)
	i := slices.Index(a.Repos, r)
	a.Repos = slices.Insert(slices.Delete(a.Repos, i, i+1), to, r)
	switch info, err := os.Stat(a.WsPath); {
	case !a.InWsFile(r):
		var added []string
		for _, x := range a.Repos {
			if slices.Contains(a.St.Added, x.Root) {
				added = append(added, x.Root)
			}
		}
		a.St.Added = added
		a.SaveState()
	case err == nil && info.IsDir():
		// 打开的是文件夹：下面找到的仓库没有文件可写，只改内存
	default:
		var roots []string
		for _, x := range a.Repos {
			if a.InWsFile(x) {
				roots = append(roots, x.Root)
			}
		}
		err = workspace.Reorder(a.WsPath, roots)
		if err == nil {
			a.WsRoots, err = workspace.Load(a.WsPath)
		}
		if err != nil {
			a.Repos = old
			a.Fail(i18n.T("没能写工作区文件"), err)
			return
		}
	}
	a.Selected = max(slices.Index(a.Repos, cur), 0)
	a.tree.ver++
	if !a.Filtering() {
		if k := rowOf(a.TreeRows(), r, "", false); k >= 0 {
			a.setTreeSel(k) // 光标跟着挪过去的仓库行
		}
	}
}

// ChooseRepoDir 弹系统的选目录对话框，把选中的目录加进列表
// （⇧⌘O、"仓库"菜单、侧栏的 + 按钮）。对话框会阻塞，所以在 goroutine 里开。
func (a *App) ChooseRepoDir() {
	up, win, open := a.Update, a.Win, a.OpenDialog
	go func() {
		paths, err := open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       i18n.T("添加仓库"),
			Message:     i18n.T("选 Git 仓库的根目录（选了子目录也会用它所在的仓库），或任意文件夹"),
			ButtonLabel: i18n.T("添加"),
			Directory:   true,
			Multiple:    true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		up(func() {
			var errs []string
			for _, p := range paths {
				if err := a.AddRepoDir(p); err != nil {
					errs = append(errs, err.Error())
				}
			}
			if len(errs) > 0 {
				go mygo.Dialog.Error(i18n.T("有目录没加进来"), strings.Join(errs, "\n"))
			}
		})
	}()
}

// Upstream 是当前分支的上游，没有就是空。
func Upstream(r *Repo) string {
	for _, b := range r.Branches {
		if b.Current {
			return b.Upstream
		}
	}
	return ""
}

// UpDown 是 "↑1 ↓2" 这样的领先 / 落后，都没有为空。
func UpDown(st git.Status) string {
	var parts []string
	if st.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", st.Ahead))
	}
	if st.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", st.Behind))
	}
	return strings.Join(parts, " ")
}

// ChangedIn 是文件在仓库的 git 状态里的字母，0 为没改。
func ChangedIn(r *Repo, rel string) byte {
	for _, ch := range r.Status.Changes {
		if ch.Path == rel {
			return ch.Code
		}
	}
	return 0
}
