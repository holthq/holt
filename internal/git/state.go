package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是状态以外 Git 栏和历史要的几样，都是只读的：进行到一半的合并 / 变基、HEAD、还没推送的提交、
// 上一次提交的信息（amend 时填进提交框）、贮藏列表、远程仓库的网页。

// Ongoing 是仓库里进行到一半、停下来等用户的操作（看 git 目录里的记号文件）。
type Ongoing int

const (
	NoOp Ongoing = iota
	Merging
	Rebasing
	CherryPicking
	Reverting
)

var (
	ongoingNames   = [...]string{"", "合并", "变基", "拣选", "还原"}
	abortLabels    = [...]string{"", "中止合并", "中止变基", "中止拣选", "中止还原"}
	continueLabels = [...]string{"", "继续合并", "继续变基", "继续拣选", "继续还原"}
)

// String 是操作的名字（"合并"）；没有为空。
func (o Ongoing) String() string { return i18n.T(ongoingNames[o]) }

// AbortLabel 是中止它的按钮名（"中止合并"）。
func (o Ongoing) AbortLabel() string { return i18n.T(abortLabels[o]) }

// AbortArgs 是中止它的 git 参数。
func (o Ongoing) AbortArgs() []string {
	return [...][]string{nil, {"merge", "--abort"}, {"rebase", "--abort"}, {"cherry-pick", "--abort"}, {"revert", "--abort"}}[o]
}

// ContinueLabel 是接着做下去的按钮名（"继续变基"）。
func (o Ongoing) ContinueLabel() string { return i18n.T(continueLabels[o]) }

// ContinueArgs 是接着做下去的 git 参数（冲突都标成已解决之后）。
func (o Ongoing) ContinueArgs() []string {
	return [...][]string{nil, {"merge", "--continue"}, {"rebase", "--continue"}, {"cherry-pick", "--continue"}, {"revert", "--continue"}}[o]
}

// SkipLabel 是跳过停在的这个提交的按钮名；合并没有这一说，为空。
func (o Ongoing) SkipLabel() string {
	if o.SkipArgs() == nil {
		return ""
	}
	return i18n.T("跳过这个提交")
}

// SkipArgs 是跳过停在的这个提交的 git 参数；合并没有（nil）。
func (o Ongoing) SkipArgs() []string {
	return [...][]string{nil, nil, {"rebase", "--skip"}, {"cherry-pick", "--skip"}, {"revert", "--skip"}}[o]
}

// Progress 是进行到一半的操作停在哪（Git 栏的横幅上写的）。读不到的项是零值。
type Progress struct {
	Step, Total int    // 变基：停在第几个、一共几个提交；合并、拣选 / 还原为 0
	Left        int    // 拣选 / 还原好几个提交时，停的这个后面还排着几个（sequencer/todo）
	Subject     string // 停在的那个提交的标题；合并是 MERGE_MSG 的第一行（"Merge branch 'feature'"）
	Head        string // 停在的那个提交（完整哈希）；合并是 MERGE_HEAD 的第一个
	Onto        string // 变基：变到哪（分支名，解不出是短哈希）
	Branch      string // 变基：正在变基的分支（head-name 去掉 refs/heads/）
}

