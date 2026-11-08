package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/holthq/holt/internal/askpass"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/testutil/fakelsp"
)

// TestMain 分出 askpass 模式：写命令的 GIT_ASKPASS 脚本 exec 的是 os.Executable()，测试里就是这个测试程序。
// 也分出假语言服务器模式：internal/lsp 的测试和这里都用测试程序自己当服务端。
func TestMain(m *testing.M) {
	if os.Getenv(fakelsp.Env) == "1" {
		fakelsp.Main()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "askpass" {
		os.Exit(askpass.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// askAlias 是一个 git 别名，跑脚本 body：像要凭据的 git 那样调 $GIT_ASKPASS。
func askAlias(t *testing.T, body string) []string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ask.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{"-c", "alias.ask=!" + script, "ask"}
}

// 一条写命令要凭据：凭据框排在发起它的窗口里（标题的仓库名、主机）；用户名框里一起填的密码 git 紧接着问时直接回，
// 不弹第二个框；回答不进输出栏（只记"凭据提示：主机"）。凭据框里取消：命令失败，但只 toast "已取消"、不弹失败框。
func TestAskpassCancelOp(t *testing.T) {
	t.Cleanup(AskpassClose)
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))

	out := filepath.Join(t.TempDir(), "got")
	args := askAlias(t, `u=$("$GIT_ASKPASS" "Username for 'https://git.test': ") || { echo "fatal: could not read Username for 'https://git.test': terminal prompts disabled" >&2; exit 128; }
p=$("$GIT_ASKPASS" "Password for 'https://$u@git.test': ") || { echo "fatal: Authentication failed for 'https://git.test/'" >&2; exit 128; }
printf '%s|%s\n' "$u" "$p" > '`+out+`'`)

	a.RunOp(r, "推送", args...)
	wait("凭据框", func() bool { return len(a.Cred) == 1 })
	q := a.Cred[0]
	if q.Kind != askpass.Username || q.Host != "git.test" || q.Repo != r.Name || r.Busy != "推送" {
		t.Fatalf("凭据框 %+v，Busy %q", q.Prompt, r.Busy)
	}
	if CredReady(q) {
		t.Error("没填用户名就能点登录")
	}
	q.User, q.Pass = "octo", "tok-123"
	a.AnswerCred(true)
	wait("推送跑完", idle(r))
	if b, err := os.ReadFile(out); err != nil || string(b) != "octo|tok-123\n" {
		t.Fatalf("git 拿到 %q %v", b, err)
	}
	if len(a.Cred) != 0 || a.AlertOpen || a.Toast != "推送完成" {
		t.Fatalf("答完：凭据框 %d，提示 %v %q，toast %q", len(a.Cred), a.AlertOpen, a.AlertMsg, a.Toast)
	}
	asks := 0
	for _, e := range a.Output {
		if strings.Contains(e.Title+e.Text, "tok-123") {
			t.Errorf("输出栏里有密码：%+v", e)
		}
		if e.Title == "凭据提示" && e.Text == "git.test" {
			asks++
		}
	}
	if asks != 2 {
		t.Errorf("凭据提示记了 %d 条，应为 2", asks)
	}

	os.Remove(out)
	a.Toast = ""
	a.RunOp(r, "推送", args...)
	wait("凭据框", func() bool { return len(a.Cred) == 1 })
	a.AnswerCred(false)
	wait("推送跑完", idle(r))
	if a.AlertOpen || a.Toast != "已取消：推送" {
		t.Fatalf("取消：提示 %v %q，toast %q", a.AlertOpen, a.AlertMsg, a.Toast)
	}
	if e := a.Output[len(a.Output)-1]; !e.Failed || !strings.Contains(e.Text, "已取消：推送") {
		t.Errorf("取消后输出栏 %+v", e)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("取消了 git 还是拿到了凭据")
	}
}

// 密码记一次：用户名框里填的密码，紧接着的 Password for 直接回、用掉就忘；过期的不用；没记着的弹只有密码的框。
// 取消过的命令再问不弹；命令结束时还排着的框撤掉（回不 ok）；不认识的 id 回不 ok。
func TestAskpassPasswordCache(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, _ := newTestApp(t, ws)
	r := a.Current()
	up := func(fn func()) { fn() }
	o := a.askRegister(r, r.Name, up)
	defer a.askDone(o)

	var got []askpass.Reply
	reply := func(rep askpass.Reply) { got = append(got, rep) }
	a.credAsk(o, "Username for 'https://h.test': ", reply)
	if len(a.Cred) != 1 || len(got) != 0 {
		t.Fatalf("用户名：框 %d，回了 %v", len(a.Cred), got)
	}
	a.Cred[0].User, a.Cred[0].Pass = " u ", "p1"
	a.AnswerCred(true)
	a.credAsk(o, "Password for 'https://u@h.test': ", reply)
	if len(a.Cred) != 0 || len(got) != 2 || got[0] != (askpass.Reply{OK: true, Text: "u"}) || got[1] != (askpass.Reply{OK: true, Text: "p1"}) {
		t.Fatalf("记着的密码：框 %d，回了 %v", len(a.Cred), got)
	}
	a.credAsk(o, "Password for 'https://u@h.test': ", reply) // 用过一次就忘
	if len(a.Cred) != 1 || a.Cred[0].Kind != askpass.Password || a.Cred[0].User != "u" {
		t.Fatalf("第二次问密码：%+v", a.Cred)
	}
	if CredReady(a.Cred[0]) {
		t.Error("空密码能点登录")
	}
	a.Cred[0].Pass = "p2"
	a.AnswerCred(true)
	if got[2] != (askpass.Reply{OK: true, Text: "p2"}) {
		t.Fatalf("密码框回了 %v", got[2])
	}

	a.credCache = map[string]credPass{credKey("h.test", "u"): {"old", time.Now().Add(-time.Second)}}
	a.credAsk(o, "Password for 'https://u@h.test': ", reply)
	if len(a.Cred) != 1 || len(got) != 3 {
		t.Fatalf("过期的密码不该用：框 %d，回了 %v", len(a.Cred), got)
	}

	// 取消之后这条命令再问一律不弹；主机指纹回 yes
	a.AnswerCred(false)
	if got[3].OK || !o.canceled {
		t.Fatalf("取消：%v %v", got[3], o.canceled)
	}
	a.credAsk(o, "git@h.test's password: ", reply)
	if len(a.Cred) != 0 || got[4].OK {
		t.Fatalf("取消后再问：框 %d，回了 %v", len(a.Cred), got[4])
	}
	o2 := a.askRegister(r, r.Name, up)
	a.credAsk(o2, "The authenticity of host 'h.test (10.0.0.1)' can't be established.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ", reply)
	a.AnswerCred(true)
	if got[5] != (askpass.Reply{OK: true, Text: "yes"}) {
		t.Fatalf("主机指纹回了 %v", got[5])
	}
	a.credAsk(o2, "Enter passphrase for key '/k': ", reply)
	a.askDone(o2) // 命令结束了框还在：撤掉
	if len(a.Cred) != 0 || got[6].OK {
		t.Fatalf("结束后：框 %d，回了 %v", len(a.Cred), got[6])
	}
	a.credAsk(o2, "Enter passphrase for key '/k': ", reply) // 注销了的命令
	if len(a.Cred) != 0 || got[7].OK {
		t.Fatalf("注销后：框 %d，回了 %v", len(a.Cred), got[7])
	}
	askHandle(askpass.Request{ID: "nope", Prompt: "x"}, reply)
	if len(got) != 9 || got[8].OK {
		t.Fatalf("不认识的 id：%v", got)
	}
}
