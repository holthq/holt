package app

import (
	"errors"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是设置（⌘, 的设置窗口）怎么生效。设置窗口每改一项调 SettingsChanged：外观和语言马上换，
// 已经开着的终端换字体、配色和选中即复制（shell、回滚大小、光标闪烁、⌥ 作 Meta 和后台运行新开的终端才用），编辑器的选项每帧从设置里读
// （EditorOptions），拉取方式、提交后推送、作者在下次操作时用，自动保存见 AutoSaveTick。

// AutoSaveDelay 是"停手后自动保存"等的时间。
const AutoSaveDelay = time.Second

// SettingsChanged 在设置改了之后调：抄给别的窗口，套到界面和每个窗口的终端上，存进 state.json，窗口都重画。
func (a *App) SettingsChanged() {
	for _, o := range a.peers() {
		o.St.Settings = a.St.Settings
	}
	a.ApplySettings()
	for _, o := range a.peers() {
		o.tree.ver++ // 合并目录的开关
		o.reloadIgnored()
		o.reloadExclude()
		o.dropInlineBlames()
		o.syncShownGroups()
		o.eachPane(func(_ *Repo, p *TermPane) {
			if p.Term != nil {
				p.Term.SetFont(o.TermFont())
				p.Term.SetTheme(o.TermTheme())
				p.Term.SetCopyOnSelect(o.copyOnSelect())
			}
		})
		o.syncSettingsDocs()
	}
	a.SaveState()
	for _, o := range a.peers() {
		o.Update(func() {})
	}
}

// ApplySettings 换语言，把外观和语言交给窗口（main 里给的 OnSettings，套到每个窗口上）；启动时和设置改了之后调。
func (a *App) ApplySettings() {
	i18n.Set(a.St.Settings.Lang)
	if a.OnSettings != nil {
		a.OnSettings(&a.St.Settings)
	}
}

// TermFont 是终端的字体：没设就跟编辑器一样的家族，12.5 号。
func (a *App) TermFont() terminal.Font {
	s := &a.St.Settings
	f := terminal.Font{Family: s.TermFont, Size: s.TermSize, LineHeight: 1.1}
	if f.Family == "" {
		f.Family = a.EditorFont().Family
	}
	if f.Size == 0 {
		f.Size = 12.5
	}
	return f
}

// CardDark 是深色外观里卡片（编辑器组、终端、右栏）的底色，比 mygo 深色主题的底 #18181b 亮一档；
// 浅色的卡片是白色，就是主题的底色。
var CardDark = wui.Hex("#202023")

// termLight、termDark 是内置的浅色 / 深色终端配色：mygo 的配色，底色换成卡片的颜色，终端和它所在的卡片一色
// （前景色表不动）。各只有一份：终端按指针认配色，换了才重新套。
var termLight, termDark = func() (*terminal.Theme, *terminal.Theme) {
	d := terminal.DarkTheme()
	d.Background = CardDark
	return terminal.LightTheme(), d
}()

// TermTheme 是终端的配色：没设就跟着窗口的浅色 / 深色走（两份都给）；Ghostty 的主题名找不到（主题文件删了）也跟着外观。
func (a *App) TermTheme() (theme, dark *terminal.Theme) {
	switch name := a.St.Settings.TermTheme; name {
	case "":
	case "light":
		return termLight, termLight
	case "dark":
		return termDark, termDark
	default:
		if t, err := terminal.GhosttyTheme(name); err == nil {
			return t, t
		}
	}
	return termLight, termDark
}

// termOptions 是新开的终端用的 shell、字体、配色、回滚大小和光标闪不闪；profile 是终端配置的名字（"" 默认）：
// 有这个配置就跑它的命令、加它的环境，没有（删了）用设置里的 shell。
func (a *App) termOptions(o *terminal.Options, profile string) {
	s := &a.St.Settings
	if pr := s.Profile(profile); pr != nil {
		o.Command = pr.Command()
		o.Env = append(o.Env, pr.Env...)
	} else if s.Shell != "" {
		o.Command = []string{s.Shell, "-l"}
	}
	o.Font = a.TermFont()
	o.Theme, o.DarkTheme = a.TermTheme()
	if s.Scrollback > 0 {
		o.Scrollback = s.Scrollback << 20
	}
	o.NoBlink = !s.TermBlink
	o.OptionAsAlt = s.TermOptionAsAlt
	o.CopyOnSelect = a.copyOnSelect()
}

// copyOnSelect 是设置里的"选中即复制"：开着时终端里选中的字松手就进剪贴板。
func (a *App) copyOnSelect() terminal.CopyOnSelect {
	if a.St.Settings.TermCopyOnSelect {
		return terminal.CopyOnSelectClipboard
	}
	return terminal.CopyOnSelectNone
}

// TermDaemonChanged 在设置里开关"关掉窗口后终端继续在后台运行"之后调：只对新开的终端生效，
// 已经开着的另一种终端提示一句。
func (a *App) TermDaemonChanged() {
	on := a.St.Settings.TermDaemon
	other := 0
	for _, o := range a.peers() {
		o.eachPane(func(_ *Repo, p *TermPane) {
			if p.Term != nil && !p.Exited && p.Daemon() != on {
				other++
			}
		})
	}
	switch {
	case other == 0:
	case on:
		a.Toast = i18n.T("已经开着的终端重新启动后才会在后台运行")
	default:
		a.Toast = i18n.T("已经开着的终端重新启动后才会在进程内运行")
	}
}

// PullArgs 是设置里的拉取方式对应的 git 参数：默认只快进。
func PullArgs(mode string) []string {
	switch mode {
	case "rebase":
		return []string{"pull", "--rebase"}
	case "merge":
		return []string{"pull", "--no-rebase"}
	}
	return []string{"pull", "--ff-only"}
}

// commitArgs 是提交的 git 参数；设置里填了作者（名字和邮箱都要有）就用 --author 换掉 git 配置里的。
func (a *App) commitArgs(msg string) []string {
	args := []string{"commit", "-q", "-m", msg}
	if s := &a.St.Settings; s.AuthorName != "" && s.AuthorEmail != "" {
		args = append(args, "--author="+s.AuthorName+" <"+s.AuthorEmail+">")
	}
	return args
}

// OnBlur 在窗口切走时调：存标签和展开状态，自动保存是"切走时"就保存改过的文件。
func (a *App) OnBlur() {
	a.SaveState()
	a.noteTermDirsLater()
	if a.St.Settings.AutoSave == "blur" {
		for _, d := range a.AllDocs() {
			if a.canAutoSave(d) {
				a.autoWrite(d, true)
			}
		}
	}
}

// AutoSaveTick 每帧调：自动保存是"停手后"时，改过的文件在最后一次改动 AutoSaveDelay 之后保存。
func (a *App) AutoSaveTick() {
	if a.St.Settings.AutoSave != "delay" {
		a.autoVer = nil
		return
	}
	now := time.Now()
	for _, d := range a.AllDocs() {
		if !a.canAutoSave(d) {
			delete(a.autoVer, d)
			continue
		}
		v := d.Buf.Version()
		p, ok := a.autoVer[d]
		switch {
		case !ok || p.ver != v:
			if a.autoVer == nil {
				a.autoVer = map[*docs.Doc]autoSave{}
			}
			a.autoVer[d] = autoSave{v, now}
			up := a.Update
			time.AfterFunc(AutoSaveDelay, func() { up(func() {}) })
		case now.Sub(p.at) >= AutoSaveDelay:
			delete(a.autoVer, d)
			a.autoWrite(d, false) // 打字中间停一下，不动尾空白
		}
	}
}

// autoSave 是一个文件最后一次改动的版本和时间。
type autoSave struct {
	ver uint64
	at  time.Time
}

// canAutoSave：改过的、能写的文件标签，磁盘上没被别处改过（那要用户自己决定覆不覆盖）。
func (a *App) canAutoSave(d *docs.Doc) bool {
	return d.Kind == docs.File && d.Editable() && d.Modified() && !d.NeedsOverwrite()
}

// autoWrite 自动保存一个文件：不弹"已保存"，出错照样提示。tidy 时先整理（切走时；停手后的不整理）。
func (a *App) autoWrite(d *docs.Doc, tidy bool) {
	if tidy {
		a.TidyDoc(d)
	}
	if err := a.diskWrite(d, d.Write); err != nil {
		if ue := (*docs.UnencodableError)(nil); errors.As(err, &ue) { // 不弹问话：⌘S 时再问要不要转成 UTF-8
			a.Toast = i18n.Tf("%s 没自动保存：%v", d.Title, err)
			return
		}
		a.Fail(i18n.T("自动保存失败"), err)
		return
	}
	a.refreshSaved(d)
}
