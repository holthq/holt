package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是会改仓库的 git 操作和历史 / 分支的读取。
// 写操作只在用户明确点了按钮或菜单后才跑（RunOp），读操作走 Run / RunCtx。

// opTimeout 是一条会改仓库的命令最多跑多久（fetch / push 走网络）；有 Keep 时是多久没有进展（见 Keep）。测试里改短。
var opTimeout = 2 * time.Minute

// killAfter 是取消后等 git 自己收尾（SIGTERM）的时间，过了就连它的子进程（ssh、远程助手）一起 SIGKILL。
const killAfter = 3 * time.Second

// ErrCanceled 是命令被取消了（用户点了取消、切工作区、退出）。
var ErrCanceled = errors.New("已取消")

// RunRW 跑一条可能改仓库的 git 命令（不能取消，测试和一次性的小命令用），见 RunOp。
func RunRW(root string, args ...string) (string, error) { return RunRWInput(root, "", args...) }

// RunRWInput 同 RunRW，input 不空时从标准输入喂给命令（git apply 的补丁）。
func RunRWInput(root, input string, args ...string) (string, error) {
	return RunOp(context.Background(), root, input, args...)
}

// RunOp 跑一条可能改仓库的 git 命令，stdout 和 stderr 合在一起返回；input 不空时从标准输入喂给它。
// 不交互：不会弹凭据提示、不会开编辑器（没给 -m 的 commit 会因为空信息而失败）。
// git 在自己的进程组里跑：ctx 取消时整组先收到 SIGTERM，killAfter 后还没退的整组 SIGKILL，
// 返回 ErrCanceled；超过 opTimeout 同样结束，返回超时的错误。
func RunOp(ctx context.Context, root, input string, args ...string) (string, error) {
	return RunOpEnv(ctx, root, input, nil, args...)
}

// RunOpEnv 同 RunOp，env 接在默认的环境变量后面（同一个键后面的赢）：交互式变基用它把
// GIT_SEQUENCE_EDITOR 换成复制写好的 todo，每条写命令都用它带上凭据提示（GIT_ASKPASS 等，见 internal/askpass）。
func RunOpEnv(ctx context.Context, root, input string, env []string, args ...string) (string, error) {
	return RunOpKeep(ctx, root, input, env, nil, args...)
}

// RunOpKeep 同 RunOpEnv，keep 是这条命令的超时钟（nil 就是固定的 opTimeout）：凭据框开着时 Hold 住，答完 Release。
func RunOpKeep(ctx context.Context, root, input string, env []string, keep *Keep, args ...string) (string, error) {
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	if keep == nil {
		keep = &Keep{}
	}
	keep.start(opTimeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer keep.stop()
	cmd, reap := newOp(tctx, root, env, args)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	reap()
	text := strings.TrimRight(out.String(), "\n")
	switch {
	case ctx.Err() != nil:
		return text, ErrCanceled
	case timedOut.Load():
		return text, fmt.Errorf(i18n.T("超过 %s 没结束，已终止"), opTimeout)
	case err != nil:
		if text == "" {
			text = err.Error()
		}
		return text, errors.New(text)
	}
	return text, nil
}

// newOp 是写命令共用的 git 进程：默认的环境（不交互、英文、不开编辑器和分页）接上 env，自己一个进程组，
// ctx 取消时整组先 SIGTERM、killAfter 后还没退的整组 SIGKILL。跑完调 reap：git 退了，组里剩下的（ssh 之类）也不留。
func newOp(ctx context.Context, dir string, env, args []string) (cmd *exec.Cmd, reap func()) {
	cmd = exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotePath=false", "-c", "color.ui=never"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true",
		"GIT_SEQUENCE_EDITOR=true", "GIT_PAGER=cat", "PAGER=cat", "GIT_ASKPASS=true")
	cmd.Env = append(cmd.Env, env...)
	setProcGroup(cmd)
	var kill *time.Timer
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		kill = time.AfterFunc(killAfter, func() { _ = killGroup(pid) })
		return termGroup(pid)
	}
	cmd.WaitDelay = killAfter + time.Second
	return cmd, func() {
		if kill != nil {
			kill.Stop()
			_ = killGroup(cmd.Process.Pid)
		}
	}
}

