package git

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
)

// 字符串夹具：主、分支、分离、bare、带原因的 locked、prunable、带空格的路径；换行和 -z 两种写法一样解析。
func TestWorktreesParse(t *testing.T) {
	text := `worktree /code/atlas
HEAD 1111111111111111111111111111111111111111
branch refs/heads/main

worktree /code/atlas fix io
HEAD 2222222222222222222222222222222222222222
branch refs/heads/wt/fix-io
locked 在 U 盘上

worktree /code/atlas-demo
HEAD 3333333333333333333333333333333333333333
detached
locked

worktree /code/atlas-gone
HEAD 4444444444444444444444444444444444444444
branch refs/heads/wt/gone
prunable gitdir file points to non-existent location
something-new 以后的版本加的键

worktree /code/bare.git
bare
`
	want := []Worktree{
		{Path: "/code/atlas", Head: strings.Repeat("1", 40), Branch: "main", Main: true},
		{Path: "/code/atlas fix io", Head: strings.Repeat("2", 40), Branch: "wt/fix-io", Locked: true, LockReason: "在 U 盘上"},
		{Path: "/code/atlas-demo", Head: strings.Repeat("3", 40), Detached: true, Locked: true},
		{Path: "/code/atlas-gone", Head: strings.Repeat("4", 40), Branch: "wt/gone", Prunable: true, PruneReason: "gitdir file points to non-existent location"},
		{Path: "/code/bare.git", Bare: true},
	}
	if got := ParseWorktrees([]byte(text)); !reflect.DeepEqual(got, want) {
		t.Errorf("换行写法：\n got %+v\nwant %+v", got, want)
	}
	z := strings.ReplaceAll(text, "\n", "\x00")
	if got := ParseWorktrees([]byte(z)); !reflect.DeepEqual(got, want) {
		t.Errorf("-z 写法：\n got %+v\nwant %+v", got, want)
	}
	if got := ParseWorktrees(nil); len(got) != 0 {
		t.Errorf("空输出：%+v", got)
	}
	if want[1].Name() != "wt/fix-io" || want[2].Name() != "atlas-demo" {
		t.Errorf("Name：%q %q", want[1].Name(), want[2].Name())
	}
}

// wtRepo 是一个有一次提交的临时仓库，和它旁边放工作树的目录。
func wtRepo(t *testing.T) (*testutil.Repo, string) {
	t.Helper()
	r := testutil.NewRepo(t, "main")
	r.Write("a.txt", "一\n")
	r.Commit("初始")
	return r, t.TempDir()
}

