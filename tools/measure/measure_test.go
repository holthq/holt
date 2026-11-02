package measure

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 量内存的脚本：两个 zsh 脚本语法对、report.py 的自测过、种子按工作区换对（MEASURE_DRY 只造 state.json，不等空闲、不起应用）。
func TestMeasureScripts(t *testing.T) {
	for _, f := range []string{"measure.sh", "longrun.sh"} {
		if out, err := exec.Command("zsh", "-n", f).CombinedOutput(); err != nil {
			t.Errorf("zsh -n %s：%v\n%s", f, err, out)
		}
	}
	if out, err := exec.Command("python3", "-I", "report.py", "--selftest").CombinedOutput(); err != nil {
		t.Errorf("report.py --selftest：%v\n%s", err, out)
	}
	if out, err := exec.Command("zsh", "measure.sh").CombinedOutput(); err == nil || !strings.Contains(string(out), "用法") {
		t.Errorf("没参数应该打用法、退出码非零：%v\n%s", err, out)
	}

	// 五个仓库：第 1 个只有根提交（新加的文件），第 3 个最近一次改了文件；第 0 个在工作区文件里写相对路径
	repos := make([]*testutil.Repo, 5)
	roots := make([]string, 5)
	for i := range repos {
		r := testutil.NewRepo(t, "r"+string(rune('0'+i)))
		r.Write("a.txt", "一\n")
		r.Commit("一")
		if i != 1 {
			r.Write("a.txt", "一\n二\n")
			r.Commit("二")
		}
		repos[i], roots[i] = r, r.Root
	}
	ws := filepath.Join(filepath.Dir(roots[0]), "five.code-workspace")
	folders := `{"path": "r0"}`
	for _, r := range roots[1:] {
		folders += `, {"path": "` + r + `"}`
	}
	if err := os.WriteFile(ws, []byte(`{"folders": [`+folders+`], "settings": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(t.TempDir(), "Grove.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "Grove"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleExecutable</key><string>Grove</string><key>CFBundleShortVersionString</key><string>9.9.9</string></dict></plist>
`), 0o644)
	out := t.TempDir()
	dry := func(label, seed string) *workspace.State {
		t.Helper()
		cmd := exec.Command("zsh", "measure.sh", label, app, "-w", ws, "-s", seed)
		cmd.Env = append(os.Environ(), "MEASURE_DRY=1", "MEASURE_OUT="+out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("measure.sh %s：%v\n%s", label, err, b)
		}
		b, err := os.ReadFile(filepath.Join(out, label, "state", "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		var st workspace.State
		if err := json.Unmarshal(b, &st); err != nil {
			t.Fatalf("%s 的 state.json 读不了：%v\n%s", label, err, b)
		}
		if _, err := os.Stat(filepath.Join(out, label, "state", "window-state.json")); err != nil {
			t.Errorf("%s 没拷窗口位置：%v", label, err)
		}
		if st.AppVersion != "9.9.9" || !slices.Equal(st.Windows, []string{ws}) || st.Sessions[ws] == nil {
			t.Fatalf("%s：版本 %q、窗口 %v、会话 %v", label, st.AppVersion, st.Windows, st.Sessions)
		}
		if s := st.Sessions[ws]; !slices.Equal(s.Open, roots) {
			t.Errorf("%s 展开的仓库：%v", label, s.Open)
		}
		return &st
	}

	idle := dry("idle", "seed/idle.json")
	if s := idle.Sessions[ws]; len(s.Tabs) != 0 || idle.BottomBar || s.Repo != roots[0] {
		t.Errorf("空闲：%d 个标签、底栏 %v、当前仓库 %s", len(s.Tabs), idle.BottomBar, s.Repo)
	}

	all := dry("allopen", "seed/allopen.json")
	s := all.Sessions[ws]
	if len(s.Tabs) != 10 || !all.BottomBar || s.Repo != roots[3] || s.Terms[roots[3]] == nil || len(s.Terms[roots[3]].Tabs) != 3 {
		t.Fatalf("全开：%d 个标签、底栏 %v、当前仓库 %s、终端 %v", len(s.Tabs), all.BottomBar, s.Repo, s.Terms)
	}
	if tab := s.Tabs[1]; tab.Path != filepath.Join(roots[3], "src/components/ListManage/ListManage.vue") {
		t.Errorf("文件标签的路径：%s", tab.Path)
	}
	if !s.Tabs[7].Preview {
		t.Error("README.md 应该是预览")
	}
	head := func(i int) string { return repos[i].Git("rev-parse", "HEAD") }
	want := []workspace.TabChange{
		{Kind: "commit", Path: "a.txt", Code: "M", Commit: head(3), Parent: repos[3].Git("rev-parse", "HEAD^")},
		{Kind: "commit", Path: "a.txt", Code: "A", Commit: head(1)}, // 只有根提交：没有父提交
	}
	for i, w := range want {
		tab := s.Tabs[8+i]
		if tab.Kind != workspace.TabDiff || tab.Change == nil || *tab.Change != w {
			t.Errorf("第 %d 个 Diff 标签：%+v，改动 %+v，要 %+v", i+1, tab, tab.Change, w)
		}
	}

	// 同一个标签再量一次不盖掉上一次的
	cmd := exec.Command("zsh", "measure.sh", "idle", app, "-w", ws)
	cmd.Env = append(os.Environ(), "MEASURE_DRY=1", "MEASURE_OUT="+out)
	if b, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(b), "已经有") {
		t.Errorf("同一个标签又跑了一次：%v\n%s", err, b)
	}
}