// lockNames 是 git 写东西时拿的锁文件（相对 git 目录）。命令被杀掉时可能留下，之后的写命令会报
// "Unable to create '….lock': File exists"。
var lockNames = []string{"index.lock", "HEAD.lock", "config.lock", "packed-refs.lock", "shallow.lock"}

// StaleLocks 是仓库里现在在的锁文件（绝对路径，只读的 rev-parse 加 stat）。取消一条写命令之后看一眼，
// 只提醒，不删：也可能是别的程序正在用。
func StaleLocks(root string) []string {
	args := []string{"rev-parse"}
	for _, n := range lockNames {
		args = append(args, "--git-path", n)
	}
	out, err := Run(root, args...)
	if err != nil {
		return nil
	}
	var locks []string
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, err := os.Stat(p); err == nil {
			locks = append(locks, filepath.Clean(p))
		}
	}
	return locks
}

// SplitArgs 把用户敲的一行命令拆成参数，支持单双引号和反斜杠；开头的 git 去掉。
func SplitArgs(line string) []string {
	args := SplitWords(line)
	if len(args) > 0 && args[0] == "git" {
		args = args[1:]
	}
	return args
}

// SplitWords 和 SplitArgs 一样拆，开头的 git 不去掉（终端配置的参数）。
func SplitWords(line string) []string {
	var args []string
	var cur strings.Builder
	in, quote, esc := false, byte(0), false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case esc:
			cur.WriteByte(ch)
			esc = false
		case ch == '\\' && quote != '\'':
			esc = true
			in = true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				cur.WriteByte(ch)
			}
		case ch == '"' || ch == '\'':
			quote = ch
			in = true
		case ch == ' ' || ch == '\t':
			if in {
				args = append(args, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(ch)
			in = true
		}
	}
	if in {
		args = append(args, cur.String())
	}
	return args
}

// Branch 是一个本地或远程分支。
type Branch struct {
	Name     string // 本地名，或 origin/x
	Remote   bool
	Current  bool
	Upstream string
	Hash     string
	Date     time.Time
	Worktree string // 检出在哪个工作树（绝对路径）；没在任何地方检出为空，在 root 自己这里检出就是 root 的路径
}

// CheckedOutElsewhere 是这个本地分支检出在 root 以外的工作树里（git switch 过去会被拒绝）。
func (b Branch) CheckedOutElsewhere(root string) bool {
	return !b.Remote && b.Worktree != "" && !SamePath(b.Worktree, root)
}

// Branches 列出本地和远程分支，最近提交的在前。
func Branches(ctx context.Context, root string) ([]Branch, error) {
	out, err := RunCtx(ctx, root, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname:short)%1f%(objectname:short)%1f%(upstream:short)%1f%(committerdate:unix)%1f%(HEAD)%1f%(symref)%1f%(refname)%1f%(worktreepath)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var bs []Branch
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) < 8 || f[5] != "" { // 跳过 origin/HEAD 这种符号引用
			continue
		}
		b := Branch{Name: f[0], Hash: f[1], Upstream: f[2], Current: f[4] == "*", Remote: strings.HasPrefix(f[6], "refs/remotes/")}
		if f[7] != "" {
			b.Worktree = filepath.Clean(f[7])
		}
		var unix int64
		fmt.Sscan(f[3], &unix)
		b.Date = time.Unix(unix, 0)
		bs = append(bs, b)
	}
	return bs, nil
}

// Commit 是历史里的一次提交。
type Commit struct {
	Hash, Short string
	Parents     []string
	Author      string
	Email       string
	Date        time.Time
	Refs        []string // 指向它的分支 / 标签，HEAD -> 的那个在最前
	Subject     string
	Body        string
	Agent       string // 正文的 Co-Authored-By: trailer 认出的编码 agent（AgentOf），不是为空
}

