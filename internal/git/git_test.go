package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 变更数必须与 `git status --porcelain` 的行数一致；分支名与 `git branch --show-current` 一致。
// 用户的真实工作区，只读。
func TestStatusMatchesPorcelain(t *testing.T) {
	roots, err := workspace.Load(testutil.DefaultWorkspace)
	if err != nil {
		t.Skipf("没有工作区文件: %v", err)
	}
	for _, root := range roots {
		st, err := ReadStatus(context.Background(), root)
		if err != nil {
			t.Errorf("%s: %v", root, err)
			continue
		}
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: git status: %v", root, err)
		}
		want := bytes.Count(out, []byte("\n"))
		branch := testutil.GitOut(t, root, "branch", "--show-current")
		t.Logf("%s: branch=%s count=%d (porcelain %d) staged=%d unstaged=%d untracked=%d", root, st.Branch, st.Count, want, st.Staged, st.Unstaged, st.Untracked)
		if st.Count != want {
			t.Errorf("%s: 变更数 %d，git status --porcelain 有 %d 行", root, st.Count, want)
		}
		if branch != "" && st.Branch != branch {
			t.Errorf("%s: 分支 %q，期望 %q", root, st.Branch, branch)
		}
		for _, ch := range st.Changes {
			dc, err := LoadDiffContent(context.Background(), root, ch)
			if err != nil {
				t.Errorf("%s: diff %s %s: %v", root, ch.Kind, ch.Path, err)
				continue
			}
			t.Logf("  %s %c %s old=%d new=%d binary=%v large=%v note=%q", ch.Kind, ch.Code, ch.Path, len(dc.Old), len(dc.New), dc.Binary, dc.TooLarge, dc.Note)
		}
	}
}

