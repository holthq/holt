package app

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是快捷键自定义：<状态目录>/keybindings.json 里的条目盖在注册表的默认键上。
// 每次 apply 都从默认键起重算（“重置”= 从文件里删掉那条），命令的 Mods / Key 就是生效的键，菜单栏、窗口快捷键、
// 右键菜单、工具提示照旧从注册表读。冲突按分派域算（keyDomain）：同域同键用户的赢，被顶掉的命令没有键、
// Shadowed 记下是谁。编辑器自带的命令（editor.*）键由编辑器自己接：改了的生成 editor.Binding 给编辑器
// （EditorBindings），旧的默认键拿走（NoCommand）。不做和弦、不做 when 条件。命令表是整个进程的，几个窗口共用。

// Keybinding 是 keybindings.json 里的一条：key 为空 = 去掉这条命令的默认键。
type Keybinding struct {
	Command string `json:"command"`
	Key     string `json:"key"`
}

// keyDefault 是一条命令的默认键：注册表里写的，编辑器自带命令的是 mygo 默认键里显示的那条（hint）和它的第一下。
type keyDefault struct {
	mods     ui.Modifiers
	key      ui.Key
	hint     string
	hintMods ui.Modifiers
	hintKey  ui.Key // 0 = 没有，或是和弦（和弦不参加冲突，只靠第一下被拿走）
}

var (
	keyDefaults  = map[*Command]keyDefault{}
	keyFile      []Keybinding     // 文件里现在的条目，原样（看不懂的写回时也留着）
	keyErrors    []string         // 看不懂的条目，设置页列出来
	keyBroken    bool             // 整个文件读不了（不是 JSON 数组）：设置页不往里写，免得盖掉
	keyRev       int              // apply 一次加一：菜单栏跟着重建（menuSignature）
	edBindings   []editor.Binding // 文件标签编辑器的 st.Bindings
	diffBindings []editor.Binding // Diff 的 Bindings：J / K 跳改动、Diff 标签命令拿走的键，再是 edBindings
	keyToasted   bool             // 启动时“有几条看不懂”只说一次（几个窗口共用）

	// KeyRecording 是设置窗口正在录键：菜单栏先不带快捷键（不然 ⇧⌘P 这种先被菜单接走，录不到）。
	KeyRecording bool
)

// 修饰键和键的写法（ParseKeys）。
var (
	modWords = map[string]ui.Modifiers{
		"cmd": ui.Super, "command": ui.Super, "meta": ui.Super, "⌘": ui.Super,
		"ctrl": ui.Ctrl, "control": ui.Ctrl, "⌃": ui.Ctrl,
		"alt": ui.Alt, "option": ui.Alt, "opt": ui.Alt, "⌥": ui.Alt,
		"shift": ui.Shift, "⇧": ui.Shift,
	}
	keyWords = map[ui.Key]string{ // 规范写法（FormatKeys 写回文件用）
		ui.KeyEnter: "enter", ui.KeyEscape: "escape", ui.KeyBackspace: "backspace", ui.KeyTab: "tab", ui.KeySpace: "space",
		ui.KeyDelete: "delete", ui.KeyInsert: "insert", ui.KeyHome: "home", ui.KeyEnd: "end",
		ui.KeyPageUp: "pageup", ui.KeyPageDown: "pagedown",
		ui.KeyLeft: "left", ui.KeyRight: "right", ui.KeyUp: "up", ui.KeyDown: "down",
		ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/", ui.KeySemicolon: ";",
		ui.KeyQuote: "'", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
	}
	keyAliases = map[string]ui.Key{
		"esc": ui.KeyEscape, "minus": ui.KeyMinus, "equal": ui.KeyEqual, "comma": ui.KeyComma, "period": ui.KeyPeriod,
		"slash": ui.KeySlash, "semicolon": ui.KeySemicolon, "quote": ui.KeyQuote, "bracketleft": ui.KeyBracketLeft,
		"bracketright": ui.KeyBracketRight, "backslash": ui.KeyBackslash, "backquote": ui.KeyBackquote,
	}
)

// errKeys 是 ParseKeys 看不懂的写法。
var errKeys = errors.New("看不懂的快捷键")

