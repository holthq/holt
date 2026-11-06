package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是 git 写命令的队列：每个仓库一条队列，同一个仓库里一条跑完再跑下一条，不同仓库各跑各的。
// 正在跑的可以取消（整个进程组结束，见 git.RunOp），排着的可以移出队列；取消之后重读状态，
// 留下了锁文件（index.lock 之类）就提醒一声，不删。全部抓取 / 全部拉取按 Git 栏的顺序一个仓库一个仓库地排进来。

// Op 是排队的一条会改仓库的 git 命令。
type Op struct {
	Label string // 如 "推送"
	Cmd   string // 输出栏里记的那一行，如 "$ git push"

	args   []string
	input  string                      // 不空时从标准输入喂给命令（git apply 的补丁）
	env    []string                    // 接在默认环境和凭据提示的环境后面的（只有交互式变基用，见 git.RunOpEnv）
	then   func(out string)            // 成功后在主线程上跑
	done   func(out string, err error) // 有的话由它记输出、出提示（命令面板的 git 命令、全部抓取），代替默认的
	cancel context.CancelFunc
	ask    *askOwner // 跑起来时登记的凭据提示（askpass.go）
}

// Running 是仓库里正在跑的那条，没有为 nil。
func (r *Repo) Running() *Op { return r.running }

// Queued 是仓库里叫 label 的命令在跑或在排队（防止连点两下提交排进两条）。
func (r *Repo) Queued(label string) bool {
	if r.running != nil && r.running.Label == label {
		return true
	}
	return slices.ContainsFunc(r.Queue, func(o *Op) bool { return o.Label == label })
}

// runOp 是 RunOp 们的底：排进仓库的队列，前面没有在跑的就马上跑。
// input 不空时从标准输入喂给命令（git apply 的补丁），note 跟在输出栏那条命令后面。
func (a *App) runOp(r *Repo, label, input, note string, then func(out string), args ...string) {
	a.enqueue(r, &Op{Label: label, Cmd: "$ git " + strings.Join(args, " ") + note, args: args, input: input, then: then})
}

func (a *App) enqueue(r *Repo, o *Op) {
	if r.gone() {
		return
	}
	r.Queue = append(r.Queue, o)
	a.nextOp(r)
}

// nextOp 在仓库没有在跑的命令时跑队列里的下一条。
func (a *App) nextOp(r *Repo) {
	if r.running != nil || len(r.Queue) == 0 || r.gone() {
		return
	}
	o := r.Queue[0]
	r.Queue = slices.Delete(r.Queue, 0, 1)
	ctx, cancel := context.WithCancel(r.ctx)
	r.running, r.Busy, o.cancel = o, o.Label, cancel
	up := a.Update
	o.ask = a.askRegister(r, r.Name, up)
	env := append(askEnv(o.ask), o.env...) // 交互式变基的 GIT_SEQUENCE_EDITOR 之类接在后面
	go func() {
		out, err := git.RunOpKeep(ctx, r.Root, o.input, env, o.ask.keep, o.args...)
		cancel()
		var locks []string
		if errors.Is(err, git.ErrCanceled) {
			locks = git.StaleLocks(r.Root)
		}
		up(func() {
			a.askDone(o.ask)
			a.opDone(r, o, out, err, locks)
		})
	}()
}

// opDone 是一条命令跑完（主线程）：记输出、出提示，重读状态，跑下一条。
func (a *App) opDone(r *Repo, o *Op, out string, err error, locks []string) {
	if r.running == o {
		r.running, r.Busy = nil, ""
	}
	if o.ask != nil && o.ask.canceled && err != nil { // 凭据框里点了取消：git 是没拿到凭据失败的，当作取消
		err = git.ErrCanceled
	}
	canceled := errors.Is(err, git.ErrCanceled)
	if r.gone() { // 切了工作区或关了窗口：只在输出栏记一笔
		if o.done != nil {
			o.done(out, err)
		} else {
			a.Log(r, o.Cmd, canceledText(out, o.Label, locks), canceled || err != nil)
		}
		return
	}
	switch {
	case o.done != nil:
		o.done(out, err)
		if canceled && len(locks) > 0 {
			a.Fail(i18n.T("取消后留下了锁文件"), humanErr(locksText(locks)))
		}
	case canceled:
		a.Log(r, o.Cmd, canceledText(out, o.Label, locks), true)
		if len(locks) > 0 {
			a.Fail(i18n.T("取消后留下了锁文件"), humanErr(locksText(locks)))
		} else {
			a.Toast = i18n.Tf("已取消：%s", o.Label)
		}
	case err != nil:
		a.Log(r, o.Cmd, logText(out, err), true)
		a.Fail(i18n.Tf("%s失败", o.Label), err)
	default:
		a.Log(r, o.Cmd, logText(out, nil), false)
		a.Toast = i18n.Tf("%s完成", o.Label)
		if o.then != nil {
			o.then(out)
		}
	}
	a.RefreshRepo(r, WhyOp)
	a.nextOp(r)
	a.writesStopped()
}

