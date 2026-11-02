package git

import (
	"os"
	"path/filepath"
	"testing"
)

// git 目录里的记号文件 → 进行到一半的操作；变基优先（变基停在冲突上时也可能有 CHERRY_PICK_HEAD 之类）。
func TestReadOngoing(t *testing.T) {
	for _, c := range []struct {
		marks []string
		want  Ongoing
	}{
		{nil, NoOp},
		{[]string{"MERGE_HEAD"}, Merging},
		{[]string{"rebase-merge/"}, Rebasing},
		{[]string{"rebase-apply/"}, Rebasing},
		{[]string{"rebase-merge/", "CHERRY_PICK_HEAD"}, Rebasing},
		{[]string{"CHERRY_PICK_HEAD"}, CherryPicking},
		{[]string{"REVERT_HEAD"}, Reverting},
	} {
		dir := t.TempDir()
		for _, m := range c.marks {
			p := filepath.Join(dir, m)
			var err error
			if m[len(m)-1] == '/' {
				err = os.Mkdir(p, 0o755)
			} else {
				err = os.WriteFile(p, []byte("x\n"), 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := ReadOngoing(dir); got != c.want {
			t.Errorf("%v: %v，应为 %v", c.marks, got, c.want)
		}
		if c.want != NoOp && (c.want.AbortLabel() == "" || len(c.want.AbortArgs()) != 2 || c.want.AbortArgs()[1] != "--abort") {
			t.Errorf("%v: 中止 %q %v", c.want, c.want.AbortLabel(), c.want.AbortArgs())
		}
	}
}
