package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
)

// 块级的补丁在临时仓库里真的交给 git apply：暂存一块（--cached）后索引就是"索引套上这一块"，
// 丢弃一块（-R）后工作区就是"工作区去掉这一块"，取消暂存一块（--cached -R）同理；
// 包括文件头尾、没有末尾换行、CRLF、带空格和中文的文件名。
func TestHunkPatchApplies(t *testing.T) {
	ten := func(edit func([]string) []string) string {
		var l []string
		for i := 1; i <= 10; i++ {
			l = append(l, "第 "+string(rune('0'+i%10))+" 行")
		}
		return strings.Join(edit(l), "\n") + "\n"
	}
	same := func(l []string) []string { return l }
	for _, tc := range []struct {
		name, path, old, new string
		// 块在 diff 视图里的位置：旧的 a0..a1 换成新的 b0..b1
		a0, a1, b0, b1 int
	}{
		{"中间改一行", "a.txt", ten(same), ten(func(l []string) []string { l[4] = "改了"; return l }), 4, 5, 4, 5},
		{"开头插两行", "a.txt", ten(same), ten(func(l []string) []string { return append([]string{"x", "y"}, l...) }), 0, 0, 0, 2},
		{"末尾删一行", "a.txt", ten(same), ten(func(l []string) []string { return l[:9] }), 9, 10, 9, 9},
		{"补上末尾换行", "a.txt", "a\nb", "a\nb\n", 1, 2, 1, 3},
		{"去掉末尾换行", "a.txt", "a\nb\n", "a\nb", 1, 3, 1, 2},
		{"CRLF", "w.txt", "a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\n", 1, 2, 1, 2},
		{"文件名有空格和中文", "目录 x/文 件.md", ten(same), ten(func(l []string) []string { l[0] = "头"; return l }), 0, 1, 0, 1},
		{"空文件加内容", "e.txt", "", "一\n二\n", 0, 1, 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testutil.NewRepo(t, "hunk")
			r.Write(tc.path, tc.old)
			r.Commit("初始")
			r.Write(tc.path, tc.new)

			// 暂存这一块：索引（= old）套上新的那几行
			want, ok := ReplaceLines([]byte(tc.old), tc.a0, tc.a1, []byte(tc.new), tc.b0, tc.b1)
			if !ok {
				t.Fatal("行号超出范围")
			}
			if string(want) != tc.new {
				t.Fatalf("只有一块时暂存后应和工作区一样: %q", want)
			}
			if out, err := RunRWInput(r.Root, HunkPatch(tc.path, []byte(tc.old), want), "apply", "--cached", "-"); err != nil {
				t.Fatalf("暂存这一块: %v\n%s\n补丁:\n%s", err, out, HunkPatch(tc.path, []byte(tc.old), want))
			}
			if got := string(Blob(r.Root, "", tc.path)); got != tc.new {
				t.Errorf("暂存后索引 %q，要 %q", got, tc.new)
			}

			// 取消暂存这一块：索引（= new）去掉这一块
			back, _ := ReplaceLines([]byte(tc.new), tc.b0, tc.b1, []byte(tc.old), tc.a0, tc.a1)
			if out, err := RunRWInput(r.Root, HunkPatch(tc.path, back, []byte(tc.new)), "apply", "--cached", "-R", "-"); err != nil {
				t.Fatalf("取消暂存这一块: %v\n%s", err, out)
			}
			if got := string(Blob(r.Root, "", tc.path)); got != tc.old {
				t.Errorf("取消暂存后索引 %q，要 %q", got, tc.old)
			}

			// 丢弃这一块：工作区（= new）去掉这一块
			if out, err := RunRWInput(r.Root, HunkPatch(tc.path, back, []byte(tc.new)), "apply", "-R", "-"); err != nil {
				t.Fatalf("丢弃这一块: %v\n%s", err, out)
			}
			if got, _ := os.ReadFile(filepath.Join(r.Root, tc.path)); string(got) != tc.old {
				t.Errorf("丢弃后工作区 %q，要 %q", got, tc.old)
			}
		})
	}
}