// ParseKeys 把 "cmd+shift+p"（"⌘⇧P"、"Command+Shift+P"……，大小写随意）读成修饰键和键；空串是没有键（0, 0）。
func ParseKeys(s string) (ui.Modifiers, ui.Key, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, 0, nil
	}
	var mods ui.Modifiers
	for { // 开头的 ⌘⌃⌥⇧ 可以不用 + 连
		r, n := firstRune(s)
		m, ok := modWords[r]
		if !ok || n == len(s) {
			break
		}
		mods |= m
		s = strings.TrimPrefix(s[n:], "+")
	}
	parts := strings.Split(s, "+")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if m, ok := modWords[p]; ok && i < len(parts)-1 {
			mods |= m
			continue
		}
		if i < len(parts)-1 {
			return 0, 0, errKeys
		}
		if k := keyNamed(p); k != 0 {
			return mods, k, nil
		}
	}
	return 0, 0, errKeys
}

func firstRune(s string) (string, int) {
	for i := range s {
		if i > 0 {
			return s[:i], i
		}
	}
	return s, len(s)
}

// keyNamed 是小写的键名对应的键；不认识为 0。
func keyNamed(p string) ui.Key {
	switch {
	case len(p) == 1 && p[0] >= 'a' && p[0] <= 'z':
		return ui.KeyA + ui.Key(p[0]-'a')
	case len(p) == 1 && p[0] >= '0' && p[0] <= '9':
		return ui.Key0 + ui.Key(p[0]-'0')
	case len(p) >= 2 && p[0] == 'f':
		if n, err := strconv.Atoi(p[1:]); err == nil && n >= 1 && n <= 12 && strconv.Itoa(n) == p[1:] {
			return ui.KeyF1 + ui.Key(n-1)
		}
	}
	if k, ok := keyAliases[p]; ok {
		return k
	}
	for k, w := range keyWords {
		if w == p {
			return k
		}
	}
	return 0
}

// FormatKeys 是 mods+key 的规范写法（"cmd+shift+p"，写回 keybindings.json 用）；没有键为空。
func FormatKeys(mods ui.Modifiers, key ui.Key) string {
	if key == 0 {
		return ""
	}
	var parts []string
	for _, m := range []struct {
		mod  ui.Modifiers
		name string
	}{{ui.Super, "cmd"}, {ui.Ctrl, "ctrl"}, {ui.Alt, "alt"}, {ui.Shift, "shift"}} {
		if mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, keyWord(key)), "+")
}

func keyWord(k ui.Key) string {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return string(rune('a' + k - ui.KeyA))
	case k >= ui.Key0 && k <= ui.Key9:
		return string(rune('0' + k - ui.Key0))
	case k >= ui.KeyF1 && k <= ui.KeyF12:
		return "f" + strconv.Itoa(int(k-ui.KeyF1)+1)
	}
	return keyWords[k]
}

// Recordable 是这个键能不能当快捷键（录键时用）：后退 / 前进键、菜单键、认不出的键不行。
func Recordable(k ui.Key) bool { return keyWord(k) != "" }

// editorOwn 是键由编辑器自己接的命令（editor.* 里 mygo 自带的那些）。
func editorOwn(c *Command) bool { return c.Local && c.ed != editor.NoCommand }

// keyDomain 是命令的键由谁分派：全局、Diff 标签、编辑器的都在窗口上（同一张表），别的各自一张。
func keyDomain(ctx Context) Context {
	if ctx == DiffTab || ctx == Editor {
		return Global
	}
	return ctx
}

// clash 报告 a、b 用同一个键时算不算冲突：同域，且上下文相同或有一个是全局（⌥⌘F 在 Diff 标签和编辑器里
// 各是一个命令是设计，不算）。
func clash(a, b Context) bool {
	return keyDomain(a) == keyDomain(b) && (a == b || a == Global || b == Global)
}

// strokeOf 是 c 现在占着的键：注册表里的，或编辑器自带命令显示的默认键（和弦不算）。
func strokeOf(c *Command) (ui.Modifiers, ui.Key, bool) {
	if c.Key != 0 {
		return c.Mods, c.Key, true
	}
	if editorOwn(c) && !c.Custom && c.Hint != "" && c.hintKey != 0 {
		return c.hintMods, c.hintKey, true
	}
	return 0, 0, false
}

// saveKeyDefaults 记下每条命令的默认键（第一次 apply 时；之后注册表里的就是改过的了）。
func saveKeyDefaults() {
	if len(keyDefaults) == len(Commands) {
		return
	}
	for _, c := range Commands {
		if _, ok := keyDefaults[c]; !ok {
			keyDefaults[c] = keyDefault{c.Mods, c.Key, c.Hint, c.hintMods, c.hintKey}
		}
	}
}

