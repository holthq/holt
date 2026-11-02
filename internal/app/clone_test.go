package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/askpass"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 地址 → 目录名（git clone 不给目录名时用的那个）；会跑的命令里地址的密码换成 ***。
func TestCloneName(t *testing.T) {
	for u, want := range map[string]string{
		"https://github.com/a/b.git":       "b",
		"https://github.com/a/b":           "b",
		"https://github.com/a/b/":          "b",
		"https://github.com/a/b.git?x=1#y": "b",
		"git@github.com:a/b":               "b",
		"git@github.com:b.git":             "b",
		"ssh://host/p/x.git/":              "x",
		"file:///tmp/src":                  "src",
		"/tmp/src/.git":                    "src",
		" https://h/a/中文.git ":             "中文",
		".git":                             "",
		"":                                 "",
		"https://h/..":                     "",
		"https://h/-x.git":                 "",
	} {
		if got := CloneName(u); got != want {
			t.Errorf("CloneName(%q) = %q，应为 %q", u, got, want)
		}
	}
	for s, want := range map[string]bool{
		"https://github.com/a/b.git": true,
		"git@github.com:a/b":         true,
		"ssh://h/x":                  true,
		"file:///tmp/x":              true,
		"/tmp/x.git":                 true,
		"hello world":                false,
		"https://a\nb":               false,
		"README.md":                  false,
		"":                           false,
	} {
		if LooksGitURL(s) != want {
			t.Errorf("LooksGitURL(%q) 应为 %v", s, want)
		}
	}
	d := CloneDialog{URL: "https://octo:tok-123@h.test/a/b.git", Name: "b", Sub: true}
	if got := d.Command(); got != "git clone --recurse-submodules https://octo:***@h.test/a/b.git b" {
		t.Errorf("命令 %q", got)
	}
	if got := redactURL("https://octo@h.test/a.git"); got != "https://octo@h.test/a.git" {
		t.Errorf("没有密码的地址被改了：%q", got)
	}
}

// git clone --progress 的几段 stderr → 占位行上的字和百分比。
func TestCloneProgress(t *testing.T) {
	for _, c := range []struct {
		in   string
		text string
		pct  int
		ok   bool
	}{
		{"Cloning into 'b'...", "连接中…", -1, true},
		{"remote: Enumerating objects: 1200, done.", "整理对象", -1, true},
		{"remote: Counting objects:  40% (480/1200)", "整理对象 40%", 40, true},
		{"remote: Compressing objects: 100% (600/600), done.", "整理对象 100%", 100, true},
		{"Receiving objects:  45% (540/1200), 1.20 MiB | 2.40 MiB/s", "接收对象 45%", 45, true},
		{"Resolving deltas:   7% (12/170)", "解析差异 7%", 7, true},
		{"Updating files: 100% (90/90), done.", "检出文件 100%", 100, true},
		{"Submodule 'lib' (https://h/lib.git) registered for path 'lib'", "子模块 lib", -1, true},
		{"warning: You appear to have cloned an empty repository.", "", 0, false},
		{"remote: Total 1200 (delta 170), reused 0", "", 0, false},
	} {
		text, pct, ok := CloneProgress(c.in)
		if text != c.text || pct != c.pct || ok != c.ok {
			t.Errorf("CloneProgress(%q) = %q %d %v，应为 %q %d %v", c.in, text, pct, ok, c.text, c.pct, c.ok)
		}
	}
}

