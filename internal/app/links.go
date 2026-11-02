package app

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// OpenLink 处理终端里 ⌘点击 的 link（terminal.Options.OpenLink，在主线程上调），dir 是终端现在的目录：
// 文件路径（绝对、~/、相对 dir；file:// 网址也算）在编辑器里固定打开，带 :行[:列] 就跳到那里，焦点给编辑器；
// 仓库里的目录在文件树里显示。返回 false 的交给终端：别的网址用系统浏览器开，找不到的路径什么都不做。
func (a *App) OpenLink(dir, link string) bool {
	if strings.HasPrefix(link, "file://") {
		u, err := url.Parse(link)
		if err != nil || u.Path == "" {
			return false
		}
		link = u.Path
	} else if strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
		return false
	}
	p, j, at := splitLine(link)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		p = home + p[1:]
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	p = filepath.Clean(p)
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	r, rel := a.linkRepo(p)
	if fi.IsDir() {
		if r == nil {
			return false
		}
		a.Reveal(r, rel)
		return true
	}
	switch {
	case r != nil && at:
		a.OpenAt(r, rel, j, true)
	case r != nil:
		a.OpenFile(r, rel, true)
	default:
		d := a.OpenPath(p)
		if at {
			a.jumpIn(d, j, false)
		}
	}
	a.FocusDoc = true
	return true
}

// linkRepo 是磁盘路径 p 落在哪个仓库、仓库里的路径（仓库根自己是 ""）。shell 报的目录可能是解开符号链接的
// （/var → /private/var），对不上时两边都解开再比一次。
func (a *App) linkRepo(p string) (*Repo, string) {
	in := func(root, p string) (string, bool) {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", false
		}
		if rel == "." {
			return "", true
		}
		return filepath.ToSlash(rel), true
	}
	for _, r := range a.Repos {
		if rel, ok := in(r.Root, p); ok {
			return r, rel
		}
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, ""
	}
	for _, r := range a.Repos {
		if root, err := filepath.EvalSymlinks(r.Root); err == nil {
			if rel, ok := in(root, real); ok {
				return r, rel
			}
		}
	}
	return nil, ""
}