// ReadProgress 读 op 停在哪：只读 git 目录里的文件，Subject 读不到时再跑一条 git log，变基时一条 name-rev。
// 读不了的都是零值，不报错。
func ReadProgress(ctx context.Context, root, gitDir string, op Ongoing) Progress {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(name)))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	first := func(s string) string { line, _, _ := strings.Cut(s, "\n"); return strings.TrimSpace(line) }
	num := func(name string) int { n, _ := strconv.Atoi(read(name)); return n }
	var p Progress
	switch op {
	case Rebasing:
		// rebase-merge 是现在默认的；rebase-apply 是 git rebase --apply / 老 git 的
		dir, step, total, stopped, msg := "rebase-merge/", "msgnum", "end", "stopped-sha", "message"
		if _, err := os.Stat(filepath.Join(gitDir, "rebase-merge")); err != nil {
			dir, step, total, stopped, msg = "rebase-apply/", "next", "last", "original-commit", "msg-clean"
		}
		p.Step, p.Total = num(dir+step), num(dir+total)
		p.Branch = strings.TrimPrefix(read(dir+"head-name"), "refs/heads/")
		if p.Head = first(read(dir + stopped)); p.Head == "" {
			p.Head = first(read("REBASE_HEAD"))
		}
		p.Subject = first(read(dir + msg))
		if onto := read(dir + "onto"); onto != "" {
			p.Onto = refName(ctx, root, onto)
		}
	case Merging:
		p.Head, p.Subject = first(read("MERGE_HEAD")), first(read("MERGE_MSG"))
	case CherryPicking, Reverting:
		head := "CHERRY_PICK_HEAD"
		if op == Reverting {
			head = "REVERT_HEAD"
		}
		p.Head, p.Subject = first(read(head)), first(read("MERGE_MSG"))
		p.Left = seqLeft(read("sequencer/todo"), p.Head)
	}
	if p.Subject == "" && p.Head != "" {
		if out, err := RunCtx(ctx, root, "log", "-1", "--format=%s", p.Head, "--"); err == nil {
			p.Subject = strings.TrimSpace(string(out))
		}
	}
	return p
}

// refName 是提交 hash 的本地分支名（git name-rev，可能带 ~n），解不出就短哈希。
func refName(ctx context.Context, root, hash string) string {
	out, err := RunCtx(ctx, root, "name-rev", "--name-only", "--no-undefined", "--refs=refs/heads/*", hash)
	if name := strings.TrimSpace(string(out)); err == nil && name != "" {
		return name
	}
	return Short(hash)
}

// seqLeft 是拣选 / 还原好几个提交时停的这个后面还排着几个：git 2.54 的 sequencer/todo 里第一行就是停在的这个
// （没有 done），只数 pick / revert 行。
func seqLeft(todo, head string) int {
	left, firstLine := 0, true
	for _, line := range strings.Split(todo, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "pick" && f[0] != "revert" {
			continue
		}
		if firstLine && head != "" && strings.HasPrefix(head, f[1]) {
			firstLine = false
			continue
		}
		firstLine = false
		left++
	}
	return left
}

// MaxUnpushed 是最多列出多少个没推送的提交（历史里打标记、判断 HEAD 推没推过用，够了）。
const MaxUnpushed = 200

// MaxStashes 是 Git 栏里最多列出多少条贮藏。
const MaxStashes = 50

// Stash 是一条贮藏。
type Stash struct {
	Ref     string // stash@{0}：应用 / 弹出 / 丢弃时用
	Hash    string // 贮藏的提交：丢弃前核对 Ref 还指着它
	Message string // "WIP on main: 1a2b3c4 上一次的提交" / "On main: 贮藏时写的说明"
	Date    time.Time
}

// Extra 是一个仓库状态以外的几样。
type Extra struct {
	Ongoing   Ongoing
	Progress  Progress        // Ongoing 停在哪；没有进行中的为零值
	Head      string          // HEAD 的哈希；还没有提交时为空
	HeadRoot  bool            // HEAD 没有父提交（仓库的第一个提交）：撤销上次提交灰着
	HasRemote bool            // 有至少一个远程分支（没有时不标"未推送"：哪个提交都没推送过，标了全是）
	Unpushed  map[string]bool // HEAD 上还没在任何远程分支里的提交（最多 MaxUnpushed 个）
	Stashes   []Stash         // 贮藏，最新的在前（最多 MaxStashes 条）
	Remotes   []Remote        // 远程，按名字排（git remote -v）
	Web       Web             // origin（没有就第一个远程）在 GitHub / GitLab 上的网页；不认识的主机为零值
	GitDir    string          // 这个工作树的 git 目录（绝对路径）
	CommonDir string          // 公共 git 目录：普通仓库和 GitDir 一样，链接的工作树是主仓库的 .git
	Linked    bool            // 这个仓库是 git worktree add 出来的链接工作树（GitDir 在主仓库的 .git/worktrees 下）
	Subs      []Submodule     // 子模块（.gitmodules 里登记的，最多 MaxSubmodules 个）；没有为 nil
}

