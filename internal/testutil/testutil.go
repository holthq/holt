// Package testutil 是测试共用的辅助：临时仓库构造器、后台回调队列、推帧等待。
//
// 会改仓库的操作只在这里建的临时仓库上测：用户的 5 个仓库从头到尾只读。
package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// DefaultWorkspace 是真实工作区（环境变量 WORKBENCH_TEST_WS）：只读测试（status / diff / log、截图）用它，没设就跳过。
var DefaultWorkspace = os.Getenv("WORKBENCH_TEST_WS")

// gitEnv 让临时仓库里的 git 不读本机配置，作者固定。
func gitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=测试", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=测试", "GIT_COMMITTER_EMAIL=t@example.com")
}

// Budget 是验收里的时长预算：-race 下整条链路慢一两倍，预算放宽到 3 倍；平常就是 d。
func Budget(d time.Duration) time.Duration {
	if Race {
		return 3 * d
	}
	return d
}

// Repo 是测试里的一个临时仓库。
type Repo struct {
	t    testing.TB
	Root string
}

// NewRepo 在临时目录下建一个空仓库 name（main 分支，作者信息写进仓库自己的配置，
// 因为应用里的 commit 用的是这台机器的环境）。
func NewRepo(t testing.TB, name string) *Repo {
	t.Helper()
	r := &Repo{t: t, Root: filepath.Join(t.TempDir(), name)}
	if err := os.MkdirAll(r.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "user.name", "测试")
	r.Git("config", "user.email", "t@example.com")
	r.Git("config", "commit.gpgsign", "false")
	return r
}

// Git 在仓库里跑 git，失败就 Fatal，返回去掉首尾空白的输出。
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	return r.gitWith(nil, args...)
}

