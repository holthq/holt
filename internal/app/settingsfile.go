package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/version"
	"github.com/holthq/holt/internal/workspace"
)

// 这个文件是设置（二）：界面缩放（⌘= / ⌘- / ⌘0）、编辑器区的 settings.json 文档、导出 / 导入 / 恢复默认。
// 设置还是 state.json 里的 settings 一份（workspace.Settings），这里只是换几种方式看它、改它。

// ZoomFactor 是界面缩放给 c.SetZoom 的倍数（100% 是 1）。每个窗口（工作台、设置、关于）每帧自己设一次：mygo 的缩放是按窗口的。
func (a *App) ZoomFactor() float32 { return float32(a.St.Settings.ZoomPercent()) / 100 }

// SetZoom 把界面缩放换成 p%（夹到 50–300），所有窗口一起，存进设置；提示一行 "125%"。
// 不走 Clamp：设置窗口里正在填的终端配置不能被它丢掉。
func (a *App) SetZoom(p int) {
	p = min(max(p, workspace.ZoomSteps[0]), workspace.ZoomSteps[len(workspace.ZoomSteps)-1])
	if p == 100 {
		p = 0
	}
	a.St.Settings.Zoom = p
	a.SettingsChanged()
	a.Toast = fmt.Sprintf("%d%%", a.St.Settings.ZoomPercent())
}

// ZoomIn、ZoomOut 是 ⌘=、⌘-：缩放表里比现在大 / 小的下一档（现在不在表上的，比如设置里填的 105，也是挨着的那档），到头了不动。
func (a *App) ZoomIn() {
	cur := a.St.Settings.ZoomPercent()
	for _, z := range workspace.ZoomSteps {
		if z > cur {
			a.SetZoom(z)
			return
		}
	}
	a.SetZoom(cur)
}

func (a *App) ZoomOut() {
	cur := a.St.Settings.ZoomPercent()
	for i := len(workspace.ZoomSteps) - 1; i >= 0; i-- {
		if z := workspace.ZoomSteps[i]; z < cur {
			a.SetZoom(z)
			return
		}
	}
	a.SetZoom(cur)
}

// ZoomReset 是 ⌘0：回到 100%（已经是 100% 就不动）。
func (a *App) ZoomReset() {
	if a.Zoomed() {
		a.SetZoom(100)
	}
}

// Zoomed 是界面缩放不是 100%（⌘0 能用、状态栏有缩放的那一格）。
func (a *App) Zoomed() bool { return a.St.Settings.ZoomPercent() != 100 }

// OpenSettingsDoc 在编辑器区开设置文档（settings.json：全部设置键，零值也写），键盘给它；已经开着就切过去，
// 没改过的按现在的设置重新生成。⌘S 是套用（applySettingsDoc），不写盘。
func (a *App) OpenSettingsDoc() {
	text := string(workspace.SettingsJSON(a.St.Settings))
	if d := a.reuse(docs.Key{Kind: docs.SettingsDoc}, true); d != nil {
		if !d.Modified() {
			d.SetSettingsText(text)
		}
	} else {
		a.addDoc(docs.NewSettingsDoc(text), true)
	}
	a.FocusDoc = true
}

// RegenSettingsDoc 是设置文档上面那条的“恢复成现在的设置”：内容换回现在的设置。改了没保存的先问（换了不能撤销）。
func (a *App) RegenSettingsDoc(d *docs.Doc) {
	regen := func() {
		d.SetSettingsText(string(workspace.SettingsJSON(a.St.Settings)))
		a.Toast = i18n.T("已换回现在的设置")
	}
	if !d.Modified() {
		regen()
		return
	}
	a.AskDanger(i18n.T("丢掉设置文档里的改动？"), i18n.T("内容换回现在的设置，这里没保存的改动不能撤销。"), i18n.T("丢掉改动"), regen)
}

// syncSettingsDocs 让没改过的设置文档跟上现在的设置（设置窗口、⌘=、导入改了之后，SettingsChanged 里调）：
// 不然开着一份旧的，改别的键一保存就把刚改的盖回去。改过没保存的不动（保存时整份套上，提示里报改了几项）。
func (a *App) syncSettingsDocs() {
	var text string
	for _, d := range a.AllDocs() {
		if d.Kind == docs.SettingsDoc && !d.Modified() {
			if text == "" {
				text = string(workspace.SettingsJSON(a.St.Settings))
			}
			d.SetSettingsText(text)
		}
	}
}

// applySettingsDoc 是设置文档的保存（WriteDoc 里分出来的，不写盘）：严格地解（不认识的键、类型不对的值报错、
// 光标跳到出错的地方，什么都不改），Clamp，整份换成它，所有窗口套上、存进 state.json；内容换成规范格式（Clamp 过的），算已保存。
func (a *App) applySettingsDoc(d *docs.Doc) error {
	s, err := workspace.ParseSettings([]byte(d.Buf.String()))
	if err != nil {
		var se *workspace.SettingsError
		if errors.As(err, &se) && se.Line > 0 {
			d.JumpTo(docs.Jump{Line: se.Line - 1, Col: se.Col - 1})
		}
		a.Fail(i18n.T("settings.json 不对"), settingsErrText(err))
		return err
	}
	s.Clamp()
	n := len(workspace.ChangedKeys(a.St.Settings, s))
	a.St.Settings = s
	d.SetSettingsText(string(workspace.SettingsJSON(s)))
	if n > 0 {
		a.SettingsChanged()
	}
	a.Toast = i18n.T("设置没变")
	if n > 0 {
		a.Toast = i18n.Tn("已应用 %d 项设置", n)
	}
	return nil
}

