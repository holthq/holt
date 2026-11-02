package workspace

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// 设置的 JSON：全部键都写（零值也写、照结构体的顺序），写出来再解回去一样；严格地解时报的错带行列；
// 导入宽松地读（有 "settings" 取它、整份 state.json 也行，不认识的键和类型不对的跳过）；ChangedKeys 照结构体的顺序。
func TestSettingsJSON(t *testing.T) {
	keys := SettingsKeys()
	if n := reflect.TypeFor[Settings]().NumField(); len(keys) != n || keys[0] != "theme" || !slices.Contains(keys, "zoom") {
		t.Fatalf("SettingsKeys %d 个（结构体 %d 个字段）：%v", len(keys), n, keys)
	}
	empty := string(SettingsJSON(Settings{}))
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(empty), &top); err != nil || len(top) != len(keys) {
		t.Fatalf("零值的 JSON 不全：%v %d 个键\n%s", err, len(top), empty)
	}
	for _, want := range []string{"{\n  \"theme\": \"\",\n", "\n  \"editorSize\": 0,\n", "\n  \"termProfiles\": [],\n", "\n  \"formatters\": {}"} {
		if !strings.Contains(empty, want) {
			t.Errorf("零值的 JSON 里没有 %q", want)
		}
	}
	if !strings.HasSuffix(empty, "}\n") || strings.Index(empty, `"theme"`) > strings.Index(empty, `"editorFont"`) {
		t.Error("零值的 JSON 末尾没换行，或键没照结构体的顺序")
	}

	s := Settings{Theme: "dark", Zoom: 125, EditorSize: 15, SoftWrap: true, Shell: "/bin/sh", TermSize: 13.5,
		TermProfiles: []TermProfile{{Name: "login", Shell: "/bin/sh", Args: []string{"-l"}}},
		WebHosts:     []WebHost{{Host: "git.example.com", Kind: "gitlab"}},
		Formatters:   map[string]string{"Go": "gofmt", "Markdown": "-"}}
	back, err := ParseSettings(SettingsJSON(s))
	if err != nil {
		t.Fatal(err)
	}
	if ch := ChangedKeys(s, back); len(ch) != 0 {
		t.Fatalf("写出来再解回去变了：%v", ch)
	}
	if ch := ChangedKeys(Settings{}, s); !slices.Equal(ch, []string{"theme", "zoom", "editorSize", "softWrap", "shell", "termSize", "termProfiles", "webHosts", "formatters"}) {
		t.Fatalf("ChangedKeys = %v", ch)
	}
	if ch := ChangedKeys(Settings{Formatters: map[string]string{}}, Settings{}); len(ch) != 0 {
		t.Fatalf("空的表和没有该算一样：%v", ch)
	}

	// 严格地解：不认识的键、类型不对、不是对象、对象后面还有东西、语法错，都带行列（列按字算）
	for _, c := range []struct {
		in        string
		kind, key string
		line, col int
	}{
		{"{\n  \"theme\": \"dark\",\n  \"colour\": 1\n}\n", "unknown", "colour", 3, 3},
		{"{\n  \"editorSize\": \"大\"\n}", "type", "editorSize", 2, 17},
		{"{\"shell\": \"/bin/sh\", \"editorFont\": \"a:b\", \"softWrap\": \"是:否\"}", "type", "softWrap", 1, 55},
		{"{\"termProfiles\": [{\"name\": \"x\", \"shel\": \"/bin/sh\"}]}", "unknown", "shel", 1, 33},
		{"[1, 2]", "notObject", "", 1, 1},
		{"\n  42", "notObject", "", 2, 3},
		{"{\"theme\": \"dark\"} {}", "trailing", "", 1, 19},
		{"{\n  \"theme\": \"dark\",\n}", "syntax", "", 3, 1},
		{"{\"theme\": \"dark\"", "syntax", "", 1, 17},
	} {
		_, err := ParseSettings([]byte(c.in))
		var e *SettingsError
		if !errors.As(err, &e) || e.Kind != c.kind || e.Key != c.key || e.Line != c.line || e.Col != c.col {
			t.Errorf("ParseSettings(%q) = %#v，要 %s %q 第 %d 行第 %d 列", c.in, err, c.kind, c.key, c.line, c.col)
		}
	}
	_, err = ParseSettings([]byte(`{"editorSize": "大"}`))
	if e := (*SettingsError)(nil); !errors.As(err, &e) || e.Want != "number" || e.Got != "string" {
		t.Errorf("类型不对的 Want / Got：%#v", err)
	}

	// 宽松地读：导出的文件取 "settings"；整份 state.json 也是；不认识的、类型不对的跳过，照文件里的顺序记下
	exported := `{"workbench": "0.18.2", "exported": "2026-11-01T10:00:00+08:00", "settings": {"theme": "light", "colour": 1, "editorSize": "大", "zoom": 150}}`
	got, ignored, err := ReadSettingsFile([]byte(exported))
	if err != nil || got.Theme != "light" || got.Zoom != 150 || got.EditorSize != 0 || !slices.Equal(ignored, []string{"colour", "editorSize"}) {
		t.Fatalf("读导出的文件：%+v 忽略 %v（%v）", got, ignored, err)
	}
	state, _ := json.Marshal(State{Version: 3, RecentWs: []string{"/x"}, Settings: Settings{PullMode: "rebase", TermDock: "right"}})
	if got, ignored, err = ReadSettingsFile(state); err != nil || got.PullMode != "rebase" || got.TermDock != "right" || len(ignored) != 0 {
		t.Fatalf("读整份 state.json：%+v 忽略 %v（%v）", got, ignored, err)
	}
	if got, ignored, err = ReadSettingsFile([]byte(`{"theme": "dark", "workbench": 1}`)); err != nil || got.Theme != "dark" || !slices.Equal(ignored, []string{"workbench"}) {
		t.Fatalf("没有 settings 键的对象：%+v 忽略 %v（%v）", got, ignored, err)
	}
	if _, _, err = ReadSettingsFile([]byte("不是 JSON")); err == nil {
		t.Fatal("不是 JSON 也读进来了")
	}
}

// ZoomSteps 从小到大、有 100；Clamp 把 100 和表外太大太小的换回 0（存的 0 是 100），表外但在范围里的（105）留着。
func TestZoomClamp(t *testing.T) {
	if !slices.IsSorted(ZoomSteps) || !slices.Contains(ZoomSteps, 100) || ZoomSteps[0] != 50 || ZoomSteps[len(ZoomSteps)-1] != 300 {
		t.Fatalf("ZoomSteps = %v", ZoomSteps)
	}
	for in, want := range map[int]int{0: 0, 100: 0, 49: 0, 301: 0, -5: 0, 50: 50, 105: 105, 300: 300} {
		s := Settings{Zoom: in}
		if s.Clamp(); s.Zoom != want {
			t.Errorf("Clamp(zoom %d) = %d，要 %d", in, s.Zoom, want)
		}
	}
	if s := (Settings{}); s.ZoomPercent() != 100 {
		t.Fatal("零值的缩放不是 100%")
	}
}
