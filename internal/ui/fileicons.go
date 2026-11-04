package ui

import (
	"math"
	"path"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
)

// 文件类型图标：文件树、编辑器标签、快速打开、搜索结果、面包屑都用 fileGlyph。默认是彩色底块：
// 图标见方的圆角块填上类别色的淡色，上面是同色的 Lucide 线性图标，代码文件换成语言的两三个等宽字母（Go、Py、TS……，不描边）；
// 设置里的"图标风格：线形"是单色线性图标（代码文件是描边圆角方块里的字母徽章，Zed / Seti 那一路）。
// 颜色按类别一组十来个（代码按语言取 GitHub linguist 的主色、压到和强调色差不多的饱和度，深色主题亮一档），
// 不是每种语言一个色。目录是实心的文件夹，颜色按目录名查表（folderTint）。不用系统的 Finder 图标（mygo.Shell.FileIcon）：
// 源码文件全是白纸、位图、和线性图标不搭；也不用各家的 logo（商标、多色，和线性图标不搭）。

var (
	iconCodeXML   = icon(`<path d="m18 16 4-4-4-4"/><path d="m6 8-4 4 4 4"/><path d="m14.5 4-5 16"/>`)
	iconPalette   = icon(`<path d="M12 22a1 1 0 0 1 0-20 10 9 0 0 1 10 9 5 5 0 0 1-5 5h-2.25a1.75 1.75 0 0 0-1.4 2.8l.3.4a1.75 1.75 0 0 1-1.4 2.8z"/><circle cx="13.5" cy="6.5" r=".5" fill="currentColor"/><circle cx="17.5" cy="10.5" r=".5" fill="currentColor"/><circle cx="6.5" cy="12.5" r=".5" fill="currentColor"/><circle cx="8.5" cy="7.5" r=".5" fill="currentColor"/>`)
	iconBraces    = icon(`<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1"/><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1"/>`)
	iconLock      = icon(`<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>`)
	iconFileText  = icon(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>`) // 外框和 iconFile 一样
	iconBook      = icon(`<path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/>`)
	iconScale     = icon(`<path d="M12 3v18"/><path d="m19 8 3 8a5 5 0 0 1-6 0zV7"/><path d="M3 7h1a17 17 0 0 0 8-2 17 17 0 0 0 8 2h1"/><path d="m5 8 3 8a5 5 0 0 1-6 0zV7"/><path d="M7 21h10"/>`)
	iconType      = icon(`<path d="M12 4v16"/><path d="M4 7V5a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v2"/><path d="M9 20h6"/>`)
	iconMusic     = icon(`<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>`)
	iconFilm      = icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M7 3v18"/><path d="M3 7.5h4"/><path d="M3 12h18"/><path d="M3 16.5h4"/><path d="M17 3v18"/><path d="M17 7.5h4"/><path d="M17 16.5h4"/>`)
	iconContainer = icon(`<path d="M22 7.7c0-.6-.4-1.2-.8-1.5l-6.3-3.9a1.72 1.72 0 0 0-1.7 0l-10.3 6c-.5.2-.9.8-.9 1.4v6.6c0 .5.4 1.2.8 1.5l6.3 3.9a1.72 1.72 0 0 0 1.7 0l10.3-6c.5-.3.9-1 .9-1.5Z"/><path d="M10 21.9V14L2.1 9.1"/><path d="m10 14 11.9-6.9"/><path d="M14 19.8v-8.1"/><path d="M18 17.5V9.4"/>`)
	iconHammer    = icon(`<path d="m15 12-9.373 9.373a1 1 0 0 1-3.001-3L12 9"/><path d="m18 15 4-4"/><path d="m21.5 11.5-1.914-1.914A2 2 0 0 1 19 8.172v-.344a2 2 0 0 0-.586-1.414l-1.657-1.657A6 6 0 0 0 12.516 3H9l1.243 1.243A6 6 0 0 1 12 8.485V10l2 2h1.172a2 2 0 0 1 1.414.586L18.5 14.5"/>`)
	iconSettings  = icon(`<path d="M14 17H5"/><path d="M19 7h-9"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>`) // settings-2
	iconPackage   = icon(`<path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z"/><path d="M12 22V12"/><polyline points="3.29 7 12 12 20.71 7"/><path d="m7.5 4.27 9 5.15"/>`)
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

// fileKind 是一类文件的图标：badge 不空时画这几个字母的徽章（代码文件，按语言），icon 是线性图标（代码文件也记着 iconCode，
// 是它们的类别）。
type fileKind struct {
	icon  *wui.SVG
	tint  tint
	badge string
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
	// 代码按语言一个徽章；jsx / tsx 跟 JS / TS、h 跟 C、hpp 跟 C++，颜色还是原来那一类
	lang := func(badge string, k tint, exts ...string) { add(fileKind{iconCode, k, badge}, exts...) }
	lang("Go", tintCyan, "go")
	lang("Dt", tintCyan, "dart")
	lang("JS", tintCyan, "jsx")
	lang("Py", tintBlue, "py", "pyi")
	lang("Lua", tintBlue, "lua")
	lang("TS", tintBlue, "ts", "tsx", "mts", "cts")
	lang("Rs", tintOrange, "rs")
	lang("Sw", tintOrange, "swift")
	lang("Jv", tintOrange, "java")
	lang("Sc", tintOrange, "scala")
	lang("Kt", tintPurple, "kt", "kts")
	lang("php", tintPurple, "php")
	lang("Rb", tintRed, "rb")
	lang("C", tintSlate, "c", "h")
	lang("C++", tintSlate, "cc", "cpp", "cxx", "hpp", "hh")
	lang("OC", tintSlate, "m", "mm")
	lang("C#", tintSlate, "cs")
	lang("Zig", tintSlate, "zig")
	lang("JS", tintYellow, "js", "mjs", "cjs")
	lang("Vue", tintGreen, "vue")
	lang("Sv", tintGreen, "svelte")
	lang("SQL", tintKhaki, "sql")
	lang("Pb", tintKhaki, "proto")
	lang("GQL", tintKhaki, "graphql")
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
// 灰的那类 tileFaint）；上面的线性图标 size × tileIcon（取整到 2 倍屏的整像素居中），字母不描边，两个字母 size × tileFont2、
// 三个 size × tileFont3。在实心选中块上图形是 AccentText、底块是它的 tileOnAccent。
const (
	tileRadius    = 0.22
	tileIcon      = 0.72
	tileFont2     = 0.5
	tileFont3     = 0.42
	tileAlpha     = 0.16
	tileAlphaDark = 0.22
	tileFaint     = 0.10
	tileOnAccent  = 0.22
)

// 文件树（iconTool 那档：文件树、打开的编辑器）里的彩色图标是"安静"的（#20）：一屏几十个同色底块太重，
// 所以不画底块，图形照旧按类别取色、往次级字色混 quietMix（饱和度和亮度都降下来），字母徽章不用粗体。
// 类别还是靠颜色和图形分得开；标签、快速打开、面包屑这些一行一两个的地方（iconRow）照旧画底块。
const (
	quietMix    = 0.35
	quietWeight = 600
)

// glyph 是一个类型图标怎么画（彩色底块）：size 见方的底块（圆角 radius，颜色 tile），上面 ink 色的线性图标（icon 大）
// 或字母（font 大、字距 spacing）。
type glyph struct {
	kind                fileKind
	size, radius        float32
	tile, ink           wui.Color
	icon, font, spacing float32
	weight              int
}

// glyphOf 是文件 name 在主题 t 下 size 见方的彩色底块：on 为画在实心选中块上，dim 为灰显的行（被忽略的、没初始化的子模块）。
func glyphOf(t *wui.Theme, name string, on, dim bool, size float32) glyph {
	k := fileKindOf(name)
	g := glyph{kind: k, size: size, radius: size * tileRadius, ink: k.tint.color(t), spacing: -0.25, weight: 700}
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
	g.font = size * tileFont2
	if len(k.badge) > 2 {
		g.font, g.spacing = size*tileFont3, -0.5
	}
	return g
}

// quietGlyphOf 是文件树里的彩色图标（见 quietMix）：glyphOf 去掉底块，图形的类别色往次级字色混一些；
// 选中块上、灰显的行和 glyphOf 一样（本来就是单色的）。
func quietGlyphOf(t *wui.Theme, name string, on, dim bool, size float32) glyph {
	g := glyphOf(t, name, on, dim, size)
	g.tile, g.weight = wui.Transparent, quietWeight
	if !on && !dim && g.kind.tint.of == nil {
		g.ink = g.ink.Mix(t.TextMuted, quietMix)
	}
	return g
}

// 字母徽章（"图标风格：线形"）：图标见方的格子里一个描边的圆角方块，和 Lucide 图标的外框（24 格里 3–21）差不多大；
// 字母是等宽粗体。三个字母的放不下：方块横着放宽到 wide（两边各出格子 0.5，文件名还是对齐的），字小一档、字距收紧。
// 两套尺寸：iconRow（标签、快速打开、面包屑、搜索结果）和 iconTool（文件树），按比例放大。
type badgeDims struct{ box, wide, radius, stroke, font, font3 float32 }

var (
	badgeRow  = badgeDims{box: 11.5, wide: 14, radius: 3, stroke: 1.25, font: 7.5, font3: 6.5}
	badgeTool = badgeDims{box: 14, wide: 17, radius: 3.5, stroke: 1.5, font: 9, font3: 8}
)

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
		return plainGlyph(c, fileKindOf(name), color, size)
	}
	g := glyphOf(t, name, on, dim, size)
	if size >= iconTool {
		g = quietGlyphOf(t, name, on, dim, size)
	}
	return wui.Row(c).Size(size, size).Shrink(0).Radius(g.radius).Background(g.tile).
		AlignItems(wui.Center).Justify(wui.Center).Children(func() {
		if g.kind.badge == "" {
			wui.Icon(c, g.kind.icon).FontSize(g.icon).TextColor(g.ink).Shrink(0)
			return
		}
		// 字母是装饰：读屏读后面的文件名
		wui.Text(c, g.kind.badge).Font(mono()).FontSize(g.font).FontWeight(g.weight).LetterSpacing(g.spacing).
			TextColor(g.ink).SingleLine().Shrink(0).Role(wui.RoleNone)
	})
}

