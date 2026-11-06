package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// TestScreenshotSearchExtras 是搜索小功能的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark），临时仓库 demo（没有远程）：
//
//	preview    替换预览：a.txt 的三处 needle 换成 pin 的只读 Diff，顶栏"替换这个文件（3 处）"
//	stale      改了替换词之后：顶栏"搜索结果变了，这个预览是旧的"、"重新生成"
//	searchdoc  搜索编辑器：needle 的结果，每处前后 2 行，段间 ⋯，匹配标底色
//	advanced   正则 \b(\w+) \1\b 的结果，摘要末尾" · 高级正则"
//	deltag     删标签 v1：没有远程，说明只写"只删本地的"
//	error      出错框：左下"Esc / ⏎ 好"
//	reopen     改了没存的 b.txt 按 GB18030 重新打开：红的问话
func TestScreenshotSearchExtras(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	repo := testutil.NewRepo(t, "demo")
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	lines[4], lines[6], lines[29] = "needle five", "needle seven", "needle thirty"
	repo.Write("a.txt", strings.Join(lines, "\n")+"\n")
	repo.Write("b.txt", "needle in b\nother\n")
	repo.Write("c.txt", "it is the the end\nfoobar foobaz\nso so good\n")
	repo.Commit("初始")
	repo.Git("tag", "v1")

	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	shot := func(name string, dark bool) {
		t.Helper()
		tt.Frame()
		suffix := "-light.png"
		if dark {
			suffix = "-dark.png"
		}
		writeShot(t, tt, dir, name+suffix)
	}
	search := func(text string, re bool) {
		t.Helper()
		a.Search.Query.Text, a.Search.Query.Regexp = text, re
		a.SearchNow()
		wait("搜索", func() bool { return a.Search.Done && !a.Search.Running })
	}
	closeAll := func() {
		for _, d := range a.AllDocs() {
			a.RemoveDoc(d)
		}
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		closeAll()
		a.ShowReplace()
		search("needle", false)
		a.Search.Replace = "pin"
		a.PreviewReplaceFirst()
		wait("替换预览", func() bool { d := a.CurrentDoc(); return d != nil && d.Kind == docs.ReplacePreview && d.DiffSt != nil })
		shot("preview", dark)
		a.Search.Replace = "pins"
		shot("stale", dark)
		a.Search.Replace = "pin"

		closeAll()
		a.Search.Replacing = false
		a.OpenSearchEditor()
		wait("搜索编辑器", func() bool {
			d := a.CurrentDoc()
			return d != nil && d.Kind == docs.SearchDoc && d.Buf != nil && !d.Loading
		})
		shot("searchdoc", dark)

		closeAll()
		search(`\b(\w+) \1\b`, true)
		shot("advanced", dark)
		a.Search.Query.Regexp = false

		a.ShowSidebar(workspace.ModeGit)
		a.OpenDeleteTag(r, git.Tag{Name: "v1"})
		shot("deltag", dark)
		a.RefDlg = app.RefDialog{}

		a.Fail("推送失败", errors.New("To https://github.com/owner/demo.git\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs to 'https://github.com/owner/demo.git'\nhint: Updates were rejected because the remote contains work that you do not\nhint: have locally. This is usually caused by another repository pushing to\nhint: the same ref. If you want to integrate the remote changes, use\nhint: 'git pull' before pushing again.\nhint: See the 'Note about fast-forwards' in 'git push --help' for details.")) // 真的 git 原话，说明是 git.Explain 的
		shot("error", dark)
		a.AlertOpen = false

		d := a.OpenFile(r, "b.txt", true)
		wait("b.txt", func() bool { return d.Buf != nil })
		d.Buf.Set("x" + d.Buf.String())
		a.ReopenWithEncoding(d, docs.GB18030)
		wait("重新打开的问话", func() bool { return a.ConfirmOpen })
		shot("reopen", dark)
		a.Confirm, a.ConfirmOpen = nil, false
		d.Buf.Undo()
	}
}
