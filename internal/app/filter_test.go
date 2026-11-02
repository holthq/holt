package app

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/testutil"
)

// 筛选是模糊匹配：两个仓库的匹配放在一起按分取前 FilterCap 个——第一个仓库一大堆差的匹配挤不掉第二个仓库
// 好的那个；显示还是按仓库分组（仓库的顺序不变），组里按分（文件名命中的在只在目录里命中的前面），分一样时按树的顺序；
// 字可以隔开；行带着命中的字。
func TestFilterFuzzy(t *testing.T) {
	alpha := testutil.NewRepo(t, "alpha")
	alpha.Write("docs/api.md", "api\n")
	alpha.Write("notes/docs.txt", "notes\n")
	alpha.Write("src/tree_sidebar.go", "package src\n")
	for i := range 600 {
		alpha.Write(fmt.Sprintf("w/d/o/c/s%03d.txt", i), "x\n") // 只在目录里、隔开命中 docs：分低
	}
	alpha.Commit("init")
	beta := testutil.NewRepo(t, "beta")
	beta.Write("docs.md", "# docs\n")
	beta.Write("other.txt", "o\n")
	beta.Commit("init")
	a, wait := newTestApp(t, testutil.Workspace(t, alpha.Root, beta.Root))
	a.SetFilter("docs")
	wait("读两个仓库的文件", func() bool { return a.Repos[0].Files.Loaded && a.Repos[1].Files.Loaded })
	a.SetFilter("docs") // 读完重算（SetFilter 让缓存作废）

	rows := a.TreeRows()
	if !a.TreeTruncated() {
		t.Error("603 个匹配该截断")
	}
	var got []string
	nfiles := 0
	for _, row := range rows {
		if row.Root {
			got = append(got, "["+row.Repo.Name+"]")
			continue
		}
		nfiles++
		if row.Depth != 1 {
			t.Errorf("筛选的行深度 %d：%+v", row.Depth, row)
		}
	}
	if nfiles != files.FilterCap {
		t.Errorf("显示了 %d 个文件，要 %d", nfiles, files.FilterCap)
	}
	if !reflect.DeepEqual(got, []string{"[alpha]", "[beta]"}) {
		t.Fatalf("分组 %v：要按仓库的顺序", got)
	}
	// alpha 那组：文件名就是 docs 的在前，目录叫 docs 的其次，再是那一串差的（分一样，按树的顺序，截到 s496）
	if rows[1].Path != "notes/docs.txt" || rows[2].Path != "docs/api.md" || rows[3].Path != "w/d/o/c/s000.txt" {
		t.Errorf("alpha 组的前三个：%q %q %q", rows[1].Path, rows[2].Path, rows[3].Path)
	}
	if last := rows[len(rows)-3]; last.Path != "w/d/o/c/s496.txt" {
		t.Errorf("alpha 组最后一个 %q，要 w/d/o/c/s496.txt", last.Path)
	}
	if b := rows[len(rows)-1]; b.Repo != a.Repos[1] || b.Path != "docs.md" || !reflect.DeepEqual(b.Hits, []int{0, 1, 2, 3}) {
		t.Errorf("beta 的 docs.md 被挤掉了或命中不对：%+v", b)
	}
	if !reflect.DeepEqual(rows[1].Hits, []int{6, 7, 8, 9}) {
		t.Errorf("notes/docs.txt 命中 %v", rows[1].Hits)
	}

	// 字隔开、大小写、空格
	for _, q := range []string{"tsb", "TreeSide", "tree side"} {
		a.SetFilter(q)
		rows = a.TreeRows()
		if len(rows) != 2 || rows[1].Path != "src/tree_sidebar.go" || a.TreeTruncated() {
			t.Errorf("%q：%d 行 %+v", q, len(rows), rows)
		}
	}
	// 只有空格：不算在筛选，照常的树
	a.SetFilter("   ")
	if rows = a.TreeRows(); len(rows) == 0 || !rows[0].Root || rows[0].Hits != nil || a.TreeTruncated() {
		t.Errorf("只有空格：%+v", rows)
	}
}