const LogPage = 200

// LogQuery 是读历史时的筛选。零值是 HEAD 往前的全部。
type LogQuery struct {
	All    bool   // 所有分支：本地、远程分支和标签（不含贮藏），否则只 HEAD
	Rev    string // 从这个提交往前读（工作树的 HEAD），代替 HEAD 和 All
	Search string // 提交信息或作者里有它（不分大小写、不当正则），或是某个提交哈希的开头
	Path   string // 只看动过这个文件或目录的提交（仓库内路径）
	Follow bool   // Path 是文件：跟着重命名往前找（git log --follow）

	// 历史搜索框的查询语法拆出来的别的条件，和 Search 是"且"
	Regex   bool      // Search 是正则（git log -E）：信息或作者里匹配，不再认哈希开头
	Authors []string  // 作者名或邮箱里有其中一个（不分大小写、不当正则）；有它时 Search 不再按作者找
	After   time.Time // 提交时间在这之后（含），零值不限
	Before  time.Time // 提交时间在这之前，零值不限
	Paths   []string  // 动过其中任一个文件或目录（仓库内路径）；有 Path（文件历史）时不算
}

// Narrowed 是这次读只要一部分提交（有搜索词或别的条件），提交图没意义；Path（文件历史）另算。
func (q LogQuery) Narrowed() bool {
	return q.Search != "" || len(q.Authors) > 0 || !q.After.IsZero() || !q.Before.IsZero() || q.Path == "" && len(q.Paths) > 0
}

// filterArgs 是 Search 以外的条件给 git log 的参数：作者（-i，Regex 时 -E 也管它，就先转义）、时间。
// 路径在 logRun 里接在 -- 后面。
func (q LogQuery) filterArgs() []string {
	var args []string
	for _, a := range q.Authors {
		if q.Regex && q.Search != "" {
			a = regexp.QuoteMeta(a)
		}
		args = append(args, "--author="+a)
	}
	if len(q.Authors) > 0 && (q.Search == "" || !q.Regex) {
		args = append(args, "-F")
	}
	if len(q.Authors) > 0 {
		args = append(args, "-i")
	}
	if !q.After.IsZero() {
		args = append(args, "--since=@"+strconv.FormatInt(q.After.Unix(), 10))
	}
	if !q.Before.IsZero() {
		// --until 是"不晚于"，before 要的是"早于"
		args = append(args, "--until=@"+strconv.FormatInt(q.Before.Unix()-1, 10))
	}
	return args
}

// matchLocal 是这个提交满足 q 的条件（文件历史跟着重命名时在这边筛）：时间按作者时间比，正则按 Go 的语法。
func (q LogQuery) matchLocal(c Commit, re *regexp.Regexp) bool {
	if len(q.Authors) > 0 && !slices.ContainsFunc(q.Authors, func(a string) bool {
		a = strings.ToLower(a)
		return strings.Contains(strings.ToLower(c.Author), a) || strings.Contains(strings.ToLower(c.Email), a)
	}) {
		return false
	}
	if !q.After.IsZero() && c.Date.Before(q.After) || !q.Before.IsZero() && !c.Date.Before(q.Before) {
		return false
	}
	switch {
	case q.Search == "":
		return true
	case re != nil:
		return re.MatchString(c.Subject) || re.MatchString(c.Body) || len(q.Authors) == 0 && (re.MatchString(c.Author) || re.MatchString(c.Email))
	case len(q.Authors) > 0:
		s := strings.ToLower(q.Search)
		return strings.Contains(strings.ToLower(c.Subject), s) || strings.Contains(strings.ToLower(c.Body), s) ||
			isHashPrefix(s) && strings.HasPrefix(c.Hash, s)
	}
	return c.Matches(q.Search)
}

// BadDate 是 ResolveDate 认不出的日期。
var BadDate = errors.New("bad date")

