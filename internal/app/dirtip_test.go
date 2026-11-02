package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/testutil"
)

// 目录行悬停的大小、最近改动和大仓库一层层找空目录，都在临时仓库上测。

func TestDirTip(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	os.MkdirAll(filepath.Join(root, "d", "sub"), 0o755)
	newest := time.Now().Add(-50 * time.Hour)
	for name, size := range map[string]int{"d/x.txt": 10, "d/y.txt": 20, "d/sub/z.txt": 30} {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o644)
		mod := newest.Add(-time.Duration(size) * time.Hour)
		if name == "d/y.txt" {
			mod = newest
		}
		os.Chtimes(p, mod, mod)
	}
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("读好", func() bool { return idle(r)() && hasFile(r, "d/sub/z.txt") })

	// 第一次悬停：文件数马上有，后两段在算；算好了换上，再悬停不重算
	_, d := treeRow(t, a, "d")
	if tip := a.RowTip(d); tip != "3 个文件 · 正在算…" {
		t.Fatalf("第一帧：%q", tip)
	}
	want := "3 个文件 · 共 60 B · 最近改动 2 天前"
	wait("算好", func() bool { return a.RowTip(d) == want })
	c := a.dirInfos[d.Key()]
	if a.RowTip(d); a.dirInfos[d.Key()] != c || c.busy {
		t.Fatal("算好了又重算")
	}

	// 保存过、状态重读了（文件列表没整棵读）：先给上次的，后台重算（大小变了跟着变）
	os.WriteFile(filepath.Join(root, "d", "x.txt"), []byte(strings.Repeat("x", 110)), 0o644)
	at, gen := r.LoadedAt, r.Files.Gen
	r.hints.noteFile("d/x.txt")
	a.RefreshRepo(r, WhySave)
	wait("状态重读", func() bool { return idle(r)() && !r.LoadedAt.Equal(at) })
	if r.Files.Gen != gen {
		t.Fatal("保存后整棵重读了文件列表")
	}
	if tip := a.RowTip(d); tip != want {
		t.Fatalf("重算时先给上次的：%q", tip)
	}
	wait("重算好", func() bool { return a.RowTip(d) == "3 个文件 · 共 160 B · 最近改动 刚刚" })

	// 文件列表整棵重读过：缓存清掉，又从"正在算"开始
	a.LoadFiles(r)
	wait("重读文件列表", func() bool { return idle(r)() && hasFile(r, "d/x.txt") })
	_, sub := treeRow(t, a, "d")
	if tip := a.RowTip(sub); tip != "3 个文件 · 正在算…" {
		t.Fatalf("重读之后：%q", tip)
	}
	wait("又算好", func() bool { return strings.HasPrefix(a.RowTip(sub), "3 个文件 · 共 160 B") })
}

func TestEmptyDirsLazy(t *testing.T) {
	old := files.MaxEmptyScan
	files.MaxEmptyScan = 1 // 临时仓库有三四个文件：当大仓库
	t.Cleanup(func() { files.MaxEmptyScan = old })
	root, ws := testutil.TempRepo(t)
	for _, d := range []string{"e1", "outer/inner", "ign", "keep/deep/er"} {
		os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755)
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ign/\n"), 0o644)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	r.Expanded = true
	r.Files.SetOpen("keep", true) // 会话里记着展开的：读好文件列表后也读它那一层
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)

	has := func(d string) bool { a.TreeRows(); return r.Files.Dir(d) != nil }
	wait("根下的空目录", func() bool { return idle(r)() && r.Files.Loaded && has("e1") && has("outer") && has("keep/deep") })
	if has("outer/inner") || has("keep/deep/er") {
		t.Fatal("没展开的那层也读了")
	}
	if has("ign") {
		t.Fatal("被忽略的空目录也放进来了")
	}
	if r.emptyRead[""] != r.Files.Gen || r.emptyRead["keep"] != r.Files.Gen {
		t.Fatalf("读过的：%v", r.emptyRead)
	}

	// 展开 outer：读它那一层
	a.ToggleDir(r, "outer")
	wait("outer 里面的", func() bool { return has("outer/inner") })

	// 增量刷新时冒出来的新目录：只算它自己，里面的不往下找
	os.MkdirAll(filepath.Join(root, "fresh", "kid"), 0o755)
	r.hints.dirs = append(r.hints.dirs, "fresh")
	a.RefreshRepo(r, WhyWatchFiles)
	wait("新目录", func() bool { return idle(r)() && has("fresh") })
	if has("fresh/kid") {
		t.Fatal("新目录里面的也找了")
	}
}
