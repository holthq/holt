package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/fuzzy"
	"github.com/lemoeade/grove/internal/git"
)

func names(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%d:%s", r.Depth, r.Path)
	}
	return out
}

func TestTree(t *testing.T) {
	var tr Tree
	gen := tr.Start()
	stale := tr.Start()
	if tr.Done(gen, []string{"x"}, nil, nil) {
		t.Fatal("旧的结果应该被丢掉")
	}
	paths := []string{"b.txt", "A/z.go", "a/y.go", "a/sub/x.go", "README.md", "c.txt"}
	if !tr.Done(stale, paths, nil, nil) {
		t.Fatal("最近一次的结果被丢了")
	}
	if tr.Count != 6 || !tr.Loaded || tr.Loading {
		t.Errorf("tree = %+v", tr)
	}
	codes, marks := Marks([]git.Change{{Path: "a/sub/x.go", Code: 'M'}, {Path: "c.txt", Code: '?'}})
	rows := tr.Rows(1, codes, marks, nil)
	if got, want := names(rows), []string{"1:A", "1:a", "1:b.txt", "1:c.txt", "1:README.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("收起时 %v，要 %v", got, want)
	}
	if rows[0].Mark || !rows[1].Mark || rows[3].Code != '?' {
		t.Errorf("标记不对 %+v", rows)
	}
	v := tr.Version
	tr.Reveal("a/sub/x.go")
	if tr.Version == v {
		t.Error("展开后 Version 没变")
	}
	rows = tr.Rows(0, codes, marks, nil)
	if got, want := names(rows), []string{"0:A", "0:a", "1:a/sub", "2:a/sub/x.go", "1:a/y.go", "0:b.txt", "0:c.txt", "0:README.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("展开后 %v，要 %v", got, want)
	}
	if rows[3].Code != 'M' || !rows[2].Mark || !rows[1].Open {
		t.Errorf("展开后标记不对 %+v", rows[:4])
	}
	tr.Toggle("a")
	if got := len(tr.Rows(0, nil, nil, nil)); got != 5 {
		t.Errorf("收起 a 之后 %d 行", got)
	}
	tr.CollapseAll()
	if len(tr.Open) != 0 {
		t.Error("CollapseAll 没收起")
	}
	if !tr.Has("a/sub/x.go") || !tr.Has("b.txt") || tr.Has("a/x.go") || tr.Has("nope/b.txt") {
		t.Error("Has 判断错")
	}
	if n := tr.Dir("a/sub"); n == nil || n.Path != "a/sub" || !reflect.DeepEqual(n.Files, []string{"x.go"}) {
		t.Errorf("Dir(a/sub) = %+v", n)
	}
	if tr.Dir("") != tr.Root || tr.Dir("a/nope") != nil || tr.Dir("b.txt") != nil {
		t.Error("Dir 判断错")
	}
}

// matchRows 是筛选 q 匹配到的那些行（按树的顺序）。
func matchRows(tr *Tree, q string, depth int, codes map[string]byte) []Row {
	pat := fuzzy.New(q)
	var rows []Row
	tr.Match(pat, func(s Scored) { rows = append(rows, tr.HitRow(pat, s, depth, codes)) })
	return rows
}

// 筛选是模糊匹配：字按顺序出现在路径里就算（不分大小写、空格去掉），按树的顺序给出、带分和命中的字；
// 字可以隔开（tsb 找到 tree_sidebar.go），文件名里命中的比只在目录里命中的分高。
func TestMatch(t *testing.T) {
	var tr Tree
	tr.Done(tr.Start(), []string{"docs/Guide.md", "docs/api.md", "src/guide.go", "readme", "src/tree_sidebar.go", "notes/docs.txt"}, nil, nil)
	hs := matchRows(&tr, " GUIDE  ", 1, nil)
	if got, want := names(hs), []string{"1:docs/Guide.md", "1:src/guide.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("guide %v，要 %v", got, want)
	}
	if !reflect.DeepEqual(hs[0].Hits, []int{5, 6, 7, 8, 9}) || hs[0].Name != "Guide.md" {
		t.Errorf("命中的字 %v", hs[0])
	}
	// 空格去掉，有顺序（和快速打开一样）："docs md" 就是 docsmd
	if got, want := names(matchRows(&tr, "docs md", 0, nil)), []string{"0:docs/api.md", "0:docs/Guide.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("docs md %v，要 %v", got, want)
	}
	if got := names(matchRows(&tr, "md docs", 0, nil)); len(got) != 0 {
		t.Errorf("md docs 顺序反了不该匹配：%v", got)
	}
	if got := names(matchRows(&tr, "tsb", 0, nil)); !reflect.DeepEqual(got, []string{"0:src/tree_sidebar.go"}) {
		t.Errorf("隔开的字 tsb：%v", got)
	}
	// 文件名就是 docs 的比目录叫 docs 的分高；根下的文件名也算文件名
	score := map[string]int32{}
	tr.Match(fuzzy.New("docs"), func(s Scored) { score[s.Path] = s.Score })
	if score["notes/docs.txt"] <= score["docs/api.md"] || score["notes/docs.txt"] <= score["docs/Guide.md"] {
		t.Errorf("docs 的分：%v", score)
	}
	var readme []Scored
	tr.Match(fuzzy.New("readme"), func(s Scored) { readme = append(readme, s) })
	if len(readme) != 1 || readme[0].Score <= score["notes/docs.txt"]-20 {
		t.Errorf("根下的 readme：%+v", readme)
	}
	// 造行时带上状态字母
	if hs := matchRows(&tr, "api", 2, map[string]byte{"docs/api.md": 'M'}); len(hs) != 1 || hs[0].Code != 'M' || hs[0].Depth != 2 || hs[0].Ignored || !reflect.DeepEqual(hs[0].Hits, []int{5, 6, 7}) {
		t.Errorf("api：%+v", hs)
	}
}