// ResolveDate 把历史搜索里写的日期换成时间：yyyy-mm-dd hh:mm:ss（本地时间）自己认，别的（2 weeks ago、yesterday）
// 交给 git（rev-parse --since，只读，要在仓库里跑）。git 认不出的一律当"现在"，所以得到"现在"又不是 now / today 时算认不出。
func ResolveDate(ctx context.Context, root, s string) (time.Time, error) {
	if t, err := time.ParseInLocation(time.DateTime, s, time.Local); err == nil {
		return t, nil
	}
	before := time.Now().Unix()
	out, err := RunCtx(ctx, root, "rev-parse", "--since="+s)
	if err != nil {
		return time.Time{}, err
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(string(out)), "--max-age="), 10, 64)
	if err != nil {
		return time.Time{}, BadDate
	}
	if l := strings.ToLower(strings.TrimSpace(s)); n >= before && l != "now" && l != "today" {
		return time.Time{}, BadDate
	}
	return time.Unix(n, 0), nil
}

// logFormat 是 Log 读的字段，用 \x1f 隔开、每条以 NUL 结尾（-z）。
const logFormat = "--format=%H%x1f%h%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%D%x1f%s%x1f%b"

// MaxFollowSearch 是文件历史（跟着重命名）里搜索时最多读的提交数。
const MaxFollowSearch = 5000

// RevHash 是 rev 指的提交的完整哈希（只读的 rev-parse），没有（还没有提交、分支没了）是空的。
func RevHash(ctx context.Context, root, rev string) string {
	out, err := RunCtx(ctx, root, "rev-parse", "-q", "--verify", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Log 读历史，从第 skip 条起最多 n 条；仓库还没有提交时返回空。有 Search 时是按信息找、按作者找
// 和哈希对得上的那个合在一起，按时间排（各取前 skip+n 条再合，分页照样对）；别的条件（作者、时间、路径）每一趟都带上。
// 没有 Search 时按 --date-order：父提交一定排在它所有的子提交后面（提交图要），别的照样按提交时间。
func Log(ctx context.Context, root string, skip, n int, q LogQuery) ([]Commit, error) {
	filter := q.filterArgs()
	if q.Follow && q.Path != "" && q.Narrowed() {
		// --follow 和 --grep / --author 一起用时，git 先按信息筛掉了改名的那个提交，就跟不到改名前：
		// 读这个文件的整段历史（最多 MaxFollowSearch 条）在这边筛
		var re *regexp.Regexp
		if q.Regex && q.Search != "" {
			var err error
			if re, err = regexp.Compile("(?i)" + q.Search); err != nil {
				return nil, err
			}
		}
		cs, err := logRun(ctx, root, LogQuery{Rev: q.Rev, All: q.All, Path: q.Path, Follow: true}, "", "-n", strconv.Itoa(MaxFollowSearch))
		if err != nil {
			return nil, err
		}
		cs = slices.DeleteFunc(cs, func(c Commit) bool { return !q.matchLocal(c, re) })
		if skip >= len(cs) {
			return nil, nil
		}
		return cs[skip:min(len(cs), skip+n)], nil
	}
	if q.Search == "" {
		return logRun(ctx, root, q, "", append(filter, "--date-order", "--skip="+strconv.Itoa(skip), "-n", strconv.Itoa(n))...)
	}
	limit := []string{"-n", strconv.Itoa(skip + n)}
	mode := "-F"
	if q.Regex {
		mode = "-E"
	}
	byMsg, err := logRun(ctx, root, q, "", slices.Concat(limit, filter, []string{"-i", mode, "--grep=" + q.Search})...)
	if err != nil {
		return nil, err
	}
	all := byMsg
	if len(q.Authors) == 0 {
		byAuthor, err := logRun(ctx, root, q, "", slices.Concat(limit, filter, []string{"-i", mode, "--author=" + q.Search})...)
		if err != nil {
			return nil, err
		}
		all = append(all, byAuthor...)
	}
	if !q.Regex && isHashPrefix(q.Search) {
		if out, err := RunCtx(ctx, root, "rev-parse", "-q", "--verify", q.Search+"^{commit}"); err == nil {
			h := strings.TrimSpace(string(out))
			// --no-walk：只看这一个提交，别的条件照样筛它
			one, err := logRun(ctx, root, LogQuery{Path: q.Path, Paths: q.Paths}, h, append(filter, "--no-walk")...)
			if err == nil && len(one) == 1 {
				all = append(all, one...)
			}
		}
	}
	slices.SortStableFunc(all, func(a, b Commit) int { return b.Date.Compare(a.Date) })
	seen := map[string]bool{}
	all = slices.DeleteFunc(all, func(c Commit) bool {
		dup := seen[c.Hash]
		seen[c.Hash] = true
		return dup
	})
	if skip >= len(all) {
		return nil, nil
	}
	return all[skip:min(len(all), skip+n)], nil
}

// Matches 是提交信息、作者名或邮箱里有 s（不分大小写），或哈希以 s 开头：和 Log 的 Search 一样。
func (c Commit) Matches(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(strings.ToLower(c.Subject), s) || strings.Contains(strings.ToLower(c.Body), s) ||
		strings.Contains(strings.ToLower(c.Author), s) || strings.Contains(strings.ToLower(c.Email), s) ||
		isHashPrefix(s) && strings.HasPrefix(c.Hash, s)
}

// isHashPrefix 是 s 看着像提交哈希的开头（4 到 40 位十六进制）。
func isHashPrefix(s string) bool {
	return len(s) >= 4 && len(s) <= 40 && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F')
	})
}