// DefaultKeys 是 c 的默认键的显示写法（编辑器自带命令是 mygo 的默认键）；没有为空。
func DefaultKeys(c *Command) string {
	saveKeyDefaults()
	d := keyDefaults[c]
	if d.key != 0 {
		return keysOf(d.mods, d.key)
	}
	return d.hint
}

// applyKeybindings 从默认键起把 list 盖上去，返回看不懂的条目（其余照常生效）。
func applyKeybindings(list []Keybinding) []string {
	saveKeyDefaults()
	for _, c := range Commands {
		d := keyDefaults[c]
		c.Mods, c.Key, c.Hint, c.Custom, c.Shadowed = d.mods, d.key, d.hint, false, ""
		c.hintMods, c.hintKey = d.hintMods, d.hintKey
	}
	type entry struct {
		c    *Command
		mods ui.Modifiers
		key  ui.Key
	}
	var es []entry
	var errs []string
	for i, b := range list {
		c := Find(b.Command)
		if c == nil {
			errs = append(errs, i18n.Tf("第 %d 条：没有命令 %s", i+1, b.Command))
			continue
		}
		mods, key, err := ParseKeys(b.Key)
		if err != nil {
			errs = append(errs, i18n.Tf("第 %d 条：键 %s 看不懂", i+1, b.Key))
			continue
		}
		es = slices.DeleteFunc(es, func(e entry) bool { return e.c == c }) // 同一条命令写了几次，靠后的算
		es = append(es, entry{c, mods, key})
	}
	for _, e := range es { // 按文件里的顺序：靠后的顶掉靠前的
		u := e.c
		u.Mods, u.Key, u.Custom, u.Shadowed = e.mods, e.key, true, ""
		if editorOwn(u) {
			u.Hint, u.hintKey = "", 0
		}
		if u.Key == 0 {
			continue
		}
		for _, o := range Commands {
			if o == u || !clash(u.Context, o.Context) {
				continue
			}
			if m, k, ok := strokeOf(o); ok && m == u.Mods && k == u.Key {
				o.Mods, o.Key, o.Shadowed = 0, 0, u.ID
				if editorOwn(o) {
					o.Hint, o.hintKey = "", 0
				}
			}
		}
	}
	editorKeys()
	keyRev++
	return errs
}

// menuTaken 是菜单栏现在占着的键（编辑器先收不到它们）。
func menuTaken(cs []*Command) func(ui.Modifiers, ui.Key) bool {
	type stroke struct {
		mods ui.Modifiers
		key  ui.Key
	}
	taken := map[stroke]bool{}
	for _, c := range cs {
		if c.Group != "" && c.Key != 0 {
			taken[stroke{c.Mods, c.Key}] = true
		}
	}
	return func(m ui.Modifiers, k ui.Key) bool { return taken[stroke{m, k}] }
}

// edHint 是 mygo 给 cmd 的默认键里第一下没被 taken 占的第一条（显示写法，和弦写两段）和那条绑定。
func edHint(defs []editor.Binding, cmd editor.Command, taken func(ui.Modifiers, ui.Key) bool) (string, editor.Binding, bool) {
	for _, b := range defs {
		if b.Command != cmd || taken(b.Mods, b.Key) {
			continue
		}
		h := keysOf(b.Mods, b.Key)
		if b.ThenKey != 0 {
			h += " " + keysOf(b.ThenMods, b.ThenKey)
		}
		return h, b, true
	}
	return "", editor.Binding{}, false
}

