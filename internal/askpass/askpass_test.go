package askpass

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// shortDir 是一个短的临时目录：t.TempDir() 在 macOS 上太长，unix 套接字路径超过 104 字节。
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ap")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// helper 和应用之间来回一趟：答了的 stdout 是回答加换行、退出 0；取消的退出 1 不打印；套接字不在的退出 1。
// 应用拿到的是原样的提示和 id；脚本是 0700，exec 的路径带空格也照样用。
func TestAskpassRoundTrip(t *testing.T) {
	dir := shortDir(t)
	var got []Request
	s, err := Listen(dir, "/Applications/My Apps/Holt.app/Contents/MacOS/Holt", func(req Request, reply func(Reply)) {
		got = append(got, req) // Run 是同步的：一次只有一个连接
		if strings.HasPrefix(req.Prompt, "Username") {
			reply(Reply{OK: true, Text: "octo"})
			return
		}
		reply(Reply{OK: false})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var out bytes.Buffer
	if code := Run("Username for 'https://github.com': ", s.Sock, "op1", &out); code != 0 || out.String() != "octo\n" {
		t.Errorf("答了：退出 %d，输出 %q", code, out.String())
	}
	out.Reset()
	if code := Run("Password for 'https://octo@github.com': ", s.Sock, "op1", &out); code != 1 || out.Len() != 0 {
		t.Errorf("取消：退出 %d，输出 %q", code, out.String())
	}
	if len(got) != 2 || got[0].ID != "op1" || got[0].Prompt != "Username for 'https://github.com': " {
		t.Errorf("应用收到 %+v", got)
	}
	if code := Run("x", filepath.Join(dir, "nope.sock"), "op1", &out); code != 1 {
		t.Errorf("套接字不在：退出 %d", code)
	}
	if code := Run("x", "", "op1", &out); code != 1 {
		t.Errorf("没设套接字：退出 %d", code)
	}

	fi, err := os.Stat(s.Script)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("脚本：%v %v", fi, err)
	}
	if fi, err := os.Stat(s.Sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("套接字权限：%v %v", fi, err)
	}
	script, _ := os.ReadFile(s.Script)
	if !strings.Contains(string(script), `exec '/Applications/My Apps/Holt.app/Contents/MacOS/Holt' askpass "$@"`) {
		t.Errorf("脚本：%q", script)
	}

	// 脚本真的能跑：exec 一个带空格路径的假程序，参数原样传过去
	exe := filepath.Join(dir, "with space", "fake")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("#!/bin/sh\necho \"$1|$2\"\n"), 0o755)
	s2, err := Listen(shortDir(t), exe, func(Request, func(Reply)) {})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if b, err := exec.Command(s2.Script, "Username for 'https://x': ").Output(); err != nil || string(b) != "askpass|Username for 'https://x': \n" {
		t.Errorf("跑脚本：%q %v", b, err)
	}

	// 关掉后套接字和脚本都删了
	s.Close()
	if _, err := os.Stat(s.Sock); !os.IsNotExist(err) {
		t.Error("关掉后套接字还在")
	}
	if _, err := os.Stat(s.Script); !os.IsNotExist(err) {
		t.Error("关掉后脚本还在")
	}
}

// 进程已经不在了的旧文件删掉，别的（自己的、还活着的、不认识的）不动。
func TestAskpassCleanStale(t *testing.T) {
	dir := shortDir(t)
	dead := exec.Command("/bin/sh", "-c", "exit 0")
	dead.Run()
	stale := prefix + strconv.Itoa(dead.Process.Pid)
	alive := prefix + strconv.Itoa(os.Getppid())
	for _, n := range []string{stale + ".sock", stale + ".sh", alive + ".sock", "other.sock", prefix + "x.sh"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	CleanStale(dir)
	for n, want := range map[string]bool{stale + ".sock": false, stale + ".sh": false, alive + ".sock": true, "other.sock": true, prefix + "x.sh": true} {
		if _, err := os.Stat(filepath.Join(dir, n)); (err == nil) != want {
			t.Errorf("%s 在不在：%v，应为 %v", n, err == nil, want)
		}
	}
}

// 四种提示：HTTPS 用户名 / 密码（抠主机、用户名）、SSH 口令（抠钥匙路径）、主机指纹（抠主机），别的。
func TestAskpassClassify(t *testing.T) {
	cases := []struct {
		in   string
		want Prompt
	}{
		{"Username for 'https://github.com': ", Prompt{Kind: Username, Host: "github.com"}},
		{"Username for 'https://git.example.com:8443': ", Prompt{Kind: Username, Host: "git.example.com:8443"}},
		{"Password for 'https://octo@github.com': ", Prompt{Kind: Password, Host: "github.com", User: "octo"}},
		{"Password for 'https://gitee.com': ", Prompt{Kind: Password, Host: "gitee.com"}},
		{"Enter passphrase for key '/Users/x/.ssh/id_ed25519': ", Prompt{Kind: Passphrase, Key: "/Users/x/.ssh/id_ed25519"}},
		{"Enter passphrase for '/Users/x/my key': ", Prompt{Kind: Passphrase, Key: "/Users/x/my key"}},
		{"The authenticity of host 'github.com (140.82.112.4)' can't be established.\nED25519 key fingerprint is SHA256:+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU.\n" +
			"This key is not known by any other names.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ",
			Prompt{Kind: HostKey, Host: "github.com"}},
		{"The authenticity of host '[git.example.com]:2222 ([10.0.0.2]:2222)' can't be established.\nAre you sure you want to continue connecting (yes/no)? ",
			Prompt{Kind: HostKey, Host: "[git.example.com]:2222"}},
		{"git@example.com's password: ", Prompt{Kind: Other}},
	}
	for _, c := range cases {
		got := Classify(c.in)
		got.Text = ""
		if got != c.want {
			t.Errorf("%q：%+v，应为 %+v", c.in, got, c.want)
		}
	}
	if p := Classify("git@example.com's password: "); p.Title() != "git@example.com's password" {
		t.Errorf("标题 %q", p.Title())
	}
}