func TestErr(t *testing.T) {
	var tr Tree
	tr.Done(tr.Start(), []string{"a"}, nil, nil)
	tr.Done(tr.Start(), nil, fmt.Errorf("坏了"), nil)
	if tr.Root != nil || tr.Count != 0 || tr.Err == nil {
		t.Errorf("出错后 %+v", tr)
	}
	if rows := tr.Rows(0, nil, nil, nil); len(rows) != 0 {
		t.Error("出错后不该有行")
	}
}

// 1 万个文件的仓库：建树和摊平全部展开的树要够快（侧栏每次状态刷新都会重算行）。
func BenchmarkTree10k(b *testing.B) {
	paths := make([]string, 0, 10000)
	for i := range 100 {
		for j := range 100 {
			paths = append(paths, fmt.Sprintf("dir%02d/sub%d/file%03d.go", i, j%10, j))
		}
	}
	for b.Loop() {
		var tr Tree
		tr.Done(tr.Start(), paths, nil, nil)
		for i := range 100 {
			tr.Reveal(fmt.Sprintf("dir%02d/sub0/x", i))
		}
		_ = tr.Rows(0, nil, nil, nil)
		tr.Match(fuzzy.New("file05"), func(Scored) {})
	}
}

// 普通文件夹：列出文件和目录（空目录也在），不列也不进 .git，不列 .DS_Store；node_modules、点开头的目录列进 lazy、
// 不往里走（里面的文件不列），树里当被忽略的目录（收着、展开时读一层）；点开头的文件照列；取消了返回 ctx 的错。
func TestWalk(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"b.txt", "a/x.go", "a/sub/y.go", ".env", ".DS_Store", "a/.DS_Store",
		".git/HEAD", ".cache/z", "node_modules/m/i.js", "a/node_modules/n.js"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths, dirs, _, lazy, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".env", "a/sub/y.go", "a/x.go", "b.txt"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("文件 %v，要 %v", paths, want)
	}
	if want := []string{"a", "a/sub", "empty"}; !reflect.DeepEqual(dirs, want) {
		t.Errorf("目录 %v，要 %v", dirs, want)
	}
	if want := []string{".cache/", ".empty/", "a/node_modules/", "node_modules/"}; !reflect.DeepEqual(lazy, want) {
		t.Errorf("不往里走的目录 %v，要 %v", lazy, want)
	}
	tr := Tree{Open: map[string]bool{"a": true}}
	tr.Done(tr.Start(), paths, nil, lazy, dirs...)
	if got, want := names(tr.Rows(0, nil, nil, nil)), []string{"0:.cache", "0:.empty", "0:a", "1:a/node_modules", "1:a/sub", "1:a/x.go",
		"0:empty", "0:node_modules", "0:.env", "0:b.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("树 %v，要 %v", got, want)
	}
	for _, r := range tr.Rows(0, nil, nil, nil) {
		if want := r.Path == ".cache" || r.Path == ".empty" || strings.HasSuffix(r.Path, "node_modules"); r.Ignored != want || r.Open && want {
			t.Errorf("%s：灰显 %v、展开 %v，要灰显 %v、收着", r.Path, r.Ignored, r.Open, want)
		}
	}
	if tr.Count != 4 || tr.Has(".cache/z") {
		t.Errorf("不往里走的目录里的文件算进了树：%d 个，有 .cache/z %v", tr.Count, tr.Has(".cache/z"))
	}
	tr.Toggle(".cache")
	tr.ReadLazy(root)
	if got := tr.Lazy[".cache"]; !reflect.DeepEqual(got, []string{"z"}) {
		t.Errorf("展开 .cache 读到 %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, err := Walk(ctx, root); err != context.Canceled {
		t.Errorf("取消了还返回 %v", err)
	}
	if _, _, _, _, err := Walk(context.Background(), filepath.Join(root, "没有")); err == nil {
		t.Error("不存在的目录没报错")
	}
}