// logRun 跑一次 git log 并解析：从 rev 往前读，rev 空时按 q 从 HEAD 或所有分支读；extra 是别的参数。
func logRun(ctx context.Context, root string, q LogQuery, rev string, extra ...string) ([]Commit, error) {
	args := append([]string{"log", "-z", logFormat}, extra...)
	switch {
	case rev != "":
		args = append(args, rev)
	case q.Rev != "":
		args = append(args, q.Rev)
	case q.All:
		args = append(args, "--branches", "--remotes", "--tags", "HEAD")
	default:
		args = append(args, "HEAD")
	}
	if q.Follow && q.Path != "" {
		args = append(args, "--follow")
	}
	args = append(args, "--")
	if q.Path != "" {
		args = append(args, q.Path)
	} else {
		args = append(args, q.Paths...)
	}
	out, err := RunCtx(ctx, root, args...)
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "bad revision 'HEAD'") ||
			strings.Contains(err.Error(), "unknown revision") {
			return nil, nil
		}
		return nil, err
	}
	var cs []Commit
	for _, rec := range bytes.Split(out, []byte{0}) {
		f := strings.Split(string(rec), "\x1f")
		if len(f) < 9 || f[0] == "" {
			continue
		}
		c := Commit{Hash: f[0], Short: f[1], Author: f[3], Email: f[4], Subject: f[7], Body: strings.TrimSpace(f[8])}
		c.Agent = AgentOf(c.Body)
		if f[2] != "" {
			c.Parents = strings.Fields(f[2])
		}
		c.Date, _ = time.Parse(time.RFC3339, f[5])
		for _, ref := range strings.Split(f[6], ", ") {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if strings.HasSuffix(ref, "/HEAD") {
				continue // origin/HEAD 只是远程默认分支的别名，和 origin/main 重复
			}
			if name, ok := strings.CutPrefix(ref, "HEAD -> "); ok {
				c.Refs = append([]string{name}, c.Refs...)
				continue
			}
			if ref == "HEAD" {
				continue
			}
			c.Refs = append(c.Refs, ref)
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// CommitFiles 列出一次提交改了哪些文件（合并提交按第一个父提交比）。
func CommitFiles(ctx context.Context, root string, c Commit) ([]Change, error) {
	parent := ""
	if len(c.Parents) > 0 {
		parent = c.Parents[0]
	}
	return diffTreeFiles(ctx, root, parent, c.Hash)
}

// DiffFiles 列出提交 from → to 改了哪些文件（只读），给"比较两个提交"用：Commit 是 to、Parent 是 from，Range 为真。
func DiffFiles(ctx context.Context, root, from, to string) ([]Change, error) {
	fs, err := diffTreeFiles(ctx, root, from, to)
	for i := range fs {
		fs[i].Range = true
	}
	return fs, err
}

// StashFiles 列出一条贮藏改了哪些文件（只读）：和它的第一个父提交（贮藏时的 HEAD）比；
// 有第三个父提交（stash -u / -a 存下的未跟踪文件，是个没有父提交的提交）时，那些文件当新加的接在后面，
// Commit 是那个提交。
func StashFiles(ctx context.Context, root string, s Stash) ([]Change, error) {
	out, err := RunCtx(ctx, root, "log", "-1", "--format=%P", s.Hash, "--")
	if err != nil {
		return nil, err
	}
	ps := strings.Fields(string(out))
	if len(ps) == 0 {
		return nil, fmt.Errorf(i18n.T("%s 没有父提交"), s.Ref)
	}
	files, err := diffTreeFiles(ctx, root, ps[0], s.Hash)
	if err != nil {
		return nil, err
	}
	if len(ps) > 2 {
		untracked, err := diffTreeFiles(ctx, root, "", ps[2])
		if err != nil {
			return nil, err
		}
		files = append(files, untracked...)
	}
	return files, nil
}

// diffTreeFiles 列出 from → to 改了哪些文件（diff-tree -r -M，只读），from 空是和空树比（--root）；
// 再跑一次 --numstat 补上行数，那次失败只是没数字。
func diffTreeFiles(ctx context.Context, root, from, to string) ([]Change, error) {
	revs := []string{from, to}
	if from == "" {
		revs = []string{"--root", to}
	}
	base := []string{"diff-tree", "-r", "-z", "-M", "--no-commit-id"}
	out, err := RunCtx(ctx, root, slices.Concat(base, []string{"--name-status"}, revs)...)
	if err != nil {
		return nil, err
	}
	var files []Change
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		st := string(recs[i])
		if st == "" || i+1 >= len(recs) {
			continue
		}
		i++
		ch := Change{Kind: InCommit, Code: st[0], Path: string(recs[i]), Commit: to, Parent: from}
		if (ch.Code == 'R' || ch.Code == 'C') && i+1 < len(recs) {
			i++
			ch.Orig, ch.Path = ch.Path, string(recs[i])
		}
		files = append(files, ch)
	}
	if out, err := RunCtx(ctx, root, slices.Concat(base, []string{"--numstat"}, revs)...); err == nil {
		addNumstat(files, out)
	}
	return files, nil
}

// addNumstat 把 diff-tree --numstat -z 的行数按路径填进 files。-z 下一条是 "加\t删\t路径\0"；
// 重命名 / 复制时路径那格是空的，后面跟 "旧\0新\0"；二进制的两个数都是 "-"。
func addNumstat(files []Change, out []byte) {
	at := make(map[string]int, len(files))
	for i, f := range files {
		at[f.Path] = i
	}
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		parts := strings.SplitN(string(recs[i]), "\t", 3)
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(recs) {
			path = string(recs[i+2])
			i += 2
		}
		j, ok := at[path]
		if !ok {
			continue
		}
		if parts[0] == "-" && parts[1] == "-" {
			files[j].Binary = true
			continue
		}
		files[j].Add, _ = strconv.Atoi(parts[0])
		files[j].Del, _ = strconv.Atoi(parts[1])
	}
}

