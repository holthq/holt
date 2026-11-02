package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这里的测试都在 t.TempDir() 的临时仓库上跑；BenchmarkWorkspace 只读用户的工作区。

// fixture 建一个临时仓库：几种文件，外加二进制、超过 2 MB、被忽略、已删除的各一个。
func fixture(t testing.TB) Root {
	t.Helper()
	r := testutil.NewRepo(t, "demo")
	r.Write(".gitignore", "node_modules/\n")
	r.Write("a.go", "package a\n\n// Hello hello HELLO\nfunc helloWorld() {}\n\tx := \"hello\" // 中文 测试 hello\n")
	r.Write("docs/b.md", "# 标题\n\n测试一下，hello。\n")
	r.Write("docs/sub/c.md", "hello\n")
	r.Write("gone.txt", "hello\n")
	r.Commit("初始")
	if err := os.Remove(filepath.Join(r.Root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	r.Write("bin.dat", "hello\x00hello\n")
	r.Write("big.txt", strings.Repeat("hello ", MaxFileSize/6+10))
	r.Write("node_modules/x.js", "hello\n")
	r.Write("new.txt", "未跟踪的 hello\n") // 未跟踪、没被忽略：要搜
	return Root{Name: "demo", Dir: r.Root}
}

func where(ms []Match) []string {
	var out []string
	for _, m := range ms {
		out = append(out, fmt.Sprintf("%s:%d:%d", m.Path, m.Line+1, m.Col+1))
	}
	return out
}

func TestModes(t *testing.T) {
	root := fixture(t)
	cases := []struct {
		name string
		q    Query
		want []string
	}{
		{"不区分大小写", Query{Text: "hello"}, []string{
			"a.go:3:4", "a.go:3:10", "a.go:3:16", "a.go:4:6", "a.go:5:8", "a.go:5:32",
			"docs/b.md:3:16", "docs/sub/c.md:1:1", "new.txt:1:14"}},
		{"区分大小写", Query{Text: "Hello", CaseSensitive: true}, []string{"a.go:3:4"}},
		{"整词", Query{Text: "hello", WholeWord: true, Include: "*.go"}, []string{
			"a.go:3:4", "a.go:3:10", "a.go:3:16", "a.go:5:8", "a.go:5:32"}},
		{"正则", Query{Text: `hel+o[A-Z]\w+`, Regexp: true, CaseSensitive: true}, []string{"a.go:4:6"}},
		{"正则不区分大小写", Query{Text: `^// h`, Regexp: true}, []string{"a.go:3:1"}},
		{"中文按字节算列", Query{Text: "测试"}, []string{"a.go:5:25", "docs/b.md:3:1"}},
		{"包含目录", Query{Text: "hello", Include: "docs/**"}, []string{"docs/b.md:3:16", "docs/sub/c.md:1:1"}},
		{"包含仓库名开头", Query{Text: "hello", Include: "demo/docs/sub"}, []string{"docs/sub/c.md:1:1"}},
		{"排除目录名", Query{Text: "hello", Exclude: "sub, *.go, new.txt"}, []string{"docs/b.md:3:16"}},
		{"花括号", Query{Text: "hello", Include: "*.{md,txt}", Exclude: "docs/sub/"}, []string{"docs/b.md:3:16", "new.txt:1:14"}},
	}
	for _, c := range cases {
		res := Search(context.Background(), []Root{root}, c.q)
		if res.Err != nil {
			t.Fatalf("%s: %v", c.name, res.Err)
		}
		if got := where(res.Matches); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}

	res := Search(context.Background(), []Root{root}, Query{Text: "hello"})
	// 二进制和超过 2 MB 的跳过；删掉的不在 ls-files 里；node_modules 被忽略
	if res.Files != 4 || res.Skipped != 2 || res.Searched != 5 || res.Truncated {
		t.Errorf("files=%d skipped=%d searched=%d truncated=%v", res.Files, res.Skipped, res.Searched, res.Truncated)
	}
	m := res.Matches[4] // a.go 第 5 行：开头的制表符去掉、匹配在预览里的位置对得上
	if m.Preview != `x := "hello" // 中文 测试 hello` || m.Preview[m.PreviewCol:m.PreviewCol+m.Len] != "hello" {
		t.Errorf("预览 %q @%d", m.Preview, m.PreviewCol)
	}
	if m.Repo != "demo" || m.Root != root.Dir {
		t.Errorf("仓库 %q %q", m.Repo, m.Root)
	}

	for _, q := range []Query{{Text: ""}, {Text: "a(", Regexp: true}, {Text: "x", Include: "{"}} {
		if res := Search(context.Background(), []Root{root}, q); res.Err == nil && q.Include == "" {
			t.Errorf("%+v 应报错", q)
		}
	}
	if res := Search(context.Background(), []Root{root}, Query{Text: "a(", Regexp: true}); !strings.HasPrefix(res.Err.Error(), "正则写错了") {
		t.Errorf("错误 %v", res.Err)
	}
}

func TestPreviewLongLine(t *testing.T) {
	line := []byte(strings.Repeat("甲", 100) + "needle" + strings.Repeat("乙", 200))
	s := 300
	p, pc := preview(line, s, s+6)
	if !strings.HasPrefix(p, "…") || !strings.HasSuffix(p, "…") || p[pc:pc+6] != "needle" {
		t.Fatalf("预览 %q @%d", p, pc)
	}
	if len(p) > previewBefore+previewAfter+20 {
		t.Errorf("预览太长：%d 字节", len(p))
	}
}

// 很多结果：截在 MaxMatches，留下的是按路径、行排的前 MaxMatches 处，每次一样。
func TestCapAndOrder(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := range 40 {
		name := fmt.Sprintf("f%02d.txt", i)
		files = append(files, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x x x\n", 100)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := Root{Name: "r", Dir: dir, Files: files}
	var first []string
	for range 5 {
		res := Search(context.Background(), []Root{root}, Query{Text: "x"})
		if len(res.Matches) != MaxMatches || !res.Truncated || res.Files != MaxMatches/300+1 {
			t.Fatalf("matches=%d truncated=%v files=%d", len(res.Matches), res.Truncated, res.Files)
		}
		got := where(res.Matches)
		if first == nil {
			first = got
			if got[0] != "f00.txt:1:1" || got[MaxMatches-1] != "f16.txt:67:3" {
				t.Fatalf("顺序：%s … %s", got[0], got[MaxMatches-1])
			}
		} else if !slices.Equal(first, got) {
			t.Fatal("两次结果不一样")
		}
	}
}

func TestCancel(t *testing.T) {
	root := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := Search(ctx, []Root{root}, Query{Text: "hello"}); res.Err != context.Canceled || len(res.Matches) != 0 {
		t.Fatalf("取消后 err=%v matches=%d", res.Err, len(res.Matches))
	}
}

// bigRepo 建 files 个 lines 行的文件（git init 但不提交：ls-files --others 也列出它们）。
func bigRepo(t testing.TB, files, lines int) Root {
	t.Helper()
	r := testutil.NewRepo(t, "big")
	var b strings.Builder
	for i := range lines {
		fmt.Fprintf(&b, "func handler%d(w http.ResponseWriter, r *http.Request) { // 第 %d 行 TODO 处理错误\n", i, i)
	}
	text := []byte(b.String())
	for i := range files {
		dir := filepath.Join(r.Root, fmt.Sprintf("pkg%02d", i/50))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.go", i)), text, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Root{Name: "big", Dir: r.Root}
}

// TestHundredThousandLines 是定好的预算：10 万行的工作区搜索 < 1 s（含 git ls-files；-race 下放宽，见 testutil.Budget）。
func TestHundredThousandLines(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	root := bigRepo(t, 1000, 100)
	for _, q := range []Query{
		{Text: "handler99("},                          // 字面，不区分大小写，每个文件一处：1000 个文件全搜一遍
		{Text: "handler7"},                            // 字面，截在 5000
		{Text: "ResponseWriter", CaseSensitive: true}, // 字面，每行一处：截在 5000
		{Text: "处理错误", WholeWord: true},
		{Text: `handler\d+5\(`, Regexp: true},
	} {
		r := root // 每次都现跑 ls-files
		start := time.Now()
		res := Search(context.Background(), []Root{r}, q)
		d := time.Since(start)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		t.Logf("%-16q %5d 处 %4d 个文件 搜了 %4d 个 %v", q.Text, len(res.Matches), res.Files, res.Searched, d.Round(time.Millisecond))
		if d > testutil.Budget(time.Second) {
			t.Errorf("%q 用了 %v，超过 %v", q.Text, d, testutil.Budget(time.Second))
		}
	}
}

func BenchmarkHundredThousandLines(b *testing.B) {
	root := bigRepo(b, 1000, 100)
	files := Search(context.Background(), []Root{root}, Query{Text: "x"}).Searched
	b.Logf("%d 个文件", files)
	for _, q := range []Query{{Text: "handler7"}, {Text: `handler\d+5\(`, Regexp: true}} {
		b.Run(q.Text, func(b *testing.B) {
			for b.Loop() {
				if res := Search(context.Background(), []Root{root}, q); res.Err != nil {
					b.Fatal(res.Err)
				}
			}
		})
	}
}

// BenchmarkWorkspace 在用户的真实工作区里搜（只读：ls-files 和读文件），没有就跳过。
func BenchmarkWorkspace(b *testing.B) {
	roots, err := workspace.Load(workspace.DefaultPath)
	if err != nil {
		b.Skip(err)
	}
	var rs []Root
	for _, dir := range roots {
		rs = append(rs, Root{Name: filepath.Base(dir), Dir: dir})
	}
	res := Search(context.Background(), rs, Query{Text: "the"})
	b.Logf("%d 个仓库，搜了 %d 个文件，跳过 %d 个，%d 处（截断=%v），%v", len(rs), res.Searched, res.Skipped, len(res.Matches), res.Truncated, res.Elapsed)
	for _, q := range []Query{{Text: "TODO", CaseSensitive: true}, {Text: "the"}, {Text: `func \w+\(`, Regexp: true}} {
		b.Run(q.Text, func(b *testing.B) {
			for b.Loop() {
				if res := Search(context.Background(), rs, q); res.Err != nil {
					b.Fatal(res.Err)
				}
			}
		})
	}
}

// "也搜被忽略的文件"：.gitignore 忽略的一个个列出来接在后面搜（按路径排、不重复）；
// 给了 Files（文件树的列表）也不改它；文件夹（不是 Git 仓库）没有被忽略的一说。
func TestIgnoredFiles(t *testing.T) {
	root := fixture(t)
	if err := os.MkdirAll(filepath.Join(root.Dir, "node_modules/pkg/deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root.Dir, "node_modules/pkg/deep/z.js"), []byte("x hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := Query{Text: "hello", Include: "*.js, *.txt"}
	if got := where(Search(context.Background(), []Root{root}, q).Matches); !slices.Equal(got, []string{"new.txt:1:14"}) {
		t.Fatalf("没开：%q", got)
	}
	q.Ignored = true
	res := Search(context.Background(), []Root{root}, q)
	if got := where(res.Matches); !slices.Equal(got, []string{"new.txt:1:14", "node_modules/pkg/deep/z.js:1:3", "node_modules/x.js:1:1"}) {
		t.Fatalf("开了：%q", got)
	}
	if res.IgnoredCut {
		t.Error("没超也说只搜了前面的")
	}
	all := Search(context.Background(), []Root{root}, Query{Text: "hello", Ignored: true})
	if seen := where(all.Matches); len(slices.Compact(slices.Clone(seen))) != len(seen) || all.Files != 6 {
		t.Errorf("全搜：%d 个文件 %q", all.Files, seen)
	}

	listed := make([]string, 1, 8)
	listed[0] = "a.go"
	tree := root
	tree.Files = listed
	res = Search(context.Background(), []Root{tree}, Query{Text: "hello", Ignored: true})
	if got := where(res.Matches); len(got) != 8 || got[6] != "node_modules/pkg/deep/z.js:1:3" {
		t.Errorf("给了 Files：%q", got)
	}
	if len(tree.Files) != 1 || listed[:2][1] != "" {
		t.Errorf("改了文件树的列表：%q", listed[:2])
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := Search(context.Background(), []Root{{Name: "p", Dir: dir, Plain: true}}, Query{Text: "hello", Ignored: true})
	if plain.Err != nil || len(plain.Matches) != 1 {
		t.Errorf("文件夹：%v %q", plain.Err, where(plain.Matches))
	}
}

// "也搜 2 MB 以上的大文件"：3 MB 的平常跳过、开了能搜（预览照常截）；超过 MaxBigFileSize 的照旧跳过算进 Skipped。
func TestBigFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) {
		t.Helper()
		b := []byte(strings.Repeat("abcdefghi\n", size/10))
		b = append(b, "needle in the haystack\n"...)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("three.txt", 3<<20)
	write("huge.txt", MaxBigFileSize+1<<20)
	write("small.txt", 100)
	root := Root{Name: "r", Dir: dir, Files: []string{"huge.txt", "small.txt", "three.txt"}}
	res := Search(context.Background(), []Root{root}, Query{Text: "needle"})
	if got := where(res.Matches); !slices.Equal(got, []string{"small.txt:11:1"}) || res.Skipped != 2 {
		t.Fatalf("没开：%q skipped=%d", got, res.Skipped)
	}
	res = Search(context.Background(), []Root{root}, Query{Text: "needle", Big: true})
	want := fmt.Sprintf("three.txt:%d:1", 3<<20/10+1)
	if got := where(res.Matches); !slices.Equal(got, []string{"small.txt:11:1", want}) || res.Skipped != 1 {
		t.Fatalf("开了：%q skipped=%d", got, res.Skipped)
	}
	if m := res.Matches[1]; m.Preview != "needle in the haystack" {
		t.Errorf("预览 %q", m.Preview)
	}
	if (Query{}).MaxSize() != MaxFileSize || (Query{Big: true}).MaxSize() != MaxBigFileSize {
		t.Error("MaxSize")
	}
}

// Query.Limit：0 是 MaxMatches，往上加能多收，最多 MaxLimit；一个文件里的也按它截。
func TestLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte(strings.Repeat("x\n", MaxLimit+100)), 0o644); err != nil {
		t.Fatal(err)
	}
	root := Root{Name: "r", Dir: dir, Files: []string{"x.txt"}}
	for _, c := range []struct{ limit, want int }{{0, MaxMatches}, {12000, 12000}, {MaxLimit * 3, MaxLimit}} {
		res := Search(context.Background(), []Root{root}, Query{Text: "x", Limit: c.limit})
		if len(res.Matches) != c.want || !res.Truncated || res.Files != 1 {
			t.Errorf("limit %d：%d 处 truncated=%v", c.limit, len(res.Matches), res.Truncated)
		}
	}
}
