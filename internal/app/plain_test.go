package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// 普通文件夹（不是 Git 仓库）和一个仓库放在同一个工作区：文件树直接列目录，Git 栏只有段头、不能展开，
// 历史不读、"所有仓库"跳过它，git 命令不能用（git init 除外），搜索也搜它；监听照样报文件变化，
// git init 之后重读就成了仓库。git init 只在 t.TempDir() 里跑。
func TestPlainFolder(t *testing.T) {
	g := testutil.NewRepo(t, "g")
	g.Write("README.md", "仓库\n")
	g.Commit("一")
	dir := filepath.Join(t.TempDir(), "notes")
	for _, f := range []string{"a.txt", "sub/b.go", "node_modules/m/i.js", ".cache/z"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("needle "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws := wsFile(t, "混合", g.Root, dir)
	a, wait := newTestApp(t, ws)
	defer a.Close()
	a.Start()
	gr, r := a.Repos[0], a.Repos[1]
	wait("两个都读完", func() bool { return idle(gr)() && idle(r)() && r.Watching() && gr.Watching() })
	if !r.Plain || r.Err != nil || gr.Plain || r.Status.Count != 0 || r.Branches != nil {
		t.Fatalf("plain=%v err=%v 仓库 plain=%v status=%+v", r.Plain, r.Err, gr.Plain, r.Status)
	}

	// 文件树：直接列目录，node_modules 和点开头的目录列着但不往里走（见 TestPlainDotDirs）
	a.ToggleRepo(r)
	wait("文件树", func() bool { return r.Files.Loaded && !r.Files.Loading })
	if r.Files.Err != nil || !slices.Equal(r.Files.Paths, []string{"a.txt", "sub/b.go"}) {
		t.Fatalf("文件：%v %v", r.Files.Paths, r.Files.Err)
	}

	// Git 栏：只有段头；展开 / 收起都不动它
	rows := a.GitRows()
	if n := len(slices.DeleteFunc(slices.Clone(rows), func(row ChangeRow) bool { return row.Repo != r })); n != 1 || r.GitOpen() {
		t.Errorf("普通文件夹在 Git 栏里有 %d 行，open=%v", n, r.GitOpen())
	}
	a.ToggleGitRepo(r)
	a.SetAllGitOpen(false)
	if r.GitFold != FoldAuto || r.GitOpen() || gr.GitOpen() || a.AllGitOpen() {
		t.Errorf("fold=%d open=%v 仓库 open=%v all=%v", r.GitFold, r.GitOpen(), gr.GitOpen(), a.AllGitOpen())
	}
	a.SetAllGitOpen(true)
	if r.GitFold != FoldAuto || !a.AllGitOpen() {
		t.Errorf("全部展开后 fold=%d all=%v", r.GitFold, a.AllGitOpen())
	}

	// 历史：不读；"所有仓库"只排仓库的提交
	a.EnsureHistory(r)
	if r.Hist.Loading || r.Hist.Loaded {
		t.Error("普通文件夹读了历史")
	}
	a.EnsureAllHistory()
	wait("仓库的历史", func() bool { return gr.Hist.Loaded && !gr.Hist.Loading })
	if !a.AllHistDone() || AllHistShown(a.AllHistRows()) != 1 {
		t.Errorf("所有仓库：done=%v 提交 %d", a.AllHistDone(), AllHistShown(a.AllHistRows()))
	}

	// 命令：当前是普通文件夹时 git 命令都不能用（刷新、运行 git 命令、全部抓取除外）
	a.UseRepo(r)
	for id, want := range map[string]bool{"git.fetch": false, "git.pull": false, "git.push": false, "git.stageAll": false,
		"git.newBranch": false, "git.stash": false, "git.refresh": true, "git.command": true, "git.fetchAll": true} {
		if got := a.CanRun(Find(id)); got != want {
			t.Errorf("%s 能用 = %v，要 %v", id, got, want)
		}
	}
	a.RunCommand(r, "git status")
	if a.Toast != "当前文件夹不是 Git 仓库" || r.Running() != nil || len(r.Queue) != 0 {
		t.Errorf("git status：toast %q", a.Toast)
	}
	a.UseRepo(gr)
	if !a.CanRun(Find("git.newBranch")) { // 抓取要有远程（FetchWhy），这里看新建分支
		t.Error("切回仓库后新建分支还是不能用")
	}

	// 搜索：普通文件夹也搜（列目录）
	a.Search.Query.Text = "needle"
	a.SearchNow()
	wait("搜完", func() bool { return !a.Search.Running })
	var hits []string
	for _, m := range a.Search.Res.Matches {
		if m.Root == r.Root {
			hits = append(hits, m.Path)
		}
	}
	if a.Search.Res.Err != nil || !slices.Equal(hits, []string{"a.txt", "sub/b.go"}) {
		t.Errorf("搜到 %v（%v）", hits, a.Search.Res.Err)
	}

	// 监听：新建的文件进文件树
	os.WriteFile(filepath.Join(dir, "sub", "c.md"), []byte("新\n"), 0o644)
	wait("新文件进文件树", func() bool { return r.Files.Loaded && slices.Contains(r.Files.Paths, "sub/c.md") })

	// git init（命令面板里唯一能跑的）：跑完重读就成了仓库，文件树换成 ls-files、监听重开
	a.RunCommand(r, "init")
	wait("成了仓库", func() bool {
		return idle(r)() && !r.Plain && r.Status.Untracked > 0 && r.Watching() && r.Files.Loaded && !r.Files.Loading
	})
	if !slices.Contains(r.Files.Paths, "a.txt") || !r.GitOpen() {
		t.Errorf("git init 之后：文件 %v open=%v", r.Files.Paths, r.GitOpen())
	}
	a.UseRepo(r)
	if !a.CanRun(Find("git.stageAll")) {
		t.Error("git init 之后暂存全部还不能用")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
}

// 普通文件夹里点开头的目录、node_modules 当被忽略的目录列（不管"显示被忽略的文件"）：树里有、收着、灰显，
// 里面的不在 Paths 里（⌘P 搜不到），展开时读一层；.git 不列；根下新建的点目录监听到了也进树。
func TestPlainDotDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "py")
	for _, f := range []string{"main.py", ".coverage", ".venv/bin/python", ".venv/pyvenv.cfg", ".pytest_cache/README.md",
		"node_modules/m/i.js", ".git/HEAD", ".DS_Store"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(dir, ".git", "HEAD")) // 空的 .git 目录：git 不认它是仓库，Walk 也不列
	ws := wsFile(t, "py", dir)
	a, wait := newTestApp(t, ws)
	defer a.Close()
	a.Start()
	r := a.Repos[0]
	wait("读完", func() bool { return idle(r)() && r.Watching() })
	if !r.Plain {
		t.Fatal("不是普通文件夹")
	}
	if !r.Expanded { // 只有一个仓库时本来就展开着
		a.ToggleRepo(r)
	}
	wait("文件树", func() bool { return r.Files.Loaded && !r.Files.Loading })
	if !slices.Equal(r.Files.Paths, []string{".coverage", "main.py"}) {
		t.Fatalf("文件：%v %v", r.Files.Paths, r.Files.Err)
	}
	row := func(p string) (TreeRow, bool) {
		for _, row := range a.TreeRows() {
			if row.Repo == r && !row.Root && row.Path == p {
				return row, true
			}
		}
		return TreeRow{}, false
	}
	for _, p := range []string{".pytest_cache", ".venv", "node_modules"} {
		if row, ok := row(p); !ok || !row.Dir || row.Open || !row.Ignored {
			t.Errorf("%s：有 %v、目录 %v、展开 %v、灰显 %v", p, ok, row.Dir, row.Open, row.Ignored)
		}
	}
	for _, p := range []string{".git", ".DS_Store", ".venv/pyvenv.cfg"} {
		if _, ok := row(p); ok {
			t.Errorf("树里有 %s", p)
		}
	}

	// 展开 .venv：读到里面一层
	a.ToggleDir(r, ".venv")
	if row, ok := row(".venv/bin"); !ok || !row.Dir || !row.Ignored {
		t.Errorf(".venv/bin：%v %+v", ok, row)
	}
	if row, ok := row(".venv/pyvenv.cfg"); !ok || !row.Ignored {
		t.Errorf(".venv/pyvenv.cfg：%v %+v", ok, row)
	}

	// ⌘P：搜不到 .venv 里的
	p := &a.Palette
	a.OpenPalette("pyvenv")
	a.UpdatePalette()
	if got := itemTexts(p.Items); slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "pyvenv") }) {
		t.Errorf("⌘P 搜到了 .venv 里的：%v", got)
	}
	a.ClosePalette()

	// 根下新建 .cache/、删掉 .pytest_cache/：树跟着变
	if err := os.Mkdir(filepath.Join(dir, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".pytest_cache")); err != nil {
		t.Fatal(err)
	}
	wait("新的点目录进树", func() bool {
		c, ok := row(".cache")
		_, old := row(".pytest_cache")
		return ok && c.Ignored && !old
	})
}
