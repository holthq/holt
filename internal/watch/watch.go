// Package watch 盯着一个仓库：工作区里除了 .git、node_modules 和被 git 忽略的目录都看，.git 里只看
// HEAD、index、packed-refs 这些、refs/ 和 worktrees/<名字>（objects、logs 等不看）。变化合并 300 ms（一直在变时最多 1 s）
// 之后报一次，由 app 统一重读状态。
//
// 用 mygo.WatchFiles 的 HintsOnly 模式：不留快照、不 stat 文件，只知道哪个目录里有东西变了。
// 这就够了，因为变了之后反正要重读 git status、文件列表和打开的文件；macOS 上整棵树只占一个 FSEvents
// 流，几十万个文件的仓库也不会碰到条目上限。
package watch

import (
	"context"
	"errors"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 合并时间：最后一次变化之后等 Debounce 再报，一直在变时离第一次变化最多 MaxDelay 就报。
const (
	Debounce = 300 * time.Millisecond
	MaxDelay = time.Second
)

// ignoredTTL 之后再有变化，就重读一次被忽略的目录（.gitignore 可能改了、新建了 dist/ 之类）。
const ignoredTTL = 30 * time.Second

// ErrGone 是仓库目录（或它的 git 目录）被删掉、移走了。
var ErrGone = errors.New("目录不见了")

// MaxDirs 是一批变化最多记几个目录，再多就当 Full。
const MaxDirs = 200

// Change 是合并过的一批变化。
type Change struct {
	Git   bool // .git 里变了：提交、暂存、切分支、拉取……
	Files bool // 工作区里的文件变了
	// Full 是工作区里说不清哪里变了：根下的文件（系统只给 "."）、系统丢了事件要全看、目录超过 MaxDirs 个。
	Full bool
	// Dirs 是 Files 且不 Full 时变了的目录（仓库内路径，去重，祖先在就不记子孙）：这个目录里、或下面
	// 哪一层有东西变了（系统把同一批里的子目录并到祖先上，所以只能当"这棵子树"看）。
	Dirs []string
}

// add 把 d 并进 c：路径取并集，祖先盖住子孙，超过 MaxDirs 个或有一边 Full 就 Full、路径清空。
func (c *Change) add(d Change) {
	c.Git, c.Files, c.Full = c.Git || d.Git, c.Files || d.Files, c.Full || d.Full
	for _, p := range d.Dirs {
		if c.Full {
			break
		}
		if slices.ContainsFunc(c.Dirs, func(q string) bool { return under(p, q) }) {
			continue
		}
		c.Dirs = slices.DeleteFunc(c.Dirs, func(q string) bool { return under(q, p) })
		c.Dirs = append(c.Dirs, p)
		c.Full = len(c.Dirs) > MaxDirs
	}
	if c.Full {
		c.Dirs = nil
	}
}

// under 是 p 就是目录 dir 或在它下面（仓库内路径）。
func under(p, dir string) bool {
	return p == dir || len(p) > len(dir) && p[len(dir)] == '/' && strings.HasPrefix(p, dir)
}

func (c Change) any() bool { return c.Git || c.Files }

// String 是给输出栏看的原因。
func (c Change) String() string {
	switch {
	case c.Git && c.Files:
		return i18n.T("文件和 .git 变了")
	case c.Git:
		return i18n.T(".git 变了")
	default:
		return i18n.T("文件变了")
	}
}

// Watcher 是一个仓库的监听。Close 可在任何 goroutine 调。
type Watcher struct {
	root     string
	ctx      context.Context
	cancel   context.CancelFunc
	events   chan Change
	ignored  atomic.Pointer[map[string]bool]
	loadedAt time.Time // 只有合并的 goroutine 读写
	stopOnce sync.Once
	onStop   func(error)
	wg       sync.WaitGroup
	plain    bool // 普通文件夹（StartPlain）：没有被忽略的目录要读
}

// Start 开始监听仓库 root。变化合并之后在监听的 goroutine 上调 onChange（调用方自己回主线程）；
// 监听停了（目录没了、系统出错）在监听的 goroutine 上调一次 onStop，之后不再调 onChange。
// Close 停掉的不调 onStop。
func Start(root string, onChange func(Change), onStop func(error)) (*Watcher, error) {
	gitDir, commonDir, err := git.GitDirs(root)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{root: root, ctx: ctx, cancel: cancel, events: make(chan Change, 16), onStop: onStop}
	w.loadIgnored()

	// 工作区；.git 是 root 下的目录时 HEAD、index、refs 也在这个监听里
	inRoot := func(dir string) bool { return within(root, dir) }
	gitHere := inRoot(gitDir)
	fws := []*mygo.FileWatcher{}
	start := func(dir string, ignore func(string, bool) bool, classify func(string) Change) error {
		fw, err := mygo.WatchFiles(ctx, dir, mygo.FileWatchOptions{Recursive: true, HintsOnly: true, Ignore: ignore})
		if err != nil {
			return err
		}
		fws = append(fws, fw)
		w.wg.Add(1)
		go w.read(fw, classify)
		return nil
	}
	err = start(root, func(p string, _ bool) bool { return w.ignoreWork(p, gitHere) }, workChange)
	// git 目录不在 root 下（链接的工作树）：另外看它（HEAD、index），公共目录也不在时再看公共目录（refs）
	gitChange := func(string) Change { return Change{Git: true} }
	if err == nil && !gitHere {
		err = start(gitDir, ignoreGitDir, gitChange)
	}
	if err == nil && !inRoot(commonDir) && !within(gitDir, commonDir) {
		err = start(commonDir, ignoreGitDir, gitChange)
	}
	if err != nil {
		cancel()
		for _, fw := range fws {
			fw.Close()
		}
		w.wg.Wait()
		return nil, err
	}
	w.wg.Add(1)
	go w.merge(onChange)
	return w, nil
}

// StartPlain 和 Start 一样，但 root 是个不在 Git 仓库里的普通文件夹：整个目录都看，除了 node_modules 和
// 点开头的目录；没有 .gitignore 可读。根下新出现的 .git（git init 了）也报，调用方据此把它当成仓库重读。
func StartPlain(root string, onChange func(Change), onStop func(error)) (*Watcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{root: root, ctx: ctx, cancel: cancel, events: make(chan Change, 16), onStop: onStop, plain: true}
	w.ignored.Store(&map[string]bool{})
	fw, err := mygo.WatchFiles(ctx, root, mygo.FileWatchOptions{Recursive: true, HintsOnly: true, Ignore: ignorePlain})
	if err != nil {
		cancel()
		return nil, err
	}
	w.wg.Add(2)
	go w.read(fw, workChange)
	go w.merge(onChange)
	return w, nil
}

// StartTree 只看仓库 root 的工作区（被 git 忽略的目录、node_modules 照样不看），不看它的 git 目录，一个 FSEvents 流：
// Git 栏"工作树"块展开时给链接的工作树用——它的 HEAD、index 在主仓库的 .git/worktrees/<名字> 下，
// refs 在主仓库的 .git 里，主仓库自己的监听已经在看。
func StartTree(root string, onChange func(Change), onStop func(error)) (*Watcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{root: root, ctx: ctx, cancel: cancel, events: make(chan Change, 16), onStop: onStop}
	w.loadIgnored()
	fw, err := mygo.WatchFiles(ctx, root, mygo.FileWatchOptions{Recursive: true, HintsOnly: true,
		Ignore: func(p string, _ bool) bool {
			return p == ".git" || strings.HasPrefix(p, ".git/") || w.ignoreWork(p, false) // 主工作树的 .git 是目录，也不看
		}})
	if err != nil {
		cancel()
		return nil, err
	}
	w.wg.Add(2)
	go w.read(fw, workChange)
	go w.merge(onChange)
	return w, nil
}

// Close 停掉监听，等它的 goroutine 都退出。
func (w *Watcher) Close() {
	w.stopOnce.Do(func() {})
	w.cancel()
	w.wg.Wait()
}

// stop 是监听自己停了：只报一次，然后把别的监听也停掉。
func (w *Watcher) stop(err error) {
	w.stopOnce.Do(func() {
		if w.onStop != nil {
			w.onStop(err)
		}
	})
	w.cancel()
}

// read 把一个 FileWatcher 的提示归类后交给 merge。
func (w *Watcher) read(fw *mygo.FileWatcher, classify func(string) Change) {
	defer w.wg.Done()
	defer fw.Close()
	for {
		evs, err := fw.Next(w.ctx)
		if err != nil {
			if w.ctx.Err() != nil {
				return
			}
			if errors.Is(err, io.EOF) {
				err = ErrGone
			}
			w.stop(err)
			return
		}
		var c Change
		for _, e := range evs {
			if e.Op == mygo.FileRemove && e.Path == "." {
				continue // 目录没了：下一次 Next 返回 io.EOF
			}
			c.add(classify(e.Path))
		}
		if !c.any() {
			continue
		}
		select {
		case w.events <- c:
		case <-w.ctx.Done():
			return
		}
	}
}

// merge 合并变化：最后一次之后等 Debounce，一直在变时离第一次最多 MaxDelay。
func (w *Watcher) merge(onChange func(Change)) {
	defer w.wg.Done()
	var pending Change
	var first time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case c := <-w.events:
			if !pending.any() {
				first = time.Now()
			}
			pending.add(c)
			timer.Reset(max(min(Debounce, MaxDelay-time.Since(first)), 0))
		case <-timer.C:
			if w.ctx.Err() != nil {
				return
			}
			c := pending
			pending = Change{}
			onChange(c)
			if !w.plain && time.Since(w.loadedAt) > ignoredTTL {
				w.loadIgnored()
			}
		}
	}
}

