package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 不开 termd 的回滚：关窗口时这一格的回滚写进 termhist（超过上限从一行的开头截，留最后的），会话里记着文件名；
// 重开时这一格启动前喂回去，前面一行“上次的输出（时间）”，提示符接在下面；读完文件就删了、会话里也不记了。
func TestTermHistSaveRestore(t *testing.T) {
	old := app.TermHistCap
	app.TermHistCap = 64 << 10 // 终端自己的回滚这里只留一万来行（约 128 KB），上限调小才截得到
	t.Cleanup(func() { app.TermHistCap = old })
	_, ws := testutil.TempRepo(t)
	state := termState(t, true)
	a, tt, wait := termUI(t, ws, state)
	r := a.Repos[0]
	wait("git status", idle(r))
	p := readyPane(t, a, tt)
	typeLine(t, tt, p, "seq 1 $((60000-1))", "59999")
	typeLine(t, tt, p, "echo hello-$((6*7))", "hello-42")
	start := time.Now()
	a.NoteTermDirs()
	took := time.Since(start)
	a.SaveState()
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[a.WsPath]
	if s == nil || s.Terms[r.Root] == nil || s.Terms[r.Root].Tabs[0].Split == nil {
		t.Fatalf("会话里没记回滚：%+v", s)
	}
	hist := s.Terms[r.Root].Tabs[0].Split.Hist
	dirs, _ := filepath.Glob(filepath.Join(state, "termhist", "*"))
	if hist == "" || len(dirs) != 1 {
		t.Fatalf("回滚文件 %q，目录 %q", hist, dirs)
	}
	path := filepath.Join(dirs[0], hist)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > int64(app.TermHistCap) || fi.Size() < int64(app.TermHistCap)-200 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("回滚文件 %d 字节（上限 %d）、权限 %v", fi.Size(), app.TermHistCap, fi.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	first, _, _ := strings.Cut(string(data), "\n")
	if !regexp.MustCompile(`^[0-9]+(\x1b\[0m)?\r?$`).MatchString(first) || !strings.Contains(string(data), "hello-42") {
		t.Fatalf("截的不是从一行的开头起，或者没有最后的输出：第一行 %q", first)
	}
	t.Logf("写回滚（含 lsof 读目录）%v，%d 字节", took, fi.Size())
	a.Close()

	a, tt, wait = termUI(t, ws, state)
	r = a.Repos[0]
	wait("git status", idle(r))
	start = time.Now()
	p = readyPane(t, a, tt)
	t.Logf("重开到出提示符 %v", time.Since(start))
	text := p.Term.Text()
	if !strings.Contains(text, "上次的输出（") || !strings.Contains(text, "hello-42") {
		t.Fatalf("重开后没喂回上次的输出：\n%s", text[max(0, len(text)-500):])
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("读完应删掉文件：%v", err)
	}
	typeLine(t, tt, p, "echo again-$((1+2))", "again-3")
	a.SaveState()
	st, _ = workspace.LoadState(state)
	if ts := st.Sessions[a.WsPath].Terms[r.Root]; ts != nil && ts.Tabs[0].Split != nil && ts.Tabs[0].Split.Hist != "" {
		t.Fatalf("读回去之后会话里还记着回滚：%+v", ts.Tabs[0].Split)
	}
}