// 被忽略的文件并进树里：灰显、没有状态字母，不算进 Count；整个被忽略的目录是一个空目录节点。
func TestBuildIgnored(t *testing.T) {
	var tr Tree
	gen := tr.Start()
	tr.Done(gen, []string{"a.txt", "src/x.go"}, nil, []string{"build/", "debug.log", "src/gen/", "src/trace.log", "a.txt"})
	if tr.Count != 2 || len(tr.Paths) != 2 {
		t.Errorf("Count = %d，被忽略的不该算", tr.Count)
	}
	tr.Toggle("src")
	codes, marks := Marks([]git.Change{{Path: "src/x.go", Code: 'M'}})
	var got []string
	for _, r := range tr.Rows(0, codes, marks, nil) {
		got = append(got, fmt.Sprintf("%d:%s:%v:%v:%c", r.Depth, r.Path, r.Dir, r.Ignored, max(r.Code, '-')))
	}
	want := []string{"0:build:true:true:-", "0:src:true:false:-", "1:src/gen:true:true:-", "1:src/trace.log:false:true:-",
		"1:src/x.go:false:false:M", "0:a.txt:false:false:-", "0:debug.log:false:true:-"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("行：\n%v\n要\n%v", got, want)
	}
	if !tr.Dir("build").Ignored || tr.Dir("src").Ignored {
		t.Error("只有整个被忽略的目录才标 Ignored")
	}
	// git 会把只装着被忽略文件的未跟踪目录也列出来（"logs/" 和 "logs/t.log"）：目录里有文件，不灰、不按需读
	var tl Tree
	tl.Done(tl.Start(), []string{"a.txt"}, nil, []string{"logs/", "logs/t.log"})
	if d := tl.Dir("logs"); d == nil || d.Ignored || len(d.Files) != 1 || tl.lazyDir("logs") {
		t.Errorf("logs/：%+v", d)
	}
	// 筛选也找得到被忽略的文件
	if hs := matchRows(&tr, "trace", 0, codes); len(hs) != 1 || !hs[0].Ignored || hs[0].Path != "src/trace.log" || hs[0].Code != 0 {
		t.Errorf("筛选：%+v", hs)
	}
	// 关掉之后重读：一个都没有
	tr.Done(tr.Start(), []string{"a.txt", "src/x.go"}, nil, nil)
	if tr.Ignored != nil || tr.Dir("build") != nil {
		t.Error("不显示被忽略的文件时树里还有它们")
	}
}

// 被忽略的目录展开时一层一层读磁盘；太大的只列 FilterCap 项，末尾一行说还有几个。
func TestLazyDir(t *testing.T) {
	root := t.TempDir()
	write := func(p string) {
		abs := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(abs), 0o755)
		os.WriteFile(abs, []byte("x"), 0o644)
	}
	write("a.txt")
	write("build/out/deep.bin")
	write("build/x.o")
	write("build/.DS_Store")
	for i := range FilterCap + 3 {
		write(fmt.Sprintf("big/f%04d", i))
	}
	var tr Tree
	tr.Done(tr.Start(), []string{"a.txt"}, nil, []string{"build/", "big/"})
	if tr.ReadLazy(root) {
		t.Error("没展开的不该读")
	}
	tr.Toggle("build")
	if !tr.ReadLazy(root) || tr.ReadLazy(root) {
		t.Error("展开后读一次，再调不再读")
	}
	tr.Toggle("build/out")
	tr.ReadLazy(root)
	if got := names(tr.Rows(0, nil, nil, nil)); !reflect.DeepEqual(got, []string{"0:big", "0:build", "1:build/out", "2:build/out/deep.bin", "1:build/x.o", "0:a.txt"}) {
		t.Errorf("行 %v", got)
	}
	if hs := matchRows(&tr, "x.o", 0, nil); len(hs) != 1 || hs[0].Path != "build/x.o" || !hs[0].Ignored || !reflect.DeepEqual(hs[0].Hits, []int{6, 7, 8}) {
		t.Errorf("筛选读到的那层：%+v", hs)
	}
	tr.Toggle("big")
	tr.ReadLazy(root)
	rows := tr.Rows(0, nil, nil, nil)
	last := rows[len(rows)-1]
	for _, r := range rows {
		if r.More > 0 {
			last = r
		}
	}
	if len(tr.Lazy["big"]) != FilterCap || last.More != 3 || last.Path != "big" {
		t.Errorf("大目录：列了 %d 项，说明行 %+v", len(tr.Lazy["big"]), last)
	}
	// 重读（git 状态变了）清掉读过的层，展开着的下次再读
	tr.Done(tr.Start(), []string{"a.txt"}, nil, []string{"build/", "big/"})
	if tr.Lazy != nil || !tr.ReadLazy(root) {
		t.Error("Done 之后应该重读展开着的层")
	}
}

