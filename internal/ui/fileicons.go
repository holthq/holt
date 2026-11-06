package ui

import (
	"math"
	"path"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
)

// 文件类型图标：文件树、编辑器标签、快速打开、搜索结果、面包屑都用 fileGlyph。默认是彩色底块：
// 图标见方的圆角块填上类别色的淡色，上面是同色的线性图标，代码文件是语言的单色图标；
// 设置里的"图标风格：线形"是单色的图标，不画底块，目录是线框。
// 颜色按类别一组十来个（代码按语言取 GitHub linguist 的主色、压到和强调色差不多的饱和度，深色主题亮一档），
// 不是每种语言一个色。目录是自己画的实心文件夹，颜色和用途符号按目录名查表（folderKinds）。不用系统的 Finder 图标（mygo.Shell.FileIcon）：
// 源码文件全是白纸、位图、和线性图标不搭。语言图标是单色的，跟着类别色走，不用各家的彩色原版。

var (
	iconCodeXML   = icon("brackets-angle")
	iconPalette   = icon("palette")
	iconBraces    = icon("brackets-curly")
	iconLock      = icon("lock")
	iconFileText  = icon("file-text") // 外框和 iconFile 一样
	iconBook      = icon("book")
	iconScale     = icon("scales")
	iconType      = icon("text-aa")
	iconMusic     = icon("music-notes-simple")
	iconFilm      = icon("film-strip")
	iconContainer = icon("cube")
	iconHammer    = icon("hammer")
	iconSettings  = icon("sliders-horizontal")
	iconPackage   = icon("package")
)

// tint 是一类文件图标的颜色：浅色、深色主题各一个；都是零值就用主题的颜色（of）。faint 的底块更淡（灰的那类）。
type tint struct {
	light, dark wui.Color
	of          func(t *wui.Theme) wui.Color
	faint       bool
}

func (k tint) color(t *wui.Theme) wui.Color {
	switch {
	case k.of != nil:
		return k.of(t)
	case t.Dark:
		return k.dark
	}
	return k.light
}

func hues(light, dark string) tint { return tint{light: wui.Hex(light), dark: wui.Hex(dark)} }

// 十个类别色，加两个跟主题走的灰。
var (
	tintCyan   = hues("#1597b8", "#3cc0de") // Go、Dart、jsx
	tintBlue   = hues("#3a72b0", "#6a9fd8") // Python、TypeScript、Docker、Lua
	tintOrange = hues("#c8642c", "#ec8a55") // Rust、Swift、Java、HTML
	tintYellow = hues("#b39012", "#dcc046") // JavaScript
	tintKhaki  = hues("#94844e", "#c2b27c") // 数据与配置、压缩包
	tintSlate  = hues("#5b7a9a", "#8ca8c6") // C、C++、C#、Makefile、字体
	tintGreen  = hues("#3f9d63", "#4dbd7a") // 脚本、Vue、Svelte
	tintPurple = hues("#8a63d2", "#a68bec") // CSS、Kotlin、PHP
	tintPink   = hues("#c25595", "#e07bb8") // 图片、音视频
	tintRed    = hues("#cc4545", "#ea6a6a") // PDF、Ruby

	tintMuted = tint{of: func(t *wui.Theme) wui.Color { return t.TextMuted }, faint: true} // 锁文件、点文件、不认识的
	tintDoc   = tint{of: func(t *wui.Theme) wui.Color { return t.Text.Alpha(0.62) }}       // 文档：正文色偏灰
)

// fileKind 是一类文件的图标：线性图标或语言图标，和它的类别色。
type fileKind struct {
	icon *wui.SVG
	tint tint
}

