package git

import (
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/i18n"
)

// 命令面板 >git 跑的命令不弹密码提示、不开编辑器（RunOp 的环境），要人输入的命令在那里跑不成：
// commit 没写信息就放弃、add -p 什么都不做、rebase -i 按原清单直接跑完。NeedsTerminal 认出这些，
// 应用把它们转到终端里跑。

// NeedsTerminal 是 git 参数 args（SplitArgs 拆好的，不带开头的 git）要人输入的原因，不用的为 ""。
// 开头的 -c / -C 这些全局参数先剥掉再看子命令；别名不展开。
func NeedsTerminal(args []string) string {
	args = stripGlobal(args)
	if len(args) == 0 {
		return ""
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "commit":
		o := parseOpts(rest, "mFCct", "message", "file", "reuse-message", "reedit-message", "template", "author", "date",
			"cleanup", "fixup", "squash", "trailer", "pathspec-from-file")
		if o.any("p", "patch", "interactive") {
			return i18n.T("要一块块选")
		}
		fixup, _ := o.value("fixup")
		msg := o.any("m", "F", "C", "message", "file", "reuse-message", "no-edit") ||
			o.has("fixup") && !strings.HasPrefix(fixup, "amend:") && !strings.HasPrefix(fixup, "reword:")
		if !o.has("dry-run") && (!msg || o.any("e", "edit", "c", "reedit-message")) {
			return i18n.T("要写提交信息")
		}
	case "add":
		o := parseOpts(rest, "", "chmod", "pathspec-from-file")
		if o.any("p", "i", "patch", "interactive") {
			return i18n.T("要一块块选")
		}
		if o.any("e", "edit") {
			return i18n.T("要在编辑器里改信息")
		}
	case "checkout", "reset", "restore":
		o := parseOpts(rest, "bBs", "orphan", "source", "conflict", "pathspec-from-file")
		if o.any("p", "patch") {
			return i18n.T("要一块块选")
		}
	case "stash":
		o := parseOpts(rest, "m", "message", "pathspec-from-file")
		if s := o.first(); (s == "" || s == "push" || s == "save") && o.any("p", "patch") { // stash show -p 是看 diff
			return i18n.T("要一块块选")
		}
	case "rebase":
		o := parseOpts(rest, "sXx", "onto", "strategy", "strategy-option", "exec")
		if o.any("i", "interactive", "edit-todo") {
			return i18n.T("要编辑变基清单")
		}
	case "merge":
		o := parseOpts(rest, "mFsX", "message", "file", "strategy", "strategy-option", "cleanup", "into-name")
		if o.any("e", "edit") {
			return i18n.T("要在编辑器里改信息")
		}
	case "tag":
		o := parseOpts(rest, "mFu", "message", "file", "local-user", "cleanup", "sort", "format", "contains", "no-contains",
			"merged", "no-merged", "points-at", "color", "column", "trailer")
		if o.any("e", "edit") || o.any("a", "s", "u", "annotate", "sign", "local-user") && !o.any("m", "F", "message", "file", "d", "delete", "l", "list", "v", "verify") {
			return i18n.T("要在编辑器里改信息")
		}
	case "cherry-pick", "revert":
		o := parseOpts(rest, "mX", "mainline", "strategy", "strategy-option", "cleanup")
		if o.any("e", "edit") {
			return i18n.T("要在编辑器里改信息")
		}
		if sub == "revert" && !o.any("no-edit", "n", "no-commit", "continue", "abort", "skip", "quit") {
			return i18n.T("要在编辑器里改信息")
		}
	case "notes":
		o := parseOpts(rest, "mFCc", "message", "file", "reuse-message", "reedit-message", "ref")
		switch o.first() {
		case "add", "edit", "append":
			if !o.any("m", "F", "C", "message", "file", "reuse-message") || o.any("c", "reedit-message") {
				return i18n.T("要在编辑器里改信息")
			}
		}
	case "am", "clean":
		if o := parseOpts(rest, "", "exclude", "directory", "patch-format"); o.any("i", "interactive") {
			return i18n.T("要交互")
		}
	case "bisect":
		if o := parseOpts(rest, ""); o.first() != "run" {
			return i18n.T("要交互")
		}
	case "mergetool":
		return i18n.T("要交互")
	case "config":
		if o := parseOpts(rest, "f", "file", "blob", "type"); o.any("e", "edit") || o.first() == "edit" {
			return i18n.T("要在编辑器里改信息")
		}
	}
	return ""
}

// stripGlobal 去掉子命令前面的全局参数（-c 名=值、-C 目录、--git-dir 目录……）。
func stripGlobal(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-c", "-C", "--git-dir", "--work-tree", "--namespace", "--config-env", "--super-prefix":
			if len(args) < 2 {
				return nil
			}
			args = args[2:]
		default:
			args = args[1:]
		}
	}
	return args
}

// opts 是一条子命令的参数里出现过的选项：短的按字母、长的按 = 前面的名字；还有不是选项的参数。
type opts struct {
	short map[byte]bool
	long  map[string]string
	pos   []string
}

// parseOpts 收集 args 里的选项，到 "--" 为止。短选项可以连写（-am）；takes 里的短选项后面是值（连写时
// 剩下的字是值，单独写时下一个参数是值）。valued 里的长选项不带 = 时下一个参数是值。
func parseOpts(args []string, takes string, valued ...string) opts {
	o := opts{short: map[byte]bool{}, long: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			o.pos = append(o.pos, args[i+1:]...)
			return o
		case strings.HasPrefix(a, "--"):
			name, val, eq := strings.Cut(a[2:], "=")
			if !eq && i+1 < len(args) && slices.Contains(valued, name) {
				i++
				val = args[i]
			}
			o.long[name] = val
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				o.short[a[j]] = true
				if strings.IndexByte(takes, a[j]) >= 0 {
					if j == len(a)-1 && i+1 < len(args) {
						i++
					}
					break
				}
			}
		default:
			o.pos = append(o.pos, a)
		}
	}
	return o
}

// has 是长选项 name 出现过。
func (o opts) has(name string) bool {
	_, ok := o.long[name]
	return ok
}

// value 是长选项 name 的值。
func (o opts) value(name string) (string, bool) {
	v, ok := o.long[name]
	return v, ok
}

// any 是 names 里有一个出现过：一个字的是短选项，别的是长选项。
func (o opts) any(names ...string) bool {
	for _, n := range names {
		if len(n) == 1 && o.short[n[0]] || len(n) > 1 && o.has(n) {
			return true
		}
	}
	return false
}

// first 是第一个不是选项的参数（stash push、notes add 这类的二级子命令），没有为 ""。
func (o opts) first() string {
	if len(o.pos) == 0 {
		return ""
	}
	return o.pos[0]
}
