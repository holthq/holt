package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestScreenshotTasksProfiles 是任务和终端配置的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark），临时仓库 shop：
//
//	tasks        ⇧⌘B 的面板 "!"：package.json（pnpm）、Makefile、go.mod 认出来的任务
//	profiles     设置 → 终端 的“其他配置”表（两个配置）
//	term-editor  编辑器区的终端标签（左右分屏），旁边是文件标签，底栏照旧有自己的终端
//	term-hist    关窗口再开：底栏那一格喂回上次的输出，“上次的输出（时间）”下面接着新的提示符
//
// 底栏「+」的右键菜单是系统菜单，Tester 不画。
func TestScreenshotTasksProfiles(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	shop := testutil.NewRepo(t, "shop")
	shop.Write("package.json", `{"name":"shop","scripts":{"dev":"vite","build":"vite build","test":"vitest run","lint":"eslint src"}}`+"\n")
	shop.Write("pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	shop.Write("Makefile", ".PHONY: all build lint\nall: build\nbuild:\n\tgo build ./...\nlint:\n\tgo vet ./...\n")
	shop.Write("go.mod", "module shop\n")
	shop.Write("src/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"shop\")\n}\n")
	shop.Write("README.md", "# Shop\n")
	shop.Commit("初始")

	stateDir := t.TempDir()
	st := workspace.Default()
	st.Settings.Shell, st.BottomBar, st.BottomH = "/bin/sh", true, 260
	st.Settings.TermProfiles = []workspace.TermProfile{
		{Name: "login", Shell: "/bin/sh", Args: []string{"-l"}},
		{Name: "node-dev", Shell: "/bin/sh", Env: []string{"NODE_ENV=development", "PORT=5173"}},
	}
	if err := workspace.SaveState(stateDir, st); err != nil {
		t.Fatal(err)
	}
	ws := testutil.Workspace(t, shop.Root)
	a, tt, wait := termUI(t, ws, stateDir)
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	r := a.Repos[0]
	wait("读完状态和任务", func() bool { return idle(r)() && len(r.Tasks) > 0 })
	p := readyPane(t, a, tt)
	typeLine(t, tt, p, "echo build-$((6*7))", "build-42")
	suffix := func(dark bool) string {
		if dark {
			return "-dark.png"
		}
		return "-light.png"
	}
	shot := func(name string, dark bool) {
		t.Helper()
		tt.Frame()
		tt.Frame()
		writeShot(t, tt, dir, name+suffix(dark))
	}

	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		a.Exec("task.run")
		a.UpdatePalette()
		shot("tasks", dark)
		a.ClosePalette()
	}

	sv := wui.NewTester(SettingsView(a), 720, 560)
	sv.SetScale(2)
	sv.SetLocale(i18n.Locale())
	sv.Frame()
	mustClick(t, sv, "终端")
	for _, dark := range []bool{false, true} {
		sv.SetDark(dark)
		writeShot(t, sv, dir, "profiles"+suffix(dark))
	}

	tt.SetDark(false)
	a.OpenFile(r, "src/main.go", true)
	wait("main.go 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	a.Exec("term.newEditor")
	tab := r.EdTerms[0]
	ready := func(p *app.TermPane) {
		t.Helper()
		tt.Frame()
		if p.Term == nil {
			t.Fatalf("终端没起来：%+v", p)
		}
		testutil.WaitTermReady(t, tt, p.Term, 300*time.Millisecond, 15*time.Second)
		tt.Frame()
	}
	ep := tab.Current()
	ready(ep)
	typeLine(t, tt, ep, "ls", "package.json")
	a.SplitPane(r, tab, ep, true)
	ready(tab.Current())
	typeLine(t, tt, tab.Current(), "git --no-pager log --oneline", "初始")
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		shot("term-editor", dark)
	}

	a.NoteTermDirs()
	a.SaveState()
	a.Close()
	a, tt, wait = termUI(t, ws, stateDir)
	a.Start()
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	r = a.Repos[0]
	wait("git status", idle(r))
	a.SelectIn(a.Grp, 0) // 编辑器区先看 main.go（终端标签的那一格不抢键盘）
	p = readyPane(t, a, tt)
	if !strings.Contains(p.Term.Text(), "上次的输出（") {
		t.Fatal("重开后没喂回上次的输出")
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		shot("term-hist", dark)
	}
}