// canceledText 是取消了的命令在输出栏里的正文：命令已经输出的、"已取消：…"、留下的锁文件。
func canceledText(out, label string, locks []string) string {
	text := strings.TrimRight(out, "\n")
	if text != "" {
		text += "\n"
	}
	text += i18n.Tf("已取消：%s", label)
	if len(locks) > 0 {
		text += "\n" + locksText(locks)
	}
	return text
}

// locksText 是取消后留下锁文件的提醒。
func locksText(locks []string) string {
	return i18n.Tf("仓库里有锁文件：%s。如果确定没有别的 git 在跑，手动删掉它再继续；Holt 不会替你删。",
		i18n.List(locks))
}

// CancelOp 取消仓库里正在跑的命令（git 的整个进程组都结束）；跑完的回调照常走，记"已取消"并重读状态。
func (a *App) CancelOp(r *Repo) {
	if o := r.running; o != nil && o.cancel != nil {
		o.cancel()
	}
}

// Dequeue 把仓库队列里第 i 条（还没跑的）移出队列。
func (a *App) Dequeue(r *Repo, i int) {
	if i < 0 || i >= len(r.Queue) {
		return
	}
	o := r.Queue[i]
	r.Queue = slices.Delete(r.Queue, i, i+1)
	if o.done != nil {
		o.done("", git.ErrCanceled)
	} else {
		a.Log(r, o.Cmd, i18n.Tf("已移出队列：%s", o.Label), false)
	}
}

// WritesRunning 是还在跑或在排队的写命令，"仓库：名字"（克隆是"克隆 目录名"），退出和切工作区前问用。
func (a *App) WritesRunning() []string {
	var ops []string
	for _, r := range a.Repos {
		if o := r.running; o != nil {
			ops = append(ops, r.Name+"："+o.Label)
		}
		for _, o := range r.Queue {
			ops = append(ops, r.Name+"："+o.Label)
		}
	}
	for _, cl := range a.Clones {
		ops = append(ops, i18n.Tf("克隆 %s", cl.Name))
	}
	return ops
}

// gone 是仓库已经不在用了（移除、切工作区、关窗口）：它的读命令和写命令都取消了。
func (r *Repo) gone() bool { return r.ctx.Err() != nil }

// close 取消仓库所有在跑的 git 命令（读的和写的）、清空队列、关掉它的终端和监听。
func (r *Repo) close() {
	queued := r.Queue
	r.Queue = nil
	r.stop()
	for _, o := range queued {
		if o.done != nil { // 全部抓取里排着的：算没跑，接着排下一个（下一个多半也关了，跳过）
			o.done("", git.ErrCanceled)
		}
	}
	r.closeTerms()
}

// Batch 是正在跑的全部抓取 / 全部拉取：按 Git 栏的顺序一个仓库跑完再排下一个，失败的不挡后面的。
type Batch struct {
	Label   string // "抓取" / "拉取"
	Total   int
	Done    int   // 跑完（或跳过）的仓库数
	Cur     *Repo // 正在跑（或排着）的那个仓库
	OK      int
	Failed  []string // 失败的仓库名
	Skipped []string // 跳过的仓库名（拉取时没有上游的）
	Stopped bool     // 用户点了停止：后面的不跑了

	repos []*Repo
	args  func(r *Repo) (args []string, skip string)
	op    *Op
}

