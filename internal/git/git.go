// Package git 包一层 git 命令行：只读的 status / diff / log / branches / blob（Run），
// 和会改仓库的写操作（RunOp：非交互、有超时、能取消）。写操作只在临时仓库上测。
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/i18n"
)

// 超过这个大小的文件不加载 diff，避免原型里一次性读入大文件。
const MaxDiffBytes = 2 << 20

// Kind 是变更列表的分组。
type Kind int

const (
	Staged    Kind = iota // 索引相对 HEAD 的改动
	Unstaged              // 工作区相对索引的改动
	Untracked             // 未跟踪
	InCommit              // 历史里某次提交改的文件
	Conflict              // 合并 / 变基 / 拣选冲突、还没解决的文件
)

func (k Kind) String() string {
	switch k {
	case Staged:
		return i18n.T("已暂存")
	case Unstaged:
		return i18n.T("未暂存")
	case InCommit:
		return i18n.T("提交")
	case Conflict:
		return i18n.T("冲突")
	default:
		return i18n.T("未跟踪")
	}
}

// Change 是变更列表里的一项。同一个文件既有暂存又有未暂存改动时出现两次。
type Change struct {
	Kind Kind
	Path string // 相对仓库根；重命名时是新路径
	Orig string // 重命名/复制时的原路径
	Code byte   // M A D R C T U ?（冲突的都是 U）
	Sub  string // 子模块：git status 里那三位（C 检出的提交变了、M 里面有改动、U 有未跟踪的，没有的是 .）；不是子模块为空
	Dir  bool   // 整个未跟踪的目录：git status 不带 -uall 时折叠成一项（Path 带尾斜杠，如 "harness/"），里面的文件见 Status.DirFiles

	Commit, Parent string // InCommit：哪次提交、和哪个父提交比（根提交为空）
	Range          bool   // InCommit：Parent 不是 Commit 的父提交，是"比较两个提交"的起点（DiffFiles）

	// Add / Del 是加了、删了几行（diff-tree --numstat，只有 InCommit 有）；Binary 是二进制，没有行数。
	Add, Del int
	Binary   bool
}

// Status 是一个仓库的 git status。
type Status struct {
	Branch        string
	Ahead, Behind int
	// Count 与 `git status --porcelain` 的行数一致：每个路径记一次，
	// 不管它同时有暂存和未暂存改动。
	Count                                  int
	Conflicts, Staged, Unstaged, Untracked int
	Changes                                []Change // 先冲突，再已暂存、未暂存，最后未跟踪
	// DirFiles 是每个未跟踪目录（Dir 的那项，键是它的 Path）里没被忽略的文件：相对仓库根、排好序，最多 MaxDirList 个，
	// 超了的目录 DirMore 记一下。是读完状态后另外列的（ListUntrackedDirs），没列到的目录没有键。
	DirFiles map[string][]string
	DirMore  map[string]bool
}

// Run 在仓库里跑一条只读的 git 命令。
// GIT_OPTIONAL_LOCKS=0 让 git status 不去刷新磁盘上的索引（不写 index.lock）。
func Run(root string, args ...string) ([]byte, error) {
	return RunCtx(context.Background(), root, args...)
}

// RunCtx 同 Run，ctx 取消时杀掉 git（切工作区、关窗口时没读完的状态、diff、历史）。
func RunCtx(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return out, ErrCanceled
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New(msg)
	}
	return out, nil
}

// ReadStatus 解析 `git status --porcelain=v2 --branch -z`。
// 不加 -uall：未跟踪目录折叠成一项，和 `git status --porcelain` 的计数一致。
func ReadStatus(ctx context.Context, root string) (Status, error) {
	out, err := RunCtx(ctx, root, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return Status{}, err
	}
	var st Status
	var conflicts, staged, unstaged, untracked []Change
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := string(recs[i])
		switch {
		case rec == "":
		case strings.HasPrefix(rec, "# branch.head "):
			st.Branch = strings.TrimPrefix(rec, "# branch.head ")
		case strings.HasPrefix(rec, "# branch.ab "):
			fmt.Sscanf(strings.TrimPrefix(rec, "# branch.ab "), "+%d -%d", &st.Ahead, &st.Behind)
		case strings.HasPrefix(rec, "# "), strings.HasPrefix(rec, "! "):
			// 其他头部、被忽略的文件
		case strings.HasPrefix(rec, "? "):
			st.Count++
			untracked = append(untracked, Change{Kind: Untracked, Path: rec[2:], Code: '?', Dir: strings.HasSuffix(rec, "/")})
		case strings.HasPrefix(rec, "1 ") && len(rec) > 4:
			st.Count++
			x, y := rec[2], rec[3]
			path, sub := nthField(rec, 8), subField(rec)
			if x != '.' {
				staged = append(staged, Change{Kind: Staged, Path: path, Code: x, Sub: sub})
			}
			if y != '.' {
				unstaged = append(unstaged, Change{Kind: Unstaged, Path: path, Code: y, Sub: sub})
			}
		case strings.HasPrefix(rec, "2 ") && len(rec) > 4:
			st.Count++
			x, y := rec[2], rec[3]
			path, sub := nthField(rec, 9), subField(rec)
			orig := ""
			if i+1 < len(recs) {
				i++
				orig = string(recs[i])
			}
			if x != '.' {
				staged = append(staged, Change{Kind: Staged, Path: path, Orig: orig, Code: x, Sub: sub})
			}
			if y != '.' {
				unstaged = append(unstaged, Change{Kind: Unstaged, Path: path, Orig: orig, Code: y, Sub: sub})
			}
		case strings.HasPrefix(rec, "u "):
			st.Count++
			conflicts = append(conflicts, Change{Kind: Conflict, Path: nthField(rec, 10), Code: 'U'})
		}
	}
	if st.Branch == "" || st.Branch == "(detached)" {
		if out, err := RunCtx(ctx, root, "rev-parse", "--short", "HEAD"); err == nil {
			st.Branch = "(detached " + strings.TrimSpace(string(out)) + ")"
		}
	}
	st.Conflicts, st.Staged, st.Unstaged, st.Untracked = len(conflicts), len(staged), len(unstaged), len(untracked)
	st.Changes = slices.Concat(conflicts, staged, unstaged, untracked)
	return st, nil
}