// fileNames 按完整文件名（小写）查。
var fileNames = map[string]fileKind{
	"go.mod": {icon: iconPackage, tint: tintCyan}, "go.work": {icon: iconPackage, tint: tintCyan}, "go.sum": {icon: iconLock, tint: tintMuted},
	"cargo.toml": {icon: iconPackage, tint: tintOrange}, "package.json": {icon: iconPackage, tint: tintYellow},
	"pyproject.toml": {icon: iconPackage, tint: tintBlue}, "requirements.txt": {icon: iconPackage, tint: tintBlue},
	"package.swift": {icon: iconPackage, tint: tintOrange}, "composer.json": {icon: iconPackage, tint: tintPurple},
	"gemfile": {icon: iconPackage, tint: tintRed}, "pom.xml": {icon: iconPackage, tint: tintOrange}, "build.gradle": {icon: iconPackage, tint: tintOrange},
	"package-lock.json": {icon: iconLock, tint: tintMuted}, "pnpm-lock.yaml": {icon: iconLock, tint: tintMuted}, "bun.lockb": {icon: iconLock, tint: tintMuted},
	"makefile": {icon: iconHammer, tint: tintSlate}, "gnumakefile": {icon: iconHammer, tint: tintSlate}, "justfile": {icon: iconHammer, tint: tintSlate},
	".gitignore": {icon: iconBranch, tint: tintMuted}, ".gitattributes": {icon: iconBranch, tint: tintMuted}, ".gitmodules": {icon: iconBranch, tint: tintMuted},
	".dockerignore": {icon: iconContainer, tint: tintMuted},
}

// filePrefixes 按文件名开头查（README.md、LICENSE-MIT、Dockerfile.dev、docker-compose.yml、.env.local……）。
// doc 的只在没有扩展名或扩展名是文档时算（license.go 还是 Go）。
var filePrefixes = []struct {
	prefix string
	doc    bool
	kind   fileKind
}{
	{"readme", true, fileKind{icon: iconBook, tint: tintDoc}},
	{"license", true, fileKind{icon: iconScale, tint: tintDoc}},
	{"licence", true, fileKind{icon: iconScale, tint: tintDoc}},
	{"copying", true, fileKind{icon: iconScale, tint: tintDoc}},
	{"changelog", true, fileKind{icon: iconHistory, tint: tintDoc}},
	{"dockerfile", false, fileKind{icon: iconContainer, tint: tintBlue}},
	{"containerfile", false, fileKind{icon: iconContainer, tint: tintBlue}},
	{"docker-compose", false, fileKind{icon: iconContainer, tint: tintBlue}},
	{"compose.y", false, fileKind{icon: iconContainer, tint: tintBlue}},
	{".env", false, fileKind{icon: iconBraces, tint: tintKhaki}},
}

// fileExts 按最后一段扩展名（小写、不带点）查。
var fileExts = map[string]fileKind{}

