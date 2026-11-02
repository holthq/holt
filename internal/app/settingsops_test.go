package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/version"
	"github.com/lemoeade/grove/internal/workspace"
)

// 界面缩放：⌘= / ⌘- 照缩放表一档一档走（表外的 105 走到挨着的那档），到头不动、照样提示；⌘0 回到 100%（存 0），
// 100% 时 ⌘0 用不了；夹到 50–300；改了交给窗口、存进 state.json。
func TestZoomSteps(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, _ := newTestAppIn(t, ws, stateDir)
	applied := 0
	a.OnSettings = func(*workspace.Settings) { applied++ }
	s := &a.St.Settings
	reset := Find("view.zoomReset")
	if a.Zoomed() || a.CanRun(reset) || a.ZoomFactor() != 1 {
		t.Fatalf("一开始：Zoomed %v、⌘0 能用 %v、倍数 %v", a.Zoomed(), a.CanRun(reset), a.ZoomFactor())
	}
	step := func(id string, want int) {
		t.Helper()
		a.Exec(id)
		if s.ZoomPercent() != want || a.Toast != fmt.Sprintf("%d%%", want) {
			t.Fatalf("%s：%d%%（提示 %q），要 %d%%", id, s.ZoomPercent(), a.Toast, want)
		}
	}
	step("view.zoomIn", 110)
	step("view.zoomIn", 125)
	if a.ZoomFactor() != 1.25 || !a.Zoomed() || !a.CanRun(reset) || applied != 2 {
		t.Fatalf("125%%：倍数 %v、⌘0 能用 %v、交给窗口 %d 次", a.ZoomFactor(), a.CanRun(reset), applied)
	}
	step("view.zoomOut", 110)
	step("view.zoomOut", 100)
	if s.Zoom != 0 {
		t.Fatalf("100%% 存成了 %d，要 0", s.Zoom)
	}
	for _, want := range []int{90, 80, 75, 67, 50, 50} {
		step("view.zoomOut", want)
	}
	step("view.zoomReset", 100)
	a.Toast = ""
	if a.Exec("view.zoomReset"); a.Toast != "" {
		t.Fatalf("100%% 时 ⌘0 还跑了：%q", a.Toast)
	}
	a.SetZoom(300)
	step("view.zoomIn", 300)
	for in, want := range map[int]int{1000: 300, 10: 50, 100: 100, 137: 137} {
		if a.SetZoom(in); s.ZoomPercent() != want {
			t.Errorf("SetZoom(%d) = %d%%，要 %d%%", in, s.ZoomPercent(), want)
		}
	}
	s.Zoom = 105 // 设置窗口里填的、不在表上的
	step("view.zoomIn", 110)
	s.Zoom = 105
	step("view.zoomOut", 100)
	step("view.zoomIn", 110)

	st, err := workspace.LoadState(stateDir)
	if err != nil || st.Settings.Zoom != 110 {
		t.Fatalf("state.json 里的缩放 %d（%v），要 110", st.Settings.Zoom, err)
	}
}

