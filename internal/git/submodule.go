package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是子模块（只读）：.gitmodules 里登记了哪些、父仓库记的是哪次提交、初始化了没有。子模块是另一个仓库，
// 文件树里不展开，要看里面的就把它添加为工作区里的仓库（照"所有仓库同时可见"）。

// MaxSubmodules 是最多列几个子模块。
const MaxSubmodules = 200

// Submodule 是 .gitmodules 里登记的一个子模块。
type Submodule struct {
	Path, Name, URL string
	Head            string // 父仓库索引里记的那次提交
	// State 是 git submodule status 的第一位：' ' 正常、'-' 未初始化、'+' 检出的不是记的那次、'U' 有冲突。
	State    byte
	Describe string // 记的那次提交的 git describe（括号里那段），可能为空
}

// Inited 是初始化过（检出了）。
func (s Submodule) Inited() bool { return s.State != '-' }

// StateText 是 State 的说法。
func (s Submodule) StateText() string {
	switch s.State {
	case '-':
		return i18n.T("未初始化")
	case '+':
		return i18n.T("检出的不是记录的")
	case 'U':
		return i18n.T("冲突")
	}
	return i18n.T("正常")
}

// ReadSubmodules 读仓库的子模块：没有 .gitmodules 时为 nil（不跑命令）。路径和地址来自
// `git config -f .gitmodules`，记的提交和状态来自 `git submodule status --cached`（都只读）。最多 MaxSubmodules 个。
func ReadSubmodules(ctx context.Context, root string) ([]Submodule, error) {
	if _, err := os.Stat(filepath.Join(root, ".gitmodules")); err != nil {
		return nil, nil
	}
	out, err := RunCtx(ctx, root, "config", "-f", ".gitmodules", "-z", "--get-regexp", `^submodule\..*\.(path|url)$`)
	if err != nil { // 一项都没有时 git config 也是退出码 1
		return nil, nil
	}
	var subs []Submodule
	byName := map[string]int{}
	for _, rec := range bytes.Split(out, []byte{0}) {
		key, val, ok := strings.Cut(string(rec), "\n")
		if !ok {
			continue
		}
		name, field, ok := cutLast(strings.TrimPrefix(key, "submodule."), ".")
		if !ok {
			continue
		}
		i, seen := byName[name]
		if !seen {
			if len(subs) == MaxSubmodules {
				continue
			}
			i = len(subs)
			byName[name] = i
			subs = append(subs, Submodule{Name: name, State: '-'})
		}
		switch field {
		case "path":
			subs[i].Path = val
		case "url":
			subs[i].URL = val
		}
	}
	// 没写 path 的不算（git 也不认）
	kept := subs[:0]
	for _, s := range subs {
		if s.Path != "" {
			kept = append(kept, s)
		}
	}
	subs = kept
	if len(subs) == 0 {
		return nil, nil
	}
	out, err = RunCtx(ctx, root, "submodule", "status", "--cached")
	if err != nil {
		return subs, err
	}
	byPath := map[string]int{}
	for i, s := range subs {
		byPath[s.Path] = i
	}
	for _, line := range strings.Split(string(out), "\n") {
		// <状态><哈希> <路径>[ (<describe>)]，路径里可以有空格
		if len(line) < 3 {
			continue
		}
		hash, rest, ok := strings.Cut(line[1:], " ")
		if !ok {
			continue
		}
		path, desc := rest, ""
		if _, known := byPath[path]; !known && strings.HasSuffix(rest, ")") {
			if j := strings.LastIndex(rest, " ("); j >= 0 {
				path, desc = rest[:j], rest[j+2:len(rest)-1]
			}
		}
		if i, known := byPath[path]; known {
			subs[i].State, subs[i].Head, subs[i].Describe = line[0], hash, desc
		}
	}
	return subs, nil
}

// cutLast 在 s 里最后一个 sep 处切开（子模块的名字里可以有点）。
func cutLast(s, sep string) (before, after string, ok bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// SubText 是 git status 里子模块那三位（Change.Sub）的说法：提交变了 / 里面有改动 / 有未跟踪的。
func SubText(sub string) string {
	var parts []string
	if strings.Contains(sub, "C") {
		parts = append(parts, i18n.T("检出的提交变了"))
	}
	if strings.Contains(sub, "M") {
		parts = append(parts, i18n.T("里面有改动"))
	}
	if strings.Contains(sub, "U") {
		parts = append(parts, i18n.T("里面有未跟踪的文件"))
	}
	return i18n.List(parts)
}

// subDiffNote 是子模块那一项的 Diff 标签里显示的话：git diff 对子模块只有两个提交（"Subproject commit"），
// 写成"子模块 lib：abc1234 → def5678"，里面有改动的也说一声。ok 为 false 时 ch 不是子模块。
func subDiffNote(ctx context.Context, root string, ch Change) (note string, ok bool) {
	rev := func(spec string) string {
		out, err := RunCtx(ctx, root, "rev-parse", "-q", "--verify", spec)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	isLink := func(commit string) bool { // 子模块记的提交不在父仓库里，cat-file 看不了，看树里的模式
		out, err := RunCtx(ctx, root, "ls-tree", "-z", commit, "--", ch.Path)
		return err == nil && bytes.HasPrefix(out, []byte("160000 "))
	}
	var from, to string
	switch ch.Kind {
	case Staged:
		if ch.Sub == "" {
			return "", false
		}
		from, to = rev("HEAD:"+ch.Path), rev(":"+ch.Path)
	case Unstaged:
		if ch.Sub == "" {
			return "", false
		}
		from = rev(":" + ch.Path)
		if out, err := RunCtx(ctx, filepath.Join(root, filepath.FromSlash(ch.Path)), "rev-parse", "-q", "--verify", "HEAD"); err == nil {
			to = strings.TrimSpace(string(out))
		}
	case InCommit:
		in := ch.Commit
		if ch.Code == 'D' {
			in = ch.Parent
		}
		if in == "" || !isLink(in) {
			return "", false
		}
		if ch.Parent != "" && ch.Code != 'A' {
			from = rev(ch.Parent + ":" + ch.Path)
		}
		if ch.Code != 'D' {
			to = rev(ch.Commit + ":" + ch.Path)
		}
	default:
		return "", false
	}
	short := func(h string) string {
		if h == "" {
			return i18n.T("（没有）")
		}
		return Short(h)
	}
	note = i18n.Tf("子模块 %s：%s → %s", ch.Path, short(from), short(to))
	if from == to && from != "" {
		note = i18n.Tf("子模块 %s：%s", ch.Path, short(from))
	}
	if s := SubText(strings.ReplaceAll(ch.Sub, "C", "")); s != "" && ch.Kind == Unstaged { // 里面的改动只在工作区那边
		note += i18n.Tf("（%s）", s)
	}
	return note, true
}