func init() {
	add := func(k fileKind, exts ...string) {
		for _, e := range exts {
			fileExts[e] = k
		}
	}
	// 代码按语言一个图标：jsx / tsx 是 React，h 跟 C、hpp 跟 C++；图标是单色的，颜色是那一类的类别色
	lang := func(logo *wui.SVG, k tint, exts ...string) { add(fileKind{icon: logo, tint: k}, exts...) }
	react := icon("lang-react")
	lang(icon("lang-go"), tintCyan, "go")
	lang(icon("lang-dart"), tintCyan, "dart")
	lang(react, tintCyan, "jsx")
	lang(icon("lang-python"), tintBlue, "py", "pyi")
	lang(icon("lang-lua"), tintBlue, "lua")
	lang(icon("lang-typescript"), tintBlue, "ts", "mts", "cts")
	lang(react, tintBlue, "tsx")
	lang(icon("lang-rust"), tintOrange, "rs")
	lang(icon("lang-swift"), tintOrange, "swift")
	lang(icon("lang-java"), tintOrange, "java")
	lang(icon("lang-scala"), tintOrange, "scala")
	lang(icon("lang-kotlin"), tintPurple, "kt", "kts")
	lang(icon("lang-php"), tintPurple, "php")
	lang(icon("lang-ruby"), tintRed, "rb")
	lang(icon("lang-c"), tintSlate, "c", "h")
	lang(icon("lang-cpp"), tintSlate, "cc", "cpp", "cxx", "hpp", "hh")
	lang(icon("lang-zig"), tintSlate, "zig")
	lang(icon("lang-javascript"), tintYellow, "js", "mjs", "cjs")
	lang(icon("lang-svelte"), tintGreen, "svelte")
	lang(icon("lang-graphql"), tintKhaki, "graphql")
	lang(icon("lang-objc"), tintSlate, "m", "mm")
	lang(icon("lang-csharp"), tintSlate, "cs")
	lang(icon("lang-vue"), tintGreen, "vue")
	lang(icon("lang-sql"), tintKhaki, "sql")
	lang(icon("lang-proto"), tintKhaki, "proto")
	add(fileKind{icon: iconTerminal, tint: tintGreen}, "sh", "bash", "zsh", "fish", "ps1", "bat", "cmd")
	add(fileKind{icon: iconCodeXML, tint: tintOrange}, "html", "htm")
	add(fileKind{icon: iconPalette, tint: tintPurple}, "css", "scss", "sass", "less")
	add(fileKind{icon: iconBraces, tint: tintKhaki}, "json", "jsonc", "json5", "yaml", "yml", "toml", "ini", "cfg", "conf", "env", "plist", "xml", "properties", "csv", "tsv")
	add(fileKind{icon: iconLock, tint: tintMuted}, "lock")
	add(fileKind{icon: iconFileText, tint: tintDoc}, "md", "mdx", "markdown", "txt", "rst", "adoc", "org", "tex")
	add(fileKind{icon: iconFileText, tint: tintRed}, "pdf")
	add(fileKind{icon: iconImage, tint: tintPink}, "png", "jpg", "jpeg", "gif", "webp", "svg", "ico", "icns", "heic", "bmp", "tif", "tiff", "avif")
	add(fileKind{icon: iconType, tint: tintSlate}, "ttf", "otf", "woff", "woff2")
	add(fileKind{icon: iconMusic, tint: tintPink}, "mp3", "wav", "flac", "m4a", "aac", "ogg")
	add(fileKind{icon: iconFilm, tint: tintPink}, "mp4", "mov", "mkv", "webm", "avi")
	add(fileKind{icon: iconArchive, tint: tintKhaki}, "zip", "tar", "gz", "tgz", "bz2", "xz", "7z", "rar", "dmg", "zst")
}

// fileKindOf 是文件名 name（可以带目录）的类别：先按完整文件名、再按开头、再按最后一段扩展名，
// 都不认识回通用的 iconFile + TextMuted；别的点文件是 settings-2 灰。
func fileKindOf(name string) fileKind {
	lower := strings.ToLower(path.Base(name))
	if k, ok := fileNames[lower]; ok {
		return k
	}
	ext, known := fileKind{}, false
	if i := strings.LastIndexByte(lower, '.'); i > 0 {
		ext, known = fileExts[lower[i+1:]]
	}
	for _, p := range filePrefixes {
		if strings.HasPrefix(lower, p.prefix) && (!p.doc || !known || ext.icon == iconFileText) {
			return p.kind
		}
	}
	if known {
		return ext
	}
	if strings.HasPrefix(lower, ".") && lower != "." { // path.Base("") 是 "."
		return fileKind{icon: iconSettings, tint: tintMuted}
	}
	return fileKind{icon: iconFile, tint: tintMuted}
}

// fileColor 是文件 name 的图标在主题 t 下的颜色；onAccent（这一行画在实心的强调色底上）时一律 AccentText。
func fileColor(t *wui.Theme, name string, onAccent bool) wui.Color {
	if onAccent {
		return t.AccentText
	}
	return fileKindOf(name).tint.color(t)
}