// Ago 是中文的相对时间，如 "3 天前"。
func Ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return i18n.T("刚刚")
	case d < time.Hour:
		return i18n.Tn("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.Tn("%d 小时前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return i18n.Tn("%d 天前", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return i18n.Tn("%d 个月前", int(d.Hours()/24/30))
	default:
		return i18n.Tn("%d 年前", int(d.Hours()/24/365))
	}
}

// Short 是提交哈希的前 7 位。
func Short(hash string) string { return hash[:min(7, len(hash))] }

// Blob 是某个版本里一个文件的内容（只读）：rev 为 "HEAD"、提交哈希，或 "" 表示索引。
// 没有这个文件时为 nil，空文件为非 nil 的空切片。
func Blob(root, rev, rel string) []byte {
	out, err := Run(root, "cat-file", "blob", rev+":"+rel)
	if err != nil {
		return nil
	}
	if out == nil {
		out = []byte{}
	}
	return out
}

// LsFiles 列出已跟踪的和未被忽略的未跟踪文件（只读），去掉工作区里已经删掉的。
func LsFiles(ctx context.Context, root string) ([]string, error) {
	l, err := lsFiles(ctx, root, false)
	return l.Paths, err
}

// Listing 是 LsFilesModes 列的：Paths 同 LsFiles；Links 是其中索引里是符号链接的（mode 120000），
// Others 是其中未跟踪的（git 不记它们是什么，要知道是不是链接得自己看磁盘）。
type Listing struct{ Paths, Links, Others []string }