// loadIgnored 重读被 git 忽略的目录；读不出来就沿用上一次的。
func (w *Watcher) loadIgnored() {
	w.loadedAt = time.Now()
	dirs, err := git.Ignored(w.root)
	if err != nil {
		if w.ignored.Load() == nil {
			w.ignored.Store(&map[string]bool{})
		}
		return
	}
	w.ignored.Store(&dirs)
}

// ignoreWork 是工作区监听不看的目录（p 相对仓库根）。gitHere 为 .git 是根下的目录：看它的
// HEAD、index、refs/，别的子目录（objects、logs、hooks……）不看。
func (w *Watcher) ignoreWork(p string, gitHere bool) bool {
	if p == ".git" {
		return !gitHere
	}
	if rest, ok := strings.CutPrefix(p, ".git/"); ok {
		return ignoreGitDir(rest, true)
	}
	switch path.Base(p) {
	case ".git", "node_modules": // 嵌套的仓库；没被忽略的 node_modules 也太大
		return true
	}
	return (*w.ignored.Load())[p]
}

// ignorePlain 是普通文件夹的监听不看的目录（p 相对根）：node_modules 和点开头的，但根下的 .git 要看
// （和仓库一样只看 refs/），git init 了好知道。
func ignorePlain(p string, isDir bool) bool {
	if p == ".git" {
		return false
	}
	if rest, ok := strings.CutPrefix(p, ".git/"); ok {
		return ignoreGitDir(rest, isDir)
	}
	name := path.Base(p)
	return isDir && (name == "node_modules" || strings.HasPrefix(name, "."))
}

// ignoreGitDir 是 git 目录里不看的子目录（p 相对 git 目录）：只看 refs/，和 worktrees/ 下一层
// （链接的工作树各自的 HEAD、index：新建 / 移除工作树、在工作树里切分支，主仓库的工作树列表要跟着变）。
func ignoreGitDir(p string, _ bool) bool {
	if p == "refs" || strings.HasPrefix(p, "refs/") || p == "worktrees" {
		return false
	}
	if rest, ok := strings.CutPrefix(p, "worktrees/"); ok {
		return strings.Contains(rest, "/")
	}
	return true
}

// workChange 是工作区监听里目录 p 的提示算哪种变化。"." 可能是根下的文件变了，也可能是系统丢了事件、
// 要全看一遍，两种都算、工作区按 Full。
func workChange(p string) Change {
	switch {
	case p == ".":
		return Change{Git: true, Files: true, Full: true}
	case p == ".git" || strings.HasPrefix(p, ".git/"):
		return Change{Git: true}
	}
	return Change{Files: true, Dirs: []string{p}}
}

// within 是 dir 在 root 下（或就是 root），符号链接解开了比（临时目录 /var → /private/var）。
func within(root, dir string) bool {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
