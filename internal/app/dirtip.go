package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/files"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 文件树目录行悬停的大小、最近改动：只 Lstat 树里列着的文件，不另外走磁盘；第一次悬停到那一行时后台算。

// dirInfoCap 是算一个目录时最多 Lstat 几个文件，多了的不算（提示写"共 > …"）。
const dirInfoCap = 50000

// DirInfo 是一个目录底下（树里列着的）文件的合计。
type DirInfo struct {
	Files   int       // 文件数
	Size    int64     // 大小合计（只算普通文件）
	Mod     time.Time // 最近的修改时间
	Partial bool      // 文件超过 dirInfoCap 个，只算了前面的
}

// dirInfo 是 DirInfo 的缓存：算的时候文件列表是第几次读的、状态是什么时候读的，变了就重算（先给上次的）。
type dirInfo struct {
	info DirInfo
	ok   bool // 算好过
	busy bool // 正在后台算
	gen  int
	at   time.Time
}

// dirTip 是目录行的悬停提示："N 个文件 · 共 X · 最近改动 Y"；后两段第一次算好之前写"正在算…"。
func (a *App) dirTip(r *Repo, p string, n *files.Node) string {
	total := filesUnder(n)
	if total == 0 {
		return i18n.Tn("%d 个文件", 0)
	}
	k := FileKey{r.Root, p, ""}
	c := a.dirInfos[k]
	if c == nil || !c.busy && (c.gen != r.Files.Gen || !c.at.Equal(r.LoadedAt)) {
		a.countDir(r, p, n, c)
		c = a.dirInfos[k]
	}
	if !c.ok {
		return i18n.Tn("%d 个文件 · 正在算…", total)
	}
	size := docs.HumanSize(c.info.Size)
	if c.info.Partial {
		size = "> " + size
	}
	return i18n.Tn("%d 个文件 · 共 %s · 最近改动 %s", total, size, git.Ago(c.info.Mod))
}

// countDir 在后台 Lstat 目录 p 下树里列着的文件（最多 dirInfoCap 个），算好了存进 dirInfos、刷一帧。
// old 是上次算的（重算时先接着给它）。文件列表重读过（Gen 变了）的仓库，别的目录的缓存一起清掉。
func (a *App) countDir(r *Repo, p string, n *files.Node, old *dirInfo) {
	if a.dirInfos == nil || len(a.dirInfos) > 500 {
		a.dirInfos = map[FileKey]*dirInfo{}
	}
	gen, at := r.Files.Gen, r.LoadedAt
	for k, c := range a.dirInfos {
		if k.Root == r.Root && c.gen != gen {
			delete(a.dirInfos, k)
		}
	}
	c := &dirInfo{busy: true, gen: gen, at: at}
	if old != nil && old.gen == gen {
		c.info, c.ok = old.info, old.ok
	}
	k := FileKey{r.Root, p, ""}
	a.dirInfos[k] = c
	var rels []string
	partial := !collectFiles(n, &rels, dirInfoCap)
	root, up := r.Root, a.Update
	go func() {
		info := DirInfo{Files: len(rels), Partial: partial}
		for _, rel := range rels {
			fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				continue
			}
			if fi.Mode().IsRegular() {
				info.Size += fi.Size()
			}
			if fi.ModTime().After(info.Mod) {
				info.Mod = fi.ModTime()
			}
		}
		up(func() {
			if a.dirInfos[k] == c {
				c.info, c.ok, c.busy = info, true, false
			}
		})
	}()
}

// collectFiles 把 n 底下的文件（仓库内路径）加进 out，最多 max 个；都加进去了返回 true。
func collectFiles(n *files.Node, out *[]string, max int) bool {
	for _, f := range n.Files {
		if f == "" {
			continue
		}
		if len(*out) >= max {
			return false
		}
		*out = append(*out, files.JoinPath(n.Path, f))
	}
	for _, d := range n.Dirs {
		if !collectFiles(d, out, max) {
			return false
		}
	}
	return true
}