// FetchAll 是全部抓取：每个仓库 git fetch --prune。
func (a *App) FetchAll() {
	a.runAll(i18n.T("抓取"), func(*Repo) ([]string, string) { return []string{"fetch", "--prune"}, "" })
}

// PullAll 是全部拉取：每个有上游分支的仓库按设置里的拉取方式拉（和单个仓库的拉取一样，默认 --ff-only）；
// 没有上游的跳过。不自动贮藏：有冲突的改动时 git 自己会拒绝，算失败。
func (a *App) PullAll() {
	a.runAll(i18n.T("拉取"), func(r *Repo) ([]string, string) {
		if Upstream(r) == "" {
			return nil, i18n.T("没有上游分支，跳过")
		}
		return PullArgs(a.St.Settings.PullMode), ""
	})
}

func (a *App) runAll(label string, args func(r *Repo) ([]string, string)) {
	if a.Batch != nil {
		a.Toast = i18n.Tf("正在全部%s（%d/%d）", a.Batch.Label, a.Batch.Done+1, a.Batch.Total)
		return
	}
	repos := slices.DeleteFunc(slices.Clone(a.Repos), func(r *Repo) bool { return r.Plain }) // 普通文件夹没有远程
	if len(repos) == 0 {
		return
	}
	b := &Batch{Label: label, Total: len(repos), repos: repos, args: args}
	a.Batch = b
	a.batchNext(b)
}

// batchNext 排下一个仓库；都跑完了出汇总的 toast。
func (a *App) batchNext(b *Batch) {
	for b.Done < b.Total {
		r := b.repos[b.Done]
		var args []string
		skip := ""
		switch {
		case b.Stopped:
			a.batchEnd(b)
			return
		case r.gone() || !slices.Contains(a.Repos, r):
			skip = i18n.T("已经不在列表里，跳过")
		default:
			args, skip = b.args(r)
		}
		if skip != "" {
			a.Log(r, i18n.Tf("全部%s", b.Label), skip, false)
			b.Skipped = append(b.Skipped, r.Name)
			b.Done++
			continue
		}
		b.Cur = r
		o := &Op{Label: b.Label, Cmd: "$ git " + strings.Join(args, " "), args: args}
		o.done = func(out string, err error) {
			switch {
			case errors.Is(err, git.ErrCanceled):
				a.Log(r, o.Cmd, canceledText(out, o.Label, nil), true)
				b.Skipped = append(b.Skipped, r.Name)
			case err != nil:
				a.Log(r, o.Cmd, logText(out, err), true)
				b.Failed = append(b.Failed, r.Name)
			default:
				a.Log(r, o.Cmd, logText(out, nil), false)
				b.OK++
			}
			b.Done++
			b.Cur, b.op = nil, nil
			a.batchNext(b)
		}
		b.op = o
		a.enqueue(r, o)
		return
	}
	a.batchEnd(b)
}

func (a *App) batchEnd(b *Batch) {
	if a.Batch == b {
		a.Batch = nil
	}
	head := i18n.Tf("全部%s完成", b.Label)
	if b.Stopped {
		head = i18n.Tf("全部%s已停止", b.Label)
	}
	msg := head + i18n.Tf("：成功 %d 个", b.OK)
	if n := len(b.Failed); n > 0 {
		msg += i18n.Tf("，失败 %d 个（%s）", n, i18n.List(b.Failed))
	}
	if n := len(b.Skipped); n > 0 {
		msg += i18n.Tf("，跳过 %d 个", n)
	}
	if rest := b.Total - b.Done; rest > 0 {
		msg += i18n.Tf("，没跑 %d 个", rest)
	}
	a.Toast = msg
}

// StopBatch 停止全部抓取 / 全部拉取：正在跑的那个仓库取消，后面的不跑了。
func (a *App) StopBatch() {
	b := a.Batch
	if b == nil {
		return
	}
	b.Stopped = true
	r, o := b.Cur, b.op
	switch {
	case r == nil || o == nil:
		a.batchNext(b)
	case r.running == o:
		a.CancelOp(r) // 跑完的回调接着走 batchNext，看到 Stopped 就收尾
	default:
		if i := slices.Index(r.Queue, o); i >= 0 {
			a.Dequeue(r, i)
		}
	}
}