// 彩色底块（默认）：size 见方、圆角 size × tileRadius 的块，填图形颜色的 tileAlpha（深色主题 tileAlphaDark，
// 灰的那类 tileFaint）；上面的线性图标 size × tileIcon（取整到 2 倍屏的整像素居中）。在实心选中块上图形是 AccentText、底块是它的 tileOnAccent。
const (
	tileRadius    = 0.22
	tileIcon      = 0.72
	tileAlpha     = 0.16
	tileAlphaDark = 0.22
	tileFaint     = 0.10
	tileOnAccent  = 0.22
)

// 文件树（iconTool 那档：文件树、打开的编辑器）里的彩色图标是"安静"的（#20）：一屏几十个同色底块太重，
// 所以不画底块，图形照旧按类别取色、往次级字色混 quietMix（饱和度和亮度都降下来）。
// 类别还是靠颜色和图形分得开；标签、快速打开、面包屑这些一行一两个的地方（iconRow）照旧画底块。
const quietMix = 0.35

// glyph 是一个类型图标怎么画（彩色底块）：size 见方的底块（圆角 radius，颜色 tile），上面 ink 色的图标（icon 大）。
type glyph struct {
	kind         fileKind
	size, radius float32
	tile, ink    wui.Color
	icon         float32
}

// glyphOf 是文件 name 在主题 t 下 size 见方的彩色底块：on 为画在实心选中块上，dim 为灰显的行（被忽略的、没初始化的子模块）。
func glyphOf(t *wui.Theme, name string, on, dim bool, size float32) glyph {
	k := fileKindOf(name)
	g := glyph{kind: k, size: size, radius: size * tileRadius, ink: k.tint.color(t)}
	alpha := float32(tileAlpha)
	if t.Dark {
		alpha = tileAlphaDark
	}
	if k.tint.faint {
		alpha = tileFaint
	}
	if dim {
		g.ink, alpha = t.TextMuted, tileFaint
	}
	if on {
		g.ink, alpha = t.AccentText, tileOnAccent
		if dim {
			g.ink = t.AccentText.Alpha(0.75)
		}
	}
	g.tile = g.ink.Alpha(alpha)
	// 图标四周的空取整（每边 size 的 14%，取整后两边一样）：2 倍屏上图标的格子落在整像素上
	pad := float32(math.Round(float64(size * (1 - tileIcon))))
	g.icon = size - pad
	return g
}

// quietGlyphOf 是文件树里的彩色图标（见 quietMix）：glyphOf 去掉底块，图形的类别色往次级字色混一些；
// 选中块上、灰显的行和 glyphOf 一样（本来就是单色的）。
func quietGlyphOf(t *wui.Theme, name string, on, dim bool, size float32) glyph {
	g := glyphOf(t, name, on, dim, size)
	g.tile = wui.Transparent
	if !on && !dim && g.kind.tint.of == nil {
		g.ink = g.ink.Mix(t.TextMuted, quietMix)
	}
	return g
}

// fileGlyph 画文件 name（可以带目录）的图标，iconRow 见方（标签、快速打开、面包屑、搜索结果）；on 为这一行画在实心的强调色底上
// （onAccent）。调用方可以接着设 Shrink / Margin / Tooltip。
func fileGlyph(a *app.App, c *wui.Context, name string, on bool) wui.Element {
	return fileGlyphSize(a, c, name, on, false, iconRow)
}

// fileGlyphSize 是 size（iconRow 或 iconTool）见方的 fileGlyph；dim 为灰显（文件树里被忽略的文件）。
// 按设置画彩色底块，或线形（TreeIconsPlain）。
func fileGlyphSize(a *app.App, c *wui.Context, name string, on, dim bool, size float32) wui.Element {
	t := c.Theme()
	if a.St.Settings.TreeIconsPlain {
		color := fileColor(t, name, on)
		if dim {
			color = t.TextMuted
			if on {
				color = t.AccentText.Alpha(0.75)
			}
		}
		return wui.Icon(c, fileKindOf(name).icon).FontSize(size).TextColor(color)
	}
	g := glyphOf(t, name, on, dim, size)
	if size >= iconTool {
		g = quietGlyphOf(t, name, on, dim, size)
	}
	return wui.Row(c).Size(size, size).Shrink(0).Radius(g.radius).Background(g.tile).
		AlignItems(wui.Center).Justify(wui.Center).Children(func() {
		wui.Icon(c, g.kind.icon).FontSize(g.icon).TextColor(g.ink).Shrink(0)
	})
}