// LsFilesModes 同 LsFiles，另外分出符号链接和未跟踪的（文件树给链接画标识用）；还是一个 ls-files 进程，
// 加了 -s：跟踪的那项前面是"mode 对象 阶段\t"，未跟踪的只有路径。
func LsFilesModes(ctx context.Context, root string) (Listing, error) { return lsFiles(ctx, root, true) }

func lsFiles(ctx context.Context, root string, modes bool) (Listing, error) {
	args := []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}
	if modes {
		args = append(args, "-s")
	}
	out, err := RunCtx(ctx, root, args...)
	if err != nil {
		return Listing{}, err
	}
	gone := map[string]bool{}
	if deleted, err := RunCtx(ctx, root, "ls-files", "-z", "--deleted"); err == nil {
		for _, p := range bytes.Split(deleted, []byte{0}) {
			if len(p) > 0 {
				gone[string(p)] = true
			}
		}
	}
	var l Listing
	seen := make(map[string]bool, 1024)
	for _, p := range bytes.Split(out, []byte{0}) {
		tracked, link := false, false
		if modes {
			if mode, path, ok := stageEntry(p); ok {
				tracked, link, p = true, string(mode) == "120000", path
			}
		}
		s := string(p) // 只拷路径那段：前面的 mode 和对象名不跟着留在内存里
		if s == "" || gone[s] || seen[s] {
			continue
		}
		seen[s] = true
		l.Paths = append(l.Paths, s)
		switch {
		case link:
			l.Links = append(l.Links, s)
		case modes && !tracked:
			l.Others = append(l.Others, s)
		}
	}
	return l, nil
}

// stageEntry 拆开 ls-files -s 的一项"100644 对象名 0\t路径"；不是这个样子的（--others 列的未跟踪文件）ok 为假。
func stageEntry(p []byte) (mode, path []byte, ok bool) {
	tab := bytes.IndexByte(p, '\t')
	if tab < 10 || p[6] != ' ' || p[tab-2] != ' ' || p[tab-1] < '0' || p[tab-1] > '3' {
		return nil, nil, false
	}
	for _, c := range p[:6] {
		if c < '0' || c > '7' {
			return nil, nil, false
		}
	}
	return p[:6], p[tab+1:], true
}

// Listed 是 specs（仓库内的文件或目录，按字面）里 LsFiles 会列出来的文件：跟踪的、没被忽略的未跟踪的，
// 一个进程（增量刷新文件树时问新出现的文件和目录用）。未跟踪的嵌套仓库列成 "名字/"。
func Listed(ctx context.Context, root string, specs []string) ([]string, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	args := append([]string{"--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, specs...)
	out, err := RunCtx(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			paths = append(paths, string(p))
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// CheckIgnore 是 paths（仓库内路径）里被 .gitignore 忽略的（它自己或上层目录被忽略都算；目录按磁盘上的认，
// build/ 那样的规则对得上），一个进程：git check-ignore --stdin -z --no-index（不看索引，子模块里的路径也不报错）。
// 一个都没被忽略时 git 退出码 1，不算出错。
func CheckIgnore(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "check-ignore", "--stdin", "-z", "--no-index")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	out, err := cmd.Output()
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, ErrCanceled
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return nil, nil
	case err != nil:
		return nil, err
	}
	ig := map[string]bool{}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			ig[string(p)] = true
		}
	}
	return ig, nil
}

