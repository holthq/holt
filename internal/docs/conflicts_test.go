package docs

import (
	"reflect"
	"testing"
)

// ParseConflicts：只认 <<<<<<< 打开之后的标记；没闭合的丢掉；CRLF、diff3、文件尾没有换行都认。
func TestParseConflicts(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []ConflictBlock
	}{
		{"没有", "a\nb\n", nil},
		{"一块", "a\n<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> feature\nb\n",
			[]ConflictBlock{{Start: 1, Mid: 3, Base: -1, End: 5, OursLabel: "HEAD", TheirsLabel: "feature"}}},
		{"两块", "<<<<<<< HEAD\n1\n=======\n2\n>>>>>>> b\nx\n<<<<<<< HEAD\n3\n4\n=======\n>>>>>>> b\n",
			[]ConflictBlock{
				{Start: 0, Mid: 2, Base: -1, End: 4, OursLabel: "HEAD", TheirsLabel: "b"},
				{Start: 6, Mid: 9, Base: -1, End: 10, OursLabel: "HEAD", TheirsLabel: "b"},
			}},
		{"diff3", "<<<<<<< HEAD\nmine\n||||||| merged common ancestors\nbase\n=======\ntheirs\n>>>>>>> topic\n",
			[]ConflictBlock{{Start: 0, Base: 2, Mid: 4, End: 6, OursLabel: "HEAD", TheirsLabel: "topic"}}},
		{"块外的 =======", "Title\n=======\n\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\n=======\n",
			[]ConflictBlock{{Start: 3, Mid: 5, Base: -1, End: 7, OursLabel: "HEAD", TheirsLabel: "x"}}},
		{"没闭合", "<<<<<<< HEAD\na\n=======\nb\n", nil},
		{"闭合的后面跟一块没闭合的", "<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\n<<<<<<< HEAD\nc\n",
			[]ConflictBlock{{Start: 0, Mid: 2, Base: -1, End: 4, OursLabel: "HEAD", TheirsLabel: "x"}}},
		{"嵌套的当普通行", "<<<<<<< HEAD\n<<<<<<< inner\n=======\nb\n>>>>>>> x\n",
			[]ConflictBlock{{Start: 0, Mid: 2, Base: -1, End: 4, OursLabel: "HEAD", TheirsLabel: "x"}}},
		{"CRLF", "a\r\n<<<<<<< HEAD\r\nm\r\n=======\r\nt\r\n>>>>>>> f\r\n",
			[]ConflictBlock{{Start: 1, Mid: 3, Base: -1, End: 5, OursLabel: "HEAD", TheirsLabel: "f"}}},
		{"文件尾没有换行", "<<<<<<< HEAD\nm\n=======\nt\n>>>>>>> f",
			[]ConflictBlock{{Start: 0, Mid: 2, Base: -1, End: 4, OursLabel: "HEAD", TheirsLabel: "f"}}},
		{"没有名字的标记", "<<<<<<<\nm\n=======\nt\n>>>>>>>\n",
			[]ConflictBlock{{Start: 0, Mid: 2, Base: -1, End: 4}}},
		{"八个 < 不算", "<<<<<<<< x\nm\n=======\nt\n>>>>>>> f\n", nil},
		{"名字带空格", "<<<<<<< HEAD (我的)\nm\n=======\nt\n>>>>>>> 3818438 (改第二行)\n",
			[]ConflictBlock{{Start: 0, Mid: 2, Base: -1, End: 4, OursLabel: "HEAD (我的)", TheirsLabel: "3818438 (改第二行)"}}},
	}
	for _, c := range cases {
		if got := ParseConflicts(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseConflicts = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// Resolved：四种解法换上的文字和范围；文件尾没有换行时换上的也不带。
func TestResolved(t *testing.T) {
	text := "a\n<<<<<<< HEAD\nm1\nm2\n||||||| base\nb\n=======\nt\n>>>>>>> f\nz\n"
	b := ParseConflicts(text)[0]
	want := map[Take]string{
		TakeOurs:         "a\nm1\nm2\nz\n",
		TakeTheirs:       "a\nt\nz\n",
		TakeBoth:         "a\nm1\nm2\nt\nz\n",
		TakeBothReversed: "a\nt\nm1\nm2\nz\n",
	}
	for how, w := range want {
		start, end, repl := Resolved(text, b, how)
		if got := text[:start] + repl + text[end:]; got != w {
			t.Errorf("how %d: %q, want %q", how, got, w)
		}
	}

	tail := "x\n<<<<<<< HEAD\nm\n=======\nt\n>>>>>>> f"
	start, end, repl := Resolved(tail, ParseConflicts(tail)[0], TakeBoth)
	if got := tail[:start] + repl + tail[end:]; got != "x\nm\nt" {
		t.Errorf("文件尾: %q", got)
	}
	empty := "<<<<<<< HEAD\n=======\nt\n>>>>>>> f\n"
	start, end, repl = Resolved(empty, ParseConflicts(empty)[0], TakeOurs)
	if got := empty[:start] + repl + empty[end:]; got != "" {
		t.Errorf("我的那边空: %q", got)
	}
}
