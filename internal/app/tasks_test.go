package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 面板 "!"：读完状态后列所有仓库的任务（"仓库 · 命令"，右边是哪个文件），空查询下面一行说找哪些；
// 跑过的（TaskHistory）排最前、标最近跑过，记进 state.json；改了 Makefile 重读状态后跟着变；没有任务的工作区说没认出。
func TestTaskPalette(t *testing.T) {
	alpha, beta := testutil.NewRepo(t, "alpha"), testutil.NewRepo(t, "beta")
	alpha.Write("package.json", `{"scripts":{"dev":"vite","build":"vite build"}}`)
	alpha.Write("pnpm-lock.yaml", "")
	beta.Write("Makefile", ".PHONY: all\nall:\n\techo all\ncheck: all\n")
	beta.Write("go.mod", "module beta\n")
	ws := testutil.Workspace(t, alpha.Root, beta.Root)
	state := t.TempDir()
	a, wait := newTestAppIn(t, ws, state)
	for _, r := range a.Repos {
		a.RefreshRepo(r, WhyManual)
	}
	for _, r := range a.Repos {
		wait("读完状态", idle(r))
	}
	texts := func() []string {
		var out []string
		for _, it := range a.Palette.Items {
			out = append(out, it.Text+" | "+it.Detail)
		}
		return out
	}
	a.OpenPalette("!")
	a.UpdatePalette()
	want := []string{
		"alpha · pnpm run dev | package.json", "alpha · pnpm run build | package.json",
		"beta · make all | Makefile", "beta · make check | Makefile",
		"beta · go build ./... | go.mod", "beta · go test ./... | go.mod", "beta · go vet ./... | go.mod",
	}
	if got := texts(); !slices.Equal(got, want) || a.Palette.Note != "找 package.json 的 scripts、Makefile / justfile 的目标、cargo / go 的常用命令" {
		t.Fatalf("! 列表：%q，提示 %q", got, a.Palette.Note)
	}

	// 模糊匹配：命令里的字
	a.Palette.Text = "!gotest"
	a.UpdatePalette()
	if got := texts(); len(got) == 0 || got[0] != "beta · go test ./... | go.mod" {
		t.Fatalf("!gotest：%q", got)
	}
	a.Palette.Text = "!zzzz"
	a.UpdatePalette()
	if len(a.Palette.Items) != 0 || a.Palette.Note != "没有匹配的任务" {
		t.Fatalf("没匹配：%q %q", texts(), a.Palette.Note)
	}

	// 跑过的靠前（不真跑：只记历史），最近的在最前；存进 state.json
	a.noteTask(a.Repos[1], a.Repos[1].Tasks[1]) // make check
	a.noteTask(a.Repos[0], a.Repos[0].Tasks[1]) // pnpm run build
	a.SaveState()
	a.Palette.Text = "!"
	a.UpdatePalette()
	if got := texts(); len(got) != 7 || got[0] != "alpha · pnpm run build | package.json" || got[1] != "beta · make check | Makefile" ||
		!a.Palette.Items[0].Recent || a.Palette.Items[2].Recent {
		t.Fatalf("跑过的应靠前：%q", got)
	}
	a.Palette.Text = "!make"
	a.UpdatePalette()
	if got := texts(); len(got) < 2 || got[0] != "beta · make check | Makefile" {
		t.Fatalf("!make 跑过的靠前：%q", got)
	}
	st, _ := workspace.LoadState(state)
	if len(st.TaskHistory) != 2 || st.TaskHistory[0] != (workspace.TaskRun{Repo: a.Repos[0].Root, Cmd: "pnpm run build"}) {
		t.Fatalf("state.json 里的任务历史：%+v", st.TaskHistory)
	}

	// 改了 Makefile：重读状态后列表跟着变（Stamp 变了才重扫）
	later := time.Now().Add(time.Minute)
	beta.Write("Makefile", "lint:\n")
	os.Chtimes(filepath.Join(beta.Root, "Makefile"), later, later)
	a.RefreshRepo(a.Repos[1], WhyManual)
	wait("重读", idle(a.Repos[1]))
	a.Palette.Text = "!make"
	a.UpdatePalette()
	if got := texts(); len(got) != 1 || got[0] != "beta · make lint | Makefile" {
		t.Fatalf("改了 Makefile：%q", got)
	}
	if got := a.TasksOf(a.Repos[1], "Makefile"); len(got) != 1 || got[0].Cmd != "make lint" || a.TasksOf(a.Repos[1], "sub/Makefile") != nil {
		t.Fatalf("文件树右键的任务：%+v", got)
	}
	a.ClosePalette()

	// 没有任务的工作区
	_, ws2 := testutil.TempRepo(t)
	b, wait2 := newTestApp(t, ws2)
	b.RefreshRepo(b.Repos[0], WhyManual)
	wait2("读完状态", idle(b.Repos[0]))
	b.OpenPalette("!")
	b.UpdatePalette()
	if len(b.Palette.Items) != 0 || b.Palette.Note != "这些仓库里没认出任务 · 找 package.json 的 scripts、Makefile / justfile 的目标、cargo / go 的常用命令" {
		t.Fatalf("没有任务：%d 项，%q", len(b.Palette.Items), b.Palette.Note)
	}
}