// 打开对话框：剪贴板里像 git 地址就预填（目录名跟着推），不像就空着；放到上次克隆的目录，没有就第一个仓库的父目录。
// 改过目录名就不再跟着地址变。红字：父目录不在、目录名带 /、目录已经有了而且不空（空目录可以）。
func TestCloneDialogPrefill(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, _ := newTestApp(t, ws)
	clip := "git@github.com:octo/hello.git"
	old := ReadClipboard
	ReadClipboard = func() string { return clip }
	t.Cleanup(func() { ReadClipboard = old })

	a.OpenClone()
	d := &a.CloneDlg
	if !d.Open || d.URL != clip || !d.Prefilled || d.Name != "hello" || untilde(d.Parent) != filepath.Dir(root) {
		t.Fatalf("预填：%+v", *d)
	}
	d.URL = "https://h/a/world.git"
	d.Sync()
	if d.Name != "world" {
		t.Fatalf("目录名没跟着地址：%q", d.Name)
	}
	d.Name = "mine"
	d.Sync()
	d.URL = "https://h/a/other.git"
	d.Sync()
	if d.Name != "mine" {
		t.Fatalf("改过的目录名被换了：%q", d.Name)
	}

	parent := t.TempDir()
	d.Parent = parent
	if p, n := a.CloneErrs(); p != "" || n != "" || !a.CloneReady() {
		t.Fatalf("能克隆的：%q %q", p, n)
	}
	d.Parent = filepath.Join(parent, "nope")
	if p, _ := a.CloneErrs(); p != "没有这个目录" || a.CloneReady() {
		t.Errorf("父目录不在：%q", p)
	}
	d.Parent, d.Name = parent, "a/b"
	if _, n := a.CloneErrs(); n != "目录名里不能有 /" {
		t.Errorf("目录名带 /：%q", n)
	}
	d.Name = "mine"
	os.Mkdir(filepath.Join(parent, "mine"), 0o755)
	if _, n := a.CloneErrs(); n != "" {
		t.Errorf("空目录应能克隆进去：%q", n)
	}
	os.WriteFile(filepath.Join(parent, "mine", "x"), nil, 0o644)
	if _, n := a.CloneErrs(); n != "已经有这个目录了，而且不是空的" || a.CloneReady() {
		t.Errorf("不空的目录：%q", n)
	}
	d.URL = ""
	d.Name = "fresh"
	if p, n := a.CloneErrs(); p != "" || n != "" || a.CloneReady() {
		t.Errorf("没填地址：%q %q %v", p, n, a.CloneReady())
	}

	clip = "hello world"
	a.OpenClone()
	if a.CloneDlg.URL != "" || a.CloneDlg.Prefilled || a.CloneDlg.Name != "" {
		t.Errorf("剪贴板不是地址也预填了：%+v", a.CloneDlg)
	}
	a.St.CloneDir = parent
	a.OpenClone()
	if untilde(a.CloneDlg.Parent) != parent {
		t.Errorf("没放到上次克隆的目录：%q", a.CloneDlg.Parent)
	}
}

// 用 file:// 地址真克隆一个临时仓库（纯路径走硬链接不打进度）：进仓库列表和 state.json 的 added，记住放到的目录，
// 占位没了，输出栏记着命令。再克隆一个不存在的：出错框是 git.Explain 的人话、原话放下面，目录没留下。
// 空工作区里克隆：克隆下来的当工作区打开。
func TestCloneRuns(t *testing.T) {
	t.Cleanup(AskpassClose)
	src := testutil.NewRepo(t, "src")
	src.Write("a.txt", "一\n")
	src.Commit("第一个")
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	parent := t.TempDir()

	a.CloneDlg = CloneDialog{Open: true, URL: "file://" + src.Root, Parent: parent}
	a.CloneDlg.Sync()
	if a.CloneDlg.Name != "src" || !a.CloneReady() {
		t.Fatalf("对话框：%+v", a.CloneDlg)
	}
	a.StartClone()
	if a.CloneDlg.Open || len(a.Clones) != 1 || !slices.Contains(a.WritesRunning(), "克隆 src") {
		t.Fatalf("开始后：对话框 %v，占位 %d，在跑 %v", a.CloneDlg.Open, len(a.Clones), a.WritesRunning())
	}
	wait("克隆完", func() bool { return len(a.Clones) == 0 })
	dest := filepath.Join(parent, "src")
	if a.AlertOpen || a.Toast != "已克隆 src" {
		t.Fatalf("克隆完：提示 %v %q %q，toast %q", a.AlertOpen, a.AlertMsg, a.AlertDetail, a.Toast)
	}
	if !slices.ContainsFunc(a.Repos, func(r *Repo) bool { return git.SamePath(r.Root, dest) }) {
		t.Fatalf("没进仓库列表：%v", repoRoots(a))
	}
	if b, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(b) != "一\n" {
		t.Fatalf("克隆下来的 a.txt：%q %v", b, err)
	}
	st, _ := workspace.LoadState(stateDir)
	if s := st.Sessions[ws]; !slices.ContainsFunc(s.Added, func(p string) bool { return git.SamePath(p, dest) }) || st.CloneDir != parent {
		t.Errorf("state.json：added %v，cloneDir %q", s.Added, st.CloneDir)
	}
	if e := a.Output[len(a.Output)-1]; e.Failed || e.Title != "$ git clone file://"+src.Root+" src" {
		t.Errorf("输出栏 %+v", e)
	}

	// 失败：原话里认得出的说一句人话
	a.CloneDlg = CloneDialog{Open: true, URL: "file://" + filepath.Join(t.TempDir(), "nope.git"), Parent: parent}
	a.CloneDlg.Sync()
	a.StartClone()
	wait("克隆失败", func() bool { return len(a.Clones) == 0 })
	if !a.AlertOpen || a.AlertTitle != "克隆失败" || a.AlertMsg != "远程仓库不存在，或者没有权限看它。" || !strings.Contains(a.AlertDetail, "does not appear to be a git repository") {
		t.Fatalf("失败框：%q %q %q", a.AlertTitle, a.AlertMsg, a.AlertDetail)
	}
	if _, err := os.Stat(filepath.Join(parent, "nope")); err == nil {
		t.Error("失败了还留着目录")
	}
	a.AlertOpen = false
	a.Fail("推送失败", errors.New("To h:x\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs to 'h:x'\nhint: Updates were rejected\nhint: See git push --help."))
	if a.AlertMsg != "远程有本地没有的提交：先拉取（或变基到远程）再推送。" || a.AlertDetail != "To h:x\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs to 'h:x'" {
		t.Errorf("推送被拒：%q %q", a.AlertMsg, a.AlertDetail)
	}
	a.Fail("取消后留下了锁文件", humanErr("仓库里有锁文件：.git/index.lock。"))
	if a.AlertMsg != "仓库里有锁文件：.git/index.lock。" || a.AlertDetail != "" {
		t.Errorf("自己写的话也拿去认了：%q %q", a.AlertMsg, a.AlertDetail)
	}
	a.AlertOpen = false

	// 空工作区：克隆下来的当工作区打开
	e, wait2 := newTestAppIn(t, "", t.TempDir())
	if e.WsPath != "" {
		t.Fatalf("不是空工作区：%q", e.WsPath)
	}
	e.CloneDlg = CloneDialog{Open: true, URL: "file://" + src.Root, Parent: t.TempDir(), Name: "copy"}
	e.CloneDlg.Sync()
	dest2 := e.CloneDlg.Dest()
	e.StartClone()
	wait2("空工作区里克隆完", func() bool { return len(e.Clones) == 0 && e.WsPath != "" })
	if !git.SamePath(e.WsPath, dest2) || e.AlertOpen {
		t.Errorf("空工作区克隆完：工作区 %q（应为 %q），提示 %q", e.WsPath, dest2, e.AlertMsg)
	}
}

