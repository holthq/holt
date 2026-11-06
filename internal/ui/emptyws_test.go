package ui

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 第一次用（空 state、没给工作区）是空工作区：标题栏、侧栏三页和右栏都写"没有打开的工作区"，侧栏两个按钮走系统的
// 打开面板（测试里换掉）；面板取消了什么都不变，选了文件夹就换过去、文件树里有它。
// 底栏的终端不画（画了会起 shell），家目录的终端见 app 的 TestStartEmpty。
func TestEmptyWorkspace(t *testing.T) {
	a, tt, wait := newUI(t, "", termState(t, false))
	r := testutil.NewRepo(t, "solo")
	r.Write("hello.txt", "hi\n")
	calls, replies := make(chan mygo.OpenDialogOptions, 1), make(chan []string, 1)
	a.OpenDialog = func(o mygo.OpenDialogOptions) ([]string, error) {
		calls <- o
		return <-replies, nil
	}
	dialog := func(button string, reply []string) mygo.OpenDialogOptions {
		t.Helper()
		replies <- reply
		if err := tt.Click(button); err != nil {
			t.Fatal(err)
		}
		select {
		case o := <-calls:
			return o
		case <-time.After(5 * time.Second):
			t.Fatalf("点 %s 没开打开面板", button)
		}
		return mygo.OpenDialogOptions{}
	}
	none := i18n.T("没有打开的工作区")
	count := func() int {
		n := 0
		for _, s := range tt.Texts() {
			if s == none {
				n++
			}
		}
		return n
	}

	a.Start()
	if a.WsPath != "" || len(a.Repos) != 0 {
		t.Fatalf("应是空工作区：ws=%q repos=%d", a.WsPath, len(a.Repos))
	}
	a.St.RightBar = false
	tt.Frame()
	base := count() // 标题栏按钮（标签和文字）、侧栏和主区
	for _, mode := range []string{workspace.ModeFiles, workspace.ModeSearch, workspace.ModeGit} {
		a.ShowSidebar(mode)
		tt.Frame()
		if base < 2 || count() != base {
			t.Errorf("侧栏 %s：标题栏和侧栏应都写%q，有 %d 处（文件页 %d 处）", mode, none, count(), base)
		}
		mustText(t, tt, i18n.T("打开文件夹…"))
		mustText(t, tt, i18n.T("打开工作区文件…"))
	}
	if _, ok := tt.Find(app.Tip(i18n.T("添加仓库…"), "repo.add")); ok {
		t.Error("空工作区的侧栏不该有添加仓库的 +")
	}
	a.St.RightBar = true
	tt.Frame()
	if count() != base+1 {
		t.Errorf("右栏也应写%q，一共 %d 处", none, count())
	}
	a.ShowSidebar(workspace.ModeFiles)
	tt.Frame()

	home, _ := os.UserHomeDir()
	o := dialog(i18n.T("打开工作区文件…"), nil) // 取消
	if o.Directory || len(o.Filters) == 0 || len(o.Filters[0].Extensions) == 0 || o.Filters[0].Extensions[0] != "code-workspace" || o.DefaultPath != home {
		t.Errorf("打开工作区文件的面板：%+v", o)
	}
	tt.Frame()
	if a.WsPath != "" || len(a.Repos) != 0 {
		t.Fatalf("取消了还换了工作区：%q", a.WsPath)
	}

	if o := dialog(i18n.T("打开文件夹…"), []string{r.Root}); !o.Directory || o.DefaultPath != home {
		t.Errorf("打开文件夹的面板：%+v", o)
	}
	wait("换到 solo", func() bool { return a.WsPath == r.Root && len(a.Repos) == 1 && a.Repos[0].Loaded })
	wait("文件树里有 hello.txt", func() bool { return tt.HasText("hello.txt") })
	if count() != 0 {
		t.Errorf("换过去以后还写着%q", none)
	}
	if _, ok := tt.Find("solo（仓库）"); !ok {
		t.Error("文件树里没有仓库 solo")
	}
}
