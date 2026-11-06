package app

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holthq/holt/internal/files"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/globs"
	"github.com/holthq/holt/internal/watch"
)

// 这个文件是文件树的增量刷新：只有工作区里的文件变了（监听报的目录、保存的文件）时不整棵 git ls-files，
// 只看变了的那几处，加进 / 去掉树里的文件（files.Tree.Apply）。.git 变了、目录变了、说不清哪里变了还是整棵读。

// 可替换的 git 调用（测试数调用次数）。
var (
	lsFiles = git.LsFilesModes
	listed  = git.Listed
)

// fileHints 是下一次刷新文件树要看的地方：监听报的目录（这棵子树里有变化）、保存的文件；full 是说不清
// 哪里变了（.git 变了、根下的文件、太多、别的原因的刷新），整棵读。RefreshRepo 开始读时取走。
type fileHints struct {
	dirs, files []string
	full        bool
}

// note 记下一批监听到的变化：.git 变了或说不清哪里变了就整棵读。
func (h *fileHints) note(c watch.Change) {
	h.full = h.full || c.Git || c.Full
	for _, d := range c.Dirs {
		if !slices.ContainsFunc(h.dirs, func(q string) bool { return files.Under(d, q) }) {
			h.dirs = append(slices.DeleteFunc(h.dirs, func(q string) bool { return files.Under(q, d) }), d)
		}
	}
	h.limit()
}

// noteFile 记下保存了的文件 rel（仓库内路径）。
func (h *fileHints) noteFile(rel string) {
	if rel != "" && !slices.Contains(h.files, rel) {
		h.files = append(h.files, rel)
	}
	h.limit()
}

func (h *fileHints) limit() {
	if h.full || len(h.dirs)+len(h.files) > watch.MaxDirs {
		*h = fileHints{full: true}
	}
}

// treeDiff 是一次增量刷新：RefreshRepo 开始时在主线程上建（拿着那时的树根），和 git status 一起在后台
// run，读完回主线程 apply。后台只读 snap 的节点和它自己的一份 Links：节点只在 apply（主线程、这次读完之后）里改，Done 换的是新的根。
type treeDiff struct {
	snap           *files.Tree
	gen            int
	hints          fileHints
	nested         []string
	added, removed []string
	unlinked       []string              // 不再是符号链接的（files.Delta.Unlinked）
	links          map[string]files.Link // added 和成了链接的那几个里的符号链接（只有它们才看磁盘）
	full           bool                  // 算不了（树里的目录没了、新目录里有文件、git 出错……）：apply 时整棵读
	lazy           bool                  // 大仓库（文件数超过 files.MaxEmptyScan）：新目录不往里找，只算它自己（emptylazy.go）
	empty          []string              // 冒出来的空目录（emptyIn）
	exclude        globs.Set             // 树按的 files.exclude：合的加进来的不要（exclude.go）
	excludeKey     string                // 它是哪些模式：这次看到 .vscode/settings.json 改了时重读一遍比较，变了整棵读
	base           []string              // 工作区文件和设置里的模式（重读 .vscode/settings.json 时并上）
}

// startTreeDiff 是这次刷新能不能只看变了的地方：能就返回要在后台跑的 treeDiff，不能（要整棵读）为 nil。
// 普通文件夹、显示着被忽略的文件、树还没读完或读失败了都整棵读。
func (a *App) startTreeDiff(r *Repo, h fileHints) *treeDiff {
	t := &r.Files
	if h.full || len(h.dirs)+len(h.files) == 0 || r.Plain || a.ignoredOn || !t.Loaded || t.Loading || t.Err != nil || t.Root == nil {
		return nil
	}
	return &treeDiff{snap: &files.Tree{Root: t.Root, Links: maps.Clone(t.Links)}, gen: t.Gen, hints: h, nested: r.nested,
		lazy: t.Count > files.MaxEmptyScan, exclude: r.exclude, excludeKey: r.excludeKey, base: a.excludeBase()}
}

