package app

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// 启动计时（量启动用）：第一个窗口的仓库都读完时日志里每个仓库一行“就绪”、最后一行“全部就绪”。
// 每一步是它自己从开始到结束的用时（后台读的），全部就绪是从进程启动（Launched）算起。

// Launched 是进程启动的时刻，main 一开始记；零值（测试、没记）不打。
var Launched time.Time

// readyLogf 是打就绪行的地方（测试换掉）。
var readyLogf = log.Printf

// launchLogged 是已经给第一个窗口打过（之后开的窗口、换的工作区不打）。
var launchLogged bool

// readyLog 是启动时还在等的那几步：状态每个仓库都要；树、空目录、历史是启动时开始读了的才等。
type readyLog struct {
	win   time.Duration // 进程启动到窗口建好（App.Start）
	repos map[*Repo]*repoReady
}

type repoReady struct {
	status, tree, empty, hist time.Duration
	files, hidden             int
	// asked / done：readyTree、readyEmpty、readyHist 各一位；状态总要等
	asked, done int
	statusDone  bool
}

const (
	readyTree = 1 << iota
	readyEmpty
	readyHist
)

// startReadyLog 在第一个窗口的 Start 里调：进程记了启动时刻、还没打过时开始等。
func (a *App) startReadyLog() {
	if Launched.IsZero() || launchLogged || len(a.Repos) == 0 {
		return
	}
	launchLogged = true
	a.ready = &readyLog{win: time.Since(Launched), repos: map[*Repo]*repoReady{}}
}

func (a *App) readyOf(r *Repo) *repoReady {
	if a.ready == nil {
		return nil
	}
	rr := a.ready.repos[r]
	if rr == nil {
		rr = &repoReady{}
		a.ready.repos[r] = rr
	}
	return rr
}

// readyAsk 是启动时开始读 r 的树 / 空目录 / 历史（step）：全部就绪要等它读完。读完过的不再等。
func (a *App) readyAsk(r *Repo, step int) {
	if rr := a.readyOf(r); rr != nil && rr.done&step == 0 {
		rr.asked |= step
	}
}

// readyStatus 是 r 第一次读完状态，用了 d。
func (a *App) readyStatus(r *Repo, d time.Duration) {
	if rr := a.readyOf(r); rr != nil && !rr.statusDone {
		rr.statusDone, rr.status = true, d
		a.readyCheck()
	}
}

// readyTreeDone 是 r 的文件列表第一次读完：files 个文件、files.exclude 去掉了 hidden 个；scan 是接着要整棵找空目录。
func (a *App) readyTreeDone(r *Repo, d time.Duration, files, hidden int, scan bool) {
	rr := a.readyOf(r)
	if rr == nil || rr.done&readyTree != 0 {
		return
	}
	rr.done |= readyTree
	rr.tree, rr.files, rr.hidden = d, files, hidden
	if scan {
		rr.asked |= readyEmpty
	}
	a.readyCheck()
}

// readyStep 是 r 的空目录或历史（step）第一次读完，用了 d。
func (a *App) readyStep(r *Repo, step int, d time.Duration) {
	rr := a.readyOf(r)
	if rr == nil || rr.done&step != 0 {
		return
	}
	rr.done |= step
	switch step {
	case readyEmpty:
		rr.empty = d
	case readyHist:
		rr.hist = d
	}
	a.readyCheck()
}

// readyCheck 是都读完了没有：每个仓库的状态、开始读了的树 / 空目录 / 历史。读完了打出来，不再等。
func (a *App) readyCheck() {
	rl := a.ready
	if rl == nil {
		return
	}
	for _, r := range a.Repos {
		rr := rl.repos[r]
		if rr == nil || !rr.statusDone || rr.asked&^rr.done != 0 {
			return
		}
	}
	a.ready = nil
	for _, r := range a.Repos {
		readyLogf("%s", readyLine(r.Name, rl.repos[r]))
	}
	readyLogf("全部就绪 %d ms（窗口 %d ms）", time.Since(Launched).Milliseconds(), rl.win.Milliseconds())
}

// readyLine 是一个仓库的就绪行：就绪 <仓库>：状态 12 ms、树 40 ms（11 539 个文件，排除 0）、空目录 900 ms、历史 30 ms。
// 启动时没读的写“没读”。是日志，不翻译：一段段经 lineLogf 写（名字以 Logf 结尾，i18n 的 TestCatalog 不收）。
func readyLine(name string, rr *repoReady) string {
	var b strings.Builder
	lineLogf := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	lineLogf("就绪 %s：状态 %d ms", name, rr.status.Milliseconds())
	if rr.asked&readyTree != 0 {
		lineLogf("、树 %d ms（%d 个文件，排除 %d）", rr.tree.Milliseconds(), rr.files, rr.hidden)
	} else {
		lineLogf("、树没读")
	}
	if rr.asked&readyEmpty != 0 {
		lineLogf("、空目录 %d ms", rr.empty.Milliseconds())
	}
	if rr.asked&readyHist != 0 {
		lineLogf("、历史 %d ms", rr.hist.Milliseconds())
	} else {
		lineLogf("、历史没读")
	}
	return b.String()
}