// LsIgnored 是被 .gitignore 忽略的未跟踪文件（只读的 ls-files --others --ignored --exclude-standard --directory）：
// 整个被忽略的目录只列它自己，名字后面带 /（node_modules/ 不往里列）。最多 MaxIgnored 项。
func LsIgnored(ctx context.Context, root string) ([]string, error) {
	out, err := RunCtx(ctx, root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			paths = append(paths, string(p))
		}
		if len(paths) >= MaxIgnored {
			break
		}
	}
	return paths, nil
}

// MaxIgnored 是 LsIgnored 最多列的项数（一个个列出来的被忽略文件太多时，再多的不显示）。
const MaxIgnored = 50000

// LsFilesIgnored 是被 .gitignore 忽略的未跟踪文件，一个个文件列出来（只读的 ls-files --others --ignored --exclude-standard，
// 不带 --directory：node_modules/ 也往里列；嵌套的仓库 git 只列 "名字/"，去掉）。搜索的"也搜被忽略的文件"用，
// 最多 MaxIgnoredFiles 个，truncated 为真时后面的没列。
func LsFilesIgnored(ctx context.Context, root string) (paths []string, truncated bool, err error) {
	out, err := RunCtx(ctx, root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return nil, false, err
	}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 || p[len(p)-1] == '/' {
			continue
		}
		if len(paths) >= MaxIgnoredFiles {
			return paths, true, nil
		}
		paths = append(paths, string(p))
	}
	return paths, false, nil
}

// MaxIgnoredFiles 是 LsFilesIgnored 最多列的文件数（几个 node_modules 加起来能有几十万个文件）。
const MaxIgnoredFiles = 200000

// Toplevel 是 dir 所在 Git 仓库的根（只读的 rev-parse），不在仓库里时报错。
func Toplevel(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf(i18n.T("%s 不是 Git 仓库（%s）"), dir, err)
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// NotRepo 是 dir 是个还在的目录、但不在任何 Git 仓库里（只读的 rev-parse）：普通文件夹。目录不在了、
// git 报别的错（权限、所有者不对……）都不算，那些照常当仓库的错显示。
func NotRepo(dir string) bool {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return false
	}
	_, err := Run(dir, "rev-parse", "--show-toplevel")
	return err != nil && strings.Contains(err.Error(), "not a git repository")
}

// GitDirs 是仓库的 git 目录和公共目录（绝对路径，只读的 rev-parse）。普通仓库两个都是 <根>/.git；
// 链接的工作树（git worktree）的 git 目录在主仓库的 .git/worktrees/<名字> 下，公共目录是主仓库的 .git。
func GitDirs(root string) (gitDir, commonDir string, err error) {
	out, err := Run(root, "rev-parse", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return "", "", fmt.Errorf(i18n.T("rev-parse 的输出不对：%q"), out)
	}
	gitDir, commonDir = filepath.Clean(lines[0]), lines[1]
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	return gitDir, filepath.Clean(commonDir), nil
}

// Ignored 是 root 下被 git 忽略的目录（相对 root、斜杠分隔、不带结尾的 /），只列最上面那层（只读）。
func Ignored(root string) (map[string]bool, error) {
	out, err := Run(root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil, err
	}
	dirs := map[string]bool{}
	for _, p := range bytes.Split(out, []byte{0}) {
		if s, ok := strings.CutSuffix(string(p), "/"); ok && s != "" {
			dirs[s] = true
		}
	}
	return dirs, nil
}