// MaxDirList 是一个未跟踪目录最多列几个文件（Status.DirFiles），多了只留前这么多、DirMore 记一下。是变量好让测试压小。
var MaxDirList = 2000

// HasDirs 是状态里有整个未跟踪的目录（Change.Dir）。
func (st Status) HasDirs() bool {
	return slices.ContainsFunc(st.Changes, func(ch Change) bool { return ch.Dir })
}

// ListUntrackedDirs 列出 st 里每个未跟踪目录里没被忽略的文件（Status.DirFiles / DirMore 的内容）：一个只读的
// git ls-files --others --exclude-standard -z，目录都作为路径给它（按字面，不当通配）。列过的目录都有键，没有文件是空的。
// 嵌套的仓库 git 只列成 "名字/"，照样算一项；目录自己就是嵌套的仓库时它是空的。
func ListUntrackedDirs(ctx context.Context, root string, st Status) (files map[string][]string, more map[string]bool, err error) {
	var dirs []string
	for _, ch := range st.Changes {
		if ch.Dir {
			dirs = append(dirs, ch.Path)
		}
	}
	if len(dirs) == 0 {
		return nil, nil, nil
	}
	out, err := RunCtx(ctx, root, append([]string{"--literal-pathspecs", "ls-files", "--others", "--exclude-standard", "-z", "--"}, dirs...)...)
	if err != nil {
		return nil, nil, err
	}
	slices.Sort(dirs)
	files = make(map[string][]string, len(dirs))
	for _, d := range dirs {
		files[d] = []string{}
	}
	for _, b := range bytes.Split(out, []byte{0}) {
		p := string(b)
		// 状态里的未跟踪目录互不包含，按字节序在 p 前面最近的那个就是它所在的目录
		i, self := slices.BinarySearch(dirs, p)
		if p == "" || self || i == 0 || !strings.HasPrefix(p, dirs[i-1]) {
			continue
		}
		files[dirs[i-1]] = append(files[dirs[i-1]], p)
	}
	for d, fs := range files {
		slices.Sort(fs)
		if len(fs) > MaxDirList {
			files[d] = slices.Clone(fs[:MaxDirList]) // 不留着整个大数组
			if more == nil {
				more = map[string]bool{}
			}
			more[d] = true
		}
	}
	return files, more, nil
}

// subField 是 porcelain v2 记录第 3 个字段里子模块的那三位（"S" 开头时，见 Change.Sub），不是子模块为空。
func subField(rec string) string {
	f, _, _ := strings.Cut(nthField(rec, 2), " ")
	if len(f) == 4 && f[0] == 'S' {
		return f[1:]
	}
	return ""
}

// nthField 返回记录里第 n 个空格之后的全部内容（n 从 0 数），路径里可以有空格。
func nthField(rec string, n int) string {
	for i := 0; i < n; i++ {
		j := strings.IndexByte(rec, ' ')
		if j < 0 {
			return ""
		}
		rec = rec[j+1:]
	}
	return rec
}

// DiffContent 是一项变更的两侧内容。
type DiffContent struct {
	Old, New         []byte
	OldName, NewName string
	Binary, TooLarge bool
	Note             string // 不能显示 diff 时的说明
	Submodule        bool   // 这一项是子模块（Note 写着两个提交）
}

var errTooLarge = errors.New("too large")

// LoadDiffContent 读取变更的两侧：
// 已暂存比较 HEAD 与索引，未暂存比较索引与工作区，未跟踪整个文件算新增，
// 冲突比较 HEAD 与工作区（索引里有多个版本；Code 'S' 比较两边：:2 我们的与 :3 他们的），历史里的文件比较父提交与该提交。
func LoadDiffContent(ctx context.Context, root string, ch Change) (DiffContent, error) {
	return LoadDiffContentMax(ctx, root, ch, MaxDiffBytes)
}

