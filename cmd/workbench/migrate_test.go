package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// TestMigrateDirs：改名 Holt 时状态目录和日志目录从 Grove（更早是 Workbench）搬过来（临时 HOME，不碰真的目录）。
// 新名字优先搬 Grove，只有更早的 Workbench 时搬 Workbench；两个都有时 Grove 搬过去、Workbench 留着；
// 新目录已经有了、设了 WORKBENCH_STATE / WORKBENCH_LOGS、名字不是 Holt → 都不动；
// 改名不行 → 拷过去（权限、符号链接照旧，FIFO 这类跳过），旧的留着；拷也不行 → 什么都不变。
func TestMigrateDirs(t *testing.T) {
	t.Setenv("WORKBENCH_STATE", "")
	t.Setenv("WORKBENCH_LOGS", "")
	type dirs struct{ oldState, newState, oldLogs, newLogs, conf, logs string }
	// fresh 换一个临时 HOME，给 old 建旧的状态目录（state.json 0600、一个子目录、一个符号链接、一个 FIFO）
	// 和旧的日志目录（workbench.log）；新目录还不存在。
	fresh := func(t *testing.T, old string) dirs {
		home := t.TempDir()
		t.Setenv("HOME", home)
		conf, err := os.UserConfigDir()
		if err != nil || !strings.HasPrefix(conf, home) {
			t.Fatalf("UserConfigDir %q %v 不在临时 HOME 下", conf, err)
		}
		logs := filepath.Join(home, "Library", "Logs")
		d := dirs{filepath.Join(conf, old), filepath.Join(conf, "Holt"), filepath.Join(logs, old), filepath.Join(logs, "Holt"), conf, logs}
		for _, f := range []struct {
			path, body string
			perm       os.FileMode
		}{
			{filepath.Join(d.oldState, "state.json"), `{"version": 3}`, 0o600},
			{filepath.Join(d.oldState, "history", "a.json"), "甲", 0o644},
			{filepath.Join(d.oldLogs, "workbench.log"), "日志\n", 0o644},
		} {
			os.MkdirAll(filepath.Dir(f.path), 0o755)
			if err := os.WriteFile(f.path, []byte(f.body), f.perm); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("state.json", filepath.Join(d.oldState, "link")); err != nil {
			t.Fatal(err)
		}
		if err := testutil.MakeFIFO(filepath.Join(d.oldState, "fifo"), 0o600); err != nil && !errors.Is(err, testutil.ErrNoFIFO) {
			t.Fatal(err)
		}
		return d
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }

	for _, old := range []string{"Grove", "Workbench"} {
		t.Run("只有 "+old, func(t *testing.T) {
			d := fresh(t, old)
			lines := migrateDirs("Holt")
			if len(lines) != 2 || !strings.Contains(lines[0], "状态目录从") || !strings.Contains(lines[1], "日志目录从") ||
				!strings.Contains(lines[0], "改名 Holt") {
				t.Fatalf("日志行：%q", lines)
			}
			if exists(d.oldState) || exists(d.oldLogs) || read(filepath.Join(d.newState, "state.json")) != `{"version": 3}` ||
				read(filepath.Join(d.newState, "history", "a.json")) != "甲" || read(filepath.Join(d.newLogs, "workbench.log")) != "日志\n" {
				t.Fatal("没整个搬过来")
			}
			if migrateDirs("Holt") != nil {
				t.Fatal("搬过一次再起又搬了")
			}
		})
	}

	t.Run("Grove 优先", func(t *testing.T) {
		d := fresh(t, "Grove")
		older := filepath.Join(d.conf, "Workbench")
		os.MkdirAll(older, 0o755)
		os.WriteFile(filepath.Join(older, "state.json"), []byte("旧\n"), 0o644)
		if lines := migrateDirs("Holt"); len(lines) != 2 {
			t.Fatalf("日志行：%q", lines)
		}
		if read(filepath.Join(d.newState, "state.json")) != `{"version": 3}` {
			t.Fatal("搬的不是 Grove 的那份")
		}
		if read(filepath.Join(older, "state.json")) != "旧\n" {
			t.Fatal("更早的 Workbench 不该动")
		}
	})

	t.Run("不动", func(t *testing.T) {
		for _, c := range []struct {
			name, env, val string
			newDir         bool
		}{
			{"两个都有", "", "", true},
			{"WORKBENCH_STATE", "WORKBENCH_STATE", "/tmp/scratch", false},
			{"WORKBENCH_LOGS", "WORKBENCH_LOGS", "/tmp/scratch", false},
			{"名字不是 Holt", "", "", false},
		} {
			d := fresh(t, "Grove")
			if c.env != "" {
				t.Setenv(c.env, c.val)
			}
			if c.newDir {
				os.MkdirAll(d.newState, 0o755)
				os.MkdirAll(d.newLogs, 0o755)
			}
			name := "Holt"
			if c.name == "名字不是 Holt" {
				name = "workbench" // go run 的可执行文件名
			}
			if lines := migrateDirs(name); lines != nil {
				t.Errorf("%s：搬了 %q", c.name, lines)
			}
			if !exists(filepath.Join(d.oldState, "state.json")) || !exists(filepath.Join(d.oldLogs, "workbench.log")) ||
				exists(filepath.Join(d.newState, "state.json")) || (!c.newDir && (exists(d.newState) || exists(d.newLogs))) {
				t.Errorf("%s：目录动了", c.name)
			}
			if c.env != "" {
				t.Setenv(c.env, "")
			}
		}
	})

	t.Run("改名不行就拷", func(t *testing.T) {
		d := fresh(t, "Grove")
		rename = func(string, string) error { return errors.New("跨卷") }
		defer func() { rename = os.Rename }()
		lines := migrateDirs("Holt")
		if len(lines) != 2 || !strings.Contains(lines[0], "拷到") || !strings.Contains(lines[0], "跨卷") {
			t.Fatalf("日志行：%q", lines)
		}
		if read(filepath.Join(d.newState, "state.json")) != `{"version": 3}` || read(filepath.Join(d.newState, "history", "a.json")) != "甲" ||
			read(filepath.Join(d.newLogs, "workbench.log")) != "日志\n" {
			t.Fatal("没拷全")
		}
		if fi, err := os.Stat(filepath.Join(d.newState, "state.json")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("state.json 的权限没照旧：%v %v", fi.Mode(), err)
		}
		if l, err := os.Readlink(filepath.Join(d.newState, "link")); err != nil || l != "state.json" {
			t.Errorf("符号链接：%q %v", l, err)
		}
		if exists(filepath.Join(d.newState, "fifo")) || exists(d.newState+".migrating") {
			t.Error("FIFO 拷了，或者临时目录没收")
		}
		if !exists(filepath.Join(d.oldState, "state.json")) || !exists(filepath.Join(d.oldLogs, "workbench.log")) {
			t.Error("旧的该留着")
		}
	})

	t.Run("拷也不行", func(t *testing.T) {
		d := fresh(t, "Grove")
		rename = func(string, string) error { return errors.New("跨卷") }
		defer func() { rename = os.Rename }()
		locked := filepath.Join(d.oldState, "history", "a.json")
		os.Chmod(locked, 0)
		defer os.Chmod(locked, 0o644)
		lines := migrateDirs("Holt")
		if len(lines) != 2 || !strings.Contains(lines[0], "没搬成") || !strings.Contains(lines[1], "拷到") {
			t.Fatalf("日志行：%q", lines)
		}
		if exists(d.newState) || exists(d.newState+".migrating") || !exists(filepath.Join(d.oldState, "state.json")) {
			t.Error("拷到一半的该删掉、旧的留着")
		}
	})
}