// 合并单子目录：internal/ui 合成一行（Path 是最深的、Chain 是那串目录、深度只算一层）；展开时链上每一层都开，
// 收起只收最深的；中间那层多出文件后链拆开，拆出来的两层还展开着；被忽略的目录、keep 不合。
func TestCompactRows(t *testing.T) {
	var tr Tree
	tr.Done(tr.Start(), []string{"internal/ui/view.go", "internal/ui/list.go", "cmd/app/main.go", "cmd/tool/x.go", "go.mod"}, nil, nil)
	rows := tr.CompactRows(1, "", nil, nil, nil)
	if got, want := names(rows), []string{"1:cmd", "1:internal/ui", "1:go.mod"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("合并后 %v，要 %v", got, want)
	}
	if ui := rows[1]; !reflect.DeepEqual(ui.Chain, []string{"internal", "internal/ui"}) || ui.Name != "ui" || ui.Open || !ui.Holds("internal") || ui.Holds("cmd") || ui.Label() != "internal / ui" || rows[0].Label() != "cmd" {
		t.Errorf("合并行 %+v", ui)
	}
	if rows[0].Chain != nil {
		t.Errorf("cmd 有两个子目录，不该合：%+v", rows[0])
	}
	tr.ToggleRow(rows[1])
	if !tr.Open["internal"] || !tr.Open["internal/ui"] {
		t.Fatalf("展开合并行应把每一层都展开：%v", tr.Open)
	}
	rows = tr.CompactRows(1, "", nil, nil, nil)
	if got, want := names(rows), []string{"1:cmd", "1:internal/ui", "2:internal/ui/list.go", "2:internal/ui/view.go", "1:go.mod"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("展开后 %v，要 %v", got, want)
	}
	tr.ToggleRow(rows[1])
	if !tr.Open["internal"] || tr.Open["internal/ui"] {
		t.Errorf("收起合并行只收最深那段：%v", tr.Open)
	}
	tr.SetOpen("internal/ui", true)

	// 中间那层多了个文件：拆成两行，都还展开着
	tr.Apply([]string{"internal/doc.go"}, nil, nil, nil)
	rows = tr.CompactRows(1, "", nil, nil, nil)
	if got, want := names(rows), []string{"1:cmd", "1:internal", "2:internal/ui", "3:internal/ui/list.go", "3:internal/ui/view.go", "2:internal/doc.go", "1:go.mod"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("拆开后 %v，要 %v", got, want)
	}
	// 去掉它又合回去；keep 那一层自己一行
	tr.Apply(nil, []string{"internal/doc.go"}, nil, nil)
	if got := names(tr.CompactRows(1, "", nil, nil, nil)); !reflect.DeepEqual(got[1], "1:internal/ui") {
		t.Errorf("去掉文件后没合回去：%v", got)
	}
	if got, want := names(tr.CompactRows(1, "internal", nil, nil, nil))[1:3], []string{"1:internal", "2:internal/ui"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keep 的目录该自己一行：%v", got)
	}
	// 三层的链；不合并的 Rows 照旧
	tr.Apply([]string{"a/b/c/d.txt"}, nil, nil, nil)
	rows = tr.CompactRows(0, "", nil, nil, nil)
	if rows[0].Path != "a/b/c" || len(rows[0].Chain) != 3 || rows[0].Depth != 0 {
		t.Errorf("三层的链 %+v", rows[0])
	}
	if got := names(tr.Rows(0, nil, nil, nil))[:2]; !reflect.DeepEqual(got, []string{"0:a", "0:cmd"}) {
		t.Errorf("Rows 不该合：%v", got)
	}

	// 被忽略的目录（里面懒读）、只有一个被忽略子目录的目录不合
	var ig Tree
	ig.Done(ig.Start(), []string{"x.go"}, nil, []string{"out/", "wrap/gen/"})
	if got, want := names(ig.CompactRows(0, "", nil, nil, nil)), []string{"0:out", "0:wrap", "0:x.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("被忽略的 %v，要 %v", got, want)
	}
}

// 增量：加文件（含新目录）、去文件（空了的目录跟着去掉，Extra 里的空目录留着）；Paths 换一份新的、旧的不变；
// 筛选的路径缓存跟着变；已经在的 / 已经不在的跳过。
func TestApply(t *testing.T) {
	var tr Tree
	tr.Done(tr.Start(), []string{"a/x.go", "a/y.go", "b/z.go", "keep/k.go", "top.txt"}, nil, nil)
	tr.Extra = map[string]bool{"keep": true}
	old := tr.Paths
	oldCopy := slices.Clone(old)
	tr.Match(fuzzy.New("go"), func(Scored) {}) // 先算一遍路径缓存
	v := tr.Version
	if !tr.Apply([]string{"a/new.go", "c/d/e.go", "a/x.go"}, []string{"b/z.go", "keep/k.go", "nope.txt"}, nil, nil) {
		t.Fatal("Apply 说没变")
	}
	if tr.Version == v || tr.Count != 5 {
		t.Errorf("Version %d → %d，Count %d", v, tr.Version, tr.Count)
	}
	if !reflect.DeepEqual(old, oldCopy) {
		t.Errorf("旧的 Paths 被改了：%v", old)
	}
	if want := []string{"a/x.go", "a/y.go", "top.txt", "a/new.go", "c/d/e.go"}; !reflect.DeepEqual(tr.Paths, want) {
		t.Errorf("Paths %v，要 %v", tr.Paths, want)
	}
	if got, want := names(tr.Rows(0, nil, nil, nil)), []string{"0:a", "0:c", "0:keep", "0:top.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("行 %v，要 %v（b 空了去掉，keep 在 Extra 里留着）", got, want)
	}
	if n := tr.Dir("a"); !reflect.DeepEqual(n.Files, []string{"new.go", "x.go", "y.go"}) {
		t.Errorf("a 里的文件 %v", n.Files)
	}
	if got := names(matchRows(&tr, "new", 0, nil)); !reflect.DeepEqual(got, []string{"0:a/new.go"}) {
		t.Errorf("筛选没看到新文件：%v", got)
	}
	if got := names(matchRows(&tr, "z.go", 0, nil)); len(got) != 0 {
		t.Errorf("筛选还看到去掉的文件：%v", got)
	}
	if tr.Apply([]string{"a/x.go"}, []string{"b/z.go"}, nil, nil) {
		t.Error("没有要改的也说变了")
	}
}