// settingsErrText 是设置 JSON 出错的那句人话：第几行第几列、哪里不对。
func settingsErrText(err error) error {
	var e *workspace.SettingsError
	if !errors.As(err, &e) {
		return err
	}
	var what string
	switch e.Kind {
	case "unknown":
		what = i18n.Tf("不认识的键“%s”", e.Key)
	case "type":
		what = i18n.Tf("“%s”要的是%s，写的是%s", e.Key, jsonKindName(e.Want), jsonKindName(e.Got))
	case "trailing":
		what = i18n.T("设置对象后面还有别的东西")
	case "notObject":
		what = i18n.T("最外面要是一个 { } 对象")
	default:
		what = i18n.Tf("JSON 写得不对（%s）", e.Msg)
	}
	return humanErr(i18n.Tf("第 %d 行第 %d 列：%s", e.Line, e.Col, what))
}

// jsonKindName 是 SettingsError 的 Want / Got 的名字。
func jsonKindName(k string) string {
	switch k {
	case "string":
		return i18n.T("字符串")
	case "number":
		return i18n.T("数字")
	case "bool":
		return "true / false"
	case "array":
		return i18n.T("列表 [ ]")
	case "object":
		return i18n.T("对象 { }")
	case "null":
		return "null"
	}
	return k
}

// SettingsExport 是导出文件的内容：{"workbench": 版本, "exported": 时间（RFC 3339）, "settings": {全部键}}，两格缩进。
func SettingsExport(s workspace.Settings, now time.Time) []byte {
	ver, _ := json.Marshal(version.App)
	at, _ := json.Marshal(now.Format(time.RFC3339))
	body := strings.ReplaceAll(strings.TrimSuffix(string(workspace.SettingsJSON(s)), "\n"), "\n", "\n  ")
	return []byte("{\n  \"workbench\": " + string(ver) + ",\n  \"exported\": " + string(at) + ",\n  \"settings\": " + body + "\n}\n")
}

// ExportSettings 是"导出设置…"：系统的存储面板（默认 ~/Downloads/grove-settings.json），写全部设置；parent 是面板挂在哪个窗口上
// （设置窗口的 ⋯ 菜单里点的挂设置窗口，命令面板来的挂工作台窗口）。
func (a *App) ExportSettings(parent *mygo.Window) {
	def := "grove-settings.json"
	if home, err := os.UserHomeDir(); err == nil {
		def = filepath.Join(home, "Downloads", def)
	}
	data := SettingsExport(a.St.Settings, time.Now())
	up, save := a.Update, a.SaveDialog
	go func() {
		path, err := save(mygo.SaveDialogOptions{Parent: parent, Title: i18n.T("导出设置"), DefaultPath: def, ButtonLabel: i18n.T("导出"),
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}, CreateDirectories: true})
		up(func() {
			switch {
			case err != nil:
				a.Fail(i18n.T("没能导出设置"), err)
			case path == "": // 取消了
			default:
				if err := os.WriteFile(path, data, 0o644); err != nil {
					a.Fail(i18n.T("没能导出设置"), err)
					return
				}
				a.Toast = i18n.Tf("已导出到 %s", tildePath(path))
			}
		})
	}()
}

