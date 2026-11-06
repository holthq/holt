package app

import (
	"hash/fnv"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"github.com/holthq/holt/internal/git"
)

// 终端里的 agent（claude、codex……）的状态，按工作树汇总：工作树就是 agent 的工位。
// 全在本机算，只看 Holt 自己的终端：前台进程名在 agent 名单里算有 agent；程序响铃（BEL）或要通知（OSC 9 / 777）、
// 而用户没在看这一格算"等你"，一直到用户切进这一格；最近 10 秒有输出算"运行中"，否则"空闲"。
// 不读 agent 的日志，不接它的接口，换一个 agent 也一样。

// AgentKind 是一格终端（或一个工作树）上 agent 的状态，越往后越需要你。
type AgentKind int

const (
	AgentNone    AgentKind = iota // 没有 agent
	AgentIdle                     // 前台是 agent，超过 10 秒没输出
	AgentRunning                  // 前台是 agent，最近 10 秒有输出
	AgentWaiting                  // 响了铃 / 要了通知，用户还没切进这一格
)

// AgentState 是 agent 的状态和它在哪个终端。
type AgentState struct {
	State     AgentKind
	RunSince  time.Time // 运行中：这一段从什么时候起有输出
	WaitSince time.Time // 等你：从什么时候起
	Repo      *Repo
	Tab       *TermTab
	Pane      *TermPane
}

// Waited 是到 now 等了多久；不在等为 0。
func (s AgentState) Waited(now time.Time) time.Duration {
	if s.State != AgentWaiting {
		return 0
	}
	return now.Sub(s.WaitSince)
}

// DefaultAgents 是默认认作 agent 的前台命令；设置里的「agent 命令」接在后面。
var DefaultAgents = []string{"claude", "codex", "gemini", "aider", "opencode"}

const (
	agentQuiet = 10 * time.Second // 这么久没输出就从运行中变成空闲
	agentPoll  = 2 * time.Second  // 前台进程、本地终端的屏幕隔这么久看一次
)

// now 是现在（测试换成可拨的钟）。
func (a *App) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// agentNames 是认作 agent 的命令名：默认的加设置里的（逗号或空格分开）。
func (a *App) agentNames() []string {
	names := slices.Clone(DefaultAgents)
	for _, f := range strings.FieldsFunc(a.St.Settings.AgentNames, func(r rune) bool { return r == ',' || r == ' ' || r == '，' }) {
		if f = strings.TrimSpace(f); f != "" && !slices.Contains(names, f) {
			names = append(names, f)
		}
	}
	return names
}

// isAgent 是前台命令 job 在名单里。
func (a *App) isAgent(job string) bool { return matchAgent(a.agentNames(), job) }

// paneLooked 是用户正看着这一格：键盘在它上面。
func (a *App) paneLooked(p *TermPane) bool {
	if !a.TermFocused {
		return false
	}
	_, _, cur := a.ActivePane()
	return cur == p
}

// paneBell 是这一格响了铃 / 要了通知（主线程）：用户没在看它就记成等你。
func (a *App) paneBell(p *TermPane, gen int) {
	if p.gen != gen || a.paneLooked(p) {
		return
	}
	if p.bellAt.IsZero() {
		p.bellAt = a.now()
	}
}

// paneOutput 记下这一格刚有输出（任意 goroutine：termd 的帧是在读连接的 goroutine 上到的）。
func (p *TermPane) paneOutput(t time.Time) { p.outNs.Store(t.UnixNano()) }

// lastOutput 是这一格最近一次有输出的时候；没有为零。
func (p *TermPane) lastOutput() time.Time {
	if n := p.outNs.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Time{}
}

// seen 是用户切进了这一格（键盘到了它上面）：等你的记号去掉。
func (p *TermPane) seen() { p.bellAt = time.Time{} }

// PaneAgent 是一格终端上 agent 的状态（只看记下的，不跑命令）。
func (a *App) PaneAgent(p *TermPane) AgentKind {
	if p == nil || p.Term == nil {
		return AgentNone
	}
	if !p.bellAt.IsZero() {
		return AgentWaiting
	}
	if p.Exited || !a.isAgent(p.job) {
		return AgentNone
	}
	if out := p.lastOutput(); !out.IsZero() && a.now().Sub(out) < agentQuiet {
		return AgentRunning
	}
	return AgentIdle
}

// TabAgent 是终端标签里最需要你的那一格的状态。
func (a *App) TabAgent(tab *TermTab) AgentKind {
	k := AgentNone
	for _, p := range tab.Panes() {
		k = max(k, a.PaneAgent(p))
	}
	return k
}