// Diff：磁盘上多出的文件和目录、没了的文件；树里的目录没了要整棵读；单个文件的提示。
func TestDiff(t *testing.T) {
	root := t.TempDir()
	write := func(f string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"a/x.go", "a/sub/y.go", "b/z.go", "top.txt"} {
		write(f)
	}
	var tr Tree
	tr.Done(tr.Start(), []string{"a/x.go", "a/sub/y.go", "b/z.go", "top.txt"}, nil, nil)

	if d := tr.Diff(root, []string{"a"}, nil); !reflect.DeepEqual(d, Delta{}) {
		t.Errorf("没变时 %+v", d)
	}
	write("a/new.go")
	write("a/sub/deep.go")
	write("a/fresh/f.go")
	write("a/.DS_Store")
	os.Remove(filepath.Join(root, "a/x.go"))
	if d := tr.Diff(root, []string{"a"}, nil); !reflect.DeepEqual(d, Delta{Added: []string{"a/new.go", "a/sub/deep.go"}, Removed: []string{"a/x.go"}, NewDirs: []string{"a/fresh"}}) {
		t.Errorf("a 变了：%+v", d)
	}
	// 只看提示的那棵：b 里的变化这次不管
	write("b/other.go")
	if d := tr.Diff(root, []string{"a/sub"}, nil); !reflect.DeepEqual(d.Added, []string{"a/sub/deep.go"}) {
		t.Errorf("只提示 a/sub：%v", d.Added)
	}
	// 树里的目录没了 → 整棵
	os.RemoveAll(filepath.Join(root, "a/sub"))
	if d := tr.Diff(root, []string{"a"}, nil); !d.Full {
		t.Error("a/sub 没了应该整棵读")
	}
	if d := tr.Diff(root, []string{"b"}, []string{"top.txt"}); d.Full {
		t.Error("b 没少目录，不该整棵读")
	}
	// 不在树里的目录：在磁盘上是新目录，没了就不管
	write("c/n.go")
	if d := tr.Diff(root, []string{"c", "gone"}, nil); !reflect.DeepEqual(d.NewDirs, []string{"c"}) {
		t.Errorf("新目录 c：%v", d.NewDirs)
	}
	// 单个文件：新的、删掉的、没变的
	write("saved.txt")
	os.Remove(filepath.Join(root, "top.txt"))
	if d := tr.Diff(root, nil, []string{"saved.txt", "top.txt", "b/z.go"}); !reflect.DeepEqual(d, Delta{Added: []string{"saved.txt"}, Removed: []string{"top.txt"}}) {
		t.Errorf("单个文件：%+v", d)
	}
	// 未跟踪的嵌套仓库（ls-files 里是 "n/"）：不往里看，那一条也不算删了
	write("n/inner.go")
	var nt Tree
	nt.Done(nt.Start(), []string{"n/", "saved.txt"}, nil, nil)
	if d := nt.Diff(root, []string{"n"}, nil); !reflect.DeepEqual(d, Delta{}) {
		t.Errorf("嵌套仓库：%+v", d)
	}
}

// 符号链接：ReadLinks 只认是链接的（目标原样、断了的记 Broken），Walk 读目录时就报出来、不跟进去；
// Apply 加进 / 去掉，Moved 跟着改名，Removed 连同目录下面的去掉，Done 清掉。
func TestLinks(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a.txt", "d/x.txt"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		if err := os.WriteFile(filepath.Join(root, p), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"l": "a.txt", "dl": "d", "bad": "nope", "d/up": "../a.txt", "abs": filepath.Join(root, "a.txt")} {
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	got := ReadLinks(root, []string{"a.txt", "l", "dl", "bad", "d/up", "abs", "missing"})
	want := map[string]Link{"l": {Target: "a.txt"}, "dl": {Target: "d", Dir: true}, "bad": {Target: "nope", Broken: true}, "d/up": {Target: "../a.txt"}, "abs": {Target: filepath.Join(root, "a.txt")}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadLinks %v，要 %v", got, want)
	}
	if ReadLinks(root, []string{"a.txt", "d/x.txt"}) != nil {
		t.Error("没有链接也不是 nil")
	}
	paths, dirs, links, _, err := Walk(context.Background(), root)
	slices.Sort(links)
	if err != nil || !slices.Equal(links, []string{"abs", "bad", "d/up", "dl", "l"}) || !slices.Equal(dirs, []string{"d"}) || !slices.Contains(paths, "dl") {
		t.Errorf("Walk 链接 %v 目录 %v 文件 %v %v", links, dirs, paths, err)
	}

	var tr Tree
	tr.Done(tr.Start(), []string{"a.txt", "l", "d/x.txt", "d/up"}, nil, nil)
	tr.Links = map[string]Link{"l": {Target: "a.txt"}, "d/up": {Target: "../a.txt"}}
	tr.Apply([]string{"bad", "e/new.txt"}, []string{"l"}, nil, map[string]Link{"bad": {Target: "nope", Broken: true}})
	if want := map[string]Link{"bad": {Target: "nope", Broken: true}, "d/up": {Target: "../a.txt"}}; !reflect.DeepEqual(tr.Links, want) {
		t.Errorf("Apply 后 %v，要 %v", tr.Links, want)
	}
	tr.Moved("d", "dd")
	if _, ok := tr.Links["dd/up"]; !ok || len(tr.Links) != 2 {
		t.Errorf("Moved 后 %v", tr.Links)
	}
	tr.Removed("dd")
	if want := map[string]Link{"bad": {Target: "nope", Broken: true}}; !reflect.DeepEqual(tr.Links, want) {
		t.Errorf("Removed 后 %v，要 %v", tr.Links, want)
	}
	tr.Done(tr.Start(), []string{"a.txt"}, nil, nil)
	if tr.Links != nil {
		t.Errorf("Done 没清掉 %v", tr.Links)
	}
}