// 临时仓库上的读操作：status 分组、diff 两侧、blob、ls-files、toplevel。
func TestReadOps(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n\n改了\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.GitOut(t, root, "add", "README.md")
	st, err := ReadStatus(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || st.Count != 3 || st.Staged != 1 || st.Unstaged != 1 || st.Untracked != 1 {
		t.Fatalf("status = %+v", st)
	}
	kinds := []Kind{}
	for _, ch := range st.Changes {
		kinds = append(kinds, ch.Kind)
	}
	if !reflect.DeepEqual(kinds, []Kind{Staged, Unstaged, Untracked}) {
		t.Errorf("分组顺序 %v", kinds)
	}
	if st.Changes[0].Path != "README.md" || st.Changes[1].Path != "a.txt" || st.Changes[2].Path != "u.txt" {
		t.Errorf("路径 %+v", st.Changes)
	}
	dc, err := LoadDiffContent(context.Background(), root, st.Changes[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dc.Old), "第 20 行\n") || !strings.Contains(string(dc.New), "第 20 行（改过）") {
		t.Errorf("未暂存 diff 两侧不对")
	}
	dc, _ = LoadDiffContent(context.Background(), root, st.Changes[0])
	if string(dc.Old) != "# demo\n\n第一行\n" || string(dc.New) != "# demo\n\n改了\n" {
		t.Errorf("已暂存 diff: %q → %q", dc.Old, dc.New)
	}
	dc, _ = LoadDiffContent(context.Background(), root, st.Changes[2])
	if dc.Old != nil || string(dc.New) != "未跟踪\n" {
		t.Errorf("未跟踪 diff: %q → %q", dc.Old, dc.New)
	}
	if b := Blob(root, "HEAD", "a.txt"); !strings.HasPrefix(string(b), "第 1 行\n") {
		t.Errorf("Blob HEAD:a.txt = %q", b)
	}
	if b := Blob(root, "", "README.md"); string(b) != "# demo\n\n改了\n" {
		t.Errorf("Blob :README.md = %q", b)
	}
	if b := Blob(root, "HEAD", "没有.txt"); b != nil {
		t.Errorf("没有的文件应该是 nil")
	}
	files, err := LsFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	if !reflect.DeepEqual(files, []string{"README.md", "a.txt", "u.txt"}) {
		t.Errorf("ls-files = %v", files)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	top, err := Toplevel(filepath.Join(root, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(root)
	if got, _ := filepath.EvalSymlinks(top); got != want {
		t.Errorf("toplevel = %s，要 %s", top, root)
	}
	if _, err := Toplevel(t.TempDir()); err == nil || !strings.Contains(err.Error(), "不是 Git 仓库") {
		t.Errorf("非仓库: %v", err)
	}
}

// 历史、提交的文件和分支。
func TestLogAndBranches(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	cs, err := Log(context.Background(), root, 0, LogPage, LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Subject != "加 a.txt" || cs[1].Subject != "初始提交" {
		t.Fatalf("log = %+v", cs)
	}
	if len(cs[0].Refs) == 0 || cs[0].Refs[0] != "main" {
		t.Errorf("refs = %v", cs[0].Refs)
	}
	if cs[0].Author != "测试" || len(cs[0].Parents) != 1 || cs[0].Short != Short(cs[0].Hash) {
		t.Errorf("提交字段 %+v", cs[0])
	}
	if more, _ := Log(context.Background(), root, 1, 10, LogQuery{}); len(more) != 1 || more[0].Subject != "初始提交" {
		t.Errorf("skip 1 = %+v", more)
	}
	files, err := CommitFiles(context.Background(), root, cs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "a.txt" || files[0].Code != 'A' || files[0].Kind != InCommit || files[0].Parent != cs[1].Hash {
		t.Errorf("提交的文件 %+v", files)
	}
	if files, _ := CommitFiles(context.Background(), root, cs[1]); len(files) != 1 || files[0].Path != "README.md" || files[0].Parent != "" {
		t.Errorf("根提交的文件 %+v", files)
	}
	bs, err := Branches(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, b := range bs {
		names[b.Name] = b.Current
	}
	if cur, ok := names["main"]; !ok || !cur {
		t.Errorf("分支 %+v", bs)
	}
	if cur, ok := names["feature"]; !ok || cur {
		t.Errorf("分支 %+v", bs)
	}
	empty := testutil.NewRepo(t, "empty")
	if cs, err := Log(context.Background(), empty.Root, 0, 10, LogQuery{}); err != nil || len(cs) != 0 {
		t.Errorf("空仓库 log = %v, %v", cs, err)
	}
}

// 提交的文件带行数（--numstat）：改、加、删、重命名（路径里有空格和中文，-z 下重命名的格式不一样）、二进制，根提交也有。
func TestCommitFilesNumstat(t *testing.T) {
	r := testutil.NewRepo(t, "num")
	long := strings.Repeat("同一行\n", 10)
	r.Write("a.txt", "1\n2\n3\n")
	r.Write("del.txt", "x\ny\n")
	r.Write("old.txt", long+"尾巴\n")
	r.Write("bin.dat", "\x00\x01\x02")
	r.Commit("root")
	r.Write("a.txt", "1\n二\n3\n")
	r.Write("new.txt", "n1\nn2\n")
	r.Git("rm", "-q", "del.txt")
	r.Git("rm", "-q", "old.txt")
	r.Write("目录 一/新 名.txt", long+"新尾巴\n")
	r.Write("bin.dat", "\x00\x03")
	r.Commit("second")
	cs, err := Log(context.Background(), r.Root, 0, 10, LogQuery{})
	if err != nil || len(cs) != 2 {
		t.Fatalf("log %v %v", cs, err)
	}
	files, err := CommitFiles(context.Background(), r.Root, cs[0])
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Code     byte
		Orig     string
		Add, Del int
		Binary   bool
	}
	got := map[string]row{}
	for _, f := range files {
		got[f.Path] = row{f.Code, f.Orig, f.Add, f.Del, f.Binary}
	}
	want := map[string]row{
		"a.txt":        {'M', "", 1, 1, false},
		"new.txt":      {'A', "", 2, 0, false},
		"del.txt":      {'D', "", 0, 2, false},
		"目录 一/新 名.txt": {'R', "old.txt", 1, 1, false},
		"bin.dat":      {'M', "", 0, 0, true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("行数\n got %+v\nwant %+v", got, want)
	}
	root, err := CommitFiles(context.Background(), r.Root, cs[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range root {
		if f.Path == "a.txt" && (f.Add != 3 || f.Del != 0) || f.Path == "bin.dat" && !f.Binary {
			t.Errorf("根提交的行数 %+v", f)
		}
	}
	if len(root) != 4 {
		t.Errorf("根提交 %d 个文件: %+v", len(root), root)
	}
}

// 比较任意两个提交：DiffFiles(from, to) 的 Parent 是 from、Commit 是 to，中间几个提交的改动合在一起，行数也是合起来的。
func TestDiffFiles(t *testing.T) {
	r := testutil.NewRepo(t, "cmp")
	r.Write("a.txt", "1\n2\n3\n")
	r.Commit("one")
	from := r.Git("rev-parse", "HEAD")
	r.Write("a.txt", "1\n二\n3\n")
	r.Commit("two")
	r.Write("b.txt", "b\n")
	r.Write("a.txt", "1\n二\n3\n4\n")
	r.Commit("three")
	to := r.Git("rev-parse", "HEAD")
	files, err := DiffFiles(context.Background(), r.Root, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("文件 %+v", files)
	}
	for _, f := range files {
		if f.Kind != InCommit || f.Commit != to || f.Parent != from {
			t.Errorf("Commit / Parent 不对 %+v", f)
		}
		switch f.Path {
		case "a.txt":
			if f.Code != 'M' || f.Add != 2 || f.Del != 1 {
				t.Errorf("a.txt %+v", f)
			}
		case "b.txt":
			if f.Code != 'A' || f.Add != 1 {
				t.Errorf("b.txt %+v", f)
			}
		default:
			t.Errorf("多了 %+v", f)
		}
	}
	back, _ := DiffFiles(context.Background(), r.Root, to, from)
	if len(back) != 2 || back[0].Commit != from {
		t.Errorf("反过来比 %+v", back)
	}
}

// 贮藏的文件：和贮藏时的 HEAD 比（暂存的、没暂存的都在），stash -u 存下的未跟踪文件当新加的接在后面，Commit 是第三个父提交。
func TestStashFiles(t *testing.T) {
	r := testutil.NewRepo(t, "stash")
	r.Write("a.txt", "1\n2\n")
	r.Write("b.txt", "b\n")
	r.Commit("base")
	r.Write("a.txt", "1\n二\n")
	r.Git("add", "a.txt")
	r.Write("b.txt", "b\nb2\n")
	r.Write("new.txt", "n\n")
	r.Git("stash", "push", "-q", "-u", "-m", "带未跟踪的")
	r.Write("a.txt", "only\n")
	r.Git("stash", "push", "-q", "-m", "只有 a")
	ss := ReadStashes(r.Root)
	if len(ss) != 2 {
		t.Fatalf("贮藏 %+v", ss)
	}
	files, err := StashFiles(context.Background(), r.Root, ss[1])
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, fmt.Sprintf("%c %s +%d -%d", f.Code, f.Path, f.Add, f.Del))
	}
	if want := []string{"M a.txt +1 -1", "M b.txt +1 -0", "A new.txt +1 -0"}; !slices.Equal(got, want) {
		t.Errorf("带未跟踪的贮藏: %q", got)
	}
	head := r.Git("rev-parse", "HEAD")
	third := r.Git("rev-parse", ss[1].Hash+"^3")
	if len(files) == 3 && (files[0].Commit != ss[1].Hash || files[0].Parent != head || files[2].Commit != third || files[2].Parent != "") {
		t.Errorf("Commit / Parent 不对: %+v", files)
	}
	if b, err := LoadDiffContent(context.Background(), r.Root, files[2]); err != nil || string(b.New) != "n\n" {
		t.Errorf("未跟踪文件的内容 %+v %v", b, err)
	}
	if files, _ := StashFiles(context.Background(), r.Root, ss[0]); len(files) != 1 || files[0].Path != "a.txt" {
		t.Errorf("只有 a 的贮藏: %+v", files)
	}
	if _, err := StashFiles(context.Background(), r.Root, Stash{Ref: "stash@{9}", Hash: strings.Repeat("0", 40)}); err == nil {
		t.Error("不存在的贮藏应报错")
	}
}

// 历史的筛选：按信息 / 作者 / 哈希开头找（分页）、所有分支、只看一个目录、文件跟着重命名往前找时也能搜。
func TestLogQuery(t *testing.T) {
	r := testutil.NewRepo(t, "q")
	r.Write("a.txt", "a\n")
	r.Commit("初始提交")
	for _, m := range []string{"修复 Login", "修复登出"} {
		r.Write("b.txt", m)
		r.Git("add", "-A")
		r.Git("commit", "-q", "-m", m, "--author", "张三 <z@example.com>")
	}
	r.Write("docs/.keep", "")
	r.Git("mv", "a.txt", "docs/a.txt")
	r.Commit("挪 a")
	r.Git("switch", "-q", "-c", "side")
	r.Write("s.txt", "s\n")
	r.Commit("修复侧边")
	r.Git("switch", "-q", "main")
	subjects := func(q LogQuery, skip, n int) []string {
		t.Helper()
		cs, err := Log(context.Background(), r.Root, skip, n, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			out = append(out, c.Subject)
		}
		return out
	}
	for _, tc := range []struct {
		q       LogQuery
		skip, n int
		want    []string
	}{
		{LogQuery{Search: "修复"}, 0, 10, []string{"修复登出", "修复 Login"}},
		{LogQuery{Search: "修复"}, 1, 10, []string{"修复 Login"}},
		{LogQuery{Search: "login"}, 0, 10, []string{"修复 Login"}},
		{LogQuery{Search: "张三"}, 0, 10, []string{"修复登出", "修复 Login"}},
		{LogQuery{Search: "z@example"}, 0, 1, []string{"修复登出"}},
		{LogQuery{Search: Short(r.Git("rev-parse", "HEAD~3"))}, 0, 10, []string{"初始提交"}},
		{LogQuery{Search: "修复", All: true}, 0, 10, []string{"修复侧边", "修复登出", "修复 Login"}},
		{LogQuery{Path: "docs"}, 0, 10, []string{"挪 a"}},
		{LogQuery{Path: "docs/a.txt", Follow: true}, 0, 10, []string{"挪 a", "初始提交"}},
		{LogQuery{Path: "docs/a.txt", Follow: true, Search: "初始"}, 0, 10, []string{"初始提交"}},
		{LogQuery{Path: "docs/a.txt", Follow: true, Search: "挪"}, 1, 10, nil},
		{LogQuery{Search: "没有这个"}, 0, 10, nil},
	} {
		if got := subjects(tc.q, tc.skip, tc.n); !slices.Equal(got, tc.want) {
			t.Errorf("%+v skip %d: %q，应为 %q", tc.q, tc.skip, got, tc.want)
		}
	}
}

// 查询语法拆出来的条件：作者 -F -i（有 /正则/ 时转义后跟着 -E）、after 含那一刻、before 不含；
// 临时仓库两个作者三天：各条件和组合、哈希开头也要过别的条件、跟着重命名的文件历史在这边筛。
func TestLogQueryArgs(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.Local) }
	for _, tc := range []struct {
		q    LogQuery
		want string
	}{
		{LogQuery{Search: "x"}, ""},
		{LogQuery{Authors: []string{"ada", "bob"}}, "--author=ada --author=bob -F -i"},
		{LogQuery{Search: "fix|bug", Regex: true, Authors: []string{"a.b"}}, `--author=a\.b -i`},
		{LogQuery{After: day(2)}, "--since=@" + strconv.FormatInt(day(2).Unix(), 10)},
		{LogQuery{Before: day(2)}, "--until=@" + strconv.FormatInt(day(2).Unix()-1, 10)},
	} {
		if got := strings.Join(tc.q.filterArgs(), " "); got != tc.want {
			t.Errorf("%+v：%q，应为 %q", tc.q, got, tc.want)
		}
	}
	if (LogQuery{Path: "a.txt", Paths: []string{"d"}}).Narrowed() || !(LogQuery{Paths: []string{"d"}}).Narrowed() {
		t.Error("Narrowed：文件历史时 Paths 不算")
	}

	r := testutil.NewRepo(t, "q")
	r.Write("a.txt", "1\n")
	r.CommitAs("first alpha", "Ada <ada@x.test>", day(1).Add(10*time.Hour))
	r.Write("d/b.txt", "1\n")
	r.CommitAs("second beta", "Bob <bob@x.test>", day(2).Add(10*time.Hour))
	r.Write("a.txt", "2\n")
	r.CommitAs("third alpha fix", "Ada <ada@x.test>", day(3).Add(10*time.Hour))
	r.Git("mv", "a.txt", "c.txt")
	r.CommitAs("rename", "Bob <bob@x.test>", day(4).Add(10*time.Hour))
	second := Short(r.Git("rev-parse", "HEAD~2"))
	subjects := func(q LogQuery) []string {
		t.Helper()
		cs, err := Log(context.Background(), r.Root, 0, 10, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			out = append(out, c.Subject)
		}
		return out
	}
	for _, tc := range []struct {
		q    LogQuery
		want []string
	}{
		{LogQuery{Authors: []string{"ADA"}}, []string{"third alpha fix", "first alpha"}},
		{LogQuery{Authors: []string{"ada", "bob@x"}}, []string{"rename", "third alpha fix", "second beta", "first alpha"}},
		{LogQuery{Search: "alpha", Authors: []string{"bob"}}, nil},
		{LogQuery{Search: "bob"}, []string{"rename", "second beta"}},
		{LogQuery{Search: "bob", Authors: []string{"ada"}}, nil}, // 有 author: 时裸词不再按作者找
		{LogQuery{After: day(2)}, []string{"rename", "third alpha fix", "second beta"}},
		{LogQuery{Before: day(2)}, []string{"first alpha"}},
		{LogQuery{After: day(2), Before: day(4), Search: "alpha"}, []string{"third alpha fix"}},
		{LogQuery{Paths: []string{"d"}}, []string{"second beta"}},
		{LogQuery{Paths: []string{"d", "a.txt"}}, []string{"rename", "third alpha fix", "second beta", "first alpha"}},
		{LogQuery{Search: "^(first|second)", Regex: true}, []string{"second beta", "first alpha"}},
		{LogQuery{Search: "alpha.*fix", Regex: true, Authors: []string{"ada@x.test"}}, []string{"third alpha fix"}},
		{LogQuery{Search: second}, []string{"second beta"}},
		{LogQuery{Search: second, Authors: []string{"ada"}}, nil},
		{LogQuery{Search: second, Paths: []string{"a.txt"}}, nil},
		{LogQuery{Path: "c.txt", Follow: true, Authors: []string{"ada"}}, []string{"third alpha fix", "first alpha"}},
		{LogQuery{Path: "c.txt", Follow: true, Before: day(3)}, []string{"first alpha"}},
		{LogQuery{Path: "c.txt", Follow: true, Search: "^th", Regex: true}, []string{"third alpha fix"}},
	} {
		if got := subjects(tc.q); !slices.Equal(got, tc.want) {
			t.Errorf("%+v：%q，应为 %q", tc.q, got, tc.want)
		}
	}
}

// 日期：yyyy-mm-dd hh:mm:ss 是本地时间，相对的交给 git，git 认不出（当成现在）的报 BadDate。
func TestResolveDate(t *testing.T) {
	r := testutil.NewRepo(t, "d")
	ctx := context.Background()
	if d, err := ResolveDate(ctx, r.Root, "2026-10-01 00:00:00"); err != nil || !d.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)) {
		t.Errorf("ISO：%v %v", d, err)
	}
	if d, err := ResolveDate(ctx, r.Root, "2 weeks ago"); err != nil || time.Since(d) < 13*24*time.Hour || time.Since(d) > 15*24*time.Hour {
		t.Errorf("2 weeks ago：%v %v", d, err)
	}
	for _, s := range []string{"yesterdy", "zzz"} {
		if _, err := ResolveDate(ctx, r.Root, s); !errors.Is(err, BadDate) {
			t.Errorf("%q：%v，应为 BadDate", s, err)
		}
	}
	if _, err := ResolveDate(ctx, r.Root, "now"); err != nil {
		t.Errorf("now：%v", err)
	}
}

// 写操作只在临时仓库上：RunRW 合并输出、失败带信息、不会等输入。
func TestRunRW(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	out, err := RunRW(root, "add", "-A")
	if err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if st, _ := ReadStatus(context.Background(), root); st.Staged != 2 || st.Unstaged != 0 {
		t.Errorf("add 之后 %+v", st)
	}
	if _, err := RunRW(root, "commit"); err == nil {
		t.Error("没有 -m 的 commit 应该失败而不是等编辑器")
	}
	if _, err := RunRW(root, "no-such-cmd"); err == nil || !strings.Contains(err.Error(), "not a git command") {
		t.Errorf("坏命令: %v", err)
	}
}

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		"git log --oneline -3": {"log", "--oneline", "-3"},
		`commit -m "两个 词"`:     {"commit", "-m", "两个 词"},
		`commit -m 'a "b"'`:    {"commit", "-m", `a "b"`},
		`log a\ b`:             {"log", "a b"},
		"  status   ":          {"status"},
		"":                     nil,
		`branch ""`:            {"branch", ""},
	}
	for in, want := range cases {
		if got := SplitArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitArgs(%q) = %q，要 %q", in, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "刚刚"},
		{5 * time.Minute, "5 分钟前"},
		{3 * time.Hour, "3 小时前"},
		{50 * time.Hour, "2 天前"},
		{70 * 24 * time.Hour, "2 个月前"},
		{800 * 24 * time.Hour, "2 年前"},
	}
	for _, tc := range cases {
		if got := Ago(now.Add(-tc.d)); got != tc.want {
			t.Errorf("Ago(-%s) = %q，要 %q", tc.d, got, tc.want)
		}
	}
	if Ago(time.Time{}) != "" {
		t.Error("零时间应该是空")
	}
}

// TestAgoEnglish：英文界面里 1 的是单数（"1 day ago"），别的是复数。
func TestAgoEnglish(t *testing.T) {
	t.Cleanup(func() { i18n.Set("") })
	i18n.Set("en")
	now := time.Now()
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{90 * time.Second, "1 minute ago"},
		{5 * time.Minute, "5 minutes ago"},
		{61 * time.Minute, "1 hour ago"},
		{3 * time.Hour, "3 hours ago"},
		{25 * time.Hour, "1 day ago"},
		{50 * time.Hour, "2 days ago"},
		{31 * 24 * time.Hour, "1 month ago"},
		{70 * 24 * time.Hour, "2 months ago"},
		{366 * 24 * time.Hour, "1 year ago"},
		{800 * 24 * time.Hour, "2 years ago"},
	} {
		if got := Ago(now.Add(-tc.d)); got != tc.want {
			t.Errorf("Ago(-%s) = %q，要 %q", tc.d, got, tc.want)
		}
	}
}

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("中文\n")) || !IsBinary([]byte{'a', 0, 'b'}) {
		t.Error("IsBinary 判断错")
	}
}

