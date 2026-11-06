package git

import (
	"context"
	"testing"
	"time"

	"github.com/holthq/holt/internal/testutil"
)

// 读 blame：Alice 在东八区写了三行（根提交，boundary），Bob 改名成 b.txt 又改了第 2 行，第 3 行改了没提交。
// 工作树的 blame 分三段（Alice / Bob / 未提交），提交的头只给一次也认得；Bob 那次的上一版是 Alice 那次的 a.txt；
// 按提交读是那一版；坏的输出报错。
func TestBlameParse(t *testing.T) {
	ctx := context.Background()
	r := testutil.NewRepo(t, "blame")
	commit := func(author, msg, date string) string {
		r.Git("add", "-A")
		r.Git("commit", "-q", "--author="+author, "--date="+date, "-m", msg)
		return r.Git("rev-parse", "HEAD")
	}
	r.Write("a.txt", "one\ntwo\nthree\n")
	alice := commit("Alice <alice@example.com>", "先写三行", "2026-10-01T09:30:00+08:00")
	r.Git("mv", "a.txt", "b.txt")
	r.Write("b.txt", "one\nTWO\nthree\n")
	bob := commit("Bob <bob@example.com>", "改名、改第二行", "2026-10-20T12:00:00-05:00")
	r.Write("b.txt", "one\nTWO\nTHREE\n")

	b, err := ReadBlame(ctx, r.Root, "", "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b.Hash != "" || len(b.Lines) != 3 || len(b.Commits) != 3 {
		t.Fatalf("工作树的 blame：hash %q，%d 行，%d 个提交", b.Hash, len(b.Lines), len(b.Commits))
	}
	for i, want := range []string{alice, bob, ZeroHash} {
		if b.Lines[i].Hash != want {
			t.Errorf("第 %d 行是 %s，要 %s", i+1, b.Lines[i].Hash, want)
		}
	}
	if b.Lines[1].Orig != 2 {
		t.Errorf("Bob 那行在那次提交里是第 %d 行", b.Lines[1].Orig)
	}

	a := b.Commits[alice]
	wantTime := time.Date(2026, 10, 1, 9, 30, 0, 0, time.FixedZone("+0800", 8*3600))
	if a.Author != "Alice" || a.Email != "alice@example.com" || a.Summary != "先写三行" || !a.Boundary ||
		a.Path != "a.txt" || a.Previous != "" || a.Short != Short(alice) || !a.Time.Equal(wantTime) {
		t.Errorf("Alice 那次：%+v", a)
	}
	if _, off := a.Time.Zone(); off != 8*3600 {
		t.Errorf("Alice 那次的时区偏移 %d", off)
	}
	bc := b.Commits[bob]
	if bc.Author != "Bob" || bc.Path != "b.txt" || bc.Previous != alice || bc.PrevPath != "a.txt" || bc.Boundary {
		t.Errorf("Bob 那次：%+v", bc)
	}
	if z := b.Commits[ZeroHash]; !z.Uncommitted() || z.Author != "" || z.Email != "" {
		t.Errorf("未提交的那次：%+v", z)
	}

	segs := b.Segments()
	if len(segs) != 3 || segs[0].Commit != a || segs[1].Start != 1 || segs[1].End != 2 || !segs[2].Commit.Uncommitted() {
		t.Errorf("段：%+v", segs)
	}
	if oldest, newest := b.Span(); !oldest.Equal(a.Time) || !newest.Equal(bc.Time) {
		t.Errorf("时间范围 %v – %v", oldest, newest)
	}

	// 按提交读：Alice 那次的 a.txt 三行都是她的，没有未提交的
	old, err := ReadBlame(ctx, r.Root, alice, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if old.Hash != alice || len(old.Lines) != 3 || len(old.Commits) != 1 || len(old.Segments()) != 1 {
		t.Errorf("Alice 那次的 blame：%+v", old)
	}

	// 连着同一个提交的行只有第一行带头（"<hash> <orig> <final> <n>"），后面的只有哈希和行号
	if b2, err := ParseBlame([]byte(alice + " 1 1 2\nauthor A\nauthor-time 1\nauthor-tz +0000\nsummary s\nfilename a.txt\n\tone\n" +
		alice + " 2 2\n\ttwo\n")); err != nil || len(b2.Lines) != 2 || b2.Lines[1].Orig != 2 || b2.Commits[alice].Author != "A" {
		t.Errorf("两行同一个提交：%+v %v", b2, err)
	}
	if _, err := ParseBlame([]byte("不是 blame 的输出\n")); err == nil {
		t.Error("坏的输出没报错")
	}
	if _, err := ReadBlame(ctx, r.Root, "", "nope.txt"); err == nil {
		t.Error("没有的文件没报错")
	}
}
