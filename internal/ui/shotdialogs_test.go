package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestScreenshotDialogs 是对话框和界面打磨的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark），临时仓库 demo：
//
//	danger    删分支 feature 的问话：红的警告块、红的"删除分支"
//	reset     软重置的问话：问号、强调色按钮
//	error     出错：红的 ✕、一个"好"
//	branch    新建分支：分支名、底下会跑的命令
//	tag       新建标签的表单
//	deltag    删标签 v1：红的、"同时删除远程上的"勾选
//	worktree  新建工作树的表单（新分支 / 已有分支两段）
//	closetab  关改过没存的 b.txt
//	diff      a.txt 的 Diff 标签：标签条右端 ↑ ↓、只看改动附近、并排 / 统一，底栏"第 2 / 3 处改动"
//	toggles   搜索栏的 Aa / ab / .*、右栏历史头上的全部分支 / 图
//	banner    lossy.txt 的编码横幅：动作是链接
func TestScreenshotDialogs(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	repo := testutil.NewRepo(t, "demo")
	var lines []string
	for i := 1; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("第 %d 行", i))
	}
	repo.Write("a.txt", strings.Join(lines, "\n")+"\n")
	repo.Write("b.txt", "第一行\n第二行\n")
	if err := os.WriteFile(filepath.Join(repo.Root, "lossy.txt"), []byte{0xD6, 0xD0, 0xCE, 0xC4, 0x80, '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	repo.Commit("初始")
	repo.Git("tag", "v1")
	repo.Git("switch", "-q", "-c", "feature")
	repo.Write("f.txt", "f\n")
	repo.Commit("feature 上的一个提交")
	repo.Git("switch", "-q", "main")
	repo.Write("b.txt", "第一行\n第二行改了\n")
	repo.Commit("第二版")
	for _, i := range []int{5, 30, 55} {
		lines[i-1] = fmt.Sprintf("改了第 %d 行", i)
	}
	repo.Write("a.txt", strings.Join(lines, "\n")+"\n")

	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	branch := func(name string) git.Branch {
		for _, b := range r.Branches {
			if b.Name == name {
				return b
			}
		}
		t.Fatalf("没有分支 %s", name)
		return git.Branch{}
	}
	cs, err := git.Log(t.Context(), repo.Root, 0, 10, git.LogQuery{})
	if err != nil || len(cs) < 2 {
		t.Fatalf("git log：%v %d", err, len(cs))
	}
	shot := func(name string, dark bool) {
		t.Helper()
		tt.Frame()
		suffix := "-light.png"
		if dark {
			suffix = "-dark.png"
		}
		writeShot(t, tt, dir, name+suffix)
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		a.ShowSidebar(workspace.ModeGit)
		a.OpenDiff(r, git.Change{Kind: git.Unstaged, Code: 'M', Path: "a.txt"}, true)
		wait("a.txt 的 diff", func() bool { return diffDoc(a, "") != nil })
		tt.Frame()
		a.Exec("diff.nextHunk")
		a.Exec("diff.nextHunk")
		shot("diff", dark)

		a.DeleteBranch(r, branch("feature"))
		wait("删分支的问话", func() bool { return a.ConfirmOpen })
		shot("danger", dark)
		a.Confirm, a.ConfirmOpen = nil, false

		a.ResetTo(r, cs[1], app.ResetSoft)
		wait("软重置的问话", func() bool { return a.ConfirmOpen })
		shot("reset", dark)
		a.Confirm, a.ConfirmOpen = nil, false

		a.Fail("推送失败", errors.New("! [rejected] main -> main (fetch first)\n远程有本地没有的提交，先拉取再推送。"))
		shot("error", dark)
		a.AlertOpen = false

		a.Exec("git.newBranch")
		tt.Frame()
		tt.Type("fix-dialog")
		shot("branch", dark)
		a.Branch = app.BranchDialog{}

		a.OpenNewTag(r, "")
		tt.Frame()
		tt.Type("v2")
		shot("tag", dark)
		a.RefDlg = app.RefDialog{}

		a.OpenDeleteTag(r, git.Tag{Name: "v1"})
		shot("deltag", dark)
		a.RefDlg = app.RefDialog{}

		a.NewWorktree(r)
		tt.Frame()
		tt.Type("try-it")
		shot("worktree", dark)
		a.WtDialog = app.WorktreeDialog{}

		d := a.OpenFile(r, "b.txt", true)
		wait("b.txt", func() bool { return d.Buf != nil })
		d.Buf.Set("x" + d.Buf.String())
		a.Exec("file.close")
		shot("closetab", dark)
		a.CloseReq, a.CloseOpen = nil, false
		d.Buf.Undo()

		a.ShowSidebar(workspace.ModeSearch)
		a.Search.Query.Text = "改了"
		a.SearchNow()
		wait("搜索", func() bool { return a.Search.Done && !a.Search.Running })
		shot("toggles", dark)

		l := a.OpenFile(r, "lossy.txt", true)
		wait("lossy.txt", func() bool { return l.Buf != nil })
		shot("banner", dark)
	}
}