// HeadPushed 是 HEAD 已经在某个远程分支里了（这时 amend 会改掉别人可能已经拿到的提交）。
func (x Extra) HeadPushed() bool { return x.Head != "" && x.Pushed(x.Head) }

// Pushed 是这个提交在某个远程分支里（网页上看得到）。只核对 HEAD 往前的那些，别的分支上的当推送过。
func (x Extra) Pushed(hash string) bool { return x.HasRemote && !x.Unpushed[hash] }

// ReadExtra 读仓库的 Extra（rev-parse、for-each-ref、rev-list、stash list、remote -v、submodule status，都只读）。
func ReadExtra(ctx context.Context, root string) (Extra, error) {
	var x Extra
	out, err := RunCtx(ctx, root, "rev-parse", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return x, err
	}
	if dirs := strings.Split(strings.TrimSpace(string(out)), "\n"); len(dirs) == 2 {
		x.GitDir, x.CommonDir = filepath.Clean(dirs[0]), dirs[1]
		if !filepath.IsAbs(x.CommonDir) {
			x.CommonDir = filepath.Join(root, x.CommonDir)
		}
		x.CommonDir = filepath.Clean(x.CommonDir)
		x.Linked = !SamePath(x.GitDir, x.CommonDir) // --absolute-git-dir 解开了符号链接，--git-common-dir 是相对的
	}
	x.Ongoing = ReadOngoing(x.GitDir)
	if x.Ongoing != NoOp {
		x.Progress = ReadProgress(ctx, root, x.GitDir, x.Ongoing)
	}
	if out, err := RunCtx(ctx, root, "rev-list", "-n", "1", "--parents", "HEAD", "--"); err == nil { // 哈希 + 父提交们
		if f := strings.Fields(string(out)); len(f) > 0 {
			x.Head, x.HeadRoot = f[0], len(f) == 1
		}
	}
	if out, err := RunCtx(ctx, root, "for-each-ref", "--count=1", "--format=%(refname)", "refs/remotes"); err == nil {
		x.HasRemote = strings.TrimSpace(string(out)) != ""
	}
	x.Stashes = readStashes(ctx, root)
	x.Remotes = ReadRemotes(ctx, root)
	x.Web = ReadWeb(root)
	x.Subs, _ = ReadSubmodules(ctx, root)
	if x.Head == "" || !x.HasRemote {
		return x, nil
	}
	out, err = RunCtx(ctx, root, "rev-list", "-n", strconv.Itoa(MaxUnpushed), "HEAD", "--not", "--remotes")
	if err != nil {
		return x, err
	}
	x.Unpushed = map[string]bool{}
	for _, h := range strings.Fields(string(out)) {
		x.Unpushed[h] = true
	}
	return x, nil
}

// ReadStashes 读贮藏列表（git stash list），读不了时为空。
func ReadStashes(root string) []Stash { return readStashes(context.Background(), root) }

func readStashes(ctx context.Context, root string) []Stash {
	out, err := RunCtx(ctx, root, "stash", "list", "-n", strconv.Itoa(MaxStashes), "--format=%gd%x1f%H%x1f%cI%x1f%gs")
	if err != nil {
		return nil
	}
	var ss []Stash
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(line, "\x1f", 4)
		if len(f) < 4 {
			continue
		}
		s := Stash{Ref: f[0], Hash: f[1], Message: f[3]}
		s.Date, _ = time.Parse(time.RFC3339, f[2])
		ss = append(ss, s)
	}
	return ss
}

// ReadOngoing 看 git 目录里有没有进行到一半的合并、变基、拣选、还原（链接的工作树看它自己的 git 目录）。
func ReadOngoing(gitDir string) Ongoing {
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(gitDir, name)); return err == nil }
	switch {
	case exists("rebase-merge") || exists("rebase-apply"):
		return Rebasing
	case exists("MERGE_HEAD"):
		return Merging
	case exists("CHERRY_PICK_HEAD"):
		return CherryPicking
	case exists("REVERT_HEAD"):
		return Reverting
	}
	return NoOp
}

// LastMessage 是 HEAD 那次提交的完整信息（amend 时填进提交框）；还没有提交时为空。
func LastMessage(root string) string {
	out, err := Run(root, "log", "-1", "--format=%B", "HEAD", "--")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
