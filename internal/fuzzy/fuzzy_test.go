package fuzzy

import (
	"slices"
	"strings"
	"testing"
)

func nameAt(s string) int { return strings.LastIndexByte(s, '/') + 1 }

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		q, s string
		ok   bool
		hits []int
	}{
		{"", "a.go", true, nil},
		{"ago", "a.go", true, []int{0, 2, 3}},
		{"A GO", "a.go", true, []int{0, 2, 3}},
		{"xyz", "a.go", false, nil},
		{"goa", "a.go", false, nil},
		// 名字里能匹配完就只在名字里找
		{"app", "internal/app/app.go", true, []int{13, 14, 15}},
		// 名字里不够，才跨到目录
		{"iapp", "internal/app/app.go", true, []int{0, 9, 10, 11}},
		// 往回找最短的一段：命中挤在一起
		{"ab", "a_x_a_b", true, []int{4, 6}},
		{"快捷", "docs/快捷键.md", true, []int{5, 8}},
	} {
		p := New(c.q)
		_, hits, ok := p.Match(c.s, nameAt(c.s))
		if ok != c.ok || !slices.Equal(hits, c.hits) {
			t.Errorf("%q in %q: ok=%v hits=%v，想要 %v %v", c.q, c.s, ok, hits, c.ok, c.hits)
		}
	}
}

// rank 按分数从高到低排 list 里匹配 q 的项。
func rank(q string, list []string) []string {
	p := New(q)
	type hit struct {
		s     string
		score int
	}
	var hs []hit
	for _, s := range list {
		if sc, _, ok := p.Match(s, nameAt(s)); ok {
			hs = append(hs, hit{s, sc})
		}
	}
	slices.SortStableFunc(hs, func(a, b hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return len(a.s) - len(b.s)
	})
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.s
	}
	return out
}

func TestRanking(t *testing.T) {
	files := []string{
		"internal/app/domain.go",
		"docs/main-notes.md",
		"cmd/workbench/main.go",
		"internal/search/search_test.go",
		"internal/search/search.go",
		"internal/ui/search.go",
		"internal/app/app.go",
		"internal/app/app_test.go",
		"README.md",
		"docs/架构.md",
		"internal/app/commands.go",
	}
	for _, c := range []struct{ q, first string }{
		{"main", "cmd/workbench/main.go"},      // 文件名就是它（去掉扩展名）比名字里带它、目录里有它的靠前
		{"search.go", "internal/ui/search.go"}, // 一样好时短的靠前
		{"isearch", "internal/search/search.go"},
		{"searchsearch", "internal/search/search.go"},
		{"appgo", "internal/app/app.go"},
		{"readme", "README.md"},
		{"架构", "docs/架构.md"},
		{"cmds", "internal/app/commands.go"},
	} {
		got := rank(c.q, files)
		if len(got) == 0 || got[0] != c.first {
			t.Errorf("%q 排第一的是 %v，想要 %q", c.q, got, c.first)
		}
	}
	if got := rank("zzz", files); len(got) != 0 {
		t.Errorf("zzz 不该匹配：%v", got)
	}
}

// MatchPath：仓库根下的文件（name 为 0）也算命中在文件名里，不比子目录里同名的吃亏；有目录的和 Match 一样。
func TestMatchPath(t *testing.T) {
	p := New("guide")
	root, _, ok1 := p.MatchPath("guide.md", 0)
	sub, _, ok2 := p.MatchPath("docs/guide.md", 5)
	if !ok1 || !ok2 || root < sub {
		t.Errorf("根下的 guide.md %d，docs/guide.md %d：根下的不该分低（一样时调用方让短的靠前）", root, sub)
	}
	if plain, _, _ := p.Match("guide.md", 0); plain >= root {
		t.Errorf("Match 给根下的 %d，MatchPath %d：MatchPath 该加上文件名的分", plain, root)
	}
	if sc, h, ok := p.Match("docs/guide.md", 5); !ok {
		t.Error("Match 没匹配")
	} else if sc2, h2, _ := p.MatchPath("docs/guide.md", 5); sc2 != sc || !slices.Equal(h, h2) {
		t.Errorf("有目录的 MatchPath %d %v，Match %d %v", sc2, h2, sc, h)
	}
	if _, _, ok := p.MatchPath("gide.md", 0); ok {
		t.Error("gide 不该匹配 guide")
	}
	if sc, h, ok := New(" ").MatchPath("a", 0); !ok || sc != 0 || h != nil {
		t.Error("空查询什么都匹配")
	}
	// ScorePath 的分和 MatchPath 一样，不分配
	for _, c := range []struct {
		q, s string
		name int
	}{{"guide", "guide.md", 0}, {"guide", "docs/guide.md", 5}, {"dg", "docs/guide.md", 5}, {"xyz", "docs/guide.md", 5}, {"文件", "中文/文件树.md", 7}} {
		p := New(c.q)
		want, _, wok := p.MatchPath(c.s, c.name)
		if got, ok := p.ScorePath(c.s, c.name); got != want || ok != wok {
			t.Errorf("%q in %q：ScorePath %d %v，MatchPath %d %v", c.q, c.s, got, ok, want, wok)
		}
		if n := testing.AllocsPerRun(10, func() { p.ScorePath(c.s, c.name) }); n != 0 {
			t.Errorf("%q in %q：ScorePath 分配了 %v 次", c.q, c.s, n)
		}
	}
}

func BenchmarkMatch(b *testing.B) {
	var files []string
	for i := range 15000 {
		files = append(files, "internal/pkg"+strings.Repeat("x", i%7)+"/sub/file_"+strings.Repeat("y", i%13)+".go")
	}
	p := New("pkgsubfy")
	b.ResetTimer()
	for range b.N {
		for _, f := range files {
			p.Match(f, nameAt(f))
		}
	}
}