// 指向目录的链接：画成目录行（git 字母照给），展开时跟着链接一层层读（ReadLazy，里面的行 Linked、没有状态），
// 指向上层的（a/self -> .、a/up -> ..）和穿过 MaxLinkDepth 层链接的不展开；里面的不进筛选，收起再展开重读；
// OutsideLinks 把新建 / 粘贴的地方挪到最外面那个链接所在的目录。
func TestDirLinks(t *testing.T) {
	root := t.TempDir()
	write := func(p string) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(abs), 0o755)
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symlink := func(target, p string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	write("a/x.go")
	write("a/sub/y.go")
	write("z.txt")
	symlink("a", "link")
	symlink(".", "a/self")
	symlink("..", "a/up")
	symlink("sub", "a/inner") // 链接里面的链接目录：往下还能展开
	symlink("nope", "broken")
	paths := []string{"a/inner", "a/self", "a/sub/y.go", "a/up", "a/x.go", "broken", "link", "z.txt"}
	var tr Tree
	tr.Done(tr.Start(), paths, nil, nil)
	tr.Links = ReadLinks(root, []string{"a/inner", "a/self", "a/up", "broken", "link"})
	if l := tr.Links["link"]; !l.Dir || l.Up || !l.Expands() {
		t.Errorf("link：%+v", l)
	}
	for _, p := range []string{"a/self", "a/up"} {
		if l := tr.Links[p]; !l.Dir || !l.Up || l.Expands() {
			t.Errorf("%s 应该不展开：%+v", p, l)
		}
	}
	if l := tr.Links["broken"]; !l.Broken || l.Dir || l.Expands() {
		t.Errorf("broken：%+v", l)
	}
	tr.Toggle("link")
	if !tr.ReadLazy(root) || tr.ReadLazy(root) {
		t.Error("展开后读一次，再调不再读")
	}
	tr.Toggle("link/inner")
	tr.ReadLazy(root)
	rows := tr.Rows(0, map[string]byte{"link": 'A', "z.txt": 'M'}, nil, nil)
	if got := names(rows); !reflect.DeepEqual(got, []string{"0:a", "0:link", "1:link/inner", "2:link/inner/y.go", "1:link/sub", "1:link/self", "1:link/up", "1:link/x.go", "0:broken", "0:z.txt"}) {
		t.Errorf("行 %v", got)
	}
	for _, r := range rows {
		inside := strings.HasPrefix(r.Path, "link/")
		switch {
		case r.Path == "link" && (!r.Dir || r.Code != 'A' || r.Linked):
			t.Errorf("链接那行 %+v", r)
		case inside && (!r.Linked || r.Code != 0 || r.Ignored):
			t.Errorf("链接里面的 %+v", r)
		case r.Path == "link/self" && r.Dir, r.Path == "link/sub" && !r.Dir:
			t.Errorf("链接里面的目录 / 环 %+v", r)
		}
	}
	if !tr.InLink("link/inner/y.go") || tr.InLink("link") || tr.InLink("a/x.go") {
		t.Error("InLink")
	}
	for p, want := range map[string]string{"link": "", "link/inner": "", "link/inner/deeper": "", "a/sub": "a/sub", "": ""} {
		if got := tr.OutsideLinks(p); got != want {
			t.Errorf("OutsideLinks(%q) = %q，要 %q", p, got, want)
		}
	}
	if hs := matchRows(&tr, "y.go", 0, nil); len(hs) != 1 || hs[0].Path != "a/sub/y.go" {
		t.Errorf("筛选只认树里的，不认链接里面的：%+v", hs)
	}
	if hs := matchRows(&tr, "link", 0, nil); len(hs) != 0 {
		t.Errorf("链接目录自己不是筛选里的文件：%+v", hs)
	}
	// 收起丢掉读过的层（里面的链接也丢），再展开重读
	tr.Toggle("link")
	if _, ok := tr.Lazy["link/inner"]; ok || tr.lazyLinks["link/inner"] != (Link{}) {
		t.Errorf("收起后还留着 %v %v", tr.Lazy, tr.lazyLinks)
	}
	tr.Toggle("link")
	if !tr.ReadLazy(root) {
		t.Error("再展开要重读")
	}
	tr.CollapseAll()
	if tr.Lazy != nil || tr.lazyLinks != nil {
		t.Error("全部收起没清掉读过的层")
	}
	// 收起着的、上次留下的里层展开状态：外层没读到之前不读
	tr.Open = map[string]bool{"link/inner": true}
	if tr.ReadLazy(root) {
		t.Error("外面那层收着，里面的不该读")
	}
}

