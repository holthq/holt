package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/search"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/watch"
)

// excludeWs 写一个带 settings["files.exclude"] 的工作区文件。
func excludeWs(t *testing.T, ws string, root string, exclude map[string]any) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"folders": []map[string]string{{"path": root}}, "settings": map[string]any{"files.exclude": exclude}})
	if err := os.WriteFile(ws, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFilesExclude：工作区文件、仓库的 .vscode/settings.json、设置三处的 files.exclude 并在一起，文件树没有、Excluded 数对、
// 搜索（文件树给的列表、自己 ls-files 的、连被忽略的一起搜的）也搜不到；设置、工作区文件改了重读。
func TestFilesExclude(t *testing.T) {
	r0 := testutil.NewRepo(t, "ex")
	r0.Write(".gitignore", "ign/\n")
	r0.Write(".vscode/settings.json", "{\n\t// 只藏根下的\n\t\"files.exclude\": {\"*.log\": true,},\n}\n")
	for _, p := range []string{"src/a.go", "build/out.o", "web/build/k.go", "x.log", "logs/y.log", "src/tmp/t.go"} {
		r0.Write(p, "needle\n")
	}
	r0.Commit("初始")
	r0.Write("web/node_modules/m.js", "needle\n") // 没跟踪的
	r0.Write("ign/keep.js", "needle\n")
	r0.Write("ign/node_modules/q.js", "needle\n")
	ws := filepath.Join(t.TempDir(), "ex.code-workspace")
	excludeWs(t, ws, r0.Root, map[string]any{"**/node_modules": true, "build": true, "src": false, "**/*.go": map[string]string{"when": "$(basename).ts"}})
	a, wait := newTestApp(t, ws)
	a.St.Settings.Exclude, a.excludeOn = "**/tmp, ", "**/tmp, "
	r := a.Repos[0]
	want := []string{".gitignore", ".vscode/settings.json", "logs/y.log", "src/a.go", "web/build/k.go"}
	found := func(q search.Query) []string {
		t.Helper()
		roots, _ := a.rootsIn(nil)
		res := search.Search(context.Background(), roots, q)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		var out []string
		for _, m := range res.Matches {
			out = append(out, m.Path)
		}
		return out
	}
	needles := []string{"logs/y.log", "src/a.go", "web/build/k.go"}
	if got := found(search.Query{Text: "needle"}); !slices.Equal(got, needles) {
		t.Errorf("文件树还没读时搜到 %q，要 %q", got, needles)
	}

	a.EnsureFiles(r)
	wait("读文件树", func() bool { return r.Files.Loaded && !r.Files.Loading })
	if got := r.Files.Paths; !slices.Equal(got, want) {
		t.Errorf("Paths = %q，要 %q", got, want)
	}
	if r.Excluded != 4 {
		t.Errorf("Excluded = %d，要 4", r.Excluded)
	}
	i18n.Set("")
	if got := a.RepoTip(r); got != "按 files.exclude 藏了 4 个" {
		t.Errorf("RepoTip = %q", got)
	}
	if got := found(search.Query{Text: "needle"}); !slices.Equal(got, needles) {
		t.Errorf("搜到 %q，要 %q", got, needles)
	}
	if got := found(search.Query{Text: "needle", Ignored: true}); !slices.Equal(got, append([]string{"ign/keep.js"}, needles...)) {
		t.Errorf("连被忽略的一起搜到 %q", got)
	}

	old := ExcludeDelay
	ExcludeDelay = 0
	t.Cleanup(func() { ExcludeDelay = old })
	a.St.Settings.Exclude = ""
	a.SettingsChanged()
	wait("设置改了重读", func() bool { return hasFile(r, "src/tmp/t.go") })

	excludeWs(t, ws, r0.Root, map[string]any{"build": true})
	future := time.Now().Add(time.Minute)
	os.Chtimes(ws, future, future)
	a.RefreshAll(WhyManual)
	wait("工作区文件改了重读", func() bool { return idle(r)() && hasFile(r, "web/node_modules/m.js") })
	if r.Files.Has("build/out.o") || r.Files.Has("x.log") || r.Excluded != 2 {
		t.Errorf("还该藏着 build、x.log：Excluded = %d", r.Excluded)
	}
}

// TestExcludeIncremental：增量刷新加进来的文件也按 files.exclude 去掉，藏起来的目录里的变化不整棵读；
// 改了 .vscode/settings.json 的 files.exclude 才整棵读。
func TestExcludeIncremental(t *testing.T) {
	r0 := testutil.NewRepo(t, "exinc")
	r0.Write(".vscode/settings.json", `{"files.exclude": {"*.log": true}}`)
	r0.Write("src/a.go", "package src\n")
	r0.Write("web/node_modules/m.js", "m\n")
	r0.Write("web/w.js", "w\n")
	r0.Commit("初始")
	ws := filepath.Join(t.TempDir(), "exinc.code-workspace")
	excludeWs(t, ws, r0.Root, map[string]any{"**/node_modules": true})
	a, wait := newTestApp(t, ws)
	r := a.Repos[0]
	var calls atomic.Int32
	old := lsFiles
	lsFiles = func(ctx context.Context, root string) (git.Listing, error) { calls.Add(1); return old(ctx, root) }
	t.Cleanup(func() { lsFiles = old })
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("读文件树", func() bool { return idle(r)() && hasFile(r, "src/a.go") })
	if r.Files.Has("web/node_modules/m.js") {
		t.Fatal("node_modules 没藏")
	}
	step := func(what string, act func(), more int32, cond func() bool) {
		t.Helper()
		before := calls.Load()
		act()
		wait(what, func() bool { return idle(r)() && !r.Files.Loading && cond() })
		if got := calls.Load() - before; got != more {
			t.Errorf("%s：整棵读了 %d 次，要 %d 次", what, got, more)
		}
	}
	changed := func(dirs ...string) { a.watchChanged(r, watch.Change{Files: true, Dirs: dirs}) }
	step("藏起来的目录里新建", func() {
		r0.Write("web/node_modules/n.js", "n\n")
		changed("web/node_modules")
	}, 0, func() bool { return true })
	step("新的 node_modules", func() {
		r0.Write("src/node_modules/x.js", "x\n")
		r0.Write("src/b.go", "package src\n")
		changed("src", "web")
	}, 0, func() bool { return hasFile(r, "src/b.go") })
	if r.Files.Has("src/node_modules/x.js") || r.Files.Has("web/node_modules/n.js") || r.Files.Dir("src/node_modules") != nil {
		t.Error("增量刷新把藏起来的加进树了")
	}
	step("保存根下的 log", func() {
		r0.Write("x.log", "x\n")
		r.hints.noteFile("x.log")
		a.RefreshRepo(r, WhySave)
	}, 0, func() bool { return true })
	if r.Files.Has("x.log") {
		t.Error("x.log 该藏着")
	}
	step("改了 .vscode/settings.json", func() {
		r0.Write(".vscode/settings.json", `{"files.exclude": {"*.log": false}}`)
		changed(".vscode")
	}, 1, func() bool { return hasFile(r, "x.log") })
	if r.Files.Has("src/node_modules/x.js") || r.Excluded != 3 {
		t.Errorf("工作区文件的 **/node_modules 还在：Excluded = %d", r.Excluded)
	}
}
