package ui

import (
	"cmp"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// shotBar 是截图里红绿灯占的位置：和真的窗口一样（启动日志"标题栏：窗口按钮 {Height:38 Left:72}"），
// 截图里不画红绿灯，左边空着。
var shotBar = wui.TitleBar{Height: barHeight, Left: 72}

// TestScreenshot 把整个窗口渲染成 PNG（WORKBENCH_SHOT=目录 时才跑），用来在没有屏幕权限时看界面。
// 用用户的真实工作区，只读：git status / diff / log、ls-files、读文件，终端里敲 echo 和 ls。
//
//	light.png / dark.png  文件栏 + 编辑器标签 + 历史 + 终端，默认的柔和底
//	flat.png              同一个画面，窗口底选纯色
//	diff.png              Git 栏 + Diff 标签 + 输出
//	search.png            搜索页（搜 TODO，第一处在预览标签里打开）
//	palette.png           快速打开（readme）
//	commands.png          命令面板（>git）
//
// 要有一个仓库有改动；用户的仓库都干净时用 WORKBENCH_SHOT_WS=另一个工作区（比如那几个仓库在临时目录里的克隆）。
func TestScreenshot(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	ws := cmp.Or(os.Getenv("WORKBENCH_SHOT_WS"), workspace.DefaultPath)
	if _, err := os.Stat(ws); err != nil {
		t.Skip(err)
	}
	a, tt, wait := newUI(t, ws, t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.Start()
	wait("读完所有仓库", loaded(a))

	// 选第一个有改动的仓库：文件树展开到它的第一个改动文件并在编辑器里打开，再开它的 Diff 标签
	var r *app.Repo
	for i, x := range a.Repos {
		if x.Status.Count > 0 {
			a.SelectRepo(i)
			r = x
			break
		}
	}
	if r == nil {
		t.Skip("工作区里没有有改动的仓库")
	}
	ch := r.Status.Changes[0]
	a.OpenDiff(r, ch, true)
	var d *docs.Doc
	for _, c := range r.Status.Changes {
		if c.Code != 'D' {
			d = a.OpenFile(r, c.Path, true)
			break
		}
	}
	a.EnsureFiles(r)
	wait("文件、历史和标签", func() bool {
		for _, d := range a.Docs {
			if d.Loading {
				return false
			}
		}
		return r.Files.Loaded && r.Hist.Loaded
	})
	if d != nil {
		a.Reveal(r, d.Rel)
		wait("文件树展开", func() bool { return a.FileSel >= 0 })
	}
	// 在终端里敲一条命令，让截图里有内容；等提示符画出来再敲，免得上面多一行预输入的回显
	if _, _, p := a.ActivePane(); p != nil && p.Term != nil {
		term := p.Term
		testutil.WaitTermReady(t, tt, term, 400*time.Millisecond, 15*time.Second)
		hdr, ok := tt.Find("终端 · " + r.Name)
		if !ok {
			t.Fatalf("底栏没有终端标签；texts=%q", tt.Texts())
		}
		tt.ClickAt(hdr.X+40, hdr.Y+hdr.H+60)
		tt.Type("echo 中文测试 && ls")
		tt.Key(0, wui.KeyEnter)
		testutil.WaitTerm(t, tt, term, "\n中文测试", 15*time.Second)
		// ls 的输出紧跟着到，再推半秒帧让它和新提示符都画出来
		for range 25 {
			time.Sleep(20 * time.Millisecond)
			tt.Frame()
		}
	}
	shot := func(name string) {
		t.Helper()
		tt.Frame()
		tt.Advance(500 * time.Millisecond)
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		t.Logf("写了 %s", f.Name())
	}
	shot("light.png")
	tt.SetDark(true)
	shot("dark.png")
	tt.SetDark(false)
	a.St.Settings.Backdrop = "flat"
	shot("flat.png")
	a.St.Settings.Backdrop = ""

	// Git 栏 + Diff 标签 + 输出
	a.Exec("view.git")
	a.SelectDoc(0)
	a.Log(r, "git status", "（截图用的示例输出）", false)
	a.BottomTab = app.BottomOutput
	shot("diff.png")

	// 侧栏搜索页：搜 TODO，选中第一处（在预览标签里打开）
	a.Exec("view.search")
	a.Search.Query.Text = "TODO"
	a.SearchNow()
	wait("搜索", func() bool { return a.Search.Done && !a.Search.Running })
	if len(a.Search.Rows) > 1 {
		a.Search.Sel = 1
		a.OpenSearchMatch(a.Search.Rows[1].Match, false)
		wait("打开结果", func() bool { d := a.CurrentDoc(); return d != nil && !d.Loading })
	}
	a.BottomTab = app.BottomTerminal
	shot("search.png")

	// 快速打开和命令面板
	a.OpenPalette("")
	wait("所有仓库的文件列表", func() bool {
		for _, r := range a.Repos {
			if !r.Files.Loaded {
				return false
			}
		}
		return true
	})
	a.Palette.Text = "readme"
	shot("palette.png")
	a.Palette.Text = ">git"
	shot("commands.png")
	a.ClosePalette()
}

// TestScreenshotPreviewSettings 是预览和设置的截图（WORKBENCH_SHOT=目录 时才跑），在临时仓库里做，不碰用户的工作区：
//
//	preview.png    Markdown 预览（表格、本地图片、代码块、任务列表）
//	tree-edit.png  文件树里行内重命名（F2）
//	settings.png   设置窗口的"编辑器"页
//	en.png         英文界面
func TestScreenshotPreviewSettings(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	t.Cleanup(func() { i18n.Set("") })
	r0 := testutil.NewRepo(t, "release-notes")
	r0.Write("docs/checklist.md", "# 发布检查清单\n\n这页演示 Markdown 预览：表格、图片、代码块和链接。"+
		"点 [README](../README.md) 在编辑器里打开，点 [mygo](https://github.com/egoist/mygo) 交给浏览器。\n\n"+
		"## 本周进度\n\n| 模块 | 负责人 | 状态 |\n| --- | --- | --- |\n| 文件监听 | 甲 | 完成 |\n| Markdown 预览 | 乙 | 完成 |\n| 设置窗口 | 丙 | 进行中 |\n\n"+
		"![构建耗时](build-time.png)\n\n## 验收命令\n\n```bash\ngo vet ./... && go test ./...\n```\n\n"+
		"- [x] 外部改文件 1 秒内跟上\n- [x] 预览里图片和表格正常\n- [ ] 英文界面人工校对\n")
	r0.Write("src/main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	r0.Write("src/watch.go", "package main\n")
	r0.Write("README.md", "# release-notes\n")
	r0.Commit("初始提交")
	r0.Write("src/watch.go", "package main\n\n// 改动\n")
	img := image.NewRGBA(image.Rect(0, 0, 640, 200))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0xf5, 0xf7, 0xfa, 0xff}), image.Point{}, draw.Src)
	for i, h := range []int{60, 90, 75, 130, 110, 160, 140} {
		bar := image.Rect(40+i*80, 180-h, 90+i*80, 180)
		draw.Draw(img, bar, image.NewUniform(color.RGBA{0x3b, 0x82, 0xf6, 0xff}), image.Point{}, draw.Src)
	}
	f, err := os.Create(filepath.Join(r0.Root, "docs", "build-time.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("状态和文件树", func() bool { return idle(r)() && r.Files.Loaded })
	a.OpenFile(r, "docs/checklist.md", true)
	a.FocusDoc = true
	wait("打开", func() bool { return tt.Focused("编辑 checklist.md") })
	a.Reveal(r, "docs/checklist.md")
	tt.Key(wui.Shift|wui.Cmd, wui.KeyV)
	wait("预览", func() bool { return a.CurrentDoc().Preview && tt.HasText("进行中") })
	writeShot(t, tt, dir, "preview.png")

	a.Reveal(r, "src/main.go")
	wait("展开 src", func() bool { return tt.HasText("main.go") })
	mustClick(t, tt, "main.go")
	wait("选中 main.go", func() bool { row, ok := a.TreeRowAt(a.FileSel); return ok && row.Path == "src/main.go" })
	tt.Key(0, wui.KeyF2)
	wait("重命名输入框", func() bool { return tt.Focused("新名字") })
	tt.Type("app")
	writeShot(t, tt, dir, "tree-edit.png")
	tt.Key(0, wui.KeyEscape)
	tt.Frame()

	st := wui.NewTester(SettingsView(a), 600, 480)
	st.SetScale(2)
	st.SetLocale(i18n.Locale())
	st.Frame()
	mustClick(t, st, "编辑器")
	writeShot(t, st, dir, "settings.png")

	a.St.Settings.Lang = "en"
	a.SettingsChanged()
	tt.SetLocale(i18n.Locale())
	a.Exec("view.git")
	writeShot(t, tt, dir, "en.png")
}

func writeShot(t *testing.T, tt *wui.Tester, dir, name string) {
	t.Helper()
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	savePNG(t, filepath.Join(dir, name), tt.Image())
}

// writeMaterialShot 和 writeShot 一样，给开着通透背景（tt.SetVibrancy(true)）的窗口用：透明的材质那块铺上合成的毛玻璃。
func writeMaterialShot(t *testing.T, tt *wui.Tester, dir, name string, dark bool) {
	t.Helper()
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	savePNG(t, filepath.Join(dir, name), material(tt.Image(), dark))
}

func savePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("写了 %s", path)
}

// material 把通透背景开着的截图（材质那块是透明像素）铺到一块合成的"毛玻璃"上，和设计样稿的画法一样：
// 糊开的彩色壁纸（几团径向渐变）上盖 84% 的材质色。Tester 和 WORKBENCH_CAPTURE 都画不出系统的材质，
// 这只是近似，真窗口里透出来的是桌面，要手测。
func material(img *image.RGBA, dark bool) *image.RGBA {
	type blob struct {
		x, y, rx, ry float64 // 中心是窗口宽高的比例，半径按 1440 × 880 的窗口算
		c            color.RGBA
	}
	base, tint := color.RGBA{0xdf, 0xe3, 0xea, 255}, color.RGBA{242, 242, 245, 255}
	blobs := []blob{ // 底下的先画
		{0.30, 0.75, 500, 500, color.RGBA{0x9f, 0xe0, 0xc8, 255}},
		{0.55, 0.92, 640, 520, color.RGBA{0xf0, 0xa3, 0xc8, 255}},
		{0.88, 0.22, 600, 460, color.RGBA{0xff, 0xc4, 0x8a, 255}},
		{0.12, 0.18, 520, 420, color.RGBA{0x8f, 0xb6, 0xff, 255}},
	}
	if dark {
		base, tint = color.RGBA{0x24, 0x26, 0x2c, 255}, color.RGBA{30, 30, 34, 255}
		blobs = []blob{
			{0.55, 0.92, 640, 520, color.RGBA{0x7a, 0x3a, 0x62, 255}},
			{0.88, 0.22, 600, 460, color.RGBA{0x8a, 0x4e, 0x2a, 255}},
			{0.12, 0.18, 520, 420, color.RGBA{0x2d, 0x4f, 0x9a, 255}},
		}
	}
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	sx, sy := w/1440, h/880
	mix := func(dst *[3]float64, c color.RGBA, a float64) {
		dst[0] += (float64(c.R) - dst[0]) * a
		dst[1] += (float64(c.G) - dst[1]) * a
		dst[2] += (float64(c.B) - dst[2]) * a
	}
	out := image.NewRGBA(b)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			px := [3]float64{float64(base.R), float64(base.G), float64(base.B)}
			for _, bl := range blobs {
				dx, dy := (float64(x)-bl.x*w)/(bl.rx*sx), (float64(y)-bl.y*h)/(bl.ry*sy)
				if d := dx*dx + dy*dy; d < 0.49 { // 渐变到半径的 70% 透明
					mix(&px, bl.c, 1-math.Sqrt(d)/0.7)
				}
			}
			mix(&px, tint, 0.84)
			// 截图是预乘过的：结果 = 截图 + 材质 × (1 − 截图的不透明度)
			s := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			k := 1 - float64(s.A)/255
			out.SetRGBA(b.Min.X+x, b.Min.Y+y, color.RGBA{
				uint8(float64(s.R) + px[0]*k + 0.5), uint8(float64(s.G) + px[1]*k + 0.5), uint8(float64(s.B) + px[2]*k + 0.5), 255,
			})
		}
	}
	return out
}