// 文件树里的目录是自己画的实心文件夹（256 格，和 Phosphor 一样的外框），上面抠出目录用途的符号，所以全靠一个
// 颜色：图形跟着 folderColor 走。收起时是一层；展开时前面一层往下翻开，后面一层淡一点。
const (
	folderClosed    = "M24 64a20 20 0 0 1 20-20h50.7a20 20 0 0 1 14.1 5.9L128 68h84a20 20 0 0 1 20 20v100a20 20 0 0 1-20 20H44a20 20 0 0 1-20-20Z"
	folderBackOpen  = `<path opacity=".45" d="M24 64a20 20 0 0 1 20-20h50.7a20 20 0 0 1 14.1 5.9L128 68h68a20 20 0 0 1 20 20v12H24Z"/>`
	folderFrontOpen = "M63 96H234a14 14 0 0 1 13.4 18l-26.5 86a17 17 0 0 1-16.2 12H38a14 14 0 0 1-13.4-18l25-86A14 14 0 0 1 63 96Z"
)

// 目录用途的符号，抠在文件夹上（闭合的路径，和文件夹一起 evenodd）。
const (
	symCode   = "M112 112 76 150l36 38 16-15-22-23 22-23ZM144 112l36 38-36 38-16-15 22-23-22-23Z"
	symTest   = "M84 148l16-16 22 22 38-40 16 16-54 56Z"
	symScript = "M80 112l46 38-46 38-14-17 26-21-26-21ZM134 170h50v20h-50Z"
	symDocs   = "M80 112h96v20H80ZM80 142h96v20H80ZM80 172h60v20H80Z"
	symConfig = "M76 122h54v16H76ZM162 122h18v16h-18ZM130 130a16 16 0 1 0 32 0a16 16 0 1 0-32 0ZM126 162h54v16h-54ZM76 162h18v16H76ZM94 170a16 16 0 1 0 32 0a16 16 0 1 0-32 0Z"
	symData   = "M78 124c0-12 22-20 50-20s50 8 50 20-22 20-50 20-50-8-50-20ZM78 152c10 8 28 12 50 12s40-4 50-12v14c0 12-22 22-50 22s-50-10-50-22Z"
	symCI     = "M104 110l66 40-66 40Z"
	symBuild  = "M116 106h24v36h26l-38 46-38-46h26Z"
	symAssets = "M76 190l36-58 22 34 14-18 32 42Z"
	symI18n   = "M116 108h24l34 84h-25l-6-17h-30l-6 17H82ZM128 136l-8 21h16Z"
	symStyles = "M128 104c22 30 38 48 38 66a38 38 0 0 1-76 0c0-18 16-36 38-66Z"
	symUI     = "M78 110h100v24H78ZM78 142h40v48H78ZM128 142h50v48h-50Z"
	symTool   = "M165 118l-20 19-12-12 19-20a32 32 0 0 0-41 42l-32 32a12 12 0 0 0 17 17l32-32a32 32 0 0 0 42-41Z"
	symHook   = "M140 104v56a14 14 0 0 1-28 0h10l-21-32-21 32h10a36 36 0 0 0 76 0v-56Z"
	symType   = "M84 108h88v22h-32v60h-24v-60H84Z"
	symWeb    = "M76 108h104v62H76ZM112 176h32v14h-32Z"
	symServer = "M78 106h100v38H78ZM78 152h100v38H78ZM92 120h14v10H92ZM92 166h14v10H92Z"
	symLock   = "M88 140h80v50H88ZM100 140v-12a28 28 0 0 1 56 0v12h-18v-12a10 10 0 0 0-20 0v12Z"
	symBox    = "M78 108h100v26H78ZM86 140h84v50H86ZM112 150h32v12h-32Z"
	symPlugin = "M86 126h28a16 16 0 1 1 28 0h28v28a16 16 0 1 1 0 28v8H86Z"
	symFlask  = "M112 104h32v12h-6v26l36 48H82l36-48v-26h-6Z"
	symBolt   = "M140 102 92 156h32l-10 38 50-58h-34Z"
	symClock  = "M128 104a44 44 0 1 0 0 88a44 44 0 1 0 0-88ZM128 120a28 28 0 1 1 0 56a28 28 0 1 1 0-56ZM120 128h14v18h16v14h-30Z"
	symCloud  = "M96 188a30 30 0 0 1-4-60 38 38 0 0 1 72-8 34 34 0 0 1 2 68Z"
	symStar   = "M128 102l14 30 33 4-24 23 6 33-29-16-29 16 6-33-24-23 33-4Z"
	symPhone  = "M98 104h60v86H98ZM110 116h36v52h-36Z"
)

