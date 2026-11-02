// Package snippets 是代码片段：VS Code 片段 JSON 的子集——认 $n、${n}、${n:默认}（默认里可以再套）、$0 和
// \ 转义（\$ \} \\），不认变量（$TM_FILENAME）、选择项（${1|a,b|}）、变换，这些照原样留在文字里。
// 内置的几种语言在 builtin/ 里内嵌；用户的在 <状态目录>/snippets/<语言>.json（语言是编辑器语言名的小写：go.json、
// python.json…；global.json 所有语言都有），文件里的同名覆盖内置。纯函数和一份读好的表，不碰界面；
// 补全、跳位、命令面板、设置页在 app/snippets.go 和 ui。
package snippets

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/i18n"
)

//go:embed builtin/*.json
var builtinFS embed.FS

// Global 是所有语言都有的那份（global.json）。
const Global = "global"

// alias 是借别的语言的片段：TypeScript 也有 JavaScript 的（内置只写了一份 javascript.json）。
var alias = map[string]string{"typescript": "javascript"}

// Snippet 是一条片段。前缀写成数组的，每个前缀各算一条。
type Snippet struct {
	Name   string // JSON 里的键
	Prefix string // 敲什么出它
	Body   string // 原文，几行用 \n 连起来
	Desc   string // description；内置的是中文原文，显示用 Description
	Lang   string // 文件名去掉 .json：go、python、global…
	File   bool   // 从片段目录的文件来（否则是内置的）
}

// builtinDescs 是 builtin/*.json 里的说明：写在这里让 i18n 收进文案表（Description 过 i18n.T），TestBuiltins 对着 JSON 查。
var builtinDescs = []string{
	"for 循环", "for range 循环", "检查错误", "函数", "方法", "结构体", "main 包和 main 函数", "测试函数",
	"类", "直接运行时才跑", "with 语句", "箭头函数", "打印到控制台", "if 语句", "case 语句", "链接", "图片", "代码块", "表格",
}

// Description 是显示的说明：内置的过 i18n，文件里的照写的；没写说明的是名字。
func (s Snippet) Description() string {
	switch {
	case s.Desc == "":
		return s.Name
	case !s.File:
		return i18n.T(s.Desc)
	}
	return s.Desc
}

// Set 是读好的一份：每种语言的片段（内置的和片段目录里的合起来，同名的文件优先），按前缀排。
type Set struct {
	Dir  string
	Errs []string // 读不了、解析不了的文件："go.json：…"
	by   map[string][]Snippet
	sig  string
}

// LangKey 是编辑器语言名对应的文件名（不带 .json）：小写；没有语言（纯文本）是 ""。
func LangKey(name string) string { return strings.ToLower(name) }

// Load 读内置的和 dir 里的 *.json（dir 为空或不存在只有内置的）。
func Load(dir string) *Set {
	s := &Set{Dir: dir, by: map[string][]Snippet{}, sig: Sig(dir)}
	ents, _ := builtinFS.ReadDir("builtin")
	for _, e := range ents {
		b, _ := builtinFS.ReadFile("builtin/" + e.Name())
		list, err := Parse(b)
		if err != nil {
			panic(fmt.Sprintf("snippets: builtin/%s: %v", e.Name(), err))
		}
		s.add(strings.TrimSuffix(e.Name(), ".json"), list, false)
	}
	if dir != "" {
		names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, p := range names {
			name := filepath.Base(p)
			b, err := os.ReadFile(p)
			if err == nil {
				var list []Snippet
				if list, err = Parse(b); err == nil {
					s.add(strings.TrimSuffix(name, ".json"), list, true)
					continue
				}
			}
			s.Errs = append(s.Errs, name+": "+err.Error())
		}
	}
	for lang, list := range s.by {
		slices.SortStableFunc(list, func(a, b Snippet) int { return strings.Compare(a.Prefix, b.Prefix) })
		s.by[lang] = list
	}
	return s
}

