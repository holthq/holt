package git

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 这个文件是 git worktree 的读取：主仓库和它链接的工作树、每个有几处改动、和主仓库的 HEAD 比领先 / 落后多少。
// 都是只读的；新建、移除、清理、锁定是 app 里经 RunOp 跑的写命令。

// Worktree 是 `git worktree list --porcelain` 的一块。
type Worktree struct {
	Path        string // 绝对路径，Clean 过（git 给的原样，没解符号链接）
	Head        string // 完整哈希；bare 为空
	Branch      string // 短名（去掉 refs/heads/），分离 HEAD 为空
	Detached    bool
	Bare        bool
	Main        bool // 第一块：主工作树（或 bare 仓库本身）
	Locked      bool
	LockReason  string // "locked" 后面的原因，可能为空
	Prunable    bool   // 目录没了，git worktree prune 会清掉它的记录
	PruneReason string
}

// Name 是工作树在列表里的名字：分支，分离 HEAD 时是目录名。
func (w Worktree) Name() string {
	if w.Branch != "" {
		return w.Branch
	}
	return filepath.Base(w.Path)
}

// Worktrees 列出仓库的所有工作树（主的在最前），root 可以是其中任何一个。
func Worktrees(ctx context.Context, root string) ([]Worktree, error) {
	out, err := RunCtx(ctx, root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out), nil
}

// ParseWorktrees 解析 `git worktree list --porcelain`：带 -z 的（字段以 NUL 结尾、块之间多一个 NUL）
// 和不带的（换行、块之间空行）都认。路径到字段末尾（可以有空格），不认识的键不要。
func ParseWorktrees(out []byte) []Worktree {
	sep := byte('\n')
	if bytes.IndexByte(out, 0) >= 0 {
		sep = 0
	}
	var wts []Worktree
	var cur *Worktree
	for _, f := range bytes.Split(out, []byte{sep}) {
		line := string(f)
		if line == "" {
			cur = nil
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			wts = append(wts, Worktree{Path: filepath.Clean(val), Main: len(wts) == 0})
			cur = &wts[len(wts)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "detached":
			cur.Detached = true
		case "bare":
			cur.Bare = true
		case "locked":
			cur.Locked, cur.LockReason = true, val
		case "prunable":
			cur.Prunable, cur.PruneReason = true, val
		}
	}
	return wts
}

// SamePath 是两个路径指的是同一个目录：两边都解开符号链接再比（工作区所在的目录可能是链接，
// 临时目录 /var → /private/var），APFS 默认不分大小写，大小写不同也算。
func SamePath(a, b string) bool {
	a, b = RealPath(a), RealPath(b)
	return a == b || strings.EqualFold(a, b)
}

// RealPath 是解开符号链接、Clean 过的路径；它自己不在了（可清理的工作树）就解开还在的那层上级再接回去。
func RealPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(RealPath(parent), filepath.Base(p))
}

// Under 是 p 在 root 下面（不含 root 本身）时 p 相对 root 的路径（斜杠分隔），否则为空。符号链接解开了比。
func Under(root, p string) string {
	root, p = RealPath(root), RealPath(p)
	rel, err := filepath.Rel(root, p)
	if err != nil {
		if rel, err = filepath.Rel(strings.ToLower(root), strings.ToLower(p)); err != nil {
			return ""
		}
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// WorktreeDirty 是工作树里有几处未提交的改动：`git status --porcelain` 的条数（未跟踪的目录算一条）。
func WorktreeDirty(ctx context.Context, wt string) (int, error) {
	out, err := RunCtx(ctx, wt, "status", "--porcelain", "-z")
	if err != nil {
		return 0, err
	}
	n := 0
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 3 {
			continue
		}
		n++
		if rec[0] == 'R' || rec[0] == 'C' { // 改名、复制：下一条是原来的路径
			i++
		}
	}
	return n, nil
}

// AheadBehind 是 head 比 base 多几个提交（领先）、少几个（落后），在 root 里跑
// `rev-list --left-right --count base...head`。两个都是同一个对象库里的哈希（主仓库和它的工作树共用一个）。
func AheadBehind(ctx context.Context, root, base, head string) (ahead, behind int, err error) {
	out, err := RunCtx(ctx, root, "rev-list", "--left-right", "--count", base+"..."+head)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, 0, nil
	}
	behind, _ = strconv.Atoi(f[0])
	ahead, _ = strconv.Atoi(f[1])
	return ahead, behind, nil
}

// CommitTimes 是这些提交的提交时间（一条 `git log --no-walk`），读不出来的不在里面。
func CommitTimes(ctx context.Context, root string, hashes []string) map[string]time.Time {
	ts := map[string]time.Time{}
	if len(hashes) == 0 {
		return ts
	}
	out, err := RunCtx(ctx, root, append([]string{"log", "--no-walk=unsorted", "--format=%H%x1f%ct"}, hashes...)...)
	if err != nil {
		return ts
	}
	for _, line := range strings.Split(string(out), "\n") {
		h, ct, ok := strings.Cut(line, "\x1f")
		if !ok {
			continue
		}
		if unix, err := strconv.ParseInt(ct, 10, 64); err == nil {
			ts[h] = time.Unix(unix, 0)
		}
	}
	return ts
}

// PruneList 是 git worktree prune 会清掉的记录（`--dry-run -v`，git 写在 stderr 上），一条一行，如
// "Removing worktrees/x: gitdir file points to non-existent location"。只看不清。
func PruneList(ctx context.Context, root string) ([]string, error) {
	out, err := RunOp(ctx, root, "", "worktree", "prune", "--dry-run", "-v")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// BranchFiles 是 head 比 base 多出来的改动（`git diff base...head`，只读）：先求两边的 merge-base，再列
// merge-base → head 改了的文件（带行数，Range 为真、Parent 是 merge-base）和中间的提交数。base 后来多出的提交不算。
func BranchFiles(ctx context.Context, root, base, head string) (mb string, files []Change, commits int, err error) {
	out, err := RunCtx(ctx, root, "merge-base", base, head)
	if err != nil {
		return "", nil, 0, err
	}
	mb = strings.TrimSpace(string(out))
	if files, err = DiffFiles(ctx, root, mb, head); err != nil {
		return mb, nil, 0, err
	}
	commits, err = Count(ctx, root, mb+".."+head)
	return mb, files, commits, err
}
