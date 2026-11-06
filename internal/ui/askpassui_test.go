package ui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/askpass"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// TestMain 分出 askpass 模式：写命令的 GIT_ASKPASS 脚本 exec 的是 os.Executable()，测试里就是这个测试程序，
// 不分出去的话 git 要凭据时会把整套界面测试再跑一遍。
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "askpass" {
		os.Exit(askpass.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// shown 是界面上有整段等于 s 的字（或 Label）。
func shown(tt *wui.Tester, s string) bool {
	_, ok := tt.Find(s)
	return ok
}

// 写命令要凭据：框里先是用户名（自动聚焦），Tab 到密码 / 令牌，⏎ 登录；git 拿到两样，密码不弹第二个框。
func TestCredDialogUI(t *testing.T) {
	t.Cleanup(app.AskpassClose)
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, t.TempDir())
	r := a.Current()
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))

	out := filepath.Join(t.TempDir(), "got")
	script := filepath.Join(t.TempDir(), "ask.sh")
	body := `#!/bin/sh
u=$("$GIT_ASKPASS" "Username for 'https://git.test': ") || exit 128
p=$("$GIT_ASKPASS" "Password for 'https://$u@git.test': ") || exit 128
printf '%s|%s\n' "$u" "$p" > '` + out + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	a.RunOp(r, "推送", "-c", "alias.ask=!"+script, "ask")
	wait("凭据框", func() bool { return len(a.Cred) == 1 })
	tt.Frame()
	tt.Frame()
	if !tt.HasText("登录 git.test") || !tt.HasText("credential.helper osxkeychain") {
		t.Fatalf("凭据框的字：%v", tt.Texts())
	}
	tt.Type("octo")
	tt.Key(0, wui.KeyTab)
	tt.Type("tok-123")
	tt.Key(0, wui.KeyEnter)
	wait("推送跑完", idle(r))
	if b, err := os.ReadFile(out); err != nil || string(b) != "octo|tok-123\n" {
		t.Fatalf("git 拿到 %q %v", b, err)
	}
	if len(a.Cred) != 0 {
		t.Errorf("还剩 %d 个凭据框", len(a.Cred))
	}
}

// 克隆对话框：剪贴板里的地址预填，命令预览跟着；⏎ 克隆一个 file:// 的临时仓库，进列表。侧栏占位行：名字、进度字、✕。
func TestCloneUI(t *testing.T) {
	t.Cleanup(app.AskpassClose)
	src := testutil.NewRepo(t, "src")
	src.Write("a.txt", "一\n")
	src.Commit("第一个")
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, t.TempDir())
	old := app.ReadClipboard
	app.ReadClipboard = func() string { return "git@github.com:octo/hello.git" }
	t.Cleanup(func() { app.ReadClipboard = old })

	a.Exec("repo.clone")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("克隆仓库") || !shown(tt, "git clone git@github.com:octo/hello.git hello") {
		t.Fatalf("对话框：%v", tt.Texts())
	}
	a.CloneDlg.URL = "file://" + src.Root
	a.CloneDlg.Parent = t.TempDir()
	tt.Frame()
	tt.Key(0, wui.KeyEnter) // 焦点在地址栏
	wait("克隆完", func() bool { return len(a.Clones) == 0 && len(a.Repos) == 2 })
	if a.CloneDlg.Open || a.AlertOpen {
		t.Fatalf("克隆完：对话框 %v，提示 %q", a.CloneDlg.Open, a.AlertMsg)
	}

	a.ShowSidebar(workspace.ModeFiles)
	a.Clones = append(a.Clones, &app.Clone{URL: "https://github.com/octo/hello.git", Name: "hello", Text: "接收对象 45%", Pct: 45})
	tt.Frame()
	tt.Frame()
	if !shown(tt, "hello") || !shown(tt, "接收对象 45%") || !shown(tt, "取消克隆 hello") {
		t.Fatalf("占位行：%v", tt.Texts())
	}
	a.Clones = nil
}

// TestScreenshotCredClone 是凭据和克隆的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark）：
//
//	cred-user  HTTPS 要用户名：用户名 + 密码 / 令牌，令牌的说明，credential.helper 的小字
//	cred-pass  只要密码（用户名在提示里）
//	cred-key   SSH 密钥口令，ssh-add 的小字
//	cred-host  第一次连主机：指纹原文等宽，继续连接
//	clone      克隆对话框，剪贴板里的地址预填
//	cloning    侧栏文件页顶上的占位行：转圈、进度字、✕、进度条
//	error      推送被拒：一句人话，git 原话的最后几行在下面
func TestScreenshotCredClone(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false
	r := a.Current()
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	old := app.ReadClipboard
	app.ReadClipboard = func() string { return "https://github.com/octo/hello-world.git" }
	t.Cleanup(func() { app.ReadClipboard = old })
	shot := func(name string, dark bool) {
		t.Helper()
		tt.Frame()
		tt.Frame()
		suffix := "-light.png"
		if dark {
			suffix = "-dark.png"
		}
		writeShot(t, tt, dir, name+suffix)
	}
	cred := func(prompt string) {
		a.Cred = []*app.CredReq{{Prompt: askpass.Classify(prompt), Repo: r.Name}}
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		a.ShowSidebar(workspace.ModeFiles)

		cred("Username for 'https://github.com': ")
		a.Cred[0].User = "octo"
		shot("cred-user", dark)
		cred("Password for 'https://octo@github.com': ")
		shot("cred-pass", dark)
		cred("Enter passphrase for key '/Users/you/.ssh/id_ed25519': ")
		shot("cred-key", dark)
		cred("The authenticity of host 'github.com (140.82.112.3)' can't be established.\nED25519 key fingerprint is SHA256:+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU.\nThis key is not known by any other names.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ")
		shot("cred-host", dark)
		a.Cred = nil

		a.OpenClone()
		shot("clone", dark)
		a.CloneDlg = app.CloneDialog{}

		a.Clones = []*app.Clone{{URL: "https://github.com/octo/hello-world.git", Name: "hello-world", Text: "接收对象 45%", Pct: 45}}
		shot("cloning", dark)
		a.Clones = nil

		a.Fail("推送失败", errors.New("To https://github.com/octo/demo.git\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs to 'https://github.com/octo/demo.git'\nhint: Updates were rejected because the remote contains work that you do not\nhint: have locally. This is usually caused by another repository pushing to\nhint: the same ref. If you want to integrate the remote changes, use\nhint: 'git pull' before pushing again.\nhint: See the 'Note about fast-forwards' in 'git push --help' for details."))
		shot("error", dark)
		a.AlertOpen = false
	}
}