// plainGlyph 是线形的类型图标：代码文件是描边的字母徽章，别的是 color 色的线性图标。
func plainGlyph(c *wui.Context, k fileKind, color wui.Color, size float32) wui.Element {
	if k.badge == "" {
		return wui.Icon(c, k.icon).FontSize(size).TextColor(color)
	}
	d := badgeRow
	if size >= iconTool {
		d = badgeTool
	}
	w, font, spacing := d.box, d.font, float32(-0.25)
	if len(k.badge) > 2 {
		w, font, spacing = d.wide, d.font3, -0.5
	}
	return wui.Row(c).Size(size, size).Shrink(0).AlignItems(wui.Center).Justify(wui.Center).Children(func() {
		wui.Row(c).Size(w, d.box).Shrink(0).Radius(d.radius).Border(d.stroke, color).
			AlignItems(wui.Center).Justify(wui.Center).Children(func() {
			// 徽章的字母是装饰：读屏读后面的文件名
			wui.Text(c, k.badge).Font(mono()).FontSize(font).FontWeight(700).LetterSpacing(spacing).
				TextColor(color).SingleLine().Shrink(0).Role(wui.RoleNone)
		})
	})
}

// 实心的图标：填 currentColor、不描边（彩色底块风格里文件树的目录）。轮廓取自 Lucide 的 folder / folder-open，
// 打开的是后片半透明、前片实心。
func solid(body string) *wui.SVG {
	return wui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" stroke="none">` + body + `</svg>`))
}