// editorKeys 算编辑器自带命令显示的键（Hint）和给编辑器的绑定（edBindings / diffBindings）。
func editorKeys() {
	defs := editor.DefaultBindings()
	menu := menuTaken(Commands)
	type stroke struct {
		mods ui.Modifiers
		key  ui.Key
	}
	user := map[stroke]bool{} // 用户给全局 / 编辑器命令的键：编辑器默认键的第一下是它就让出来
	for _, c := range Commands {
		if c.Custom && c.Key != 0 && (c.Context == Global || c.Context == Editor) {
			user[stroke{c.Mods, c.Key}] = true
		}
	}
	taken := func(m ui.Modifiers, k ui.Key) bool { return menu(m, k) || user[stroke{m, k}] }
	for _, c := range Commands {
		if !editorOwn(c) || c.Custom {
			continue
		}
		h, b, ok := edHint(defs, c.ed, taken) // 默认那条被占了还有别的默认键就显示它
		c.Hint, c.hintMods, c.hintKey = h, b.Mods, b.Key
		if !ok || b.ThenKey != 0 {
			c.hintKey = 0
		}
		if ok {
			c.Shadowed = ""
		}
	}

	type full struct {
		mods, thenMods ui.Modifiers
		key, thenKey   ui.Key
	}
	var bs []editor.Binding
	seen := map[full]bool{}
	add := func(list *[]editor.Binding, seen map[full]bool, b editor.Binding) {
		if k := (full{b.Mods, b.ThenMods, b.Key, b.ThenKey}); !seen[k] {
			seen[k] = true
			*list = append(*list, b)
		}
	}
	hasDefault := func(m ui.Modifiers, k ui.Key) bool {
		return slices.ContainsFunc(defs, func(b editor.Binding) bool { return b.Mods == m && b.Key == k })
	}
	// 改了键的编辑器命令：新键给它
	for _, c := range Commands {
		if editorOwn(c) && c.Custom && c.Key != 0 {
			add(&bs, seen, editor.Binding{Mods: c.Mods, Key: c.Key, Command: c.ed})
		}
	}
	// 改了键的编辑器命令和查找那几条：mygo 的旧默认键拿走
	for _, c := range Commands {
		if c.ed == editor.NoCommand || !c.Custom {
			continue
		}
		for _, b := range defs {
			if b.Command == c.ed && !(b.ThenKey == 0 && b.Mods == c.Mods && b.Key == c.Key) {
				add(&bs, seen, editor.Binding{Mods: b.Mods, Key: b.Key, ThenMods: b.ThenMods, ThenKey: b.ThenKey})
			}
		}
	}
	// 用户给窗口上的命令的键，编辑器默认要接的（单键或和弦的第一下）：让给窗口
	for _, c := range Commands {
		if c.Custom && c.Key != 0 && !editorOwn(c) && (c.Context == Global || c.Context == Editor) && hasDefault(c.Mods, c.Key) {
			add(&bs, seen, editor.Binding{Mods: c.Mods, Key: c.Key})
		}
	}
	edBindings = bs

	// Diff：J / K 跳改动（diff.nextHunk / prevHunk 的键），Diff 标签命令的键同样让给窗口，再是文件编辑器那套
	var ds []editor.Binding
	dseen := map[full]bool{}
	for _, h := range []struct {
		id  string
		cmd editor.Command
	}{{"diff.nextHunk", editor.NextHunk}, {"diff.prevHunk", editor.PreviousHunk}} {
		if c := Find(h.id); c != nil && c.Key != 0 {
			add(&ds, dseen, editor.Binding{Mods: c.Mods, Key: c.Key, Command: h.cmd})
		}
	}
	for _, c := range Commands {
		if c.Custom && c.Key != 0 && c.Context == DiffTab && !c.Local && hasDefault(c.Mods, c.Key) {
			add(&ds, dseen, editor.Binding{Mods: c.Mods, Key: c.Key})
		}
	}
	for _, b := range bs {
		add(&ds, dseen, b)
	}
	diffBindings = ds
}

// EditorBindings 是文件标签编辑器的 st.Bindings（EditorOptions 每帧设）：改了键的编辑器命令。没改过为空。
func EditorBindings() []editor.Binding { return edBindings }

// DiffBindings 是 Diff 的 Bindings：J / K（或改过的键）跳改动，加上 EditorBindings。
func DiffBindings() []editor.Binding {
	if diffBindings == nil {
		editorKeys()
	}
	return diffBindings
}

// KeyOf 是命令 id 现在的快捷键；没有键（或没有这条命令）ok 为假。右键菜单和界面元素自己接的键从这里读。
func KeyOf(id string) (ui.Modifiers, ui.Key, bool) {
	c := Find(id)
	if c == nil || c.Key == 0 {
		return 0, 0, false
	}
	return c.Mods, c.Key, true
}

// KeyConflict 是给 c 换成 mods+key 以后会被顶掉的命令（录键卡片上提示）；没有为 nil。
func KeyConflict(c *Command, mods ui.Modifiers, key ui.Key) *Command {
	if key == 0 {
		return nil
	}
	for _, o := range Commands {
		if o == c || !clash(c.Context, o.Context) {
			continue
		}
		if m, k, ok := strokeOf(o); ok && m == mods && k == key {
			return o
		}
	}
	return nil
}