// WtAgent 是工作树 w（主工作树也算）上的 agent：这个窗口开着的终端里，现在的目录落在 w 里的那些格中最需要你的一个
// （等你 > 运行中 > 空闲；一样的取先开的）。套在 w 里面的别的工作树的目录算那个工作树的。
func (a *App) WtAgent(r *Repo, w Worktree) AgentState {
	var best AgentState
	path := w.Path
	if w.Real != "" {
		path = w.Real
	}
	a.eachPane(func(pr *Repo, p *TermPane) {
		k := a.PaneAgent(p)
		if k <= best.State {
			return
		}
		dir := a.paneDir(p, false)
		if dir == "" {
			dir = pr.Root
		}
		if !inWorktree(r, path, dir) {
			return
		}
		best = AgentState{State: k, Repo: pr, Tab: a.tabOfPane(p), Pane: p}
		switch k {
		case AgentWaiting:
			best.WaitSince = p.bellAt
		case AgentRunning:
			best.RunSince = p.runAt
		}
	})
	return best
}

// inWorktree 是目录 dir 落在工作树 path 里，而且不在仓库 r 别的、套在 path 里面的工作树里。
func inWorktree(r *Repo, path, dir string) bool {
	if !git.SamePath(path, dir) && git.Under(path, dir) == "" {
		return false
	}
	for _, o := range r.Worktrees {
		if o.Path == path || o.Real == path || git.SamePath(o.Path, path) {
			continue
		}
		if git.Under(path, o.Path) != "" && (git.SamePath(o.Path, dir) || git.Under(o.Path, dir) != "") {
			return false
		}
	}
	return true
}

// GoToPane 切到这一格终端（工作树行上的终端按钮、"需要你"条的上一个 / 下一个用）：底栏的打开底栏、切到它的仓库和标签，
// 编辑器区的切到那个标签；键盘给这一格，等你的记号随之去掉。
func (a *App) GoToPane(r *Repo, tab *TermTab, p *TermPane) {
	if r == nil || tab == nil || p == nil || tab.Root.leaf(p) == nil {
		return
	}
	tab.Active = p
	p.seen()
	if tab.InEditor {
		if d := a.termDocOf(r, tab); d != nil {
			if g, i := a.locate(d.Key()); g != nil {
				a.SelectIn(g, i)
			}
		}
		a.edTab, a.edRepo = tab, r
		a.FocusTermTab(tab)
		return
	}
	if i := slices.Index(r.Terms, tab); i >= 0 {
		a.UseRepo(r)
		a.St.BottomBar = true
		a.SelectTermTab(r, i)
		a.FocusTermTab(tab)
	}
}

// agentPoller 是后台看前台进程的那个 goroutine 和它要看的格（主线程换表，goroutine 拿副本读）。
// 一轮只跑一次 ps -A；读到的和上一轮一样就不回主线程（回主线程会重画一帧，空闲时不该有帧）。
type agentPoller struct {
	mu      sync.Mutex
	panes   []agentJob
	names   []string
	running bool
	quit    bool
}

type agentJob struct {
	p     *TermPane
	pid   int
	term  *terminal.Terminal // 本地终端（没有输出回调，看屏幕变没变）；termd 的格为 nil
	job   string
	run   bool
	local bool
}

// syncAgents 把开着的终端交给后台轮询（主线程：格认领到进程号、接上 termd、关了、前台换了之后）；有格没起就起。
func (a *App) syncAgents() {
	var jobs []agentJob
	a.eachPane(func(_ *Repo, p *TermPane) {
		if p.Term != nil && !p.Exited && p.pid != 0 {
			j := agentJob{p: p, pid: p.pid, local: p.conn == nil}
			if j.local {
				j.term = p.Term
			}
			jobs = append(jobs, j)
		}
	})
	if a.agents == nil {
		if len(jobs) == 0 {
			return
		}
		a.agents = &agentPoller{}
	}
	ap := a.agents
	ap.mu.Lock()
	defer ap.mu.Unlock()
	ap.panes, ap.names = jobs, a.agentNames()
	if ap.running || ap.quit || len(jobs) == 0 || a.Update == nil {
		return
	}
	ap.running = true
	jobsOf := a.fgJobs
	if jobsOf == nil {
		jobsOf = foregroundJobs
	}
	go ap.poll(a.Update, jobsOf, a.noteAgents)
}

// stopAgents 停掉后台轮询（窗口关了）。
func (a *App) stopAgents() {
	if ap := a.agents; ap != nil {
		ap.mu.Lock()
		ap.quit = true
		ap.mu.Unlock()
	}
}

