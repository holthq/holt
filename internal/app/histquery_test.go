package app

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/testutil"
)

// 搜索框的查询语法：各键（中文键、全角冒号、大小写）、引号、斜杠正则、混着写；日期 yyyy-mm[-dd] 换成那天 0 点，
// 写错的进 Bad，别的原样留给 git。
func TestParseHistQuery(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want HistQuery
	}{
		{"", HistQuery{}},
		{"修复 登录", HistQuery{Words: "修复 登录"}},
		{"author:ada", HistQuery{Authors: []string{"ada"}}},
		{"Author:ada 作者：张三", HistQuery{Authors: []string{"ada", "张三"}}},
		{`author:"Ada L" fix`, HistQuery{Words: "fix", Authors: []string{"Ada L"}}},
		{"after:2026-10-01 before:2026-10", HistQuery{After: "2026-10-01 00:00:00", Before: "2026-10-01 00:00:00"}},
		{"之后:2026-9-5 之前：2026-12-31", HistQuery{After: "2026-09-05 00:00:00", Before: "2026-12-31 00:00:00"}},
		{"since:yesterday until:“2 weeks ago”", HistQuery{After: "yesterday", Before: "2 weeks ago"}},
		{"after:2026-13-01 before:2026-02-30", HistQuery{Bad: []string{"2026-13-01", "2026-02-30"}}},
		{"path:internal/app/ 路径:docs path:a.txt", HistQuery{Paths: []string{"internal/app", "docs", "a.txt"}}},
		{`"带 空格 的词"`, HistQuery{Words: "带 空格 的词"}},
		{"“全角 引号”", HistQuery{Words: "全角 引号"}},
		{`"author:ada"`, HistQuery{Words: "author:ada"}},
		{"/fix(es)? bug/ author:bob", HistQuery{Words: "fix(es)? bug", Regex: true, Authors: []string{"bob"}}},
		{`/a\/b/`, HistQuery{Words: "a/b", Regex: true}},
		{"/开头 不算正则", HistQuery{Words: "/开头 不算正则"}},
		{"foo /bar/", HistQuery{Words: "bar", Regex: true}},  // 有正则时裸词不算
		{"author: path:", HistQuery{}},                       // 键后面还没写
		{"http://x.test", HistQuery{Words: "http://x.test"}}, // 不认得的键是裸词
	} {
		if got := ParseHistQuery(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q：%+v，应为 %+v", tc.in, got, tc.want)
		}
	}
}

// 搜索框里的语法真的筛历史：临时仓库两个作者三天、两个目录；写错的日期在框下面说，git 也认不出的读完才说。
func TestHistSearchFields(t *testing.T) {
	r0 := testutil.NewRepo(t, "h")
	day := func(d int) time.Time { return time.Date(2026, 10, d, 9, 0, 0, 0, time.Local) }
	r0.Write("app/a.go", "1\n")
	r0.CommitAs("加 app", "Ada <ada@x.test>", day(1))
	r0.Write("docs/b.md", "1\n")
	r0.CommitAs("写 docs", "Bob <bob@x.test>", day(2))
	r0.Write("app/a.go", "2\n")
	r0.CommitAs("修 app", "Bob <bob@x.test>", day(3))
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Current()
	subjects := func(q string) []string {
		t.Helper()
		a.HistQuery = q
		a.ReloadHistories()
		if !r.Hist.Loading {
			a.LoadHistory(r, true)
		}
		wait("读历史 "+q, func() bool { return !r.Hist.Loading && r.Hist.Loaded })
		var out []string
		for _, c := range r.Hist.Commits {
			out = append(out, c.Subject)
		}
		return out
	}
	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"", []string{"修 app", "写 docs", "加 app"}},
		{"author:bob", []string{"修 app", "写 docs"}},
		{"作者:bob path:app", []string{"修 app"}},
		{"after:2026-10-02", []string{"修 app", "写 docs"}},
		{"before:2026-10-02", []string{"加 app"}},
		{"after:2026-10-02 before:2026-10-03 app", nil},
		{"after:2026-10-02 app", []string{"修 app"}},
		{"/^(加|写)/", []string{"写 docs", "加 app"}},
		{"path:docs path:app/a.go author:ada", []string{"加 app"}},
	} {
		if got := subjects(tc.q); !slices.Equal(got, tc.want) {
			t.Errorf("%q：%q，应为 %q", tc.q, got, tc.want)
		}
		if a.HistGraph(r) != (tc.q == "") {
			t.Errorf("%q：提交图 %v", tc.q, a.HistGraph(r))
		}
	}

	if got := subjects("after:2026-13-01 author:ada"); !slices.Equal(got, []string{"加 app"}) || !strings.Contains(a.HistQueryErr(), "2026-13-01") {
		t.Errorf("写错的日期：%q，小字 %q", got, a.HistQueryErr())
	}
	a.HistQuery = "before:zzz"
	if a.HistQueryErr() != "" {
		t.Errorf("git 认之前就说了：%q", a.HistQueryErr())
	}
	if got := subjects("before:zzz"); len(got) != 3 || !strings.Contains(a.HistQueryErr(), "zzz") {
		t.Errorf("git 认不出的日期不算：%q，小字 %q", got, a.HistQueryErr())
	}
	if subjects(""); a.HistQueryErr() != "" {
		t.Errorf("清空后还有小字 %q", a.HistQueryErr())
	}
}