// fakeSSH 让 ssh:// 地址的克隆走一个脚本（GIT_SSH_COMMAND，测试进程的环境，写命令照样带过去）。
func fakeSSH(t *testing.T, body string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ssh.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	t.Setenv("GIT_SSH_VARIANT", "simple") // 别先拿 -G 探是不是 OpenSSH
}

// 取消：进度到了占位行上；同一个目录不能再克隆；取消后占位没了、只 toast、建了一半的目录删掉。
// 克隆要凭据：框上写的是目录名；框里取消同样只 toast。
func TestCloneCancel(t *testing.T) {
	t.Cleanup(AskpassClose)
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	parent := t.TempDir()
	fakeSSH(t, `sleep 0.3
printf 'Receiving objects:  45%% (9/20)\r' >&2
exec sleep 30`)

	a.CloneDlg = CloneDialog{Open: true, URL: "ssh://h.test/x.git", Parent: parent}
	a.CloneDlg.Sync()
	a.StartClone()
	cl := a.Clones[0]
	wait("进度", func() bool { return cl.Text == "接收对象 45%" && cl.Pct == 45 })
	a.CloneDlg = CloneDialog{Open: true, URL: "ssh://h.test/x.git", Parent: parent}
	a.CloneDlg.Sync()
	if _, n := a.CloneErrs(); n != "正在往这个目录克隆" {
		t.Errorf("同一个目录：%q", n)
	}
	a.CloneDlg = CloneDialog{}
	a.CancelClone(cl)
	wait("取消完", func() bool { return len(a.Clones) == 0 })
	if a.AlertOpen || a.Toast != "已取消：克隆 x" {
		t.Fatalf("取消后：提示 %v %q，toast %q", a.AlertOpen, a.AlertMsg, a.Toast)
	}
	if _, err := os.Stat(filepath.Join(parent, "x")); err == nil {
		t.Error("取消了还留着目录")
	}
	if e := a.Output[len(a.Output)-1]; !e.Failed || !strings.Contains(e.Text, "已取消：克隆 x") {
		t.Errorf("输出栏 %+v", e)
	}

	fakeSSH(t, `"$SSH_ASKPASS" "git@h.test's password: " >/dev/null || { echo "git@h.test: Permission denied (publickey,password)." >&2; exit 255; }
exec sleep 30`)
	a.Toast = ""
	a.CloneDlg = CloneDialog{Open: true, URL: "ssh://git@h.test/y.git", Parent: parent}
	a.CloneDlg.Sync()
	a.StartClone()
	wait("凭据框", func() bool { return len(a.Cred) == 1 })
	if q := a.Cred[0]; q.Repo != "y" || q.Kind != askpass.Other {
		t.Fatalf("克隆的凭据框：%q %+v", q.Repo, q.Prompt)
	}
	a.AnswerCred(false)
	wait("取消完", func() bool { return len(a.Clones) == 0 })
	if a.AlertOpen || a.Toast != "已取消：克隆 y" {
		t.Fatalf("凭据框取消后：提示 %v %q，toast %q", a.AlertOpen, a.AlertMsg, a.Toast)
	}
	if _, err := os.Stat(filepath.Join(parent, "y")); err == nil {
		t.Error("取消了还留着目录")
	}
}
