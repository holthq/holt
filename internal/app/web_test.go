package app

import (
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 设置 → Git 的"网页风格"表盖过按主机名认的风格：自建主机先按通用（GitHub 的路径）拼，指成 GitLab 后
// 提交和文件的地址跟着变、主机名不分大小写；编辑器右键"在 … 打开这几行"：选区的行号，文件有改动时灰。
func TestWebHostSetting(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	testutil.GitOut(t, root, "remote", "add", "origin", "git@git.corp.test:team/sub/demo.git")
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if w := a.WebOf(r); w.Kind != git.WebGeneric || w.Site() != "git.corp.test" || w.Commit("abc") != "https://git.corp.test/team/sub/demo/commit/abc" {
		t.Fatalf("自动认：%+v %s", w, w.Commit("abc"))
	}
	a.St.Settings.WebHosts = []workspace.WebHost{{Host: "GIT.corp.test", Kind: "gitlab"}}
	w := a.WebOf(r)
	if w.Kind != git.WebGitLab || w.Site() != "GitLab" || w.Commit("abc") != "https://git.corp.test/team/sub/demo/-/commit/abc" ||
		w.File("main", "a.txt", false) != "https://git.corp.test/team/sub/demo/-/blob/main/a.txt" {
		t.Errorf("设置指定 GitLab：%+v", w)
	}
	if r.Extra.Web.Kind != git.WebGeneric {
		t.Error("设置不该改读到的远程本身")
	}

	d := a.OpenFile(r, "README.md", true)
	wait("读 README", func() bool { return !d.Loading })
	d.St.SetSelections(editor.Selection{Anchor: d.Buf.Offset(editor.Pos{Line: 0}), Head: d.Buf.Offset(editor.Pos{Line: 2})})
	u, ok := a.WebLines(d)
	if !ok || u != "https://git.corp.test/team/sub/demo/-/blob/HEAD/README.md#L1-2" {
		t.Errorf("这几行：%q %v（选到第 3 行行首，不算第 3 行）", u, ok)
	}
	d.St.SetSelections(editor.Selection{Anchor: d.Buf.Offset(editor.Pos{Line: 2, Col: 1}), Head: d.Buf.Offset(editor.Pos{Line: 2, Col: 1})})
	if u, _ := a.WebLines(d); u != "https://git.corp.test/team/sub/demo/-/blob/HEAD/README.md#L3" {
		t.Errorf("光标那一行：%q", u)
	}
	da := a.OpenFile(r, "a.txt", true)
	wait("读 a.txt", func() bool { return !da.Loading })
	if _, ok := a.WebLines(da); ok {
		t.Error("工作区里改过的文件，行号对不上网页，该灰")
	}
}
