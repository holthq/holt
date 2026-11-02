package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Reorder 只换 folders 里那几项的原文：注释、别的字段、每一项的 name、缩进都原样；不在 roots 里的留在原位。
func TestReorder(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "w.code-workspace")
	orig := `{
	// 我的工作区
	"folders": [
		{ "path": "a" },
		{ "name": "B 仓库", "path": "b" }, /* 隐藏的 */
		{ "path": "hidden" },
		{
			// 注释在项里
			"path": "/abs/c"
		},
	],
	"settings": { "files.exclude": { "**/[x]": true }, "s": "a]b" }
}
`
	os.WriteFile(p, []byte(orig), 0o640)
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), "/abs/c"

	// 顺序没变：不写
	if err := Reorder(p, []string{a, b, c}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != orig {
		t.Fatal("顺序没变也写了")
	}

	if err := Reorder(p, []string{c, a, b}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := `{
	// 我的工作区
	"folders": [
		{
			// 注释在项里
			"path": "/abs/c"
		},
		{ "path": "a" }, /* 隐藏的 */
		{ "path": "hidden" },
		{ "name": "B 仓库", "path": "b" },
	],
	"settings": { "files.exclude": { "**/[x]": true }, "s": "a]b" }
}
`
	if string(got) != want {
		t.Fatalf("换出来的：\n%s", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Errorf("权限位变了：%v", fi.Mode())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("留下了临时文件：%v", ents)
	}

	// 普通的写法换完 Load 读回来是新的顺序；经符号链接写到它指的文件，链接还是链接
	os.WriteFile(p, []byte(`{"folders": [{"path": "a"}, {"path": "hidden"}, {"path": "b"}], "settings": {}}`), 0o644)
	link := filepath.Join(dir, "link.code-workspace")
	os.Symlink(p, link)
	if err := Reorder(link, []string{b, a}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("符号链接被换成了普通文件")
	}
	if roots, err := Load(p); err != nil || !slices.Equal(roots, []string{b, filepath.Join(dir, "hidden"), a}) {
		t.Fatalf("读回来：%q %v", roots, err)
	}

	// 认不出的写法：报错，不动
	bad := filepath.Join(dir, "bad.code-workspace")
	os.WriteFile(bad, []byte(`{"folders": {"path": "a"}}`), 0o644)
	if err := Reorder(bad, []string{a}); err == nil {
		t.Error("folders 不是数组也没报错")
	}
}