var (
	iconFolderFill     = solid(`<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`)
	iconFolderOpenFill = solid(`<path opacity=".5" d="M4 20a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2H9.24a2 2 0 0 0-1.74 1.1Z"/>` +
		`<path d="M7.5 11.1A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-1.77-1.07Z"/>`)
)

// folderTints 是常见目录名（小写）的颜色；不在表里的是灰的实心夹。node_modules 那组记成 tintMuted，画出来和不在表里的一样灰
// （记着是为了别的规则不给它们上色）。bin 算脚本（绿）：被忽略的构建输出整个灰，轮不到这张表。
var folderTints = map[string]tint{}

func init() {
	for k, names := range map[*tint][]string{
		&tintBlue:   {"src", "lib", "internal", "pkg", "cmd", "app", "core", "api", "server", "services", "handlers", "routes"},
		&tintGreen:  {"test", "tests", "spec", "specs", "__tests__", "testdata", "e2e", "scripts", "tools", "bin"},
		&tintKhaki:  {"docs", "doc", "documentation", "wiki", "config", "configs", "conf", "settings", ".config", "migrations", "db", "database", "sql", "models"},
		&tintSlate:  {".github", ".gitlab", ".circleci", "ci", ".vscode", ".idea", ".claude"},
		&tintMuted:  {"node_modules", "vendor", "third_party", "deps", ".cargo", ".venv", "venv"},
		&tintOrange: {"build", "dist", "out", "target", "obj", ".next", ".nuxt"},
		&tintPink:   {"assets", "images", "img", "public", "static", "media", "fonts", "icons"},
		&tintPurple: {"i18n", "locales", "lang", "translations", "styles", "css", "themes"},
		&tintCyan:   {"ui", "components", "views", "pages", "widgets", "layouts"},
	} {
		for _, n := range names {
			folderTints[n] = *k
		}
	}
}

// folderTint 是目录 dirs（合并行的每一段，最深的在最后）的颜色：从最深的一段往上找第一个在表里的名字（小写）；
// 都不在表里（或是 node_modules 那组）ok 为假。
func folderTint(dirs []string) (k tint, ok bool) {
	for i := len(dirs) - 1; i >= 0; i-- {
		if k, ok = folderTints[strings.ToLower(path.Base(dirs[i]))]; ok {
			return k, !k.faint
		}
	}
	return tint{}, false
}

// folderColor 是彩色底块风格里目录的颜色：表里有的按名字（folderTint），没有的灰；被忽略的更淡；on（实心选中块上）是 AccentText。
func folderColor(t *wui.Theme, dirs []string, ignored, on bool) wui.Color {
	switch {
	case on && ignored:
		return t.AccentText.Alpha(0.75)
	case on:
		return t.AccentText
	case ignored:
		return t.TextMuted.Alpha(0.5)
	}
	if k, ok := folderTint(dirs); ok {
		return k.color(t)
	}
	return t.TextMuted.Alpha(0.8)
}

// folderGlyph 画文件树里目录的图标（iconTool 见方）：彩色底块风格是按名字着色的实心夹（folderColor），
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
	ic := iconFolderFill
	if open {
		ic = iconFolderOpenFill
	}
	return wui.Icon(c, ic).FontSize(iconTool).TextColor(folderColor(t, dirs, ignored, on))
}