// 一串链接目录：穿过 MaxLinkDepth 层之后再遇到的链接目录标 Deep、画成文件行。
func TestDirLinksDeep(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "x", "y"), 0o755)
	os.Symlink("../y", filepath.Join(root, "x", "y", "l")) // x/y/l -> x/y：每层都是新的一层，不算指向上层
	os.MkdirAll(filepath.Join(root, "x", "z"), 0o755)
	os.Symlink("../z", filepath.Join(root, "x", "y", "l2"))
	os.Symlink("../y", filepath.Join(root, "x", "z", "back")) // x/z/back -> x/y：y 和 z 来回，展开不完
	var tr Tree
	tr.Done(tr.Start(), []string{"x/y/l", "x/y/l2", "x/z/back"}, nil, nil)
	tr.Links = ReadLinks(root, []string{"x/y/l", "x/y/l2", "x/z/back"})
	if !tr.DirLink("x/y/l2") {
		t.Fatalf("x/y/l2：%+v", tr.Links["x/y/l2"])
	}
	p := "x/y/l2"
	for i := 0; ; i++ {
		tr.SetOpen(p, true)
		tr.ReadLazy(root)
		next := p + "/back"
		if i%2 == 1 {
			next = p + "/l2"
		}
		l, ok := tr.Link(next)
		if !ok {
			t.Fatalf("第 %d 层没读到 %s", i, next)
		}
		if !l.Expands() {
			if !l.Deep || tr.linkDepth(next) != MaxLinkDepth {
				t.Errorf("第 %d 层 %s 停了，但不是因为太深：%+v（%d 层）", i, next, l, tr.linkDepth(next))
			}
			break
		}
		if i > 2*MaxLinkDepth {
			t.Fatal("没停下")
		}
		p = next
	}
}