// 真的 git worktree add 三个（分支、--detach、锁定带原因），再删掉一个的目录 → 它可清理。路径经过符号链接
// （临时目录 /var → /private/var）也认得出是同一个。
func TestWorktreesLive(t *testing.T) {
	r, dir := wtRepo(t)
	feat, det, locked, gone := filepath.Join(dir, "main-feat"), filepath.Join(dir, "main detached"), filepath.Join(dir, "main-locked"), filepath.Join(dir, "main-gone")
	r.Git("worktree", "add", "-q", "-b", "feat", feat)
	r.Git("worktree", "add", "-q", "--detach", det)
	r.Git("worktree", "add", "-q", "-b", "lk", locked)
	r.Git("worktree", "lock", "--reason", "在 U 盘上", locked)
	r.Git("worktree", "add", "-q", "-b", "gone", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{r.Root, feat} { // 从主仓库和从链接的工作树列出来一样
		wts, err := Worktrees(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		if len(wts) != 5 {
			t.Fatalf("从 %s 列出 %d 个：%+v", root, len(wts), wts)
		}
		head := r.Git("rev-parse", "HEAD")
		find := func(path, branch string) Worktree { // 链接的那些 git 按路径排，不按建的先后
			t.Helper()
			for _, w := range wts {
				if SamePath(w.Path, path) {
					if w.Branch != branch || w.Head != head || w.Main != (path == r.Root) {
						t.Errorf("%s：%+v，分支要 %q", path, w, branch)
					}
					return w
				}
			}
			t.Errorf("列表里没有 %s：%+v", path, wts)
			return Worktree{}
		}
		if !SamePath(wts[0].Path, r.Root) {
			t.Errorf("第一个不是主的：%+v", wts[0])
		}
		find(r.Root, "main")
		f, d, l, g := find(feat, "feat"), find(det, ""), find(locked, "lk"), find(gone, "gone")
		if !d.Detached || f.Detached {
			t.Errorf("分离 HEAD：%v %v", d.Detached, f.Detached)
		}
		if !l.Locked || l.LockReason != "在 U 盘上" || f.Locked {
			t.Errorf("锁定：%+v", l)
		}
		if !g.Prunable || g.PruneReason == "" || f.Prunable {
			t.Errorf("可清理：%+v", g)
		}
	}
	if list, err := PruneList(context.Background(), r.Root); err != nil || len(list) != 1 || !strings.Contains(list[0], "main-gone") {
		t.Errorf("会清理的：%q %v（锁着的不算）", list, err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, ".git", "worktrees", "main-gone")); err != nil {
		t.Errorf("--dry-run 不该清：%v", err)
	}
	times := CommitTimes(context.Background(), r.Root, []string{r.Git("rev-parse", "HEAD"), "0000000000000000000000000000000000000000"})
	if len(times) != 0 { // 有一个读不出来，整条失败：一个都不给（调用方按没有时间排）
		t.Logf("带一个不存在的哈希：%v", times)
	}
	if times := CommitTimes(context.Background(), r.Root, []string{r.Git("rev-parse", "HEAD")}); len(times) != 1 {
		t.Errorf("提交时间：%v", times)
	}
}

// %(worktreepath)：检出在别的工作树里的分支知道在哪，CheckedOutElsewhere 只对它们为真。
func TestBranchesWorktreePath(t *testing.T) {
	r, dir := wtRepo(t)
	feat := filepath.Join(dir, "main-feat")
	r.Git("worktree", "add", "-q", "-b", "feat", feat)
	r.Git("branch", "free")
	bs, err := Branches(context.Background(), r.Root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Branch{}
	for _, b := range bs {
		got[b.Name] = b
	}
	if b := got["main"]; !SamePath(b.Worktree, r.Root) || b.CheckedOutElsewhere(r.Root) {
		t.Errorf("main：%+v", b)
	}
	if b := got["feat"]; !SamePath(b.Worktree, feat) || !b.CheckedOutElsewhere(r.Root) || b.CheckedOutElsewhere(feat) {
		t.Errorf("feat：%+v", b)
	}
	if b := got["free"]; b.Worktree != "" || b.CheckedOutElsewhere(r.Root) {
		t.Errorf("free：%+v", b)
	}
}

// 工作树里的改动数和 git status --porcelain 的行数一样（改名算一条，未跟踪的目录算一条）。
func TestWorktreeDirty(t *testing.T) {
	r, dir := wtRepo(t)
	wt := filepath.Join(dir, "main-feat")
	r.Git("worktree", "add", "-q", "-b", "feat", wt)
	if n, err := WorktreeDirty(context.Background(), wt); err != nil || n != 0 {
		t.Fatalf("干净的：%d %v", n, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("改\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"new.txt", "dir/x.txt", "dir/y.txt"} {
		p := filepath.Join(wt, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.Git("-C", wt, "mv", "a.txt", "b.txt")
	n, err := WorktreeDirty(context.Background(), wt)
	want := len(strings.Split(testutil.GitOut(t, wt, "status", "--porcelain"), "\n"))
	if err != nil || n != want || n != 3 {
		t.Errorf("改动数 %d（%v），git status --porcelain %d 行", n, err, want)
	}
	if _, err := WorktreeDirty(context.Background(), filepath.Join(dir, "nope")); err == nil {
		t.Error("不在的目录应报错")
	}
}

// 工作树里提交两次、主仓库提交一次：领先 2、落后 1；在主仓库里算（两个哈希在同一个对象库）。
func TestAheadBehind(t *testing.T) {
	r, dir := wtRepo(t)
	wt := filepath.Join(dir, "main-feat")
	r.Git("worktree", "add", "-q", "-b", "feat", wt)
	for i, s := range []string{"二\n", "三\n"} {
		os.WriteFile(filepath.Join(wt, "a.txt"), []byte(s), 0o644)
		r.Git("-C", wt, "commit", "-qam", "工作树 "+string(rune('1'+i)))
	}
	r.Write("m.txt", "主\n")
	r.Commit("主仓库")
	base, head := r.Git("rev-parse", "HEAD"), r.Git("-C", wt, "rev-parse", "HEAD")
	ahead, behind, err := AheadBehind(context.Background(), r.Root, base, head)
	if err != nil || ahead != 2 || behind != 1 {
		t.Errorf("领先 %d 落后 %d（%v），要 2 / 1", ahead, behind, err)
	}
	if a, b, err := AheadBehind(context.Background(), r.Root, base, base); err != nil || a != 0 || b != 0 {
		t.Errorf("同一个：%d %d %v", a, b, err)
	}
}

// SamePath / Under 解开符号链接比；Under 不含自己、不含旁边同前缀的目录。
func TestSamePathUnder(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	os.MkdirAll(filepath.Join(real, ".claude", "worktrees", "x"), 0o755)
	os.MkdirAll(filepath.Join(dir, "real-sibling"), 0o755)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if !SamePath(real, link) || !SamePath(real+"/", real) || SamePath(real, filepath.Join(dir, "real-sibling")) {
		t.Error("SamePath")
	}
	if !SamePath(filepath.Join(link, "gone", "x"), filepath.Join(real, "gone", "x")) { // 不在了的（可清理的工作树）解开上级比
		t.Error("SamePath 对不在的路径")
	}
	if got := Under(link, filepath.Join(real, ".claude", "worktrees", "x")); got != ".claude/worktrees/x" {
		t.Errorf("Under 经过链接：%q", got)
	}
	for _, p := range []string{real, filepath.Join(dir, "real-sibling"), dir} {
		if got := Under(real, p); got != "" {
			t.Errorf("Under(%s) = %q，应为空", p, got)
		}
	}
}
