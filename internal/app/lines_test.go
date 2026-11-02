package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 行级暂存的配对规则在临时仓库里走一遍：旧的十行里 3、4 改成 3x、4x、4y（前两行配对，4y 没配对），8 删了。
// 暂存（改索引那份旧的）、取消暂存（改索引那份新的）、丢弃（改工作区）选中不同的行，git apply 之后的结果和手算的一样；
// 只选了没改的行时补丁是空的。
func TestLinesEdits(t *testing.T) {
	join := func(l ...string) string { return strings.Join(l, "\n") + "\n" }
	old := join("1", "2", "3", "4", "5", "6", "7", "8", "9", "10")
	nw := join("1", "2", "3x", "4x", "4y", "5", "6", "7", "9", "10")
	st := &editor.DiffState{Old: editor.NewBuffer(old), New: editor.NewBuffer(nw)}
	hunks := st.Hunks()
	if want := []editor.DiffHunk{{OldStart: 2, OldEnd: 4, NewStart: 2, NewEnd: 5}, {OldStart: 7, OldEnd: 8, NewStart: 8, NewEnd: 8}}; len(hunks) != 2 || hunks[0] != want[0] || hunks[1] != want[1] {
		t.Fatalf("diff 的块是 %+v，要 %+v", hunks, want)
	}
	newSide := func(a, b int) editor.DiffLineRange {
		return editor.DiffLineRange{Side: editor.DiffNew, Start: a, End: b}
	}
	oldSide := func(a, b int) editor.DiffLineRange {
		return editor.DiffLineRange{Side: editor.DiffOld, Start: a, End: b}
	}

	r := testutil.NewRepo(t, "lines")
	r.Write("f.txt", old)
	r.Commit("初始")
	for _, tc := range []struct {
		name  string
		sel   editor.DiffLineRange
		toOld bool
		want  string
	}{
		{"暂存配了对的一行", newSide(3, 3), true, join("1", "2", "3", "4x", "5", "6", "7", "8", "9", "10")},
		{"暂存没配对的一行", newSide(4, 4), true, join("1", "2", "3", "4", "4y", "5", "6", "7", "8", "9", "10")},
		{"暂存整块新的", newSide(2, 4), true, join("1", "2", "3x", "4x", "4y", "5", "6", "7", "8", "9", "10")},
		{"暂存删掉的一行", oldSide(7, 7), true, join("1", "2", "3", "4", "5", "6", "7", "9", "10")},
		{"暂存旧的一边的一行（配了对的）", oldSide(2, 2), true, join("1", "2", "4", "5", "6", "7", "8", "9", "10")},
		{"取消暂存配了对的一行", newSide(3, 3), false, join("1", "2", "3x", "4", "4y", "5", "6", "7", "9", "10")},
		{"取消暂存没配对的一行", newSide(4, 4), false, join("1", "2", "3x", "4x", "5", "6", "7", "9", "10")},
		{"取消暂存删掉的一行", oldSide(7, 7), false, join("1", "2", "3x", "4x", "4y", "5", "6", "7", "8", "9", "10")},
		{"取消暂存旧的一边配了对的一行", oldSide(2, 2), false, join("1", "2", "3", "3x", "4x", "4y", "5", "6", "7", "9", "10")},
		{"取消暂存两块各选一些", newSide(4, 9), false, join("1", "2", "3x", "4x", "5", "6", "7", "9", "10")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := nw
			if tc.toOld {
				base = old
			}
			r.Write("f.txt", base)
			r.Git("add", "f.txt")
			patch := git.LinesPatch("f.txt", []byte(base), LinesEdits([]byte(old), []byte(nw), hunks, tc.sel, tc.toOld))
			if out, err := git.RunRWInput(r.Root, patch, "apply", "--check", "--cached", "-"); err != nil {
				t.Fatalf("--check: %v\n%s\n补丁:\n%s", err, out, patch)
			}
			if out, err := git.RunRWInput(r.Root, patch, "apply", "--cached", "-"); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := string(git.Blob(r.Root, "", "f.txt")); got != tc.want {
				t.Errorf("索引里是 %q，要 %q\n补丁:\n%s", got, tc.want, patch)
			}
		})
	}

	// 丢弃：工作区（新的）上去掉选中的没配对的那一行
	r.Write("f.txt", nw)
	patch := git.LinesPatch("f.txt", []byte(nw), LinesEdits([]byte(old), []byte(nw), hunks, newSide(4, 4), false))
	if out, err := git.RunRWInput(r.Root, patch, "apply", "-"); err != nil {
		t.Fatalf("丢弃: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(r.Root, "f.txt")); string(got) != join("1", "2", "3x", "4x", "5", "6", "7", "9", "10") {
		t.Errorf("丢弃后工作区是 %q", got)
	}

	// 只选了没改的行
	for _, sel := range []editor.DiffLineRange{newSide(0, 1), oldSide(4, 6), newSide(9, 9)} {
		if es := LinesEdits([]byte(old), []byte(nw), hunks, sel, true); len(es) != 0 {
			t.Errorf("%+v 选的都是没改的行，却有改动 %+v", sel, es)
		}
	}
}
