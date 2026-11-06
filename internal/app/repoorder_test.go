package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 调仓库顺序：工作区文件里的那组写回 folders（只换先后，注释、name、别的设置原样），挪完 Repos 的顺序和
// 写出的 folders 一样；"添加仓库"加的那组记进会话；两组之间挪不了，到头了上移 / 下移不能；移除过的工作区仓库加回来
// 回到它那组；文件写不了就退回去报错；打开的是文件夹时只改内存。
func TestMoveRepo(t *testing.T) {
	root := func(name string) string { return realDir(t, testutil.NewRepo(t, name).Root) } // git 报的根（解开符号链接）
	ra, rb, rc, rx, ry := root("a"), root("b"), root("c"), root("x"), root("y")
	ws := filepath.Join(t.TempDir(), "甲.code-workspace")
	body := "{\n\t// 我的工作区\n\t\"folders\": [\n\t\t{\"path\": \"" + ra + "\"},\n\t\t{\"path\": \"" + rb + "\", \"name\": \"乙\"},\n\t\t{\"path\": \"" + rc + "\"}\n\t],\n\t\"settings\": {\"editor.tabSize\": 2}\n}\n"
	if err := os.WriteFile(ws, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	repo := func(root string) *Repo { return a.RepoFor(root) }
	near := func(r, target *Repo, after bool) { a.MoveRepo(r, a.RepoNear(r, target, after)) } // 拖到 target 的上半 / 下半松手
	by := func(r *Repo, n int) { a.MoveRepo(r, slices.Index(a.Repos, r)+n) }                  // 菜单的上移 / 下移
	order := func(what string, want ...string) {
		t.Helper()
		if got := repoRoots(a); !slices.Equal(got, want) {
			t.Fatalf("%s：Repos %v，想要 %v", what, got, want)
		}
	}
	file := func(what string, want ...string) {
		t.Helper()
		got, err := workspace.Load(ws)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("%s：folders %v（%v），想要 %v", what, got, err, want)
		}
		data, _ := os.ReadFile(ws)
		for _, keep := range []string{"// 我的工作区", `"name": "乙"`, `"settings": {"editor.tabSize": 2}`} {
			if !strings.Contains(string(data), keep) {
				t.Fatalf("%s：工作区文件丢了 %s：\n%s", what, keep, data)
			}
		}
	}

	if a.CanMoveRepo(repo(ra), -1) || a.CanMoveRepo(repo(rc), 3) {
		t.Fatal("到顶还能上移 / 到底还能下移")
	}
	near(repo(rc), repo(ra), false) // 拖 c 到 a 的上半
	order("c 拖到 a 上面", rc, ra, rb)
	file("c 拖到 a 上面", rc, ra, rb)
	by(repo(rc), 1) // 下移
	order("c 下移", ra, rc, rb)
	file("c 下移", ra, rc, rb)
	if a.Current() != repo(ra) {
		t.Errorf("挪了别的仓库，当前仓库变成了 %s", a.Current().Name)
	}
	if a.CanMoveRepoNear(repo(rc), repo(ra), true) || a.CanMoveRepoNear(repo(rc), repo(rb), false) {
		t.Error("放在自己原来的位置上也算挪")
	}

	// "添加仓库"的那组：排在后面、各排各的，顺序进会话，不写工作区文件
	for _, r := range []string{rx, ry} {
		if err := a.AddRepoDir(r); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(ws)
	if a.CanMoveRepo(repo(rx), 2) || a.CanMoveRepoNear(repo(rx), repo(rb), false) || a.CanMoveRepo(repo(rb), 3) {
		t.Fatal("挪过了工作区文件和添加的两组的边界")
	}
	if !a.CanMoveRepoNear(repo(ry), repo(rb), true) {
		t.Fatal("y 拖到 b 的下半（添加那组的最前面）该能挪")
	}
	near(repo(ry), repo(rb), true)
	order("y 拖到 b 下面", ra, rc, rb, ry, rx)
	if after, _ := os.ReadFile(ws); string(after) != string(before) {
		t.Error("挪添加的仓库改了工作区文件")
	}
	if st, _ := workspace.LoadState(stateDir); st.Sessions[ws] == nil || !slices.Equal(st.Sessions[ws].Added, []string{ry, rx}) {
		t.Errorf("添加的仓库的顺序没进会话：%+v", st.Sessions[ws])
	}
	if a.CanMoveRepo(repo(rx), 5) || a.CanMoveRepo(repo(ry), 2) {
		t.Error("添加的那组到头了还能挪")
	}

	// 移除过的工作区仓库：排序时文件里它的那项不动，加回来回到工作区那组里它的位置
	wait("状态", func() bool { return idle(repo(rx))() && idle(repo(ry))() })
	a.RemoveRepo(repo(rc))
	by(repo(rb), -1)
	order("移除 c 后 b 上移", rb, ra, ry, rx)
	file("移除 c 后 b 上移", rb, rc, ra) // c 那项原地不动，b 和 a 换了先后
	if err := a.AddRepoDir(rc); err != nil {
		t.Fatal(err)
	}
	order("c 加回来", rb, rc, ra, ry, rx)

	// 写不了：退回去、报错
	if err := os.Chmod(filepath.Dir(ws), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(ws), 0o755) })
	by(repo(ra), -1)
	order("写不了", rb, rc, ra, ry, rx)
	if !a.AlertOpen || a.AlertTitle != "没能写工作区文件" {
		t.Errorf("写不了没报错：%q %v", a.AlertTitle, a.AlertOpen)
	}
	os.Chmod(filepath.Dir(ws), 0o755)
	wait("状态", func() bool { return idle(repo(rc))() })

	// 打开的是文件夹：只改内存
	dir := t.TempDir()
	for _, n := range []string{"p", "q"} {
		testutil.GitOut(t, dir, "init", "-q", n)
	}
	entries := func() []string {
		es, _ := os.ReadDir(dir)
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		return names
	}
	was := entries()
	b, _ := newTestAppIn(t, dir, t.TempDir())
	p, q := b.Repos[0], b.Repos[1]
	b.MoveRepo(p, 1)
	if !slices.Equal(b.Repos, []*Repo{q, p}) || b.AlertOpen || !slices.Equal(entries(), was) {
		t.Errorf("文件夹工作区：%v alert=%q 目录 %v", repoRoots(b), b.AlertMsg, entries())
	}
}