// 两块改动只暂存第二块：索引里只有第二块，第一块还留在未暂存里；工作区在读 diff 之后又被改过、
// 上下文对不上时，丢弃会失败而不是乱改。
func TestHunkPatchOneOfTwo(t *testing.T) {
	r := testutil.NewRepo(t, "hunk")
	var l []string
	for i := 0; i < 20; i++ {
		l = append(l, "行"+string(rune('a'+i)))
	}
	old := strings.Join(l, "\n") + "\n"
	l[2], l[15] = "改 c", "改 p"
	nw := strings.Join(l, "\n") + "\n"
	r.Write("f.txt", old)
	r.Commit("初始")
	r.Write("f.txt", nw)

	staged, _ := ReplaceLines([]byte(old), 15, 16, []byte(nw), 15, 16)
	patch := HunkPatch("f.txt", []byte(old), staged)
	if strings.Contains(patch, "改 c") || strings.Count(patch, "\n@@ ") != 1 {
		t.Errorf("补丁应只有第二块:\n%s", patch)
	}
	if out, err := RunRWInput(r.Root, patch, "apply", "--cached", "-"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := testutil.GitOut(t, r.Root, "diff", "--cached", "--stat"); !strings.Contains(got, "1 insertion(+), 1 deletion(-)") {
		t.Errorf("索引里应只有一块: %s", got)
	}
	if got := testutil.GitOut(t, r.Root, "diff", "-U0"); !strings.Contains(got, "+改 c") || strings.Contains(got, "改 p") {
		t.Errorf("未暂存里应只剩第一块: %s", got)
	}

	// 读 diff 之后别处把第 1 块附近改了：丢弃第一块的补丁对不上，git apply 失败，文件不动
	back, _ := ReplaceLines([]byte(nw), 2, 3, []byte(old), 2, 3)
	stale := HunkPatch("f.txt", back, []byte(nw))
	l[1] = "别处改的"
	moved := strings.Join(l, "\n") + "\n"
	r.Write("f.txt", moved)
	if _, err := RunRWInput(r.Root, stale, "apply", "-R", "-"); err == nil {
		t.Error("上下文对不上时应失败")
	}
	if got, _ := os.ReadFile(filepath.Join(r.Root, "f.txt")); string(got) != moved {
		t.Error("失败时文件不该被改")
	}
}

func TestHunkPatchFormat(t *testing.T) {
	if p := HunkPatch("a", []byte("x\n"), []byte("x\n")); p != "" {
		t.Errorf("一样时应为空: %q", p)
	}
	got := HunkPatch(`we"ird`, []byte("a\nb\nc\n"), []byte("a\nB\nc\n"))
	want := "diff --git \"a/we\\\"ird\" \"b/we\\\"ird\"\n--- \"a/we\\\"ird\"\n+++ \"b/we\\\"ird\"\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	if got != want {
		t.Errorf("补丁\n%s\n要\n%s", got, want)
	}
	if _, ok := ReplaceLines([]byte("a\n"), 1, 5, nil, 0, 0); ok {
		t.Error("超出范围应报 false")
	}
}

// 行级的补丁（LinesPatch）在临时仓库里先 git apply --check --cached、再真的打到索引上，结果和手算的一样：
// 隔得远的两处是两块、近的并成一块，没有末尾换行的最后一行后面插东西会补上换行，CRLF 原样，
// 排错了、重叠的、超出范围的、什么都没改的都是空补丁。
func TestLinesPatch(t *testing.T) {
	twenty := ""
	for i := 1; i <= 20; i++ {
		twenty += "行" + string(rune('a'+i-1)) + "\n"
	}
	for _, tc := range []struct {
		name, old string
		edits     []LineEdit
		want      string
		hunks     int
	}{
		{"隔得远的两处", twenty, []LineEdit{{Start: 1, End: 2, With: []string{"改 b\n"}}, {Start: 15, End: 16}},
			strings.Replace(strings.Replace(twenty, "行b\n", "改 b\n", 1), "行p\n", "", 1), 2},
		{"隔得近的并成一块", twenty, []LineEdit{{Start: 1, End: 2, With: []string{"改 b\n"}}, {Start: 5, End: 5, With: []string{"插 1\n", "插 2\n"}}},
			strings.Replace(strings.Replace(twenty, "行b\n", "改 b\n", 1), "行f\n", "插 1\n插 2\n行f\n", 1), 1},
		{"开头插、结尾删", "a\nb\nc\n", []LineEdit{{Start: 0, End: 0, With: []string{"0\n"}}, {Start: 2, End: 3}}, "0\na\nb\n", 1},
		{"没换行的最后一行后面插", "a\nb", []LineEdit{{Start: 2, End: 2, With: []string{"c"}}}, "a\nb\nc", 1},
		{"改没换行的最后一行再接着插", "a\nb", []LineEdit{{Start: 1, End: 2, With: []string{"B"}}, {Start: 2, End: 2, With: []string{"c\n"}}}, "a\nB\nc\n", 1},
		{"换进来的行没换行但后面还有行", "a\nb\nc\n", []LineEdit{{Start: 1, End: 2, With: []string{"B"}}}, "a\nB\nc\n", 1},
		{"CRLF", "a\r\nb\r\nc\r\n", []LineEdit{{Start: 1, End: 2, With: []string{"B\r\n"}}}, "a\r\nB\r\nc\r\n", 1},
		{"空文件加两行", "", []LineEdit{{Start: 0, End: 0, With: []string{"一\n", "二\n"}}}, "一\n二\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testutil.NewRepo(t, "lines")
			r.Write("f.txt", tc.old)
			r.Commit("初始")
			patch := LinesPatch("f.txt", []byte(tc.old), tc.edits)
			if n := strings.Count("\n"+patch, "\n@@ "); n != tc.hunks {
				t.Errorf("应有 %d 块，有 %d:\n%s", tc.hunks, n, patch)
			}
			if out, err := RunRWInput(r.Root, patch, "apply", "--check", "--cached", "-"); err != nil {
				t.Fatalf("--check: %v\n%s\n补丁:\n%s", err, out, patch)
			}
			if out, err := RunRWInput(r.Root, patch, "apply", "--cached", "-"); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := string(Blob(r.Root, "", "f.txt")); got != tc.want {
				t.Errorf("索引里是 %q，要 %q\n补丁:\n%s", got, tc.want, patch)
			}
		})
	}
	for name, es := range map[string][]LineEdit{
		"重叠":   {{Start: 0, End: 2}, {Start: 1, End: 2}},
		"排错了":  {{Start: 2, End: 3}, {Start: 0, End: 1}},
		"超出范围": {{Start: 3, End: 5}},
		"没改":   {{Start: 1, End: 1}},
	} {
		if p := LinesPatch("f.txt", []byte("a\nb\nc\n"), es); p != "" {
			t.Errorf("%s 应是空补丁: %q", name, p)
		}
	}
}
