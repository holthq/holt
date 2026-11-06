package crash

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// 子进程：Setup 之后在后台 goroutine 里 panic（TestCrashLog 用环境变量叫它）。
func TestMain(m *testing.M) {
	if dir := os.Getenv("CRASH_TEST_DIR"); dir != "" {
		_, w, err := Setup(dir, "Holt test")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		log.SetOutput(w)
		log.Printf("读状态失败：示例")
		go func() { panic("后台出错了") }()
		time.Sleep(10 * time.Second)
		os.Exit(0) // 不会到这里
	}
	os.Exit(m.Run())
}

// TestCrashLog：后台 goroutine 的 panic 带栈写进 crash-pending.log，log 写进 workbench.log；
// 下次 Setup 把崩溃日志改名留下、交出路径，新的 crash-pending.log 只有开头；没崩就不留。
func TestCrashLog(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "CRASH_TEST_DIR="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("子进程应该崩掉")
	}
	if !strings.Contains(stderr.String(), "后台出错了") {
		t.Fatalf("stderr 里没有 panic：%s", stderr.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, PendingName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Holt test\nstarted ", sep, "panic: 后台出错了", "goroutine ", "crash_test.go"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("崩溃日志里没有 %q：\n%s", want, b)
		}
	}
	if l, _ := os.ReadFile(filepath.Join(dir, LogName)); !bytes.Contains(l, []byte("读状态失败：示例")) || !bytes.Contains(l, []byte("Holt test")) {
		t.Errorf("workbench.log：%s", l)
	}

	// 下次启动（在本进程里；最后把 SetCrashOutput 关掉）
	t.Cleanup(func() { debug.SetCrashOutput(nil, debug.CrashOptions{}) })
	last, w, err := Setup(dir, "Holt test 2")
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if !strings.HasPrefix(filepath.Base(last), "crash-") {
		t.Fatalf("last = %q", last)
	}
	if old, _ := os.ReadFile(last); !bytes.Equal(old, b) {
		t.Errorf("留下的崩溃日志变了")
	}
	if p, _ := os.ReadFile(filepath.Join(dir, PendingName)); !strings.HasPrefix(string(p), "Holt test 2\n") || !strings.HasSuffix(string(p), sep) {
		t.Errorf("新的 crash-pending.log：%q", p)
	}
	// 没崩：再启动不留东西
	last, w, err = Setup(dir, "Holt test 3")
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if last != "" {
		t.Errorf("没崩也留了 %s", last)
	}
}

// TestCrashLogRotation：错误日志超过 MaxLogSize 改名成 workbench.old.log；崩溃日志最多留 MaxCrashLogs 份。
func TestCrashLogRotation(t *testing.T) {
	t.Cleanup(func() { debug.SetCrashOutput(nil, debug.CrashOptions{}) })
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, LogName), bytes.Repeat([]byte("x"), MaxLogSize+1), 0o644)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for i := range MaxCrashLogs + 3 {
		os.WriteFile(filepath.Join(dir, "crash-"+base.Add(time.Duration(i)*time.Hour).Format("20060102-150405")+".log"), []byte("old"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, PendingName), []byte("Holt x\n"+sep+"panic: boom\n"), 0o644)
	last, w, err := Setup(dir, "Holt y")
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if info, err := os.Stat(filepath.Join(dir, OldLogName)); err != nil || info.Size() != MaxLogSize+1 {
		t.Errorf("workbench.old.log：%v %v", info, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, LogName)); info.Size() > 200 {
		t.Errorf("新的 workbench.log 有 %d 字节", info.Size())
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "crash-2*.log"))
	if len(logs) != MaxCrashLogs {
		t.Errorf("留了 %d 份崩溃日志：%v", len(logs), logs)
	}
	if _, err := os.Stat(last); err != nil {
		t.Errorf("刚崩的那份被删了：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "crash-20260101-000000.log")); err == nil {
		t.Errorf("最旧的那份还在")
	}
}