type agentSeen struct {
	job string
	run bool
	sig uint64
}

// pollAgents 隔 agentPoll 看一次：各格前台的命令（一次 ps）、本地终端的屏幕指纹（变了算有输出；termd 的格跟着帧记），
// 前台换了、运行中 / 空闲翻了才回主线程（note）。没有开着的终端就停，syncAgents 再起。
// 不碰 App：换工作区时整个 App 会被换掉（*a = *n）。
func (ap *agentPoller) poll(up func(func()), jobsOf func([]int) map[int]string, note func([]agentJob)) {
	last := map[*TermPane]agentSeen{}
	for {
		time.Sleep(agentPoll)
		ap.mu.Lock()
		if ap.quit || len(ap.panes) == 0 {
			ap.running = false
			ap.mu.Unlock()
			return
		}
		jobs, names := slices.Clone(ap.panes), ap.names
		ap.mu.Unlock()
		pids := make([]int, len(jobs))
		for i, j := range jobs {
			pids[i] = j.pid
		}
		fg := jobsOf(pids)
		now := time.Now()
		var changed []agentJob
		seen := make(map[*TermPane]agentSeen, len(jobs))
		for _, j := range jobs {
			old := last[j.p]
			s := agentSeen{job: fg[j.pid]}
			agent := matchAgent(names, s.job)
			if agent && j.term != nil {
				h := fnv.New64a()
				h.Write([]byte(j.term.Text()))
				s.sig = h.Sum64()
				if old.sig != 0 && s.sig != old.sig {
					j.p.paneOutput(now)
				}
			}
			if out := j.p.lastOutput(); agent && !out.IsZero() && now.Sub(out) < agentQuiet {
				s.run = true
			}
			if _, ok := last[j.p]; !ok || s.job != old.job || s.run != old.run {
				j.job, j.run = s.job, s.run
				changed = append(changed, j)
			}
			seen[j.p] = s
		}
		last = seen
		if len(changed) > 0 {
			up(func() { note(changed) })
		}
	}
}

// noteAgents 记下后台读到的（主线程）：前台的命令、运行从什么时候起；顺手按现在的格和名单换一次表。
func (a *App) noteAgents(jobs []agentJob) {
	for _, j := range jobs {
		p := j.p
		if p.pid != j.pid {
			continue
		}
		p.job = j.job
		if j.run && a.isAgent(j.job) {
			if p.runAt.IsZero() {
				p.runAt = p.lastOutput()
			}
		} else {
			p.runAt = time.Time{}
		}
	}
	a.syncAgents()
}

// matchAgent 是前台命令 job 在名单 names 里（不分大小写）。
func matchAgent(names []string, job string) bool {
	if job == "" || job == "?" {
		return false
	}
	for _, n := range names {
		if strings.EqualFold(job, n) {
			return true
		}
	}
	return false
}

// foregroundJobs 用一次 ps 读各个 shell（进程号 pids）前台的命令：shell 的进程组不是它终端的前台进程组时，
// 前台那一组组长的名字；在提示符那里的不在表里。脚本写的 agent（node / bun / python 跑的）按脚本的名字算。
func foregroundJobs(pids []int) map[int]string {
	out, err := exec.Command("ps", "-A", "-o", "pid=,pgid=,tpgid=,args=").Output()
	if err != nil {
		return nil
	}
	type row struct {
		pgid, tpgid int
		args        []string
	}
	rows := map[int]row{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		pgid, _ := strconv.Atoi(f[1])
		tpgid, _ := strconv.Atoi(f[2])
		rows[pid] = row{pgid, tpgid, f[3:]}
	}
	jobs := map[int]string{}
	for _, pid := range pids {
		sh, ok := rows[pid]
		if !ok || sh.tpgid <= 0 || sh.tpgid == sh.pgid {
			continue
		}
		if lead, ok := rows[sh.tpgid]; ok {
			jobs[pid] = jobName(lead.args)
		} else {
			jobs[pid] = "?"
		}
	}
	return jobs
}

// jobName 是命令行 args 的命令名：argv[0] 的文件名（登录 shell 的 - 去掉）；解释器跑的脚本取脚本的名字（去掉扩展名）。
func jobName(args []string) string {
	if len(args) == 0 {
		return "?"
	}
	name := strings.TrimPrefix(filepath.Base(args[0]), "-")
	switch name {
	case "node", "bun", "deno", "python", "python3", "ruby":
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "-") || arg == "run" {
				continue
			}
			base := filepath.Base(arg)
			return strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
	return name
}
