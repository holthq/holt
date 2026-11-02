package app

import (
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/lemoeade/grove/internal/files"
)

// 大仓库（文件数超过 files.MaxEmptyScan）不整棵 find 空目录，展开到哪一层读哪一层：展开着的目录
// （仓库展开着时含根）后台 ReadDir 一层，树里没有的子目录去掉嵌套仓库、files.exclude 藏的、被忽略的，
// 剩下的当空目录放进树（Files.AddEmpty）。每个目录每次读文件列表只读一次（Repo.emptyRead）。

// lazyEmpty 在 TreeRows 里每帧调：树或展开状态变了时找出这次文件列表还没读过一层的展开着的目录，后台串行各读一层。
func (a *App) lazyEmpty(r *Repo) {
	t := &r.Files
	at := [2]int{t.Gen, t.Version}
	if r.emptyAt == at {
		return
	}
	r.emptyAt = at
	if r.Plain || !t.Loaded || t.Loading || t.Err != nil || t.Root == nil || t.Count <= files.MaxEmptyScan {
		return
	}
	if r.emptyRead == nil {
		r.emptyRead = map[string]int{}
	}
	type layer struct {
		dir   string
		known map[string]bool // 树里已经有的子目录（Files 和 Extra 里的都在树里）
	}
	var todo []layer
	dirs := slices.Sorted(maps.Keys(t.Open))
	if r.Expanded {
		dirs = append([]string{""}, dirs...)
	}
	for _, d := range dirs {
		n := t.Dir(d)
		if r.emptyRead[d] == t.Gen || n == nil || n.Ignored || (d != "" && (!t.Open[d] || t.DirLink(d))) {
			continue
		}
		r.emptyRead[d] = t.Gen
		l := layer{d, map[string]bool{}}
		for _, c := range n.Dirs {
			l.known[c.Name] = true
		}
		todo = append(todo, l)
	}
	if len(todo) == 0 {
		return
	}
	root, nested, hide, gen, ctx, up := r.Root, r.nested, r.exclude.Matcher(), t.Gen, r.ctx, a.Update
	showIgnored := a.St.Settings.ShowIgnored
	go func() {
		var cands []string
		for _, l := range todo {
			ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(l.dir)))
			if err != nil {
				continue
			}
			for _, e := range ents {
				if e.IsDir() && e.Name() != ".git" && !l.known[e.Name()] { // 符号链接不是 IsDir
					cands = append(cands, files.JoinPath(l.dir, e.Name()))
				}
			}
		}
		cands, _ = hide.Filter(dropNested(cands, nested))
		if !showIgnored {
			cands = notIgnored(ctx, root, cands)
		}
		if len(cands) == 0 {
			return
		}
		up(func() {
			if r.Files.Gen == gen {
				r.Files.AddEmpty(cands)
			}
		})
	}()
}