// 文件和链接互换：Diff 走目录时认出树里的普通文件成了链接（Relinked）、Links 里的成了普通文件（Unlinked），
// 提示的单个文件也看（是链接的总重读一次，换了指向的认得出）；Apply 只改 Links、Version 加一、Paths 不换。
func TestDiffLinks(t *testing.T) {
	root := t.TempDir()
	write := func(p string) {
		t.Helper()
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.Remove(filepath.Join(root, filepath.FromSlash(p))) // 是链接的话不往它指的那个里写
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symlink := func(target, p string) {
		t.Helper()
		os.Remove(filepath.Join(root, filepath.FromSlash(p)))
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"a.txt", "d/f.txt", "d/g.txt"} {
		write(p)
	}
	symlink("../a.txt", "d/l")
	var tr Tree
	tr.Done(tr.Start(), []string{"a.txt", "d/f.txt", "d/g.txt", "d/l"}, nil, nil)
	tr.Links = map[string]Link{"d/l": {Target: "../a.txt"}}
	if d := tr.Diff(root, []string{"d"}, nil); !reflect.DeepEqual(d, Delta{}) {
		t.Errorf("没变时 %+v", d)
	}
	// f.txt 换成链接、l 换成普通文件
	symlink("g.txt", "d/f.txt")
	write("d/l")
	d := tr.Diff(root, []string{"d"}, nil)
	if !reflect.DeepEqual(d, Delta{Relinked: []string{"d/f.txt"}, Unlinked: []string{"d/l"}}) {
		t.Fatalf("目录 d：%+v", d)
	}
	if got := tr.Diff(root, nil, []string{"d/f.txt", "d/l", "a.txt"}); !reflect.DeepEqual(got, d) {
		t.Errorf("单个文件：%+v，要和走目录一样", got)
	}
	paths, v := tr.Paths, tr.Version
	if !tr.Apply(nil, nil, d.Unlinked, ReadLinks(root, d.Relinked)) {
		t.Fatal("Apply 说没变")
	}
	if want := map[string]Link{"d/f.txt": {Target: "g.txt"}}; !reflect.DeepEqual(tr.Links, want) || tr.Version != v+1 || !slices.Equal(tr.Paths, paths) {
		t.Errorf("Apply 后 Links %v（要 %v），Version %d → %d，Paths %v", tr.Links, want, v, tr.Version, tr.Paths)
	}
	if tr.Apply(nil, nil, d.Unlinked, ReadLinks(root, d.Relinked)) || tr.Version != v+1 {
		t.Error("再放一遍一样的也说变了")
	}
	// 还是链接、换了指向：走目录不看（只看是不是链接），提示的那个文件重读出来
	symlink("../a.txt", "d/f.txt")
	if d := tr.Diff(root, []string{"d"}, nil); !reflect.DeepEqual(d, Delta{}) {
		t.Errorf("走目录：%+v", d)
	}
	d = tr.Diff(root, nil, []string{"d/f.txt"})
	if !reflect.DeepEqual(d.Relinked, []string{"d/f.txt"}) || !tr.Apply(nil, nil, nil, ReadLinks(root, d.Relinked)) || tr.Links["d/f.txt"].Target != "../a.txt" {
		t.Errorf("换了指向：%+v，Links %v", d, tr.Links)
	}
}

// 键盘找字比的名字：去掉开头的点，合并行每一段都去；显示的 Label 不变。
func TestFindLabel(t *testing.T) {
	var tr Tree
	tr.Done(tr.Start(), []string{".gitignore", ".github/workflows/ci.yml", "src/.config/x.toml", "..odd", "a.go"}, nil, nil)
	got := map[string]string{}
	for _, row := range tr.CompactRows(0, "", nil, nil, nil) {
		got[row.Label()] = row.FindLabel()
	}
	want := map[string]string{".gitignore": "gitignore", ".github / workflows": "github / workflows", "src / .config": "src / config", "..odd": "odd", "a.go": "a.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("找字的名字 %q，要 %q", got, want)
	}
}

// 空目录：FindEmpty 只列最里面的空目录、不进 .git、可以只找几个目录；SetEmpty 放进 Extra、
// 下次没找到的去掉（应用里新建的留着），被忽略的目录、子模块、嵌套仓库里的不放；AddEmpty 只加。
func TestEmptyDirs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/b/c", "e", ".git/objects/x", "full", "only"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "full/f.txt"), []byte("f\n"), 0o644)
	os.WriteFile(filepath.Join(root, "only/.DS_Store"), nil, 0o644)
	ctx := context.Background()
	if got, want := FindEmpty(ctx, root), []string{"a/b/c", "e"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FindEmpty %v，要 %v", got, want)
	}
	if got, want := FindEmpty(ctx, root, "a"), []string{"a/b/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("只找 a：%v，要 %v", got, want)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if got := FindEmpty(done, root); got != nil {
		t.Errorf("取消了还返回 %v", got)
	}
	// 不进被忽略的目录（它们自己也不列）；目录名按字面比，[id] 不当成 i 或 d
	for _, d := range []string{"nm/pkg/empty", "[id]/x", "i/y", "skipped"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	if got, want := FindEmptySkip(ctx, root, []string{"nm/", "[id]/", "skipped"}), []string{"a/b/c", "e", "i/y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FindEmptySkip %v，要 %v", got, want)
	}
	if got, want := FindEmptySkip(ctx, root, nil), []string{"[id]/x", "a/b/c", "e", "i/y", "nm/pkg/empty", "skipped"}; !reflect.DeepEqual(got, want) {
		t.Errorf("什么都不跳：%v，要 %v", got, want)
	}
	for _, d := range []string{"nm/pkg/empty", "[id]", "i", "skipped"} {
		os.RemoveAll(filepath.Join(root, strings.Split(d, "/")[0]))
	}

	var tr Tree
	gen := tr.Start()
	tr.Done(gen, []string{"src/a.go", "sub", "nest/"}, nil, []string{"logs"})
	tr.Extra = map[string]bool{"made": true} // 应用里新建的
	tr.mkdirs("made")
	if tr.SetEmpty(gen+1, []string{"e"}) {
		t.Error("不是这次读的也放了")
	}
	v := tr.Version
	if !tr.SetEmpty(gen, []string{"a/b/c", "e", "src/empty", "logs/old", "sub/x", "nest/y"}) || tr.Version == v {
		t.Fatal("SetEmpty 说没变")
	}
	tr.Reveal("a/b/c")
	tr.Reveal("src/a.go")
	if got, want := names(tr.Rows(0, nil, nil, nil)), []string{"0:a", "1:a/b", "2:a/b/c", "0:e", "0:made", "0:nest", "0:src", "1:src/empty", "1:src/a.go", "0:logs", "0:sub"}; !reflect.DeepEqual(got, want) {
		t.Errorf("行 %v，要 %v", got, want)
	}
	for _, d := range []string{"logs/old", "sub/x", "nest/y"} {
		if tr.Extra[d] || tr.Dir(d) != nil {
			t.Errorf("%s 不该放进来", d)
		}
	}
	// 第二次：e 不空了、a/b/c 还在；应用里新建的 made 不动
	if !tr.SetEmpty(gen, []string{"a/b/c"}) {
		t.Fatal("去掉 e 说没变")
	}
	if tr.Dir("e") != nil || tr.Extra["e"] || !tr.Extra["a/b/c"] || tr.Dir("made") == nil || !tr.Extra["made"] {
		t.Errorf("第二次之后 Extra %v，e 在树里 %v", tr.Extra, tr.Dir("e") != nil)
	}
	if !tr.AddEmpty([]string{"z"}) || tr.Dir("z") == nil || tr.AddEmpty([]string{"z"}) {
		t.Error("AddEmpty 没加上，或者加过的又说变了")
	}
}
