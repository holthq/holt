package i18n

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

var update = flag.Bool("update", false, "从代码里重新收集 zh-CN.json")

// sourceDirs 是界面文案所在的代码（不含测试、i18n 自己和 testutil）。
var sourceDirs = []string{"../app", "../docs", "../files", "../fuzzy", "../git", "../globs", "../outline", "../search", "../snippets", "../ui", "../watch", "../workspace", "../../cmd"}

// TestCatalog：zh-CN.json 是代码里全部的中文文案（go test -run TestCatalog ./internal/i18n/ -update 重新收集）；
// en.json 每条都有、没有多余的、占位符和中文一样（个数、顺序、种类），译文里没有中文。
func TestCatalog(t *testing.T) {
	got := collect(t)
	if *update {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile("zh-CN.json", append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var want []string
	readJSON(t, "zh-CN.json", &want)
	if !slices.Equal(got, want) {
		for _, s := range got {
			if !slices.Contains(want, s) {
				t.Errorf("zh-CN.json 少了 %q", s)
			}
		}
		for _, s := range want {
			if !slices.Contains(got, s) {
				t.Errorf("zh-CN.json 多了 %q（代码里没有了）", s)
			}
		}
		t.Fatal("zh-CN.json 和代码对不上：go test -run TestCatalog ./internal/i18n/ -update")
	}
	en := English()
	for _, s := range got {
		e, ok := en[s]
		switch {
		case !ok || e == "":
			t.Errorf("en.json 少了 %q", s)
		case hasHan(e):
			t.Errorf("en.json 里 %q 的译文 %q 还有中文", s, e)
		case !slices.Equal(verbs(s), verbs(e)):
			t.Errorf("en.json 里 %q 的占位符 %v 和译文 %q 的 %v 不一样", s, verbs(s), e, verbs(e))
		}
	}
	for s, e := range en {
		base, suf, variant := strings.Cut(s, "|")
		switch {
		case !variant && !slices.Contains(got, s):
			t.Errorf("en.json 多了 %q（代码里没有了）", s)
		case !variant:
		case !slices.Contains(got, base):
			t.Errorf("en.json 的复数变体 %q 没有对应的 %q", s, base)
		case !slices.Contains(variantSuffixes(base), "|"+suf):
			t.Errorf("en.json 的复数变体 %q：%q 只能用 %v", s, base, variantSuffixes(base))
		case hasHan(e):
			t.Errorf("en.json 里 %q 的译文 %q 还有中文", s, e)
		case !slices.Equal(verbs(base), verbs(e)):
			t.Errorf("en.json 里 %q 的占位符 %v 和译文 %q 的 %v 不一样", s, verbs(base), e, verbs(e))
		}
		if variant && suf == "1|" && en[base+"||1"] != "" && en[base+"|1|1"] == "" {
			t.Errorf("en.json 有 %q 和 %q，也要有都是 1 的 %q", s, base+"||1", base+"|1|1")
		}
	}
}

// variantSuffixes 是格式串 s 能有的复数变体后缀（见 Tn）：一个 %d 的只有 "|1"，两个以上的是 "|1|"、"||1"、"|1|1"。
func variantSuffixes(s string) []string {
	switch strings.Count(strings.ReplaceAll(s, "%%", ""), "%d") {
	case 0:
		return nil
	case 1:
		return []string{"|1"}
	}
	return []string{"|1|", "||1", "|1|1"}
}

// collect 是 sourceDirs 里所有带汉字的字符串字面量，去重排序；log 和 panic 的参数、
// 名字以 Logf 结尾的函数变量（readyLogf 那样测试换掉的 log.Printf、拼日志行的 lineLogf）的参数、
// ShortcutDoc（生成 docs/shortcuts.md，文档是中文的）不算：不给用户看。
func collect(t *testing.T) []string {
	seen := map[string]bool{}
	for _, dir := range sourceDirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FuncDecl:
					return x.Name.Name != "ShortcutDoc"
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && (id.Name == "panic" || strings.HasSuffix(id.Name, "Logf")) {
						return false
					}
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == "log" {
							return false
						}
					}
				case *ast.BasicLit:
					if x.Kind == token.STRING {
						if s, err := strconv.Unquote(x.Value); err == nil && hasHan(s) {
							seen[s] = true
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func readJSON(t *testing.T, path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func hasHan(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

var verbRE = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)

// verbs 是格式串里的占位符，按顺序（%% 不算）。
func verbs(s string) []string {
	var out []string
	for _, v := range verbRE.FindAllString(s, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}

// TestLang：只认 "en"，其余都是中文；中文时 T 原样返回，英文时查表，表里没有的原样返回。
func TestLang(t *testing.T) {
	t.Cleanup(func() { Set("") })
	Set("fr")
	if Lang() != "" || Locale() != "zh-Hans" || T("设置") != "设置" {
		t.Fatalf("Set(fr): Lang=%q Locale=%q", Lang(), Locale())
	}
	Set("en")
	if Lang() != "en" || Locale() != "en" {
		t.Fatalf("Set(en): Lang=%q Locale=%q", Lang(), Locale())
	}
	if got := T("设置"); got != "Settings" {
		t.Errorf("T(设置) = %q", got)
	}
	if got := T("表里没有的"); got != "表里没有的" {
		t.Errorf("T(表里没有的) = %q", got)
	}
}

// fixedCounts 是数是常量（总大于 1）的文案：英文写复数就对，不用 "|1" 变体。
var fixedCounts = []string{
	"一次最多 %d 个提交",
	"文件太大（超过 %d MB 或 %d 行），不算 blame",
	"格式化失败：%d 秒还没跑完",
	"检查失败：%d 秒还没跑完",
	"一个终端标签最多分 %d 格",
	"再多搜 %d 处（最多 %d 处）",
	" · 被忽略的文件太多，每个仓库只搜了前 %d 个",
	"索引太大，只建了前 %d 个文件",
	"%d 个文件匹配，只列最好的 %d 个",
	"%d 个符号匹配，只列最好的 %d 个",
	"匹配太多，只显示前 %d 个",
	"分成 %d 格",
	"（%d 处选中）",
}

var pluralRE = regexp.MustCompile(`^%d(?: [a-z-]+)? ([a-z]+s)\b`)

// notPlural 是跟在数后面、以 s 结尾却不是复数名词的词（"Row %d has …"）。
var notPlural = []string{"is", "has", "was", "does", "this"}

// TestEnglishPlurals：英文里 "%d 个名词s" 的都要有数是 1 时的变体（见 Tn），代码里这些文案只能交给 Tn；
// Tn 对 1 和 2 挑对的。数是常量的列在 fixedCounts。
func TestEnglishPlurals(t *testing.T) {
	en := English()
	for s, e := range en {
		if strings.Contains(s, "|") || slices.Contains(fixedCounts, s) {
			continue
		}
		vs := verbs(s)
		d := 0
		for _, loc := range regexp.MustCompile(`%d`).FindAllStringIndex(e, -1) {
			pos := d
			d++
			if m := pluralRE.FindStringSubmatch(e[loc[0]:]); m == nil || slices.Contains(notPlural, m[1]) {
				continue
			}
			want := "|1"
			if len(vs) > 0 && strings.Count(strings.Join(vs, ""), "%d") > 1 {
				want = []string{"|1|", "||1"}[min(pos, 1)]
			}
			if en[s+want] == "" {
				t.Errorf("en.json 里 %q（%q）第 %d 个数是 1 时要有变体 %q", s, e, pos+1, s+want)
			}
		}
	}
	used := map[string]bool{}
	walkSource(t, func(path string, lit *ast.BasicLit, parent ast.Node) {
		s, _ := strconv.Unquote(lit.Value)
		if len(variantKeys(en, s)) == 0 {
			return
		}
		used[s] = true
		c, ok := parent.(*ast.CallExpr)
		if sel, isSel := callName(c, ok); !isSel || sel != "Tn" || c.Args[0] != lit {
			t.Errorf("%s：%q 有复数变体，要用 i18n.Tn", path, s)
		}
	})
	for s := range en {
		if base, _, ok := strings.Cut(s, "|"); ok && !used[base] {
			t.Errorf("en.json 的变体 %q 在代码里没有 Tn 用到", s)
		}
	}

	t.Cleanup(func() { Set("") })
	Set("en")
	for _, c := range []struct {
		got, want string
	}{
		{Tn("%d 个文件", 1), "1 file"},
		{Tn("%d 个文件", 2), "2 files"},
		{Tn("%d 个文件", 0), "0 files"},
		{Tn("%d 个文件 · +%d −%d", 1, 3, 1), "1 file · +3 −1"},
		{Tn("%d 处，%d 个文件", 1, 1), "1 match in 1 file"},
		{Tn("%d 处，%d 个文件", 1, 2), "1 match in 2 files"},
		{Tn("%d 处，%d 个文件", 3, 1), "3 matches in 1 file"},
		{Tn("%d 处，%d 个文件", 3, 4), "3 matches in 4 files"},
		{Tn("第 %d 行起的 %d 行", 1, 1), "from line 1, 1 line"},
	} {
		if c.got != c.want {
			t.Errorf("Tn = %q，要 %q", c.got, c.want)
		}
	}
	Set("")
	if got := Tn("%d 个文件", 1); got != "1 个文件" {
		t.Errorf("中文 Tn = %q", got)
	}
}

// TestEnglishClean：英文没有全角标点、连着的空格、三个点的省略号。
func TestEnglishClean(t *testing.T) {
	fw := regexp.MustCompile(`[，。：；？！（）、【】「」　]`)
	for s, e := range English() {
		switch {
		case fw.MatchString(e):
			t.Errorf("%q 的译文 %q 有全角标点", s, e)
		case strings.Contains(strings.TrimSpace(e), "  "):
			t.Errorf("%q 的译文 %q 有连着的空格", s, e)
		case strings.Contains(e, "..."):
			t.Errorf("%q 的译文 %q 用了 ...，要 …", s, e)
		}
	}
}

// variantKeys 是 en 里 s 的复数变体键。
func variantKeys(en map[string]string, s string) []string {
	var out []string
	for _, suf := range []string{"|1", "|1|", "||1", "|1|1"} {
		if _, ok := en[s+suf]; ok {
			out = append(out, s+suf)
		}
	}
	return out
}

// callName 是调用 c 的函数名（i18n.Tn 是 Tn）。
func callName(c *ast.CallExpr, ok bool) (string, bool) {
	if !ok || len(c.Args) == 0 {
		return "", false
	}
	switch f := c.Fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name, true
	case *ast.Ident:
		return f.Name, true
	}
	return "", false
}

// walkSource 对 sourceDirs 里（不含测试）每个字符串字面量调 fn，parent 是直接套着它的节点。
func walkSource(t *testing.T, fn func(path string, lit *ast.BasicLit, parent ast.Node)) {
	for _, dir := range sourceDirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && len(stack) > 0 {
					fn(fmt.Sprintf("%s:%d", path, fset.Position(lit.Pos()).Line), lit, stack[len(stack)-1])
				}
				stack = append(stack, n)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
