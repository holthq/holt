package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/testutil"
)

// logLines 把 *logf 换成记进一个切片的（测试完换回去），返回取当前几行的函数。
func logLines(t *testing.T, logf *func(string, ...any)) func() []string {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	old := *logf
	*logf = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	t.Cleanup(func() { *logf = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

// 启动计时：第一个窗口的仓库都读完时每个仓库一行“就绪”、再一行“全部就绪”；
// 展开着的仓库有树和空目录，右栏要了历史的有历史，别的写“没读”；之后开的窗口不再打。
func TestReadyLog(t *testing.T) {
	alpha, _, _, ws := testutil.ThreeRepos(t)
	os.MkdirAll(filepath.Join(alpha.Root, "empty"), 0o755)
	oldL, oldDone := Launched, launchLogged
	Launched, launchLogged = time.Now(), false
	t.Cleanup(func() { Launched, launchLogged = oldL, oldDone })
	lines := logLines(t, &readyLogf)

	a, wait := newTestApp(t, ws)
	defer a.Close()
	a.Start()
	a.EnsureHistory(a.Repos[1]) // 右栏开着 beta 的历史
	wait("就绪行", func() bool { return len(lines()) >= 4 })
	got := lines()
	ms := `\d+ ms`
	want := []string{
		`^就绪 alpha：状态 ` + ms + `、树 ` + ms + `（2 个文件，排除 0）、空目录 ` + ms + `、历史没读$`,
		`^就绪 beta：状态 ` + ms + `、树没读、历史 ` + ms + `$`,
		`^就绪 gamma：状态 ` + ms + `、树没读、历史没读$`,
		`^全部就绪 ` + ms + `（窗口 ` + ms + `）$`,
	}
	if len(got) != len(want) {
		t.Fatalf("就绪行：\n%s", strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !regexp.MustCompile(w).MatchString(got[i]) {
			t.Errorf("第 %d 行 %q 不是 %s", i+1, got[i], w)
		}
	}
	if a.ready != nil || !a.Repos[0].Files.Loaded || a.Repos[0].Files.Dir("empty") == nil {
		t.Error("打完还在等，或者树 / 空目录没读完就打了")
	}

	// 再开一个窗口（换工作区也是）：不打
	b, wait2 := newTestApp(t, ws)
	defer b.Close()
	b.Start()
	wait2("第二个读完", func() bool { return b.Repos[0].Files.Loaded && idle(b.Repos[2])() })
	if n := len(lines()); n != 4 || b.ready != nil {
		t.Errorf("第二个窗口也打了：%d 行", n)
	}
}

// 全开：仓库读完后开两个 Diff、搜那个词、当前仓库的终端起 shell；
// 搜完那行日志带用时、搜了几个文件、跳过几个。
func TestAllOpenTiming(t *testing.T) {
	_, _, _, ws := testutil.ThreeRepos(t)
	old := AllOpenWait
	AllOpenWait = 0
	t.Cleanup(func() { AllOpenWait = old })
	lines := logLines(t, &allOpenLogf)

	a, wait := newTestApp(t, ws)
	defer a.Close()
	a.St.Settings.Shell = "/bin/sh"
	a.Start()
	a.AllOpen("二")
	wait("全开那行", func() bool { return len(lines()) > 0 })
	re := regexp.MustCompile(`^全开：2 个标签（2 个 Diff），搜 "二" (\d+) 处（用时 \d+ ms，搜了 (\d+) 个文件、跳过 0 个），1 个 shell$`)
	m := re.FindStringSubmatch(lines()[0])
	if m == nil {
		t.Fatalf("全开那行：%q", lines()[0])
	}
	if m[1] != fmt.Sprint(len(a.Search.Res.Matches)) || m[2] != fmt.Sprint(a.Search.Res.Searched) || m[1] == "0" {
		t.Errorf("处数 / 文件数对不上：%q，结果 %d 处、%d 个文件", lines()[0], len(a.Search.Res.Matches), a.Search.Res.Searched)
	}
}
