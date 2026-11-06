package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// 读子模块：没有 .gitmodules 是 nil；一个初始化了（检出着记的那次）、一个 deinit 了（路径里有空格）；
// 子模块里检出了别的提交以后状态是 '+'，记的还是原来那次。
func TestReadSubmodules(t *testing.T) {
	ctx := context.Background()
	plain := testutil.NewRepo(t, "plain")
	plain.Write("a.txt", "a\n")
	plain.Commit("a")
	if subs, err := ReadSubmodules(ctx, plain.Root); subs != nil || err != nil {
		t.Fatalf("没有 .gitmodules 应是 nil：%+v %v", subs, err)
	}

	m, lib := testutil.SubmoduleRepo(t, "main")
	head := lib.Git("rev-parse", "HEAD")
	subs, err := ReadSubmodules(ctx, m.Root)
	if err != nil || len(subs) != 2 {
		t.Fatalf("读到 %+v %v", subs, err)
	}
	for i, want := range []Submodule{
		{Path: "vendor/lib", Name: "vendor/lib", URL: lib.Root, Head: head, State: ' '},
		{Path: "vendor/lib 2", Name: "vendor/lib 2", URL: lib.Root, Head: head, State: '-'},
	} {
		got := subs[i]
		got.Describe = ""
		if got != want {
			t.Errorf("第 %d 个是 %+v，要 %+v", i, got, want)
		}
	}
	if !subs[0].Inited() || subs[1].Inited() || subs[1].StateText() != "未初始化" || subs[0].StateText() != "正常" {
		t.Errorf("Inited / StateText 不对：%+v", subs)
	}

	sub := m.In(filepath.Join(m.Root, "vendor", "lib"))
	sub.Git("checkout", "-q", "HEAD~1")
	subs, _ = ReadSubmodules(ctx, m.Root)
	if subs[0].State != '+' || subs[0].Head != head || subs[0].StateText() != "检出的不是记录的" {
		t.Errorf("检出别的提交以后是 %+v", subs[0])
	}
}

// git status 里子模块的那三位：记录的第 3 个字段 S 开头时取后三位；普通文件为空。在真的仓库里：子模块检出了别的提交、
// 里面有未跟踪的文件是 "C.U"，它的 Diff 是两次提交；暂存以后已暂存那一项是两次提交、不提里面的（未暂存那边剩 "..U"）。
// 提交里加子模块的那次也是两次提交。
func TestStatusSubField(t *testing.T) {
	for rec, want := range map[string]string{
		"1 .M SC.U 160000 160000 160000 aaa aaa vendor/lib": "C.U",
		"1 M. S... 160000 160000 160000 aaa bbb a b/c d":    "...",
		"1 .M N... 100644 100644 100644 aaa aaa f.txt":      "",
		"2 R. N... 100644 100644 100644 aaa aaa R100 n o":   "",
		"1 .M SCMU": "CMU",
		"1 .M":      "",
	} {
		if got := subField(rec); got != want {
			t.Errorf("subField(%q) = %q，要 %q", rec, got, want)
		}
	}

	ctx := context.Background()
	m, lib := testutil.SubmoduleRepo(t, "main")
	head := lib.Git("rev-parse", "HEAD")
	prev := lib.Git("rev-parse", "HEAD~1")
	sub := m.In(filepath.Join(m.Root, "vendor", "lib"))
	sub.Git("checkout", "-q", "HEAD~1")
	if err := os.WriteFile(filepath.Join(sub.Root, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := ReadStatus(ctx, m.Root)
	if err != nil || len(st.Changes) != 1 {
		t.Fatalf("status: %+v %v", st, err)
	}
	ch := st.Changes[0]
	if ch.Kind != Unstaged || ch.Path != "vendor/lib" || ch.Code != 'M' || ch.Sub != "C.U" {
		t.Fatalf("子模块那一项是 %+v", ch)
	}
	d, err := LoadDiffContent(ctx, m.Root, ch)
	if want := "子模块 vendor/lib：" + Short(head) + " → " + Short(prev) + "（里面有未跟踪的文件）"; err != nil || !d.Submodule || d.Note != want {
		t.Errorf("未暂存的 Diff：%q %v %v，要 %q", d.Note, d.Submodule, err, want)
	}
	if SubText(ch.Sub) != "检出的提交变了、里面有未跟踪的文件" {
		t.Errorf("SubText = %q", SubText(ch.Sub))
	}

	m.Git("add", "vendor/lib")
	st, _ = ReadStatus(ctx, m.Root)
	// 里面还有未跟踪的文件，未暂存那边也还有一项（"..U"，检出的提交没变）
	if len(st.Changes) != 2 || st.Changes[0].Kind != Staged || st.Changes[1].Kind != Unstaged || st.Changes[1].Sub != "..U" {
		t.Fatalf("暂存以后：%+v", st.Changes)
	}
	d, err = LoadDiffContent(ctx, m.Root, st.Changes[0])
	if want := "子模块 vendor/lib：" + Short(head) + " → " + Short(prev); err != nil || !d.Submodule || d.Note != want {
		t.Errorf("已暂存的 Diff：%q %v %v，要 %q", d.Note, d.Submodule, err, want)
	}

	// 提交里：加子模块的那次（父仓库里没有子模块的提交，cat-file 读不了）
	c, p := m.Git("rev-parse", "HEAD"), m.Git("rev-parse", "HEAD~1")
	d, err = LoadDiffContent(ctx, m.Root, Change{Kind: InCommit, Commit: c, Parent: p, Path: "vendor/lib", Code: 'A'})
	if want := "子模块 vendor/lib：（没有） → " + Short(head); err != nil || !d.Submodule || d.Note != want {
		t.Errorf("提交里的 Diff：%q %v %v，要 %q", d.Note, d.Submodule, err, want)
	}
	// 同一次提交里的普通文件照旧
	d, err = LoadDiffContent(ctx, m.Root, Change{Kind: InCommit, Commit: c, Parent: p, Path: ".gitmodules", Code: 'A'})
	if err != nil || d.Submodule || len(d.New) == 0 {
		t.Errorf(".gitmodules 的 Diff：%+v %v", d, err)
	}
}