// run 在后台对一遍磁盘：多出来的文件问一次 git（一个进程）是不是要列（被忽略的不列），新目录里有要列的就整棵读。
func (d *treeDiff) run(ctx context.Context, root string) {
	if d.hints.touchesSettings() {
		if _, key := repoExclude(root, d.base); key != d.excludeKey {
			d.full = true // files.exclude 改了
			return
		}
	}
	hide := d.exclude.Matcher() // 藏起来的目录里的变化不用看：树里本来就没有它
	dirs, _ := hide.Filter(slices.Clone(d.hints.dirs))
	paths, _ := hide.Filter(slices.Clone(d.hints.files))
	delta := d.snap.Diff(root, dirs, paths)
	added, newDirs, full := delta.Added, delta.NewDirs, delta.Full
	added, _ = hide.Filter(added)
	newDirs, _ = hide.Filter(newDirs)
	ask := slices.Concat(added, newDirs)
	switch {
	case full || len(ask) > watch.MaxDirs:
		full = true
	case len(ask) > 0:
		out, err := listed(ctx, root, ask)
		if err != nil {
			full = true
			break
		}
		ok := map[string]bool{}
		for _, p := range dropNested(out, d.nested) {
			p = strings.TrimSuffix(p, "/") // 未跟踪的嵌套仓库
			if slices.ContainsFunc(newDirs, func(n string) bool { return files.Under(p, n) }) {
				full = true // 新目录里有要列的：目录变了，整棵读
				break
			}
			ok[p] = true
		}
		added = slices.DeleteFunc(added, func(p string) bool { return !ok[p] })
	}
	if !full {
		d.links = files.ReadLinks(root, slices.Concat(added, delta.Relinked))
		d.empty = dropNested(emptyIn(ctx, root, delta.Removed, newDirs, d.lazy), d.nested)
	}
	d.added, d.removed, d.unlinked, d.full = added, delta.Removed, delta.Unlinked, full
}

// emptyIn 是增量刷新时冒出来的空目录：去掉的文件所在的目录磁盘上空了的（没了的往上找），新目录（里面没有 git 列得出的文件）
// 里 find 出来的——新目录先问一次被忽略没有，node_modules 那样的不进去找；最后都去掉被忽略的。
// lazy（大仓库）时新目录不往里找，只算它自己（里面的展开到那层再读，见 lazyEmpty）。
// 不空了的目录不用管：里面有东西了照样是目录，留在 Extra 里也只是磁盘上没了才去掉。
func emptyIn(ctx context.Context, root string, removed, newDirs []string, lazy bool) []string {
	var cands []string
	seen := map[string]bool{}
	for _, p := range removed {
		for q := files.DirOf(p); q != "" && !seen[q]; q = files.DirOf(q) {
			seen[q] = true
			ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(q)))
			if err == nil && len(ents) == 0 {
				cands = append(cands, q)
			}
			if err == nil {
				break
			}
		}
	}
	switch {
	case len(newDirs) > 0 && lazy:
		cands = append(cands, newDirs...)
	case len(newDirs) > 0:
		if in := notIgnored(ctx, root, slices.Clone(newDirs)); len(in) > 0 {
			cands = append(cands, files.FindEmpty(ctx, root, in...)...)
		}
	}
	slices.Sort(cands)
	return notIgnored(ctx, root, slices.Compact(cands))
}

// apply 回到主线程把算出来的改动放进树。算的时候树整棵读过或正在读：以整棵的为准（那次是在变化之后开始的）。
func (a *App) applyTreeDiff(r *Repo, d *treeDiff) {
	t := &r.Files
	if t.Gen != d.gen || t.Loading || !t.Loaded {
		return
	}
	if d.full {
		a.LoadFiles(r)
		return
	}
	made := t.AddEmpty(d.empty) // 先记进 Extra：去掉文件后空了的目录 Apply 就不收掉
	if t.Apply(d.added, d.removed, d.unlinked, d.links) || made {
		if a.Edit != nil && a.Edit.Repo == r {
			a.selectEditRow() // 输入框那行跟着挪了
		}
	}
}