// 导出：存储面板（默认 ~/Downloads/grove-settings.json），写 {"workbench","exported","settings"}、0644，提示路径；取消什么都不写，写不了报错。
// 导入：打开面板挑文件，和现在一样只提示（带上忽略了几个键）；不一样先问（列出会改的、多的写“等 N 项”，忽略了几个键），确认了整份换掉、存盘；
// 整份 state.json 也认；不是 JSON 报错带行列；取消什么都不动。
func TestSettingsExportImport(t *testing.T) {
	t.Cleanup(func() { i18n.Set("") })
	_, ws := testutil.TempRepo(t)
	stateDir, dir := t.TempDir(), t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	s := &a.St.Settings
	s.Theme, s.EditorSize, s.Zoom = "dark", 15, 125
	a.SettingsChanged()

	// 导出
	out := filepath.Join(dir, "out.json")
	var opts mygo.SaveDialogOptions
	saveTo, asked := out, make(chan bool, 8)
	a.SaveDialog = func(o mygo.SaveDialogOptions) (string, error) { opts = o; asked <- true; return saveTo, nil }
	a.Toast = ""
	a.Exec("app.settingsExport")
	wait("导出", func() bool { return a.Toast != "" || a.AlertOpen })
	if !strings.HasSuffix(opts.DefaultPath, filepath.Join("Downloads", "grove-settings.json")) || opts.Title != "导出设置" || opts.ButtonLabel != "导出" ||
		opts.Parent != a.Win || len(opts.Filters) != 1 || opts.Filters[0].Extensions[0] != "json" {
		t.Fatalf("存储面板的选项 %+v", opts)
	}
	if a.Toast != "已导出到 "+out {
		t.Fatalf("导出的提示 %q", a.Toast)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o644 {
		t.Fatalf("导出的文件权限 %v", fi.Mode().Perm())
	}
	var exp struct {
		Workbench string                     `json:"workbench"`
		Exported  string                     `json:"exported"`
		Settings  map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &exp); err != nil {
		t.Fatalf("导出的不是 JSON：%v\n%s", err, data)
	}
	if at, err := time.Parse(time.RFC3339, exp.Exported); err != nil || time.Since(at) > time.Minute || exp.Workbench != version.App ||
		len(exp.Settings) != len(workspace.SettingsKeys()) || string(exp.Settings["theme"]) != `"dark"` || string(exp.Settings["zoom"]) != "125" {
		t.Fatalf("导出的内容：\n%s", data)
	}
	if !strings.HasPrefix(string(data), "{\n  \"workbench\": ") || !strings.Contains(string(data), "\n  \"settings\": {\n    \"theme\": \"dark\",\n") || !strings.HasSuffix(string(data), "\n  }\n}\n") {
		t.Fatalf("导出的格式：\n%s", data)
	}
	// 取消；写不了
	<-asked
	saveTo, a.Toast = "", ""
	a.Exec("app.settingsExport")
	<-asked
	time.Sleep(20 * time.Millisecond) // 面板返回之后排进队列的那一下
	wait("取消导出", func() bool { return true })
	if a.Toast != "" || a.AlertOpen {
		t.Fatalf("取消了还提示：%q %v", a.Toast, a.AlertOpen)
	}
	saveTo = filepath.Join(dir, "没有这个目录", "x.json")
	a.Exec("app.settingsExport")
	wait("写不了", func() bool { return a.AlertOpen })
	if a.AlertTitle != "没能导出设置" {
		t.Fatalf("写不了的提示 %q", a.AlertTitle)
	}
	a.AlertOpen = false

	// 导入：换掉三项、一个不认识的键、一个类型不对的
	pick := ""
	a.OpenDialog = func(o mygo.OpenDialogOptions) ([]string, error) {
		if o.Title != "导入设置" || o.ButtonLabel != "导入" || o.Filters[0].Extensions[0] != "json" {
			t.Errorf("打开面板的选项 %+v", o)
		}
		defer func() { asked <- true }()
		if pick == "" {
			return nil, nil
		}
		return []string{pick}, nil
	}
	imp := func(name, content string) {
		t.Helper()
		pick = filepath.Join(dir, name)
		if err := os.WriteFile(pick, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		a.Confirm, a.ConfirmOpen, a.Toast, a.AlertOpen = nil, false, "", false
		a.Exec("app.settingsImport")
		wait("导入 "+name, func() bool { return a.Confirm != nil || a.Toast != "" || a.AlertOpen })
	}
	imp("a.json", `{"workbench": "0.18.2", "settings": {"theme": "light", "editorSize": 15, "pullMode": "rebase", "termSize": "大", "colour": 1}}`)
	c := a.Confirm
	if c == nil || c.Danger || c.Title != "导入设置？" || c.Button != "导入" || // 界面缩放 125 → 没写（100）也算改
		c.Message != "会改 3 项：外观、界面缩放、拉取方式；忽略了 2 个不认识的键。现在的设置整份换掉，不合并。" {
		t.Fatalf("导入的确认 %+v", c)
	}
	if s.Theme != "dark" {
		t.Fatal("没确认就换了")
	}
	c.Fn()
	if s.Theme != "light" || s.Zoom != 0 || s.EditorSize != 15 || s.PullMode != "rebase" || a.Toast != "已导入 3 项设置" {
		t.Fatalf("确认后：%+v，提示 %q", *s, a.Toast)
	}
	if st, _ := workspace.LoadState(stateDir); st.Settings.PullMode != "rebase" || st.Settings.Theme != "light" {
		t.Fatalf("导入的没存进 state.json：%+v", st.Settings)
	}
	// 同一份再导一次：一样，只提示
	imp("b.json", `{"settings": {"theme": "light", "editorSize": 15, "pullMode": "rebase", "colour": 1}}`)
	if a.Confirm != nil || a.Toast != "和现在的设置一样 · 忽略了 1 个不认识的键" {
		t.Fatalf("一样的导入：%+v %q", a.Confirm, a.Toast)
	}
	// 导出的文件导回来：一样
	imp("c.json", string(SettingsExport(*s, time.Now())))
	if a.Toast != "和现在的设置一样" {
		t.Fatalf("导出的文件导回来：%+v %q", a.Confirm, a.Toast)
	}
	// 整份 state.json；改的多于 5 项写“等 N 项”
	st := workspace.Default()
	st.Settings = workspace.Settings{Theme: "light", Accent: "teal", EditorSize: 14, SoftWrap: true, Shell: "/bin/sh", TermDock: "right", AutoPush: true}
	whole, _ := json.Marshal(st)
	imp("state.json", string(whole))
	if c = a.Confirm; c == nil || !strings.HasPrefix(c.Message, "会改 7 项：强调色、编辑器字号、软换行、Shell、终端位置等 7 项。") {
		t.Fatalf("整份 state.json：%+v %q", c, a.Toast)
	}
	// 不是 JSON；取消
	imp("bad.json", "{\n  \"theme\": \"dark\",\n}")
	if !a.AlertOpen || a.AlertTitle != "没能导入设置" || !strings.HasPrefix(a.AlertMsg, "第 3 行第 1 列：") {
		t.Fatalf("坏文件：%v %q %q", a.AlertOpen, a.AlertTitle, a.AlertMsg)
	}
	before := *s
	pick, a.Confirm, a.AlertOpen, a.Toast = "", nil, false, ""
	for len(asked) > 0 {
		<-asked
	}
	a.Exec("app.settingsImport")
	<-asked
	time.Sleep(20 * time.Millisecond)
	wait("取消导入", func() bool { return true })
	if a.Confirm != nil || a.AlertOpen || a.Toast != "" || len(workspace.ChangedKeys(before, *s)) != 0 {
		t.Fatal("取消导入还动了")
	}
	// 面板出错
	a.OpenDialog = func(mygo.OpenDialogOptions) ([]string, error) { return nil, errors.New("面板坏了") }
	a.Exec("app.settingsImport")
	wait("面板出错", func() bool { return a.AlertOpen })
}

// 每个设置键在导入确认里都有名字（settingLabels），没有多余的；English 下也翻好了。
func TestSettingLabels(t *testing.T) {
	t.Cleanup(func() { i18n.Set("") })
	keys := workspace.SettingsKeys()
	for _, k := range keys {
		if _, ok := settingLabels[k]; !ok {
			t.Errorf("设置键 %s 没有名字", k)
		}
	}
	for k := range settingLabels {
		if !slices.Contains(keys, k) {
			t.Errorf("settingLabels 里的 %s 不是设置键", k)
		}
	}
	i18n.Set("en")
	for _, k := range keys {
		if n := SettingName(k); strings.ContainsFunc(n, func(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }) {
			t.Errorf("%s 的英文名 %q", k, n)
		}
	}
	if got := SettingNames([]string{"shell", "zoom", "lang"}, 1); got != "Shell and others (3 in all)" {
		t.Errorf("英文的 SettingNames %q", got)
	}
	i18n.Set("")
	if got := SettingNames([]string{"theme", "shell", "zoom"}, 2); got != "外观、Shell 等 3 项" {
		t.Errorf("SettingNames %q", got)
	}
	i18n.Set("en")
	if got := SettingNames([]string{"theme", "zoom", "lang"}, 2); got != "Appearance, Zoom and others (3 in all)" {
		t.Errorf("英文的 SettingNames %q", got)
	}
}

// 恢复默认：已经是默认只提示；不然红的确认（几项会回到默认），确认了整份换成零值、存盘。
func TestSettingsReset(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, _ := newTestAppIn(t, ws, stateDir)
	a.Exec("app.settingsReset")
	if a.Confirm != nil || a.Toast != "已经是默认设置" {
		t.Fatalf("默认时：%+v %q", a.Confirm, a.Toast)
	}
	s := &a.St.Settings
	s.Theme, s.Zoom, s.Formatters = "dark", 150, map[string]string{"Go": "-"}
	a.SettingsChanged()
	a.Exec("app.settingsReset")
	c := a.Confirm
	if c == nil || !c.Danger || c.Title != "恢复默认设置？" || c.Button != "恢复默认" ||
		c.Message != "3 项会回到默认（界面语言和缩放也会）；不能撤销，想留一份先导出。" {
		t.Fatalf("恢复默认的确认 %+v", c)
	}
	if s.Theme != "dark" {
		t.Fatal("没确认就恢复了")
	}
	c.Fn()
	if len(workspace.ChangedKeys(*s, workspace.Settings{})) != 0 || a.Toast != "已恢复默认设置" || a.Zoomed() {
		t.Fatalf("确认后：%+v %q", *s, a.Toast)
	}
	if st, _ := workspace.LoadState(stateDir); st.Settings.Theme != "" || st.Settings.Zoom != 0 {
		t.Fatalf("恢复的没存进 state.json：%+v", st.Settings)
	}
}