// 取消一条写命令：整个进程组（git 和它起的子进程）都结束，返回 ErrCanceled；锁文件只报告不删。
func TestRunOpCancel(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		// git 的别名起一个 sh，sh 再换成 sleep：取消时它们都得死，不能留下孤儿
		_, err := RunOp(ctx, root, "", "-c", "alias.slow=!sh -c 'echo $$ > "+pidFile+"; exec sleep 30'", "slow")
		done <- err
	}()
	// 等 sh 写出 pid 再取消：机器忙时 git → sh 起来要好一会儿，固定等 300 ms 会在 sh 起来之前就取消
	pid := 0
	deadline := time.Now().Add(testutil.Budget(5 * time.Second))
	for pid == 0 {
		select {
		case err := <-done:
			t.Fatalf("还没取消就结束了：%v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("sh 一直没写出 pid 文件")
		}
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	cancel()
	if err := <-done; !errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v，应为 ErrCanceled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("取消后过了 %v 才返回", d)
	}
	deadline = time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("取消后 sleep（%d）还活着", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if locks := StaleLocks(root); len(locks) != 0 {
		t.Fatalf("没有锁文件时 %q", locks)
	}
	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if locks := StaleLocks(root); len(locks) != 1 || locks[0] != lock {
		t.Fatalf("StaleLocks = %q，应为 %q", locks, lock)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("StaleLocks 不该删锁文件")
	}
}

// 标签：新的在前（附注标签按打标签的时间，轻量标签按提交的时间）；附注标签的 Hash 是它指着的提交，不是标签对象。
func TestReadTags(t *testing.T) {
	r := testutil.NewRepo(t, "tags")
	if ts, err := ReadTags(context.Background(), r.Root); err != nil || len(ts) != 0 {
		t.Fatalf("没有提交时: %v, %v", ts, err)
	}
	now := time.Now()
	r.Write("a.txt", "1\n")
	r.CommitAt("c1", now.Add(-72*time.Hour))
	c1 := r.Git("rev-parse", "HEAD")
	r.Git("tag", "v1")
	r.Write("a.txt", "2\n")
	r.CommitAt("c2", now.Add(-24*time.Hour))
	c2 := r.Git("rev-parse", "HEAD")
	r.Git("tag", "v3")
	r.Git("tag", "-a", "v2", "-m", "附注", c1) // 打标签的时间是现在：最新
	ts, err := ReadTags(context.Background(), r.Root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tg := range ts {
		got = append(got, tg.Name)
	}
	if want := []string{"v2", "v3", "v1"}; !slices.Equal(got, want) {
		t.Fatalf("顺序 %v，要 %v", got, want)
	}
	if v2 := ts[0]; !v2.Annotated || v2.Hash != c1 || time.Since(v2.Date) > time.Hour {
		t.Errorf("附注标签 %+v（c1 %s）", v2, c1)
	}
	if v3 := ts[1]; v3.Annotated || v3.Hash != c2 || v3.Date.Sub(now.Add(-24*time.Hour)).Abs() > time.Second {
		t.Errorf("轻量标签 %+v（c2 %s）", v3, c2)
	}
}

// 远程：按名字排；推送地址和抓取的一样时 Push 为空，不一样时是推送地址；没有远程是空的。
func TestReadRemotes(t *testing.T) {
	r := testutil.NewRepo(t, "remotes")
	if rs := ReadRemotes(context.Background(), r.Root); len(rs) != 0 {
		t.Fatalf("没有远程时: %+v", rs)
	}
	up, mirror, push := t.TempDir(), t.TempDir(), t.TempDir()
	r.Git("remote", "add", "origin", up)
	r.Git("remote", "add", "backup", mirror)
	r.Git("remote", "set-url", "--push", "origin", push)
	got := ReadRemotes(context.Background(), r.Root)
	want := []Remote{{Name: "backup", URL: mirror}, {Name: "origin", URL: up, Push: push}}
	if !slices.Equal(got, want) {
		t.Errorf("远程 %+v，要 %+v", got, want)
	}
}

// 被忽略的未跟踪文件：整个被忽略的目录只列它自己（带 /），跟踪着的、没被忽略的不列。临时仓库，只读。
func TestLsIgnored(t *testing.T) {
	r := testutil.NewRepo(t, "ign")
	r.Write(".gitignore", "build/\n*.log\n")
	r.Write("a.txt", "a\n")
	r.Write("kept.log", "跟踪着的\n")
	r.Git("add", "-f", "kept.log")
	r.Commit("root")
	r.Write("build/out/x.bin", "x")
	r.Write("build/y.o", "y")
	r.Write("debug.log", "d\n")
	r.Write("sub/trace.log", "t\n")
	r.Write("new.txt", "未跟踪，没忽略\n")
	got, err := LsIgnored(context.Background(), r.Root)
	if err != nil {
		t.Fatal(err)
	}
	// git 还会把只装着被忽略文件的未跟踪目录列成 "sub/"（2.54 如此），文件树那边照样处理（目录里有文件就不灰）
	for _, p := range []string{"build/", "debug.log", "sub/trace.log"} {
		if !slices.Contains(got, p) {
			t.Errorf("LsIgnored = %v，少了 %s", got, p)
		}
	}
	for _, p := range []string{"a.txt", "kept.log", "new.txt", "build/out/x.bin", "build/y.o", ".gitignore"} {
		if slices.Contains(got, p) {
			t.Errorf("LsIgnored = %v，不该有 %s", got, p)
		}
	}
}

// Listed：只问给的路径，跟踪的、没被忽略的未跟踪的列出来，被忽略的、不存在的不列；目录列到里面的文件；
// 名字里的 * 按字面（不是通配）。临时仓库，只读。
func TestListed(t *testing.T) {
	r := testutil.NewRepo(t, "listed")
	r.Write(".gitignore", "*.log\nbuild/\n")
	r.Write("a.txt", "a\n")
	r.Write("other.txt", "o\n")
	r.Commit("root")
	r.Write("new.txt", "n\n")
	r.Write("debug.log", "d\n")
	r.Write("dir/x.go", "x\n")
	r.Write("dir/y.log", "y\n")
	r.Write("build/z.o", "z\n")
	r.Write("*.txt", "星号\n")
	got, err := Listed(context.Background(), r.Root, []string{"a.txt", "new.txt", "debug.log", "dir", "build", "gone.txt", "*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"*.txt", "a.txt", "dir/x.go", "new.txt"}; !slices.Equal(got, want) {
		t.Errorf("Listed = %q，要 %q", got, want)
	}
	if got, err := Listed(context.Background(), r.Root, nil); err != nil || got != nil {
		t.Errorf("什么都不问 = %q %v", got, err)
	}
}

// RunOpEnv 的 env 接在默认的后面、同一个键后面的赢：GIT_SEQUENCE_EDITOR 换成 cp 写好的 todo，
// git rebase -i 就照它做（这里把两个提交换了顺序、丢掉一个），全程不开编辑器。nil 的 env 和 RunOp 一样。
func TestRunOpEnv(t *testing.T) {
	r := testutil.NewRepo(t, "env")
	for _, n := range []string{"a", "b", "c"} {
		r.Write(n+".txt", n+"\n")
		r.Commit(n)
	}
	b, c := r.Git("rev-parse", "HEAD~1"), r.Git("rev-parse", "HEAD")
	todo := filepath.Join(t.TempDir(), "it's.todo") // 路径里有单引号
	if err := os.WriteFile(todo, []byte("pick "+c+"\ndrop "+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(todo, "'", `'\''`) + "'"
	out, err := RunOpEnv(context.Background(), r.Root, "", []string{"GIT_SEQUENCE_EDITOR=cp " + quoted}, "rebase", "-i", "HEAD~2")
	if err != nil {
		t.Fatalf("rebase -i：%v\n%s", err, out)
	}
	if got := r.Git("log", "--format=%s"); got != "c\na" {
		t.Errorf("变基后的历史是 %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "b.txt")); !os.IsNotExist(err) {
		t.Errorf("丢掉的提交里的文件还在：%v", err)
	}
	// 默认的 GIT_SEQUENCE_EDITOR=true：todo 原样，什么都不变
	if out, err := RunOpEnv(context.Background(), r.Root, "", nil, "rebase", "-i", "HEAD~1"); err != nil {
		t.Fatalf("默认的 rebase -i：%v\n%s", err, out)
	}
	if got := r.Git("log", "--format=%s"); got != "c\na" {
		t.Errorf("默认 env 下历史变成了 %q", got)
	}
}

// LsFilesModes：Paths 和 LsFiles 一样；索引里 mode 120000 的进 Links（指向目录的、断了的也是），
// 未跟踪的进 Others（git 不记它们是什么）；删了没暂存的不列。路径里的空格和制表符照样（-z）。
func TestLsFilesModes(t *testing.T) {
	r := testutil.NewRepo(t, "modes")
	r.Write("a.txt", "a\n")
	r.Write("sub/b.txt", "b\n")
	r.Write("gone.txt", "g\n")
	r.Write("tab\tname.txt", "t\n")
	for link, target := range map[string]string{"l": "a.txt", "dl": "sub", "bad": "nope", "sub/up": "../a.txt"} {
		if err := os.Symlink(target, filepath.Join(r.Root, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	r.Commit("root")
	os.Remove(filepath.Join(r.Root, "gone.txt"))
	r.Write("u.txt", "u\n")
	if err := os.Symlink("a.txt", filepath.Join(r.Root, "ul")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l, err := LsFilesModes(ctx, r.Root)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := LsFiles(ctx, r.Root)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(l.Paths)
	slices.Sort(plain)
	if want := []string{"a.txt", "bad", "dl", "l", "sub/b.txt", "sub/up", "tab\tname.txt", "u.txt", "ul"}; !slices.Equal(l.Paths, want) || !slices.Equal(plain, want) {
		t.Errorf("Paths %q / LsFiles %q，要 %q", l.Paths, plain, want)
	}
	slices.Sort(l.Links)
	if want := []string{"bad", "dl", "l", "sub/up"}; !slices.Equal(l.Links, want) {
		t.Errorf("Links %q，要 %q", l.Links, want)
	}
	slices.Sort(l.Others)
	if want := []string{"u.txt", "ul"}; !slices.Equal(l.Others, want) {
		t.Errorf("Others %q，要 %q", l.Others, want)
	}
}

// 未跟踪目录：git status 把它折叠成一项 "harness/"（Dir），计数照旧；ListUntrackedDirs 列出里面没被忽略的文件，
// 排好序，名字里的 * 按字面；MaxDirList 压到 1 时只留一个、记 DirMore。暂存了里面一个文件以后剩下的不再折叠成整个目录。
// 临时仓库，只读（暂存在临时仓库里）。
func TestStatusUntrackedDir(t *testing.T) {
	r := testutil.NewRepo(t, "udir")
	r.Write(".gitignore", "*.log\n")
	r.Write("a.txt", "a\n")
	r.Commit("root")
	r.Write("harness/sub/b.md", "b\n")
	r.Write("harness/a.md", "a\n")
	r.Write("harness/skip.log", "忽略的\n")
	r.Write("c.md", "c\n")
	r.Write("st*r/x.md", "x\n")
	ctx := context.Background()
	st, err := ReadStatus(ctx, r.Root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ch := range st.Changes {
		got = append(got, fmt.Sprintf("%s %v", ch.Path, ch.Dir))
	}
	if want := []string{"c.md false", "harness/ true", "st*r/ true"}; !slices.Equal(got, want) || st.Count != 3 || st.Untracked != 3 || !st.HasDirs() {
		t.Fatalf("状态 %q（%d 条），要 %q", got, st.Count, want)
	}
	files, more, err := ListUntrackedDirs(ctx, r.Root, st)
	if err != nil {
		t.Fatal(err)
	}
	if fs := files["harness/"]; !slices.Equal(fs, []string{"harness/a.md", "harness/sub/b.md"}) || len(more) != 0 {
		t.Errorf("harness/ 里 %q，超了 %v", fs, more)
	}
	if fs := files["st*r/"]; !slices.Equal(fs, []string{"st*r/x.md"}) || len(files) != 2 {
		t.Errorf("st*r/ 里 %q（%d 个目录）", fs, len(files))
	}

	old := MaxDirList
	MaxDirList = 1
	t.Cleanup(func() { MaxDirList = old })
	files, more, _ = ListUntrackedDirs(ctx, r.Root, st)
	if fs := files["harness/"]; !slices.Equal(fs, []string{"harness/a.md"}) || !more["harness/"] || more["st*r/"] {
		t.Errorf("上限 1：%q，超了 %v", fs, more)
	}
	MaxDirList = old

	r.Git("add", "--", "harness/a.md")
	st, _ = ReadStatus(ctx, r.Root)
	got = got[:0]
	for _, ch := range st.Changes {
		got = append(got, fmt.Sprintf("%v %s %v", ch.Kind, ch.Path, ch.Dir))
	}
	if want := []string{"已暂存 harness/a.md false", "未跟踪 c.md false", "未跟踪 harness/sub/ true", "未跟踪 st*r/ true"}; !slices.Equal(got, want) {
		t.Errorf("暂存了 harness/a.md 以后 %q，要 %q", got, want)
	}
	if fs, _, _ := ListUntrackedDirs(ctx, r.Root, st); !slices.Equal(fs["harness/sub/"], []string{"harness/sub/b.md"}) {
		t.Errorf("harness/sub/ 里 %q", fs["harness/sub/"])
	}
	if fs, _, err := ListUntrackedDirs(ctx, r.Root, Status{Changes: []Change{{Kind: Untracked, Path: "c.md"}}}); fs != nil || err != nil {
		t.Errorf("没有目录时不跑 git：%v %v", fs, err)
	}
}