// add 把一种语言的一批放进来：文件里的和已有的同名时换掉已有的。
func (s *Set) add(lang string, list []Snippet, file bool) {
	lang = strings.ToLower(lang)
	cur := s.by[lang]
	if file {
		names := map[string]bool{}
		for _, sn := range list {
			names[sn.Name] = true
		}
		cur = slices.DeleteFunc(cur, func(sn Snippet) bool { return names[sn.Name] })
	}
	for _, sn := range list {
		sn.Lang, sn.File = lang, file
		cur = append(cur, sn)
	}
	s.by[lang] = cur
}

// For 是 lang（LangKey）能用的：这种语言的、借来的（alias）、所有语言的。
func (s *Set) For(lang string) []Snippet {
	var out []Snippet
	if lang != "" {
		out = append(out, s.by[lang]...)
		if a := alias[lang]; a != "" {
			out = append(out, s.by[a]...)
		}
	}
	return append(out, s.by[Global]...)
}

// Langs 是有片段的语言，所有语言的那份在最前，别的按名字排。
func (s *Set) Langs() []string {
	var out []string
	for lang := range s.by {
		if lang != Global {
			out = append(out, lang)
		}
	}
	slices.Sort(out)
	if len(s.by[Global]) > 0 {
		out = slices.Insert(out, 0, Global)
	}
	return out
}

// Of 是 lang 自己那份（不带借来的、所有语言的）：设置页一种语言一节。
func (s *Set) Of(lang string) []Snippet { return s.by[lang] }

// Stale 是片段目录里的文件变了（多了、少了、改了），该重读。
func (s *Set) Stale() bool { return Sig(s.Dir) != s.sig }

// Sig 是 dir 里 *.json 的名字、大小、修改时间连起来：变了就是文件变了。
func Sig(dir string) string {
	if dir == "" {
		return ""
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d\n", e.Name(), fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return b.String()
}

// Parse 读一个片段文件：{"名字": {"prefix": "…" 或 […], "body": "…" 或 […], "description": "…"}}。
// 没有 prefix 的用名字；没有 body 的不算。
func Parse(b []byte) ([]Snippet, error) {
	var raw map[string]struct {
		Prefix      json.RawMessage `json:"prefix"`
		Body        json.RawMessage `json:"body"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []Snippet
	for name, r := range raw {
		body, err := strOrList(r.Body)
		if err != nil {
			return nil, fmt.Errorf("%s: body: %w", name, err)
		}
		if len(body) == 0 {
			continue
		}
		prefixes, err := strOrList(r.Prefix)
		if err != nil {
			return nil, fmt.Errorf("%s: prefix: %w", name, err)
		}
		if len(prefixes) == 0 {
			prefixes = []string{name}
		}
		for _, p := range prefixes {
			out = append(out, Snippet{Name: name, Prefix: p, Body: strings.Join(body, "\n"), Desc: r.Description})
		}
	}
	slices.SortFunc(out, func(a, b Snippet) int { return strings.Compare(a.Name+"\x00"+a.Prefix, b.Name+"\x00"+b.Prefix) })
	return out, nil
}

// strOrList 读一个字符串或字符串数组（VS Code 的 prefix、body 两种写法都行）。
func strOrList(m json.RawMessage) ([]string, error) {
	if len(m) == 0 || string(m) == "null" {
		return nil, nil
	}
	var s string
	if json.Unmarshal(m, &s) == nil {
		return []string{s}, nil
	}
	var list []string
	if err := json.Unmarshal(m, &list); err != nil {
		return nil, errors.New(i18n.T("不是字符串或字符串数组"))
	}
	return list, nil
}

// WriteBuiltins 把内置的几份写进 dir（片段目录没有任何 .json 时当样板）：已有的同名文件不动。返回写了几个。
func WriteBuiltins(dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	n := 0
	err := fs.WalkDir(builtinFS, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dst := filepath.Join(dir, d.Name())
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
		b, _ := builtinFS.ReadFile(p)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}