// LoadDiffContentMax 和 LoadDiffContent 一样，但每边最多 limit 字节（图片 diff 放宽到 8 MB）。
func LoadDiffContentMax(ctx context.Context, root string, ch Change, limit int) (DiffContent, error) {
	d := DiffContent{OldName: ch.Path, NewName: ch.Path}
	if ch.Orig != "" {
		d.OldName = ch.Orig
	}
	var err error
	if ch.Sub != "" {
		d.Note, d.Submodule = subDiffNote(ctx, root, ch)
		return d, nil
	}
	sub := func(err error) (DiffContent, error) { // 提交里的子模块：记的提交不在这个仓库里，cat-file 读不了
		if note, ok := subDiffNote(ctx, root, ch); ok {
			return DiffContent{OldName: d.OldName, NewName: d.NewName, Note: note, Submodule: true}, nil
		}
		return d, err
	}
	switch ch.Kind {
	case InCommit:
		if ch.Code != 'A' && ch.Parent != "" {
			if d.Old, err = RunCtx(ctx, root, "cat-file", "blob", ch.Parent+":"+d.OldName); err != nil {
				return sub(err)
			}
		}
		if ch.Code != 'D' {
			if d.New, err = RunCtx(ctx, root, "cat-file", "blob", ch.Commit+":"+ch.Path); err != nil {
				return sub(err)
			}
		}
	case Untracked:
		d.New, err = readWorkFile(root, ch.Path, limit)
	case Staged:
		if ch.Code != 'A' {
			if d.Old, err = RunCtx(ctx, root, "cat-file", "blob", "HEAD:"+d.OldName); err != nil {
				return d, err
			}
		}
		if ch.Code != 'D' {
			d.New, err = RunCtx(ctx, root, "cat-file", "blob", ":"+ch.Path)
		}
	case Conflict:
		if ch.Code == 'S' {
			err = loadStages(ctx, root, ch.Path, &d)
			break
		}
		d.Old, _ = RunCtx(ctx, root, "cat-file", "blob", "HEAD:"+ch.Path) // 两边都新加的文件 HEAD 里没有
		d.New, err = readWorkFile(root, ch.Path, limit)
		if errors.Is(err, os.ErrNotExist) { // 一边删了、一边改了，用户选了删
			err = nil
		}
	case Unstaged:
		if d.Old, err = RunCtx(ctx, root, "cat-file", "blob", ":"+ch.Path); err != nil {
			return d, err
		}
		if ch.Code != 'D' {
			d.New, err = readWorkFile(root, ch.Path, limit)
		}
	}
	switch {
	case errors.Is(err, errTooLarge):
		d.TooLarge = true
		return d, nil
	case err != nil:
		var pe *os.PathError
		if errors.As(err, &pe) {
			if fi, statErr := os.Stat(filepath.Join(root, ch.Path)); statErr == nil && fi.IsDir() {
				d.Note = i18n.T("这是一个未跟踪目录，git status 把它折叠成一项（展开需要 -uall）")
				return d, nil
			}
		}
		return d, err
	}
	if len(d.Old) > limit || len(d.New) > limit {
		d.TooLarge = true
		return d, nil
	}
	d.Binary = IsBinary(d.Old) || IsBinary(d.New)
	return d, nil
}

// loadStages 读冲突文件索引里的两边：:2 是我们的（HEAD 那边），:3 是他们的。一边没有（那边删了、或只有一边加了）
// 就留空，Note 说明——界面显示这句话，不显示一边空的 diff。
func loadStages(ctx context.Context, root, rel string, d *DiffContent) error {
	out, err := RunCtx(ctx, root, "ls-files", "-z", "-u", "--", rel)
	if err != nil {
		return err
	}
	var has [4]bool
	for _, rec := range strings.Split(string(out), "\x00") {
		// <mode> <hash> <stage>\t<path>
		if meta, _, ok := strings.Cut(rec, "\t"); ok {
			if f := strings.Fields(meta); len(f) == 3 && len(f[2]) == 1 && f[2][0] >= '1' && f[2][0] <= '3' {
				has[f[2][0]-'0'] = true
			}
		}
	}
	if has[2] {
		if d.Old, err = RunCtx(ctx, root, "cat-file", "blob", ":2:"+rel); err != nil {
			return err
		}
	}
	if has[3] {
		if d.New, err = RunCtx(ctx, root, "cat-file", "blob", ":3:"+rel); err != nil {
			return err
		}
	}
	switch {
	case has[2] && !has[3]:
		d.Note = i18n.T("他们那边删了这个文件")
	case !has[2] && has[3]:
		d.Note = i18n.T("我们这边没有这个文件")
	case !has[2] && !has[3]:
		d.Note = i18n.T("索引里已经没有这个文件的冲突了")
	}
	return nil
}

func ReadWorkFile(root, rel string) ([]byte, error) { return readWorkFile(root, rel, MaxDiffBytes) }

func readWorkFile(root, rel string, limit int) ([]byte, error) {
	p := filepath.Join(root, rel)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if fi.Size() > int64(limit) {
		return nil, errTooLarge
	}
	return os.ReadFile(p)
}

func IsBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}