// ImportSettings 是"导入设置…"：系统的打开面板挑一个 JSON（导出的文件；有 "settings" 键的取它，所以整份 state.json 也行，
// 不然整个对象当设置），宽松地读（不认识的键、类型不对的跳过、数出来），Clamp，和现在一样就只提示；不一样先问
// （列出会改的几项、跳过了几个键），确认了整份换成它。问的卡在工作台窗口里，从设置窗口点的先把工作台窗口叫到前面。
func (a *App) ImportSettings(parent *mygo.Window) {
	up, open := a.Update, a.OpenDialog
	go func() {
		paths, err := open(mygo.OpenDialogOptions{Parent: parent, Title: i18n.T("导入设置"), ButtonLabel: i18n.T("导入"),
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
		var data []byte
		if err == nil && len(paths) > 0 {
			data, err = os.ReadFile(paths[0])
		}
		up(func() {
			switch {
			case err != nil:
				a.Fail(i18n.T("没能导入设置"), err)
			case len(paths) == 0: // 取消了
			default:
				if parent != nil && a.Win != nil && parent != a.Win {
					a.Win.Focus()
				}
				a.importSettings(data)
			}
		})
	}()
}

// tildePath 是给人看的路径：家目录换成 ~。
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// importSettings 是 ImportSettings 读到文件之后的那半。
func (a *App) importSettings(data []byte) {
	s, ignored, err := workspace.ReadSettingsFile(data)
	if err != nil {
		a.Fail(i18n.T("没能导入设置"), settingsErrText(err))
		return
	}
	s.Clamp()
	changed := workspace.ChangedKeys(a.St.Settings, s)
	if len(changed) == 0 {
		a.Toast = i18n.T("和现在的设置一样")
		if len(ignored) > 0 {
			a.Toast += " · " + i18n.Tn("忽略了 %d 个不认识的键", len(ignored))
		}
		return
	}
	msg := i18n.Tn("会改 %d 项：%s", len(changed), SettingNames(changed, 5))
	if len(ignored) > 0 {
		msg += i18n.Tn("；忽略了 %d 个不认识的键", len(ignored))
	}
	a.Ask(i18n.T("导入设置？"), msg+i18n.T("。现在的设置整份换掉，不合并。"), i18n.T("导入"), func() {
		a.St.Settings = s
		a.SettingsChanged()
		a.Toast = i18n.Tn("已导入 %d 项设置", len(changed))
	})
}

// ResetSettings 是"恢复默认设置…"：先问（几项会回到默认），确认了整份换成零值（默认）。问的卡在工作台窗口里。
func (a *App) ResetSettings() {
	n := len(workspace.ChangedKeys(a.St.Settings, workspace.Settings{}))
	if n == 0 {
		a.Toast = i18n.T("已经是默认设置")
		return
	}
	a.AskDanger(i18n.T("恢复默认设置？"), i18n.Tn("%d 项会回到默认（界面语言和缩放也会）；不能撤销，想留一份先导出。", n), i18n.T("恢复默认"), func() {
		a.St.Settings = workspace.Settings{}
		a.SettingsChanged()
		a.Toast = i18n.T("已恢复默认设置")
	})
}

// settingLabels 是设置键（json 名）在导入确认里的名字：设置窗口里那一项的名字，几处同名的（字体、字号）带上是哪一页的。
var settingLabels = map[string]string{
	"theme": "外观", "backdrop": "窗口底", "material": "材质", "accent": "强调色", "solidSelection": "选中行用淡底", "zoom": "界面缩放",
	"noDiagrams": "Markdown 预览里渲染 Mermaid 图和公式",
	"editorFont": "编辑器字体", "editorSize": "编辑器字号", "indent": "缩进", "softWrap": "软换行", "whitespace": "显示空白字符",
	"autoSave": "自动保存", "trimTrailing": "保存时去掉行尾空白", "finalNewline": "保存时补上末尾换行", "noStickyScroll": "粘性滚动",
	"noLocalHistory": "本地历史", "historyDays": "本地历史保留",
	"showIgnored": "显示被忽略的文件", "treeNoFollow": "文件树跟随当前文件", "treeNoCompact": "合并只有一个子目录的目录",
	"treeNoGuides": "缩进参考线", "treeNoStatusColors": "按 Git 状态给文件名着色", "treeIconsPlain": "图标风格", "exclude": "隐藏的文件",
	"showOpenEditors": "显示打开的编辑器", "shell": "Shell", "termFont": "终端字体", "termSize": "终端字号", "termTheme": "终端配色", "scrollback": "回滚（MB）",
	"termBlink": "光标闪烁", "termDock": "终端位置", "termDaemon": "关掉窗口后终端继续在后台运行", "termOptionAsAlt": "⌥ 作为 Meta 键",
	"termCopyOnSelect": "选中即复制", "termProfiles": "其他配置", "agentNames": "agent 命令", "pullMode": "拉取方式", "autoPush": "提交后推送",
	"showWorktrees": "在 Git 栏显示工作树", "showStashes": "在 Git 栏显示贮藏",
	"authorName": "作者名字", "authorEmail": "作者邮箱", "inlineBlame": "行内 blame", "webHosts": "网页风格",
	"formatOnSave": "保存时格式化", "formatters": "格式化命令", "lintOnSave": "保存时检查", "linters": "检查命令",
	"languageServers": "语言服务器", "lang": "界面语言",
}

// SettingName 是设置键 key 的名字（过了 i18n）；表里没有的就是键本身。
func SettingName(key string) string {
	if l, ok := settingLabels[key]; ok {
		return i18n.T(l)
	}
	return key
}

// SettingNames 是几个设置键的名字，顿号分开（英文是逗号），最多列 n 个，多的写“等 N 项”。
func SettingNames(keys []string, n int) string {
	names := make([]string, 0, min(len(keys), n))
	for _, k := range keys[:min(len(keys), n)] {
		names = append(names, SettingName(k))
	}
	s := i18n.List(names)
	if len(keys) > n {
		more := i18n.Tn("等 %d 项", len(keys))
		if last := s[len(s)-1]; last < utf8.RuneSelf && last != ' ' && !strings.HasPrefix(more, " ") { // “Shell 等 9 项”：西文和汉字之间空一格
			s += " "
		}
		s += more
	}
	return s
}
