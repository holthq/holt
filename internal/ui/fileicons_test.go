package ui

import (
	"testing"

	wui "github.com/egoist/mygo/ui"
)

// TestFileIcons 查文件名 → 图标：完整文件名、开头、最后一段扩展名、大小写不敏感、不认识的回默认；
// 颜色两种主题都不是零值，深色的比浅色亮。没有 logo 的语言是字母徽章（图标还是 iconCode）。
func TestFileIcons(t *testing.T) {
	light, dark := wui.LightTheme(), wui.DarkTheme()
	logoGo := fileExts["go"].icon
	for _, c := range []struct {
		name string
		want *wui.SVG
	}{
		{"main.go", logoGo},
		{"internal/app/app.go", logoGo},
		{"MAIN.GO", logoGo},
		{"script.sh", iconTerminal},
		{"index.html", iconCodeXML},
		{"style.scss", iconPalette},
		{"app.tsx", fileExts["jsx"].icon}, // React
		{"schema.sql", iconCode},
		{"config.yaml", iconBraces},
		{"tsconfig.json", iconBraces},
		{".env", iconBraces},
		{".env.local", iconBraces},
		{"yarn.lock", iconLock},
		{"go.sum", iconLock},
		{"package-lock.json", iconLock},
		{"notes.md", iconFileText},
		{"README.md", iconBook},
		{"README.MD", iconBook},
		{"readme", iconBook},
		{"README.zh-CN.md", iconBook},
		{"LICENSE", iconScale},
		{"LICENSE-MIT", iconScale},
		{"license.go", logoGo}, // 不是许可证
		{"CHANGELOG.md", iconHistory},
		{"changelog.json", iconBraces},
		{"logo.PNG", iconImage},
		{"Inter.woff2", iconType},
		{"a.mp3", iconMusic},
		{"a.mov", iconFilm},
		{"a.pdf", iconFileText},
		{"a.tar.gz", iconArchive},
		{"Dockerfile", iconContainer},
		{"Dockerfile.dev", iconContainer},
		{"docker-compose.yml", iconContainer},
		{"Makefile", iconHammer},
		{".gitignore", iconBranch},
		{".editorconfig", iconSettings},
		{".prettierrc", iconSettings},
		{".prettierrc.json", iconBraces},
		{"go.mod", iconPackage},
		{"package.json", iconPackage},
		{"Cargo.toml", iconPackage},
		{"pyproject.toml", iconPackage},
		{"bin", iconFile},
		{"a.unknownext", iconFile},
		{"a.", iconFile},
		{"", iconFile},
	} {
		k := fileKindOf(c.name)
		if k.icon != c.want {
			t.Errorf("%q：图标不对", c.name)
		}
		l, d := k.tint.color(light), k.tint.color(dark)
		if l == (wui.Color{}) || d == (wui.Color{}) {
			t.Errorf("%q：颜色是零值 %v / %v", c.name, l, d)
		}
	}

	// 同一类在两种主题下不一样，深色主题亮一档；pdf 和 md 同一个图标但颜色不同
	if g := fileKindOf("a.go").tint; lum(g.color(dark)) <= lum(g.color(light)) {
		t.Errorf("Go 的颜色深色主题下没有更亮：%v / %v", g.color(light), g.color(dark))
	}
	if fileColor(light, "a.pdf", false) == fileColor(light, "a.md", false) {
		t.Error("pdf 和 md 颜色一样")
	}
	// 不认识的就是原来的灰；选中行一律 AccentText
	if fileColor(light, "a.xyz", false) != light.TextMuted || fileColor(light, "a.go", true) != light.AccentText {
		t.Error("默认颜色或选中颜色不对")
	}

	// 类别色不超过 10 个（不是每种语言一个色）
	colors := map[wui.Color]bool{}
	for _, k := range fileExts {
		if k.tint.of == nil {
			colors[k.tint.light] = true
		}
	}
	for _, k := range fileNames {
		if k.tint.of == nil {
			colors[k.tint.light] = true
		}
	}
	if len(colors) > 10 {
		t.Errorf("类别色 %d 个，多于 10 个", len(colors))
	}
}

// TestFileBadges 查编程语言 → logo 或字母徽章和类别色：同一门语言的几个扩展名一样（h / hpp 跟 C / C++），
// 字母最多 3 个；不是编程语言的没有徽章。
func TestFileBadges(t *testing.T) {
	light := wui.LightTheme()
	for _, c := range []struct {
		name, badge string
		tint        *tint // nil：不查颜色
	}{
		{"main.go", "", &tintCyan}, {"MAIN.GO", "", &tintCyan}, {"main.dart", "", &tintCyan},
		{"a.py", "", &tintBlue}, {"a.ts", "", &tintBlue}, {"a.tsx", "", &tintBlue}, {"init.lua", "", &tintBlue},
		{"a.js", "", &tintYellow}, {"a.jsx", "", &tintCyan},
		{"lib.rs", "", &tintOrange}, {"App.swift", "", &tintOrange}, {"Main.java", "", &tintOrange}, {"a.scala", "", &tintOrange},
		{"a.kt", "", &tintPurple}, {"index.php", "", &tintPurple}, {"Rakefile.rb", "", &tintRed},
		{"a.c", "", &tintSlate}, {"a.cpp", "", &tintSlate}, {"build.zig", "", &tintSlate},
		{"App.svelte", "", &tintGreen}, {"q.graphql", "", &tintKhaki},
		{"a.cs", "C#", &tintSlate}, {"a.m", "OC", &tintSlate}, {"a.mm", "OC", &tintSlate},
		{"App.vue", "Vue", &tintGreen}, {"schema.sql", "SQL", &tintKhaki}, {"api.proto", "Pb", &tintKhaki},
		{"run.sh", "", &tintGreen}, {"a.json", "", &tintKhaki}, {"a.md", "", nil}, {"go.mod", "", &tintCyan}, {"a.xyz", "", nil},
	} {
		k := fileKindOf(c.name)
		if k.badge != c.badge {
			t.Errorf("%q：徽章 %q，要 %q", c.name, k.badge, c.badge)
		}
		if c.tint != nil && k.tint.color(light) != c.tint.color(light) {
			t.Errorf("%q：颜色 %v，要 %v", c.name, k.tint.color(light), c.tint.color(light))
		}
	}
	for _, same := range [][]string{{"py", "pyi"}, {"ts", "mts", "cts"}, {"js", "mjs", "cjs"}, {"jsx", "tsx"}, {"c", "h"},
		{"cpp", "cc", "cxx", "hpp", "hh"}, {"kt", "kts"}} {
		for _, ext := range same[1:] {
			if fileExts[ext].icon != fileExts[same[0]].icon {
				t.Errorf(".%s 和 .%s 的 logo 不一样", ext, same[0])
			}
		}
	}
	for ext, k := range fileExts {
		if len(k.badge) > 3 {
			t.Errorf(".%s 的徽章 %q 超过 3 个字母", ext, k.badge)
		}
		if (k.badge != "") != (k.icon == iconCode) {
			t.Errorf(".%s：徽章 %q，图标是 iconCode 要和有徽章一致", ext, k.badge)
		}
	}
}

func lum(c wui.Color) float32 {
	return 0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B)
}
