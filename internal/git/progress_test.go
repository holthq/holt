package git

import (
	"context"
	"slices"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// 进行中的操作停在哪（横幅）：变基第 1 / 2 步、合并、拣选一个 / 两个、还原、老式 rebase --apply，没有进行中是零值。临时仓库。
func TestReadProgress(t *testing.T) {
	ctx := context.Background()
	extra := func(r *testutil.Repo, want Ongoing) Progress {
		t.Helper()
		x, err := ReadExtra(ctx, r.Root)
		if err != nil || x.Ongoing != want {
			t.Fatalf("应是 %v: %v (%v)", want, x.Ongoing, err)
		}
		return x.Progress
	}

	rb := testutil.RebaseRepo(t, "rebase")
	if p := extra(rb, Rebasing); p != (Progress{Step: 1, Total: 2, Subject: "topic one", Head: rb.Git("rev-parse", "topic~1"), Onto: "main", Branch: "topic"}) {
		t.Errorf("变基: %+v", p)
	}

	mg := testutil.ConflictRepo(t, "merge", "c.txt", "1\n2\n", "1\n我的\n", "1\n他们的\n")
	if p := extra(mg, Merging); p != (Progress{Subject: "Merge branch 'feature'", Head: mg.Git("rev-parse", "feature")}) {
		t.Errorf("合并: %+v", p)
	}

	// 拣选：topic 上两个提交，第一个和 main 冲突
	cp := testutil.NewRepo(t, "pick")
	cp.Write("f.txt", "1\n2\n3\n")
	cp.Commit("base")
	cp.Git("switch", "-q", "-c", "topic")
	cp.Write("f.txt", "1\nT\n3\n")
	cp.Commit("topic one")
	cp.Write("g.txt", "g\n")
	cp.Commit("topic two")
	cp.Git("switch", "-q", "main")
	cp.Write("f.txt", "1\nM\n3\n")
	cp.Commit("main change")
	one := cp.Git("rev-parse", "topic~1")
	cp.GitFails("cherry-pick", "topic~1", "topic")
	if p := extra(cp, CherryPicking); p != (Progress{Left: 1, Subject: "topic one", Head: one}) {
		t.Errorf("拣选两个: %+v", p)
	}
	cp.Git("cherry-pick", "--abort")
	cp.GitFails("cherry-pick", "topic~1")
	if p := extra(cp, CherryPicking); p != (Progress{Subject: "topic one", Head: one}) {
		t.Errorf("拣选一个: %+v", p)
	}
	cp.Git("cherry-pick", "--abort")

	// 老式的 rebase --apply：rebase-apply/next、last
	cp.Git("switch", "-q", "topic")
	cp.GitFails("rebase", "--apply", "main")
	if p := extra(cp, Rebasing); p.Step != 1 || p.Total != 2 || p.Subject != "topic one" || p.Branch != "topic" || p.Onto != "main" {
		t.Errorf("rebase --apply: %+v", p)
	}
	cp.Git("rebase", "--abort")

	// 还原：还原 "two"，后面的 "three" 改的是同一行
	rv := testutil.NewRepo(t, "revert")
	rv.Write("f.txt", "1\n2\n")
	rv.Commit("base")
	rv.Write("f.txt", "1\ntwo\n")
	rv.Commit("two")
	rv.Write("f.txt", "1\nthree\n")
	rv.Commit("three")
	two := rv.Git("rev-parse", "HEAD~1")
	rv.GitFails("revert", "--no-edit", "HEAD~1")
	if p := extra(rv, Reverting); p != (Progress{Subject: `Revert "two"`, Head: two}) {
		t.Errorf("还原: %+v", p)
	}
	rv.Git("revert", "--abort")
	if p := extra(rv, NoOp); p != (Progress{}) {
		t.Errorf("没有进行中: %+v", p)
	}
}

// 四种进行中的操作的继续 / 跳过 / 中止参数和按钮名；合并没有跳过。
func TestOngoingArgs(t *testing.T) {
	for _, c := range []struct {
		op                   Ongoing
		cont, skip, abort    []string
		contLabel, skipLabel string
	}{
		{NoOp, nil, nil, nil, "", ""},
		{Merging, []string{"merge", "--continue"}, nil, []string{"merge", "--abort"}, "继续合并", ""},
		{Rebasing, []string{"rebase", "--continue"}, []string{"rebase", "--skip"}, []string{"rebase", "--abort"}, "继续变基", "跳过这个提交"},
		{CherryPicking, []string{"cherry-pick", "--continue"}, []string{"cherry-pick", "--skip"}, []string{"cherry-pick", "--abort"}, "继续拣选", "跳过这个提交"},
		{Reverting, []string{"revert", "--continue"}, []string{"revert", "--skip"}, []string{"revert", "--abort"}, "继续还原", "跳过这个提交"},
	} {
		if !slices.Equal(c.op.ContinueArgs(), c.cont) || !slices.Equal(c.op.SkipArgs(), c.skip) || !slices.Equal(c.op.AbortArgs(), c.abort) {
			t.Errorf("%v: 继续 %q 跳过 %q 中止 %q", c.op, c.op.ContinueArgs(), c.op.SkipArgs(), c.op.AbortArgs())
		}
		if c.op.ContinueLabel() != c.contLabel || c.op.SkipLabel() != c.skipLabel {
			t.Errorf("%v: %q / %q", c.op, c.op.ContinueLabel(), c.op.SkipLabel())
		}
	}
}