// TestScreenshotGitBar 是 Git 栏改成"所有仓库同时列出"之后的截图（WORKBENCH_SHOT=目录 时才跑），在临时仓库里做：
//
//	gitbar.png       Git 栏：四个仓库一段一段（已暂存 + 未暂存 + 未跟踪、未暂存 + 收起的未跟踪、合并冲突、干净），
//	                 段头右边是改动数徽标（干净的是绿色圈中勾）、↓ / ↑ 灰字和 24 见方的拉取 / 推送；未暂存 / 已暂存组头是橙色铅笔 /
//	                 绿色勾选框加个数，分组标题上的 + / − / 中止合并图标，选中一项在预览 Diff 标签里看，有已暂存改动的那段提交框展开
//	history-all.png  右栏"所有仓库"：置顶有改动的仓库，各仓库的提交按时间交错、带仓库胶囊，展开一个提交
func TestScreenshotGitBar(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now().Truncate(time.Minute)
	ago := func(h float64) time.Time { return now.Add(-time.Duration(h * float64(time.Hour))) }

	api := testutil.NewRepo(t, "api-server")
	api.Write("go.mod", "module example.com/api\n\ngo 1.25\n")
	api.Write("main.go", "package main\n\nfunc main() {\n\tserve(\":8080\")\n}\n")
	api.CommitAt("初始化服务骨架", ago(70))
	api.Write("handlers/user.go", "package handlers\n\n// ListUsers 列出用户。\nfunc ListUsers() []User {\n\treturn db.Users()\n}\n")
	api.CommitAt("加用户列表接口", ago(46))
	api.Write("config.yaml", "port: 8080\ntimeout: 10s\n")
	api.CommitAt("修复登录超时", ago(20))
	api.Write("handlers/user.go", "package handlers\n\n// ListUsers 列出用户，一页 page 条。\nfunc ListUsers(page int) []User {\n\treturn db.Users().Page(page, 50)\n}\n")
	api.Write("handlers/auth.go", "package handlers\n\n// Refresh 换一个新的访问令牌。\nfunc Refresh(token string) (string, error) {\n\treturn sign(token)\n}\n")
	api.Git("add", "handlers")
	api.Write("config.yaml", "port: 8080\ntimeout: 30s\n")
	api.Write("scripts/migrate.sh", "#!/bin/sh\n")

	web := testutil.NewRepo(t, "web-client")
	web.Write("src/App.tsx", "export function App() {\n  return <Users />\n}\n")
	web.Write("src/api.ts", "export const base = '/api'\n")
	web.CommitAt("前端脚手架", ago(66))
	web.Write("src/Users.tsx", "export function Users() {\n  return <ul />\n}\n")
	web.CommitAt("用户列表页", ago(30))
	web.Write("src/api.ts", "export const base = '/api/v2'\n")
	web.CommitAt("接口换到 v2", ago(5))
	web.Write("src/App.tsx", "export function App() {\n  return <Layout><Users /></Layout>\n}\n")
	web.Write("src/api.ts", "export const base = '/api/v2'\nexport const pageSize = 50\n")
	web.Write("src/components/Badge.tsx", "export const Badge = () => null\n")

	infra := testutil.NewRepo(t, "infra")
	infra.Write("deploy.yaml", "replicas: 2\nimage: api:1.0\n")
	infra.CommitAt("部署配置", ago(60))
	infra.Git("switch", "-q", "-c", "scale-up")
	infra.Write("deploy.yaml", "replicas: 4\nimage: api:1.0\n")
	infra.CommitAt("扩到 4 个副本", ago(26))
	infra.Git("switch", "-q", "main")
	infra.Write("deploy.yaml", "replicas: 3\nimage: api:1.1\n")
	infra.CommitAt("升级镜像到 1.1", ago(9))
	if out, err := git.Run(infra.Root, "merge", "-q", "scale-up"); err == nil {
		t.Fatalf("infra 应该合并冲突: %s", out)
	}

	docsRepo := testutil.NewRepo(t, "docs")
	docsRepo.Write("README.md", "# 文档\n")
	docsRepo.CommitAt("文档目录", ago(52))
	docsRepo.Write("api.md", "# 接口\n\n分页参数 page。\n")
	docsRepo.CommitAt("补接口分页说明", ago(2))

	a, tt, wait := newUI(t, testutil.Workspace(t, api.Root, web.Root, infra.Root, docsRepo.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("四个仓库的状态", loaded(a))
	a.Exec("view.git")
	a.ToggleGitGroup(a.Repos[1], app.GroupUnstaged)
	r := a.Repos[0]
	r.CommitMsg = "用户列表分页，加刷新令牌接口"
	ch := r.Status.Changes[0]
	a.GitSel = slices.IndexFunc(a.GitRows(), func(cr app.ChangeRow) bool {
		return cr.Kind == app.RowChange && cr.Repo == r && cr.Change.Path == ch.Path && cr.Change.Kind == ch.Kind
	})
	a.OpenDiff(r, ch, false)
	wait("diff 和历史", func() bool { d := a.CurrentDoc(); return d != nil && !d.Loading && r.Hist.Loaded })
	writeShot(t, tt, dir, "gitbar.png")

	a.SetHistAll(true)
	wait("所有仓库的历史", func() bool { return tt.HasText("web-client 接口换到 v2") && a.AllHistDone() && !a.AllHistLoading() })
	mustClick(t, tt, "web-client 接口换到 v2")
	w := a.Repos[1]
	wait("提交的文件", func() bool { return w.Hist.FilesFor != "" && !w.Hist.FilesLoading })
	// Git 栏里也有 src/api.ts（工作区的改动），按行选右栏里提交的那个
	a.HistAllSel = slices.IndexFunc(a.AllHistRows(), func(row app.AllHistRow) bool {
		return row.Kind == app.AllFile && row.Repo == w && w.Hist.Files[row.File].Path == "src/api.ts"
	})
	a.OnAllHistSelected()
	wait("提交里的 diff", func() bool { d := a.CurrentDoc(); return d != nil && d.Commit != "" && !d.Loading })
	writeShot(t, tt, dir, "history-all.png")
}

// TestScreenshotHunksHistory 是 Git 功能的截图（WORKBENCH_SHOT=目录 时才跑），在临时仓库里做：
//
//	hunks.png           Git 栏（两条贮藏、合并中的仓库和"中止合并"、勾着"修改上一次提交"的提交框）+
//	                    Diff 标签里光标所在那块的"暂存这一块 / 丢弃这一块"
//	history-filter.png  右栏历史：搜索框里敲着"接口"、"未推送"的标记、展开的提交详情（作者和邮箱、完整哈希、
//	                    父提交、在 GitHub 打开）
func TestScreenshotHunksHistory(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now().Truncate(time.Minute)
	ago := func(h float64) time.Time { return now.Add(-time.Duration(h * float64(time.Hour))) }

	api := testutil.NewRepo(t, "api-server")
	user := []string{"package handlers", "", "import (", "\t\"net/http\"", "\t\"strconv\"", ")", "",
		"// ListUsers 列出用户。", "func ListUsers(w http.ResponseWriter, r *http.Request) {",
		"\tusers := db.Users()", "\twriteJSON(w, users)", "}", ""}
	for i := 1; i <= 12; i++ {
		user = append(user, "// 第 "+strconv.Itoa(i)+" 个接口的说明，保持原样。")
	}
	user = append(user, "", "// GetUser 按 id 读一个用户。", "func GetUser(w http.ResponseWriter, r *http.Request) {",
		"\tid, _ := strconv.Atoi(r.PathValue(\"id\"))", "\twriteJSON(w, db.User(id))", "}", "")
	api.Write("go.mod", "module example.com/api\n\ngo 1.25\n")
	api.Write("handlers/user.go", strings.Join(user, "\n"))
	api.CommitAt("初始化服务骨架", ago(70))
	api.Write("config.yaml", "port: 8080\ntimeout: 10s\n")
	api.Git("add", "-A")
	api.Git("commit", "-q", "-m", "加用户列表接口\n\n列表先不分页，之后再说。", "--author", "张三 <zhangsan@example.com>",
		"--date", ago(46).Format(time.RFC3339))
	api.Write("config.yaml", "port: 8080\ntimeout: 15s\n")
	api.CommitAt("修复登录超时", ago(20))
	api.Git("remote", "add", "origin", "git@github.com:example/api-server.git") // 假的地址：只用来推网页，不连网
	api.Git("update-ref", "refs/remotes/origin/main", "HEAD")                   // 当前面三个推送过了
	api.Git("branch", "-q", "--set-upstream-to=origin/main")
	api.Write("handlers/auth.go", "package handlers\n\n// Refresh 换一个新的访问令牌。\nfunc Refresh(token string) (string, error) {\n\treturn sign(token)\n}\n")
	api.CommitAt("加刷新令牌接口", ago(3))
	api.Write("config.yaml", "port: 8080\ntimeout: 30s\n")
	api.Git("stash", "push", "-q", "-m", "试一下 30 秒超时")
	api.Write("go.mod", "module example.com/api\n\ngo 1.26\n")
	api.Git("stash", "push", "-q", "-m", "调试日志")
	user[3] = "\t\"errors\"\n\t\"net/http\""
	user[len(user)-3] = "\twriteJSON(w, db.User(id)) // 找不到时是 404"
	api.Write("handlers/user.go", strings.Join(user, "\n"))

	infra := testutil.NewRepo(t, "infra")
	infra.Write("deploy.yaml", "replicas: 2\nimage: api:1.0\n")
	infra.CommitAt("部署配置", ago(60))
	infra.Git("switch", "-q", "-c", "scale-up")
	infra.Write("deploy.yaml", "replicas: 4\nimage: api:1.0\n")
	infra.CommitAt("扩到 4 个副本", ago(26))
	infra.Git("switch", "-q", "main")
	infra.Write("deploy.yaml", "replicas: 3\nimage: api:1.1\n")
	infra.CommitAt("升级镜像到 1.1", ago(9))
	if out, err := git.Run(infra.Root, "merge", "-q", "scale-up"); err == nil {
		t.Fatalf("infra 应该合并冲突: %s", out)
	}

	a, tt, wait := newUI(t, testutil.Workspace(t, api.Root, infra.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("两个仓库的状态", loaded(a))
	a.Exec("view.git")
	r := a.Repos[0]
	a.SetAmend(r, true)
	wait("上一次提交的信息", func() bool { return r.CommitMsg == "加刷新令牌接口" })
	ch := r.Status.Changes[0]
	a.GitSel = slices.IndexFunc(a.GitRows(), func(cr app.ChangeRow) bool {
		return cr.Kind == app.RowChange && cr.Repo == r && cr.Change.Path == ch.Path && cr.Change.Kind == ch.Kind
	})
	a.OpenDiff(r, ch, true)
	wait("diff 和历史", func() bool { d := diffDoc(a, ""); return d != nil && !d.Loading && r.Hist.Loaded })
	focusNewSide(t, tt)
	diffDoc(a, "").DiffSt.GoToHunk(0)
	tt.Frame()
	mustText(t, tt, "暂存这一块")
	writeShot(t, tt, dir, "hunks.png")

	mustClick(t, tt, "搜索历史")
	tt.Type("接口")
	wait("搜索", func() bool { return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 2 })
	mustClick(t, tt, "加用户列表接口")
	wait("提交的文件", func() bool { return r.Hist.Expanded != "" && !r.Hist.FilesLoading })
	mustText(t, tt, "在 GitHub 打开")
	writeShot(t, tt, dir, "history-filter.png")
}

// TestScreenshotTerminals 是终端的截图（WORKBENCH_SHOT=目录 时才跑），在临时仓库里做，shell 用 /bin/sh：
//
//	terminals.png  底栏：两个终端标签（改了名的"服务"和"终端 2"）、左右分屏（左边 git log 和查找条找"令牌"，
//	               右边从左边的目录起的 git status，指针在上面所以右上角有 ×）、分屏之间 4 pt 的材质缝（通透背景开着）
func TestScreenshotTerminals(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now().Truncate(time.Minute)
	ago := func(h float64) time.Time { return now.Add(-time.Duration(h * float64(time.Hour))) }
	api := testutil.NewRepo(t, "api-server")
	api.Write("go.mod", "module example.com/api\n\ngo 1.25\n")
	api.Write("handlers/user.go", "package handlers\n")
	api.CommitAt("初始化服务骨架", ago(30))
	api.Write("handlers/auth.go", "package handlers\n\n// Refresh 换一个新的访问令牌。\nfunc Refresh() {}\n")
	api.CommitAt("加刷新令牌接口", ago(3))
	api.Write("config.yaml", "port: 8080\n")
	api.Write("handlers/user.go", "package handlers\n\n// ListUsers 列出用户。\n")
	infra := testutil.NewRepo(t, "infra")
	infra.Write("deploy.yaml", "replicas: 2\n")
	infra.CommitAt("部署配置", ago(60))

	state := termState(t, true)
	a, tt, wait := termUI(t, testutil.Workspace(t, api.Root, infra.Root), state)
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.RightBar, a.St.BottomH = false, 400
	wait("两个仓库的状态", loaded(a))
	r := a.Repos[0]
	a.ShowSidebar(workspace.ModeGit)
	tidy := func(p *app.TermPane) { // 短一点的提示符，再清屏
		typeLine(t, tt, p, `PS1='\W $ '`, "\napi-server $")
		tt.Key(wui.Cmd, wui.KeyK)
		testutil.WaitTermGone(t, tt, p.Term, "PS1=", 15*time.Second)
	}
	p1 := readyPane(t, a, tt)
	wait("认领到 shell 的进程号", func() bool { return p1.Job() == "" })
	tidy(p1)
	typeLine(t, tt, p1, "ls", "handlers")
	typeLine(t, tt, p1, "git --no-pager log --oneline", "初始化服务骨架")

	tt.Key(wui.Cmd, wui.KeyD)
	wait("左右分屏", func() bool { return len(a.CurTermTab(r).Panes()) == 2 })
	p2 := readyPane(t, a, tt)
	tidy(p2)
	typeLine(t, tt, p2, "git status -s", "?? config.yaml")

	a.StartRenameTerm(r, a.CurTermTab(r))
	tt.Frame()
	tt.Type("服务")
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	a.NewTerminal(r, "")
	readyPane(t, a, tt)
	a.SelectTermTab(r, 0)
	a.FocusPane(r, p1)
	a.FindInTerm()
	tt.Frame()
	tt.Type("令牌")
	wait("查找", func() bool { st := p1.Term.SearchStatus(); return !st.Searching && st.Total == 1 })
	mustText(t, tt, "服务")
	mustText(t, tt, "终端 2")
	div, _ := tt.Find("拖动调整分屏大小")
	tt.Move(div.X+120, div.Y+div.H/2)
	tt.Frame()
	mustText(t, tt, "关闭这一格分屏")
	tt.SetVibrancy(true)
	writeMaterialShot(t, tt, dir, "terminals.png", false)
}

// TestScreenshotTitlebarQueue 是标题栏和操作队列的截图（WORKBENCH_SHOT=目录 时才跑），在临时仓库里做：
//
//	titlebar.png  紧凑的 38 pt 标题栏（只截这一条）：左边空着的是红绿灯的位置，‹ › 挨在一起，
//	              面包屑里类型图标挨着文件名，图标、字和红绿灯一条中线
//	queue.png     全部抓取排在 api-server 的一条慢命令后面：标题栏和 Git 栏段头的队列按钮（转圈、排队 1），
//	              状态栏左边的进度"抓取 1/3：api-server…"和停止的 ×
//
// 工作区菜单（最近的工作区）是系统菜单，Tester 和窗口截图都不画，没有截图，见 TestRecentWorkspacesUI。
func TestScreenshotTitlebarQueue(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now().Truncate(time.Minute)
	ago := func(h float64) time.Time { return now.Add(-time.Duration(h * float64(time.Hour))) }
	api := testutil.NewRepo(t, "api-server")
	api.Write("go.mod", "module example.com/api\n\ngo 1.25\n")
	api.Write("handlers/user.go", "package handlers\n\n// ListUsers 列出用户。\nfunc ListUsers() []User {\n\treturn db.Users()\n}\n")
	api.CommitAt("加用户列表接口", ago(20))
	api.Write("handlers/user.go", "package handlers\n\n// ListUsers 列出用户，一页 page 条。\nfunc ListUsers(page int) []User {\n\treturn db.Users().Page(page, 50)\n}\n")
	api.Write("config.yaml", "port: 8080\n")
	web := testutil.NewRepo(t, "web-client")
	web.Write("src/api.ts", "export const base = '/api'\n")
	web.CommitAt("前端脚手架", ago(30))
	web.Write("src/api.ts", "export const base = '/api/v2'\n")
	docsRepo := testutil.NewRepo(t, "docs")
	docsRepo.Write("README.md", "# 文档\n")
	docsRepo.CommitAt("文档目录", ago(50))

	a, tt, wait := newUI(t, testutil.Workspace(t, api.Root, web.Root, docsRepo.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("三个仓库的状态", loaded(a))
	r := a.Repos[0]
	a.OpenFile(r, "handlers/user.go", true)
	wait("user.go", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil && r.Hist.Loaded })
	writeShot(t, tt, dir, "titlebar.png")
	bar := filepath.Join(dir, "titlebar.png")
	f, err := os.Open(bar)
	if err != nil {
		t.Fatal(err)
	}
	full, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	strip := full.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(image.Rect(0, 0, full.Bounds().Dx(), barHeight*2))
	if f, err = os.Create(bar); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, strip); err != nil {
		t.Fatal(err)
	}
	f.Close()

	a.Exec("view.git")
	slowOp(a, r)
	a.FetchAll()
	tt.Frame()
	mustText(t, tt, "抓取 1/3：api-server…")
	writeShot(t, tt, dir, "queue.png")
}

// TestScreenshotHistoryGraph 渲染历史提交图的截图（WORKBENCH_SHOT=目录）：
//
//	history-graph.png  右栏历史开着"所有分支"（graphRepo）：头部是仓库名、分支和图标开关；一个提交一行、按日期分段，
//	                   feature/login、feature/search 两次合并（空心节点、标题次要色），没合并的 topic，HEAD（main）
//	                   实心点外面一圈、分支标记是强调色；"未提交的改动"是橙色铅笔加改动数，橙色虚线圈用虚线连到 HEAD；
//	                   选中展开的是"合并 feature/login"，下面的卡片里是短哈希、作者、三个按钮和它改的文件
func TestScreenshotHistoryGraph(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	repo := graphRepo(t)
	repo.Write("README.md", "加 README\n\n## 提交图\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false // 测试里不起 shell，空终端占地方
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	a.OpenFile(r, "README.md", true)
	a.ToggleHistBranches()
	wait("所有分支的历史", func() bool {
		d := a.CurrentDoc()
		return d != nil && d.Buf != nil && r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 10
	})
	tt.Frame()
	mustClick(t, tt, "合并 feature/login")
	wait("展开合并", func() bool { return r.Hist.Expanded != "" && r.Hist.FilesFor == r.Hist.Expanded })
	writeShot(t, tt, dir, "history-graph.png")
}

// TestScreenshotFileIcons 是文件类型图标的截图（WORKBENCH_SHOT=目录 时才跑），临时仓库：
//
//	file-icons.png  文件树里各种文件（编程语言是字母徽章），左浅色右深色（只截侧栏）
func TestScreenshotFileIcons(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	r0 := testutil.NewRepo(t, "file-icons")
	for _, name := range []string{"main.go", "go.mod", "go.sum", "app.py", "lib.rs", "View.swift", "server.c", "build.sh",
		"index.html", "style.css", "app.ts", "util.js", "Button.jsx", "App.vue", "config.yaml", "data.json", ".env",
		"Main.java", "app.kt", "index.php", "init.lua", "build.zig", "engine.cpp", "Program.cs", "schema.sql", "api.proto",
		"README.md", "notes.md", "LICENSE", "CHANGELOG.md", "spec.pdf", "logo.png", "Inter.woff2", "dist.tar.gz",
		"Dockerfile", "Makefile", ".gitignore", ".editorconfig", "package.json", "yarn.lock", "bin"} {
		r0.Write(name, "x\n")
	}
	r0.Commit("初始提交")
	r0.Write("main.go", "y\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), t.TempDir())
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar, a.St.RightBar = false, false
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	a.ShowSidebar(workspace.ModeFiles)
	a.EnsureFiles(r)
	wait("状态和文件树", func() bool { return idle(r)() && r.Files.Loaded && tt.HasText("Makefile") })
	a.Reveal(r, "main.go")
	wait("选中 main.go", func() bool { row, ok := a.TreeRowAt(a.FileSel); return ok && row.Path == "main.go" })
	side := func() image.Image {
		tt.Frame()
		tt.Advance(500 * time.Millisecond)
		tt.Frame()
		img := tt.Image()
		b := img.Bounds()
		return img.SubImage(image.Rect(b.Min.X, b.Min.Y, b.Min.X+int(a.St.SidebarW*2), b.Max.Y))
	}
	light := side()
	tt.SetDark(true)
	dark := side()
	w, h := light.Bounds().Dx(), light.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, 2*w, h))
	draw.Draw(out, image.Rect(0, 0, w, h), light, light.Bounds().Min, draw.Src)
	draw.Draw(out, image.Rect(w, 0, 2*w, h), dark, dark.Bounds().Min, draw.Src)
	savePNG(t, filepath.Join(dir, "file-icons.png"), out)
}

// TestScreenshotTreeMulti 渲染文件树的截图（WORKBENCH_SHOT=目录 时才跑），临时仓库：
//
//	tree-multi.png       文件树多选：两个仓库和一个普通文件夹（不是 Git 仓库），跨仓库选了四行（点一下、⌘点三下），
//	                     编辑器里是第一行的预览
//	empty-workspace.png  第一次用的空工作区：标题栏、侧栏两个按钮、右栏
func TestScreenshotTreeMulti(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now().Truncate(time.Minute)
	api := testutil.NewRepo(t, "api-server")
	api.Write("go.mod", "module example.com/api\n\ngo 1.25\n")
	api.Write("handlers/user.go", "package handlers\n\n// ListUsers 列出用户。\nfunc ListUsers() []User {\n\treturn db.Users()\n}\n")
	api.Write("handlers/order.go", "package handlers\n\n// ListOrders 列出订单。\nfunc ListOrders() []Order {\n\treturn db.Orders()\n}\n")
	api.Write("README.md", "# api-server\n")
	api.CommitAt("加用户和订单接口", now.Add(-20*time.Hour))
	api.Write("handlers/order.go", "package handlers\n\n// ListOrders 列出订单，一页 page 条。\nfunc ListOrders(page int) []Order {\n\treturn db.Orders().Page(page, 50)\n}\n")
	web := testutil.NewRepo(t, "web-client")
	web.Write("package.json", "{\n  \"name\": \"web-client\"\n}\n")
	web.Write("src/api.ts", "export const base = '/api'\n")
	web.Write("src/App.vue", "<template><main /></template>\n")
	web.CommitAt("前端脚手架", now.Add(-30*time.Hour))
	notes := filepath.Join(t.TempDir(), "notes")
	for name, text := range map[string]string{"todo.md": "- 发 0.5.0\n", "ideas.txt": "分屏\n", "logo.png": "x"} {
		if err := os.MkdirAll(notes, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(notes, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a, tt, wait := newUI(t, testutil.Workspace(t, api.Root, web.Root, notes), termState(t, false)) // 底栏关着：不起 shell
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.ShowSidebar(workspace.ModeFiles)
	for _, r := range a.Repos {
		if !r.Expanded {
			a.ToggleRepo(r)
		}
		a.RefreshRepo(r, app.WhyManual)
		a.EnsureFiles(r)
	}
	wait("三段的文件", func() bool {
		return loaded(a)() && tt.HasText("handlers") && tt.HasText("src") && tt.HasText("todo.md")
	})
	a.ToggleDir(a.Repos[0], "handlers")
	a.ToggleDir(a.Repos[1], "src")
	wait("目录展开", func() bool { return tt.HasText("order.go") && tt.HasText("api.ts") })
	mustClick(t, tt, "user.go")
	wait("预览 user.go", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil && a.Repos[0].Hist.Loaded })
	for _, name := range []string{"order.go", "api.ts", "todo.md"} {
		if err := tt.ClickWith(wui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(a.ChosenRows()); n != 4 {
		t.Fatalf("选了 %d 行", n)
	}
	writeShot(t, tt, dir, "tree-multi.png")

	b, tb, _ := newUI(t, "", termState(t, false)) // 空工作区的终端在 ~，底栏关着不起 shell
	tb.SetScale(2)
	tb.SetTitleBar(shotBar)
	b.Start()
	writeShot(t, tb, dir, "empty-workspace.png")
}

// TestScreenshotWorktrees 是 worktrees.png（WORKBENCH_SHOT=目录 时才跑）：临时仓库 api-server 旁边四个工作树——
// wt/fix-io（领先 2、干净：可合并的绿勾和 ↑2，在工作区里打开着，段头有"工作树"标签，侧栏顶上的"需要你"条里有它）、
// wt/search（3 处改动：文件夹加改动数徽标）、分离 HEAD 的 demo（锁着，没有 agent）、目录删掉了的 wt/old（已合并，半透明）；
// 组里按状态排。Git 栏里工作树组展开，wt/search 展开着（摘要、审查、合并到 main、main...分支 的文件），右栏看它的分支历史。
// 深色的同一画面是 worktrees-dark.png。
func TestScreenshotWorktrees(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	now := time.Now().Truncate(time.Minute)
	ago := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	api := testutil.NewRepo(t, "api-server")
	api.Write("go.mod", "module example.com/api\n\ngo 1.25\n")
	api.Write("main.go", "package main\n\nfunc main() {}\n")
	api.CommitAt("初始化服务骨架", ago(80))
	api.Write("handlers/user.go", "package handlers\n\n// ListUsers 列出用户。\n")
	api.CommitAt("加用户接口", ago(50))
	api.Write("handlers/order.go", "package handlers\n\n// ListOrders 列出订单。\n")
	api.CommitAt("加订单接口", ago(30))
	side := filepath.Dir(api.Root)
	wt := func(name string) string { return filepath.Join(side, "api-server-"+name) }
	api.Git("worktree", "add", "-q", "-b", "wt/fix-io", wt("fix-io"))
	api.Git("worktree", "add", "-q", "-b", "wt/search", wt("search"))
	api.Git("worktree", "add", "-q", "--detach", wt("demo"), "HEAD~1")
	api.Git("worktree", "lock", "--reason", "给客户演示用，别动", wt("demo"))
	api.Git("worktree", "add", "-q", "-b", "wt/old", wt("old"))
	if err := os.RemoveAll(wt("old")); err != nil {
		t.Fatal(err)
	}
	fix := api.In(wt("fix-io"))
	fix.Write("store/io.go", "package store\n\n// Read 读一块，出错重试三次。\n")
	fix.CommitAt("读盘出错时重试", ago(6))
	fix.Write("store/io_test.go", "package store\n")
	fix.CommitAt("补重试的测试", ago(5))
	search := api.In(wt("search"))
	search.Write("search/index.go", "package search\n\n// Index 建倒排索引。\n")
	search.CommitAt("搜索：倒排索引", ago(20))
	search.Write("search/query.go", "package search\n\n// Query 按词查。\n")
	search.CommitAt("搜索：按词查", ago(4))
	search.Write("search/query.go", "package search\n\n// Query 按词查，结果按相关度排。\n")
	search.Write("search/rank.go", "package search\n")
	search.Write("handlers/search.go", "package handlers\n")
	api.Write("handlers/order.go", "package handlers\n\n// ListOrders 列出订单，一页 50 条。\n")
	web := testutil.NewRepo(t, "web-client")
	web.Write("package.json", "{\n  \"name\": \"web-client\"\n}\n")
	web.CommitAt("前端脚手架", ago(40))

	a, tt, wait := newUI(t, testutil.Workspace(t, api.Root, web.Root), termState(t, false))
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.RightBar = true
	a.ShowSidebar(workspace.ModeGit)
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("两个仓库的状态和工作树", func() bool { return loaded(a)() && len(a.Repos[0].Worktrees) == 4 })
	r := a.Repos[0]
	var fixW, searchW app.Worktree
	for _, w := range r.Worktrees {
		switch w.Branch {
		case "wt/fix-io":
			fixW = w
		case "wt/search":
			searchW = w
		}
	}
	a.OpenWorktree(fixW)
	wait("打开的 wt/fix-io", func() bool { return len(a.Repos) == 3 && loaded(a)() })
	a.ToggleWorktrees(r)
	wait("工作树的状态", func() bool { return r.WtInfo != nil && !r.WtLoading })
	a.ShowWorktreeHistory(r, searchW)
	a.ExpandWorktree(r, searchW)
	wait("wt/search 的历史和 main...wt/search", func() bool {
		return r.Hist.Loaded && !r.Hist.Loading && tt.HasText("搜索：按词查") && a.WtExp != nil && !a.WtExp.Loading
	})
	writeShot(t, tt, dir, "worktrees.png")
	tt.SetDark(true)
	writeShot(t, tt, dir, "worktrees-dark.png")
	tt.SetDark(false)
}

// TestScreenshotSplitEditors 是 split-editors.png（WORKBENCH_SHOT=目录 时才跑）：临时仓库 api-server，编辑器分成三组——
// 左边 README.md / main.go，右边上下两组 handlers/user.go 和 handlers/order.go（有键盘，顶上一条强调色细线）。
func TestScreenshotSplitEditors(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	api := testutil.NewRepo(t, "api-server")
	api.Write("go.mod", "module example.com/api\n\ngo 1.25\n")
	api.Write("README.md", "# api-server\n\n用户和订单的 HTTP 接口。\n\n```sh\ngo run .\n```\n")
	api.Write("main.go", "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/api/handlers\"\n)\n\nfunc main() {\n\thttp.HandleFunc(\"/users\", handlers.ListUsers)\n\thttp.HandleFunc(\"/orders\", handlers.ListOrders)\n\thttp.ListenAndServe(\":8080\", nil)\n}\n")
	api.Write("handlers/user.go", "package handlers\n\nimport \"net/http\"\n\n// ListUsers 列出用户。\nfunc ListUsers(w http.ResponseWriter, r *http.Request) {\n\twriteJSON(w, db.Users())\n}\n")
	api.Write("handlers/order.go", "package handlers\n\nimport \"net/http\"\n\n// ListOrders 列出订单。\nfunc ListOrders(w http.ResponseWriter, r *http.Request) {\n\twriteJSON(w, db.Orders())\n}\n")
	api.CommitAt("加用户和订单接口", time.Now().Truncate(time.Minute).Add(-20*time.Hour))
	api.Write("handlers/order.go", "package handlers\n\nimport (\n\t\"net/http\"\n\t\"strconv\"\n)\n\n// ListOrders 列出订单，一页 50 条。\nfunc ListOrders(w http.ResponseWriter, r *http.Request) {\n\tpage, _ := strconv.Atoi(r.URL.Query().Get(\"page\"))\n\twriteJSON(w, db.Orders().Page(page, 50))\n}\n")

	a, tt, wait := newUI(t, testutil.Workspace(t, api.Root), termState(t, false))
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Repos[0]
	if !r.Expanded {
		a.ToggleRepo(r)
	}
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("文件", func() bool { return loaded(a)() && tt.HasText("handlers") })
	a.ToggleDir(r, "handlers")
	var ds []*docs.Doc
	for _, p := range []string{"README.md", "main.go", "handlers/user.go", "handlers/order.go"} {
		ds = append(ds, a.OpenFile(r, p, true))
	}
	wait("四个文件读好", func() bool { return !slices.ContainsFunc(ds, func(d *docs.Doc) bool { return d.Buf == nil }) })
	a.SplitEditor(true)                  // order.go 到右边
	a.DropDoc(ds[2], a.Grp, app.SideTop) // user.go 到它上面
	a.SelectIn(a.GroupOf(ds[1]), 1)      // 左边看 main.go
	a.FocusGroup(a.GroupOf(ds[3]))       // 键盘在右下的 order.go
	if got := len(a.Groups()); got != 3 {
		t.Fatalf("分成了 %d 组", got)
	}
	tt.Frame()
	tt.Frame()
	writeShot(t, tt, dir, "split-editors.png")
}

// TestScreenshotPlainTree 是普通文件夹文件树的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一张：
// 点开头的目录和 node_modules 灰显、收着，.venv 展开读了一层；.coverage 这样的点文件照常。在临时目录里做。
func TestScreenshotPlainTree(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	root := filepath.Join(t.TempDir(), "py-service")
	for _, f := range []string{"main.py", "models.py", ".coverage", "alerting/rules.py", "api/routes.py",
		".venv/bin/python", ".venv/pyvenv.cfg", ".venv/lib/x", ".pytest_cache/README.md", ".ruff_cache/x", "node_modules/m/i.js"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stateDir := t.TempDir()
	st := workspace.Default()
	st.Settings.Shell, st.BottomBar, st.SidebarMode = "/bin/sh", false, workspace.ModeFiles
	if err := workspace.SaveState(stateDir, st); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := newUI(t, testutil.Workspace(t, root), stateDir)
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.Start()
	r := a.Repos[0]
	wait("读完", loaded(a))
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && !r.Files.Loading })
	a.ToggleDir(r, ".venv")
	a.St.SidebarW = 300
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		tt.Advance(500 * time.Millisecond)
		tt.Frame()
		name := "plain-tree-light.png"
		if dark {
			name = "plain-tree-dark.png"
		}
		writeShot(t, tt, dir, name)
	}
}