// SetKeyRecording 开始 / 结束录键：录着的时候菜单栏不带快捷键（马上重建）。
func (a *App) SetKeyRecording(on bool) {
	if KeyRecording != on {
		KeyRecording = on
		a.SyncMenu()
	}
}

// KeyErrors 是 keybindings.json 里看不懂的条目（设置页列出来）。
func KeyErrors() []string { return keyErrors }

// KeysCustomized 是 keybindings.json 里有没有条目。
func KeysCustomized() bool { return len(keyFile) > 0 }

// KeybindingsPath 是 keybindings.json 的位置；没有状态目录（测试）为空。
func (a *App) KeybindingsPath() string {
	if a.StateDir == "" {
		return ""
	}
	return filepath.Join(a.StateDir, "keybindings.json")
}

// LoadKeybindings 读 keybindings.json 再 apply（启动时、在编辑器里保存了它时）；没有这个文件就是全默认。
func (a *App) LoadKeybindings() {
	keyFile, keyErrors, keyBroken = nil, nil, false
	var err error
	if p := a.KeybindingsPath(); p != "" {
		var data []byte
		if data, err = os.ReadFile(p); err == nil && len(bytes.TrimSpace(data)) > 0 {
			if json.Unmarshal(data, &keyFile) != nil {
				keyFile = nil
				err = json.Unmarshal(workspace.StripJSONC(data), &keyFile)
			}
		}
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
	}
	keyErrors = applyKeybindings(keyFile)
	if err != nil {
		keyFile, keyBroken = nil, true
		keyErrors = []string{i18n.Tf("整个文件读不了：%s", err.Error())}
	}
}

// startKeybindings 是启动时读快捷键；有看不懂的提示一次。
func (a *App) startKeybindings() {
	a.LoadKeybindings()
	if n := len(keyErrors); n > 0 && !keyToasted {
		keyToasted = true
		a.Toast = keyErrorsToast(n)
	}
}

func keyErrorsToast(n int) string {
	if keyBroken {
		return i18n.T("keybindings.json 读不了，用的是默认快捷键；见设置 → 快捷键")
	}
	return i18n.Tn("keybindings.json 有 %d 条看不懂，见设置 → 快捷键", n)
}

// keybindingsSaved 是 d 刚写了盘：是 keybindings.json 就重读快捷键，有看不懂的说一声。
func (a *App) keybindingsSaved(d *docs.Doc) {
	if p := a.KeybindingsPath(); p == "" || d.Abs != p {
		return
	}
	a.LoadKeybindings()
	if n := len(keyErrors); n > 0 {
		a.Toast = keyErrorsToast(n)
	}
}

// SetKeybinding 把命令 c 的键换成 mods+key（key 为 0 = 去掉），写进 keybindings.json 再 apply。
// 和默认的一样就只删掉文件里的那条。
func (a *App) SetKeybinding(c *Command, mods ui.Modifiers, key ui.Key) {
	saveKeyDefaults()
	d := keyDefaults[c]
	list := slices.DeleteFunc(slices.Clone(keyFile), func(b Keybinding) bool { return b.Command == c.ID })
	same := key == d.key && (key == 0 || mods == d.mods)
	if editorOwn(c) {
		same = key == d.hintKey && mods == d.hintMods && d.hintKey != 0 || key == 0 && d.hint == ""
	}
	if !same {
		list = append(list, Keybinding{c.ID, FormatKeys(mods, key)})
	}
	a.writeKeybindings(list)
}

// ResetKeybinding 把命令 c 放回默认键（删掉文件里它的条目）。
func (a *App) ResetKeybinding(c *Command) {
	a.writeKeybindings(slices.DeleteFunc(slices.Clone(keyFile), func(b Keybinding) bool { return b.Command == c.ID }))
}

// ResetAllKeybindings 问过以后把所有快捷键放回默认（文件写成空数组）。
func (a *App) ResetAllKeybindings() {
	n := len(keyFile)
	if n == 0 {
		return
	}
	a.Ask(i18n.T("重置全部快捷键？"), i18n.Tn("keybindings.json 里的 %d 条都会删掉，所有命令回到默认键。", n), i18n.T("重置"),
		func() { a.writeKeybindings([]Keybinding{}) })
}