// topHits 留下的就是全部排好后的前 FilterCap 个：分有大量相同的（比路径长短、再比先后），个数比 FilterCap 少、正好、多。
func TestTopHits(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{0, 7, files.FilterCap, files.FilterCap + 1, 3000} {
		var top topHits
		var all []repoHit
		for i := range n {
			h := repoHit{files.Scored{Path: strings.Repeat("x", 1+rnd.IntN(4)), Score: int32(rnd.IntN(5))}, int32(i % 3), int32(i)}
			all = append(all, h)
			top.offer(h)
		}
		slices.SortFunc(all, func(x, y repoHit) int { return cmp.Compare(y.rank(), x.rank()) })
		all = all[:min(n, files.FilterCap)]
		got := slices.Clone(top.h)
		slices.SortFunc(got, func(x, y repoHit) int { return cmp.Compare(y.rank(), x.rank()) })
		if !reflect.DeepEqual(got, all) || int(top.seen) != n {
			t.Errorf("%d 个：留下 %d 个、seen %d，和全排的前 %d 个不一样", n, len(got), top.seen, len(all))
		}
	}
}

// 量筛选：5 万个文件（200 个目录 × 250 个，名字像真的代码仓库）一个字一个字敲，每次 TreeRows 重算的用时。
// 目标每次 < 30 ms。WORKBENCH_MEASURE=1 才跑。
func TestMeasureFilter(t *testing.T) {
	if os.Getenv("WORKBENCH_MEASURE") == "" {
		t.Skip("WORKBENCH_MEASURE=1 才量")
	}
	words := []string{"tree", "sidebar", "palette", "watch", "files", "search", "commit", "graph", "config", "index"}
	paths := make([]string, 0, 50000)
	for d := range 200 {
		for f := range 250 {
			paths = append(paths, fmt.Sprintf("internal/%s%d/sub_%s/%s_%s_%03d.go", words[d%10], d, words[(d/10)%10], words[f%10], words[(f/10)%10], f))
		}
	}
	a := bigFilterApp(paths)
	// 第一个字要先算一遍路径缓存（筛选每个目录第一次用时拼完整路径），单独量
	a.Filter, a.tree.ver = "t", a.tree.ver+1
	start := time.Now()
	a.TreeRows()
	t.Logf("第一个字（连路径缓存）：%v", time.Since(start).Round(100*time.Microsecond))
	// 每个查询量 7 次取中位数（机器忙时个别几次会被别的进程拖慢）
	worst := time.Duration(0)
	for _, q := range []string{"t", "tr", "tre", "tree", "trees", "treesi", "treesid", "treeside", "treesideb", "treesideba", "treesidebar", "s", "zzz", "internal", "go"} {
		var ds []time.Duration
		var rows []TreeRow
		for range 7 {
			a.Filter, a.tree.ver = q, a.tree.ver+1
			start := time.Now()
			rows = a.TreeRows()
			ds = append(ds, time.Since(start))
		}
		slices.Sort(ds)
		worst = max(worst, ds[3])
		t.Logf("%-12q %3d 行%s：中位 %v、最快 %v、最慢 %v", q, len(rows), map[bool]string{true: "（截断）"}[a.TreeTruncated()],
			ds[3].Round(100*time.Microsecond), ds[0].Round(100*time.Microsecond), ds[6].Round(100*time.Microsecond))
	}
	t.Logf("中位数最慢 %v", worst.Round(100*time.Microsecond))
}

// bigFilterApp 是只有一个仓库、树里是 paths 的 App（量筛选用，不起 git）。
func bigFilterApp(paths []string) *App {
	r := &Repo{Name: "big"}
	r.Files.Done(r.Files.Start(), paths, nil, nil)
	return &App{Repos: []*Repo{r}}
}
