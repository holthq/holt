package globs

import (
	"slices"
	"strings"
	"testing"
)

// TestGlobs 是全文搜索的包含 / 排除（从 search 包搬过来的，行为不变）。
func TestGlobs(t *testing.T) {
	cases := []struct {
		globs, path string
		want        bool
	}{
		{"*.go", "a/b/c.go", true},
		{"*.go", "a/b/c.gox", false},
		{"node_modules", "web/node_modules/x/y.js", true},
		{"src/*.ts", "src/a.ts", true},
		{"src/*.ts", "src/x/a.ts", false},
		{"src/**/*.ts", "src/x/y/a.ts", true},
		{"src/**/*.ts", "src/a.ts", true},
		{"**/test/**", "a/test/b.go", true},
		{"docs", "docs/a.md", true},
		{"docs/", "x/docs/a.md", true},
		{"/docs/a.md", "docs/a.md", true},
		{"repo/docs", "docs/a.md", true},
		{"*.MD", "a.md", true}, // 不分大小写
		{"a?.txt", "ab.txt", true},
		{"a?.txt", "a/.txt", false},
		{" , *.md ,", "x.md", true},
		{"*.{md,txt}", "a/x.txt", true},
		{"*.{md,txt}", "x.go", false},
	}
	for _, c := range cases {
		gs, err := Compile(c.globs)
		if err != nil {
			t.Fatal(err)
		}
		if got := gs.Match("repo", c.path); got != c.want {
			t.Errorf("%q ~ %q = %v", c.globs, c.path, got)
		}
	}
}

// TestMatchRoot 是 files.exclude：都从仓库根算，**/ 开头的才是任何一级；目录合了底下整棵都算。
func TestMatchRoot(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"**/node_modules", "node_modules/a/b.js", true},
		{"**/node_modules", "web/node_modules/a.js", true},
		{"**/node_modules", "web/node_modules", true},
		{"node_modules", "node_modules/a.js", true},
		{"node_modules", "web/node_modules/a.js", false},
		{"*.log", "x.log", true},
		{"*.log", "logs/x.log", false},
		{"**/*.log", "logs/x.log", true},
		{"build", "build", true},
		{"build", "build/out/a.o", true},
		{"build", "src/build.go", false},
		{"**/.DS_Store", "a/.DS_Store", true},
		{"**/*.{js,map}", "dist/a.map", true},
		{"**/*.{js,map}", "dist/a.ts", false},
		{"docs/**/*.tmp", "docs/a/b.tmp", true},
		{"docs/**/*.tmp", "x/docs/a.tmp", false},
	}
	for _, c := range cases {
		gs, err := CompileList([]string{c.pat})
		if err != nil {
			t.Fatal(err)
		}
		if got := gs.MatchRoot(c.path); got != c.want {
			t.Errorf("%q ~ %q = %v", c.pat, c.path, got)
		}
	}
	if gs, _ := CompileList([]string{"a,b"}); !gs.MatchRoot("a,b") || gs.MatchRoot("a") {
		t.Error("CompileList 不按逗号拆")
	}
	if gs, _ := CompileList(nil); gs.MatchRoot("x") {
		t.Error("空的不该合")
	}
}

// TestMatcher 是带缓存的比法和 MatchRoot 一样，Filter 原地去掉合的。
func TestMatcher(t *testing.T) {
	gs, _ := CompileList([]string{"**/node_modules", "build", "*.log"})
	paths := []string{"a.go", "build/x.o", "build", "web/node_modules/a/b.js", "web/node_modules/", "web/a.js", "x.log", "logs/x.log", "buildx/a"}
	m := gs.Matcher()
	for _, p := range paths {
		if got, want := m.Match(p), gs.MatchRoot(strings.TrimSuffix(p, "/")); got != want {
			t.Errorf("Matcher.Match(%q) = %v, MatchRoot = %v", p, got, want)
		}
	}
	kept, n := gs.Matcher().Filter(slices.Clone(paths))
	if want := []string{"a.go", "web/a.js", "logs/x.log", "buildx/a"}; !slices.Equal(kept, want) || n != 5 {
		t.Errorf("Filter = %q, %d", kept, n)
	}
	if kept, n := Set(nil).Matcher().Filter(paths); len(kept) != len(paths) || n != 0 {
		t.Error("空的规则什么也不去掉")
	}
}