// folderIcons 是一种目录的两个图标，sym 是抠在上面的符号，空的是普通的文件夹。
type folderIcons struct{ closed, open *wui.SVG }

func newFolderIcons(sym string) folderIcons {
	svg := func(under, body string) *wui.SVG {
		return wui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" fill="currentColor">` + under +
			`<path fill-rule="evenodd" d="` + body + sym + `"/></svg>`))
	}
	return folderIcons{svg("", folderClosed), svg(folderBackOpen, folderFrontOpen)}
}

// folderKind 是一种目录的颜色和图标。
type folderKind struct {
	tint  tint
	icons folderIcons
}

// plainFolder 是不在表里的目录：普通的文件夹。
var plainFolder = newFolderIcons("")

// folderKinds 是常见目录名（小写）的颜色和符号；不在表里的是灰的普通文件夹。node_modules 那组记成 tintMuted，
// 画出来和不在表里的一样，记着是为了别的规则不给它们上色。bin 算脚本（绿）：被忽略的构建输出整个灰，
// 这里不用管。
var folderKinds = map[string]folderKind{}

func init() {
	for _, g := range []struct {
		tint  tint
		sym   string
		names []string
	}{
		{tintBlue, symCode, []string{"src", "source", "lib", "internal", "pkg", "cmd", "app", "core", "api", "handlers", "routes", "controllers"}},
		{tintBlue, symServer, []string{"server", "servers", "backend", "services"}},
		{tintBlue, symBox, []string{"packages", "modules", "crates", "libs", "workspaces"}},
		{tintBlue, symType, []string{"types", "typings", "@types", "interfaces", "schemas", "dto"}},
		{tintGreen, symTest, []string{"test", "tests", "spec", "specs", "__tests__", "e2e"}},
		{tintGreen, symFlask, []string{"testdata", "fixtures", "mocks", "__mocks__", "stubs"}},
		{tintGreen, symScript, []string{"scripts", "tools", "bin"}},
		{tintGreen, symPhone, []string{"android", "ios", "mobile"}},
		{tintKhaki, symDocs, []string{"docs", "doc", "documentation", "wiki"}},
		{tintKhaki, symConfig, []string{"config", "configs", "conf", "settings", ".config"}},
		{tintKhaki, symData, []string{"migrations", "db", "database", "sql", "models", "data", "datasets"}},
		{tintSlate, symCI, []string{".github", ".gitlab", ".circleci", "ci"}},
		{tintSlate, symConfig, []string{".vscode", ".idea", ".claude"}},
		{tintSlate, symClock, []string{"logs", "log", "tmp", "temp", "cache", ".cache"}},
		{tintMuted, "", []string{"node_modules", "vendor", "third_party", "deps", ".cargo", ".venv", "venv"}},
		{tintOrange, symBuild, []string{"build", "dist", "out", "target", "obj", ".next", ".nuxt"}},
		{tintOrange, symBolt, []string{"jobs", "workers", "tasks", "events", "queues", "bench", "benchmarks", "perf"}},
		{tintPink, symAssets, []string{"assets", "images", "img", "public", "static", "media", "fonts", "icons", "resources", "res"}},
		{tintPurple, symI18n, []string{"i18n", "locales", "lang", "translations"}},
		{tintPurple, symStyles, []string{"styles", "css", "themes"}},
		{tintPurple, symPlugin, []string{"plugins", "extensions", "addons", "integrations", "middleware"}},
		{tintPurple, symData, []string{"store", "stores", "state", "redux"}},
		{tintCyan, symUI, []string{"ui", "components", "views", "pages", "widgets", "layouts", "templates"}},
		{tintCyan, symWeb, []string{"web", "www", "client", "frontend"}},
		{tintCyan, symHook, []string{"hooks", "composables", ".husky"}},
		{tintCyan, symCloud, []string{"deploy", "deployment", "infra", "infrastructure", "k8s", "kubernetes", "terraform", "helm", "docker", ".docker"}},
		{tintYellow, symTool, []string{"utils", "util", "utilities", "helpers", "common", "shared"}},
		{tintYellow, symStar, []string{"examples", "example", "samples", "demo", "demos", "playground"}},
		{tintRed, symLock, []string{"auth", "security", "secrets", "certs", "keys"}},
	} {
		k := folderKind{g.tint, newFolderIcons(g.sym)}
		for _, n := range g.names {
			folderKinds[n] = k
		}
	}
}