// writeKeybindings 把 list 写进 keybindings.json（普通 JSON，一条一行；原来的注释会丢）再 apply；
// 开着它的标签没改动的话跟着重读。文件读不了时不写（会盖掉用户手写的），提示去编辑器里改。
func (a *App) writeKeybindings(list []Keybinding) {
	p := a.KeybindingsPath()
	if p == "" {
		keyFile = list
		keyErrors = applyKeybindings(list)
		return
	}
	if keyBroken {
		a.Fail(i18n.T("没有改快捷键"), errors.New(i18n.T("keybindings.json 读不了，先在编辑器里改好它")))
		return
	}
	var b strings.Builder
	b.WriteString("[")
	for i, k := range list {
		cmd, _ := json.Marshal(k.Command)
		key, _ := json.Marshal(k.Key)
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n  {\"command\": %s, \"key\": %s}", cmd, key)
	}
	if len(list) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	if err := writeFileAtomic(p, []byte(b.String())); err != nil {
		a.Fail(i18n.T("没有改快捷键"), err)
		return
	}
	keyFile = list
	keyErrors = applyKeybindings(list)
	for _, d := range a.AllDocs() {
		if d.Abs == p && !d.Modified() {
			a.Reload(d)
		}
	}
}

// writeFileAtomic 先写临时文件再改名过去，写到一半不会留下半个文件。
func writeFileAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// OpenKeybindings 在主窗口里把 keybindings.json 开成标签（没有就先写一个空数组）。
func (a *App) OpenKeybindings() {
	p := a.KeybindingsPath()
	if p == "" {
		return
	}
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		if err := writeFileAtomic(p, []byte("[]\n")); err != nil {
			a.Fail(i18n.T("打不开 keybindings.json"), err)
			return
		}
	}
	a.OpenPath(p)
}

// KeysOf 是 mods+key 显示的写法（⇧⌘P）；key 为 0 是空串。
func KeysOf(mods ui.Modifiers, key ui.Key) string {
	if key == 0 {
		return ""
	}
	return keysOf(mods, key)
}

// ShownKeys 是快捷键表、命令面板里写的键：生效的键，编辑器自带的没改过时是它的默认键（可能是和弦）；没有为空。
func ShownKeys(c *Command) string {
	if k := c.Keys(); k != "" {
		return k
	}
	return c.Hint
}

// KeyGroup 是设置 → 快捷键表的一段（KeyGroups）：标题已经翻译好。
type KeyGroup struct {
	Title    string
	Commands []*Command
}

// KeyGroups 是快捷键表的分段，和 docs/shortcuts.md 一个顺序：全局的按菜单组（菜单栏的顺序，没有组的在"其他"），
// 再是编辑器、Diff、文件树……各个上下文，最后编辑器自带的按分组（"编辑器 · 折叠"）。没有键的命令也在里面（能配一个）。
func KeyGroups() []KeyGroup {
	var gs []KeyGroup
	at := map[string]int{}
	add := func(title string, c *Command) {
		i, ok := at[title]
		if !ok {
			i = len(gs)
			at[title] = i
			gs = append(gs, KeyGroup{Title: title})
		}
		gs[i].Commands = append(gs[i].Commands, c)
	}
	for _, c := range Commands {
		if c.Context == Global && c.Section == "" {
			add(i18n.T(cmp.Or(c.Group, "其他")), c)
		}
	}
	for ctx := Editor; ctx <= SearchList; ctx++ {
		for _, c := range Commands {
			if c.Context == ctx && c.Section == "" {
				add(i18n.T(ctx.String()), c)
			}
		}
	}
	for _, c := range Commands {
		if c.Section != "" {
			add(i18n.T("编辑器")+" · "+i18n.T(c.Section), c)
		}
	}
	return gs
}

// KeyMatches 是命令 c 对不对得上快捷键的搜索词 q（小写、去掉了首尾空白）：标题（中文原文、译文）、id、
// 键的两种写法（⇧⌘P 和 cmd+shift+p），不分大小写。
func KeyMatches(c *Command, q string) bool {
	if q == "" {
		return true
	}
	cmdKeys := ""
	if mods, key, ok := strokeOf(c); ok {
		cmdKeys = FormatKeys(mods, key)
	}
	for _, s := range []string{c.Title, i18n.English()[c.Title], c.Label(), c.ID, ShownKeys(c), cmdKeys} {
		if s != "" && strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}