// gitWith 同 Git，多加几个环境变量。
func (r *Repo) gitWith(env []string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	cmd.Env = append(gitEnv(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// In 是同一个测试里的另一个目录（如 git worktree add 出来的工作树），Git / Write / Commit 在那里跑。
func (r *Repo) In(dir string) *Repo { return &Repo{t: r.t, Root: dir} }

// Write 写仓库里的文件（需要时建目录）。
func (r *Repo) Write(name, content string) {
	r.t.Helper()
	p := filepath.Join(r.Root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Commit 暂存全部并提交。
func (r *Repo) Commit(msg string) {
	r.t.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-q", "-m", msg)
}

// SubmoduleRepo 建一个有两个子模块的临时仓库 name：lib 是另一个临时仓库（两次提交，a.txt、b.txt），
// vendor/lib 初始化了、检出着记录的那次，"vendor/lib 2"（路径里有空格）登记了但 deinit 了（没初始化，留着空目录）。
// 子模块从本机路径克隆，要 -c protocol.file.allow=always（git 2.38 起默认不让）。
func SubmoduleRepo(t testing.TB, name string) (main, lib *Repo) {
	t.Helper()
	lib = NewRepo(t, "lib")
	lib.Write("a.txt", "a\n")
	lib.Commit("a")
	lib.Write("b.txt", "b\n")
	lib.Commit("b")
	main = NewRepo(t, name)
	main.Write("README.md", "main\n")
	main.Commit("初始")
	main.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", lib.Root, "vendor/lib")
	main.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", lib.Root, "vendor/lib 2")
	main.Commit("加子模块")
	main.Git("submodule", "deinit", "-q", "--", "vendor/lib 2")
	return main, lib
}

// ConflictRepo 建一个合并到一半、file 有冲突的临时仓库 name：提交 base，feature 分支改成 theirs、main 改成 ours，
// 再在 main 上 git merge feature（应该失败）。theirs 为空串是 feature 上删掉这个文件。git 不读本机配置，
// 冲突标记是默认的样子（<<<<<<< HEAD … >>>>>>> feature，没有 diff3 那段）。
func ConflictRepo(t testing.TB, name, file, base, ours, theirs string) *Repo {
	t.Helper()
	r := NewRepo(t, name)
	r.Write(file, base)
	r.Commit("初始提交")
	r.Git("switch", "-q", "-c", "feature")
	if theirs == "" {
		r.Git("rm", "-q", file)
	} else {
		r.Write(file, theirs)
	}
	r.Commit("分支上改")
	r.Git("switch", "-q", "main")
	r.Write(file, ours)
	r.Commit("主线上改")
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = r.Root
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("git merge feature 应有冲突: %s", out)
	}
	return r
}

// GitFails 同 Git，但这条命令应该失败（停在冲突上的 merge / rebase / cherry-pick）：成功了就 Fatal。
// 带 GIT_EDITOR=true，不会等编辑器。返回输出。
func (r *Repo) GitFails(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	cmd.Env = append(gitEnv(), "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true")
	out, err := cmd.CombinedOutput()
	if err == nil {
		r.t.Fatalf("git %s 应该失败: %s", strings.Join(args, " "), out)
	}
	return string(out)
}

// RebaseRepo 建一个变基停在第 1 / 2 步冲突上的临时仓库 name：提交 base（f.txt = "1\n2\n3\n"），
// topic 分支上 "topic one"（第 2 行改成 T）、"topic two"（加 g.txt），main 上 "main change"（第 2 行改成 M），
// 再在 topic 上 git rebase main（第一个提交冲突）。
func RebaseRepo(t testing.TB, name string) *Repo {
	t.Helper()
	r := NewRepo(t, name)
	r.Write("f.txt", "1\n2\n3\n")
	r.Commit("base")
	r.Git("switch", "-q", "-c", "topic")
	r.Write("f.txt", "1\nT\n3\n")
	r.Commit("topic one")
	r.Write("g.txt", "g\n")
	r.Commit("topic two")
	r.Git("switch", "-q", "main")
	r.Write("f.txt", "1\nM\n3\n")
	r.Commit("main change")
	r.Git("switch", "-q", "topic")
	r.GitFails("rebase", "main")
	return r
}

// CommitAt 同 Commit，作者和提交时间是 when（"所有仓库"的历史按时间合并，测试要定好先后）。
func (r *Repo) CommitAt(msg string, when time.Time) {
	r.t.Helper()
	d := when.Format(time.RFC3339)
	r.Git("add", "-A")
	r.gitWith([]string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-q", "-m", msg)
}

// CommitAs 同 CommitAt，作者是 author（"名字 <邮箱>"；提交者照旧）：历史按作者搜的测试要两个人。
func (r *Repo) CommitAs(msg, author string, when time.Time) {
	r.t.Helper()
	d := when.Format(time.RFC3339)
	r.Git("add", "-A")
	r.gitWith([]string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-q", "-m", msg, "--author", author)
}

// DemoLines 是 TempRepo 里 a.txt 提交时的 40 行。
func DemoLines() []string {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("第 %d 行", i+1)
	}
	return lines
}

// TempRepo 建现有界面测试用的小仓库：main 上两个提交，feature 分支上再一个，
// 工作区里 a.txt 改了第 20 行、u.txt 未跟踪。返回仓库根和指向它的 .code-workspace。
func TempRepo(t testing.TB) (root, ws string) {
	t.Helper()
	r := NewRepo(t, "demo")
	r.Write("README.md", "# demo\n\n第一行\n")
	r.Commit("初始提交")
	lines := DemoLines()
	r.Write("a.txt", strings.Join(lines, "\n")+"\n")
	r.Commit("加 a.txt")
	r.Git("switch", "-q", "-c", "feature")
	r.Write("feat.txt", "feature\n")
	r.Commit("feature 提交")
	r.Git("switch", "-q", "main")
	lines[19] = "第 20 行（改过）"
	r.Write("a.txt", strings.Join(lines, "\n")+"\n")
	r.Write("u.txt", "未跟踪\n")
	return r.Root, Workspace(t, r.Root)
}

// ThreeRepos 建多仓库测试用的工作区：alpha、beta、gamma 三个仓库，提交按时间交错
// （从新到旧：gamma 1、alpha 3、beta 2、alpha 2、beta 1、alpha 1，相隔一小时）。
// alpha 改了 alpha.txt、notes.md 未跟踪；beta 改了 beta.go；gamma 是干净的。
func ThreeRepos(t testing.TB) (alpha, beta, gamma *Repo, ws string) {
	t.Helper()
	alpha, beta, gamma = NewRepo(t, "alpha"), NewRepo(t, "beta"), NewRepo(t, "gamma")
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	at := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	alpha.Write("alpha.txt", "一\n")
	alpha.CommitAt("alpha 1", at(0))
	beta.Write("beta.go", "package beta\n")
	beta.CommitAt("beta 1", at(1))
	alpha.Write("alpha.txt", "一\n二\n")
	alpha.CommitAt("alpha 2", at(2))
	beta.Write("beta.go", "package beta\n\nconst N = 1\n")
	beta.CommitAt("beta 2", at(3))
	alpha.Write("alpha.txt", "一\n二\n三\n")
	alpha.CommitAt("alpha 3", at(4))
	gamma.Write("gamma.md", "# gamma\n")
	gamma.CommitAt("gamma 1", at(5))
	alpha.Write("alpha.txt", "一\n二\n三（改过）\n")
	alpha.Write("notes.md", "草稿\n")
	beta.Write("beta.go", "package beta\n\nconst N = 2\n")
	return alpha, beta, gamma, Workspace(t, alpha.Root, beta.Root, gamma.Root)
}

// Workspace 在临时目录写一个列出 roots 的 .code-workspace，返回它的路径。
func Workspace(t testing.TB, roots ...string) string {
	t.Helper()
	folders := make([]map[string]string, len(roots))
	for i, r := range roots {
		folders[i] = map[string]string{"path": r}
	}
	ws := filepath.Join(t.TempDir(), "demo.code-workspace")
	data, _ := json.Marshal(map[string]any{"folders": folders})
	if err := os.WriteFile(ws, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

// GitOut 在仓库里跑一条只读 git 命令（用本机环境），返回去掉首尾空白的输出。
func GitOut(t testing.TB, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Queue 收下后台 goroutine 交回主线程的回调（应用里是 app 的 update），测试按帧跑掉。
type Queue chan func()

// NewQueue 建一个回调队列。
func NewQueue() Queue { return make(Queue, 256) }

// Post 排一个回调，可以从任何 goroutine 调。
func (q Queue) Post(fn func()) { q <- fn }

// Drain 在当前（测试主）goroutine 上跑掉排着的回调。
func (q Queue) Drain() {
	for {
		select {
		case fn := <-q:
			fn()
		default:
			return
		}
	}
}

// Waiter 返回一个等待函数：推帧、跑回调，直到 cond 成立；超时就 Fatal 并打出界面上的文字。
func Waiter(t testing.TB, tt *ui.Tester, q Queue, timeout time.Duration) func(what string, cond func() bool) {
	return func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			q.Drain()
			tt.Frame()
			if cond() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("等 %s 超时；texts=%q", what, tt.Texts())
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
}

// WaitTerm 推帧直到终端屏幕上出现 want。
func WaitTerm(t testing.TB, tt *ui.Tester, term *terminal.Terminal, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(term.Text(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("等 %q 超时，屏幕是:\n%s", want, term.Text())
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
}

// WaitTermReady 推帧直到 shell 画出了提示符：屏幕上有字，且 settle 内没再变。在这之前敲的字
// 会被终端先回显一遍、提示符出来后再画一遍（截图里多出一行）。
func WaitTermReady(t testing.TB, tt *ui.Tester, term *terminal.Terminal, settle, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last, since := "", time.Now()
	for {
		s := strings.TrimSpace(term.Text())
		if s != last {
			last, since = s, time.Now()
		} else if s != "" && time.Since(since) >= settle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等提示符超时，屏幕是:\n%s", term.Text())
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
}

// WaitTermGone 推帧直到终端屏幕上不再有 s。
func WaitTermGone(t testing.TB, tt *ui.Tester, term *terminal.Terminal, s string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for strings.Contains(term.Text(), s) {
		if time.Now().After(deadline) {
			t.Fatalf("等 %q 消失超时，屏幕是:\n%s", s, term.Text())
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
}
