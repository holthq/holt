package git

import (
	"context"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
)

// 冲突的"两边的 diff"（Code 'S'）：旧的一边是索引里的 :2（我们的），新的是 :3（他们的）；
// 一边删了这个文件时那边是空的，Note 说明。临时仓库。
func TestStagesDiff(t *testing.T) {
	r := testutil.ConflictRepo(t, "both", "c.txt", "1\n2\n3\n", "1\n我的\n3\n", "1\n他们的\n3\n")
	dc, err := LoadDiffContent(context.Background(), r.Root, Change{Kind: Conflict, Path: "c.txt", Code: 'S'})
	if err != nil {
		t.Fatal(err)
	}
	if string(dc.Old) != testutil.GitOut(t, r.Root, "cat-file", "blob", ":2:c.txt")+"\n" || string(dc.Old) != "1\n我的\n3\n" {
		t.Errorf("我们的一边 %q", dc.Old)
	}
	if string(dc.New) != "1\n他们的\n3\n" || dc.Note != "" || dc.Binary || dc.TooLarge {
		t.Errorf("他们的一边 %q，Note %q", dc.New, dc.Note)
	}
	// HEAD ↔ 工作区那个照旧：新的一边是带冲突标记的工作区文件
	head, err := LoadDiffContent(context.Background(), r.Root, Change{Kind: Conflict, Path: "c.txt", Code: 'U'})
	if err != nil || string(head.Old) != "1\n我的\n3\n" || !strings.Contains(string(head.New), "<<<<<<< HEAD") {
		t.Errorf("HEAD ↔ 工作区 %q → %q (%v)", head.Old, head.New, err)
	}

	gone := testutil.ConflictRepo(t, "gone", "c.txt", "1\n2\n3\n", "1\n我的\n3\n", "")
	dc, err = LoadDiffContent(context.Background(), gone.Root, Change{Kind: Conflict, Path: "c.txt", Code: 'S'})
	if err != nil {
		t.Fatal(err)
	}
	if string(dc.Old) != "1\n我的\n3\n" || len(dc.New) != 0 || dc.Note == "" {
		t.Errorf("他们删了: %q → %q，Note %q", dc.Old, dc.New, dc.Note)
	}
}
