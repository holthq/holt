// Package globs 是文件路径的 glob 规则：全文搜索的“包含 / 排除”和文件树的 files.exclude 共用。
// ** 跨目录，* 和 ? 不跨，{a,b} 任选其一，不分大小写；一串规则用逗号分开（花括号里的逗号不算）。
package globs

import (
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/i18n"
)

// Glob 是一条规则。
type Glob struct {
	re      *regexp.Regexp
	segment bool // 不带 /：Match 时比文件名和每一级目录名
}

// Set 是一组规则，合任一条就算。
type Set []Glob

// Compile 编译逗号分开的一串规则（空的、只有空白的跳过；开头的 ./ 和 /、结尾的 / 去掉）。
func Compile(list string) (Set, error) {
	return CompileList(Split(list))
}

// CompileList 编译一条条规则（files.exclude 的键是一条一条的，不再按逗号拆）。
func CompileList(pats []string) (Set, error) {
	var out Set
	for _, p := range pats {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "./")
		if p == "" {
			continue
		}
		g := Glob{segment: !strings.Contains(strings.TrimSuffix(p, "/"), "/")}
		p = strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
		re, err := regexp.Compile("(?i)^" + expr(p) + "$")
		if err != nil {
			return nil, errors.New(i18n.Tf("glob 写错了：%s", p))
		}
		g.re = re
		out = append(out, g)
	}
	return out, nil
}

// Split 按逗号拆开一串 glob，花括号里的逗号不算。
func Split(list string) []string {
	var out []string
	depth, from := 0, 0
	for i := 0; i < len(list); i++ {
		switch list[i] {
		case '{':
			depth++
		case '}':
			depth = max(0, depth-1)
		case ',':
			if depth == 0 {
				out = append(out, list[from:i])
				from = i + 1
			}
		}
	}
	return append(out, list[from:])
}

// expr 把 glob 转成正则：** 跨目录，* 和 ? 不跨，{a,b} 任选其一。
func expr(p string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '*' && strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			b.WriteString("(?:")
			depth++
		case c == '}' && depth > 0:
			b.WriteString(")")
			depth--
		case c == ',' && depth > 0:
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	for ; depth > 0; depth-- {
		b.WriteString(")")
	}
	return b.String()
}

// Match 是全文搜索的比法：rel（仓库 repo 里的路径）合不合任一条——不带 / 的比每一级名字，
// 带 / 的比路径和它的每一级目录（也试试带仓库名开头的）。
func (s Set) Match(repo, rel string) bool {
	for _, g := range s {
		if g.segment {
			for _, seg := range strings.Split(rel, "/") {
				if g.re.MatchString(seg) {
					return true
				}
			}
			continue
		}
		for _, p := range []string{rel, repo + "/" + rel} {
			if g.under(p) {
				return true
			}
		}
	}
	return false
}

// MatchRoot 是 files.exclude 的比法（VS Code 的）：规则都从仓库根算起，不带 / 的也一样（"node_modules" 只是根下那个，
// 任何一级的要写 "**/node_modules"）；rel 自己或它的哪一级目录合了就算（目录合了，底下整棵都算）。
func (s Set) MatchRoot(rel string) bool {
	for _, g := range s {
		if g.under(rel) {
			return true
		}
	}
	return false
}

// under 是 p 或它的某一级上级目录合这条规则。
func (g Glob) under(p string) bool {
	for q := p; q != "." && q != "/" && q != ""; q = path.Dir(q) {
		if g.re.MatchString(q) {
			return true
		}
	}
	return false
}

// self 是 p 自己合不合任一条（不看上级目录）。
func (s Set) self(p string) bool {
	for _, g := range s {
		if g.re.MatchString(p) {
			return true
		}
	}
	return false
}

// Matcher 是带缓存的 MatchRoot：同一个目录只比一次，整份文件列表过一遍时用。不能几个 goroutine 一起用。
type Matcher struct {
	s    Set
	dirs map[string]bool
}

// Matcher 是这组规则的一个新的带缓存的比法。
func (s Set) Matcher() *Matcher { return &Matcher{s: s, dirs: map[string]bool{}} }

// Match 同 MatchRoot（末尾的 / 不算：ls-files 把未跟踪的嵌套仓库列成 "x/"）。
func (m *Matcher) Match(rel string) bool {
	if len(m.s) == 0 {
		return false
	}
	rel = strings.TrimSuffix(rel, "/")
	if d := path.Dir(rel); d != "." && m.dir(d) {
		return true
	}
	return m.s.self(rel)
}

func (m *Matcher) dir(d string) bool {
	if v, ok := m.dirs[d]; ok {
		return v
	}
	p := path.Dir(d)
	v := (p != "." && m.dir(p)) || m.s.self(d)
	m.dirs[d] = v
	return v
}

// Filter 原地去掉 paths 里合的，返回留下的和去掉了几个。
func (m *Matcher) Filter(paths []string) ([]string, int) {
	if len(m.s) == 0 {
		return paths, 0
	}
	n := len(paths)
	paths = slices.DeleteFunc(paths, m.Match)
	return paths, n - len(paths)
}