// folderKindOf 是目录 dirs（合并行的每一段，最深的在最后）的颜色和符号：从最深的一段往上找第一个在表里的名字。
// ok 为要上色；node_modules 那组和没找到的不上，图标是普通的文件夹。
func folderKindOf(dirs []string) (k folderKind, ok bool) {
	for i := len(dirs) - 1; i >= 0; i-- {
		if k, ok = folderKinds[strings.ToLower(path.Base(dirs[i]))]; ok {
			return k, !k.tint.faint
		}
	}
	return folderKind{icons: plainFolder}, false
}

// folderColor 是彩色底块风格里目录的颜色：表里有的按名字（folderKindOf），没有的灰；被忽略的更淡；on（实心选中块上）是 AccentText。
func folderColor(t *wui.Theme, dirs []string, ignored, on bool) wui.Color {
	switch {
	case on && ignored:
		return t.AccentText.Alpha(0.75)
	case on:
		return t.AccentText
	case ignored:
		return t.TextMuted.Alpha(0.5)
	}
	if k, ok := folderKindOf(dirs); ok {
		return k.tint.color(t)
	}
	return t.TextMuted.Alpha(0.8)
}

// folderGlyph 画文件树里目录的图标（iconTool 见方）：彩色底块风格是按名字着色、带用途符号的实心夹（folderKinds），
// 线形是灰的线框夹（被忽略的更淡）。dirs 是合并行的每一段（不合并就是它自己），open 为展开着，on 为画在实心选中块上。
func folderGlyph(a *app.App, c *wui.Context, dirs []string, open, ignored, on bool) wui.Element {
	t := c.Theme()
	if a.St.Settings.TreeIconsPlain {
		ic, col := iconFolder, t.TextMuted
		if open {
			ic = iconFolderOpen
		}
		switch {
		case on && ignored:
			col = t.AccentText.Alpha(0.75)
		case on:
			col = t.AccentText
		case ignored:
			col = t.TextMuted.Alpha(0.6)
		}
		return wui.Icon(c, ic).FontSize(iconTool).TextColor(col)
	}
	k, _ := folderKindOf(dirs)
	ic := k.icons.closed
	if open {
		ic = k.icons.open
	}
	return wui.Icon(c, ic).FontSize(iconTool).TextColor(folderColor(t, dirs, ignored, on))
}
