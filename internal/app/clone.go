package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是克隆仓库（gaps 2.9）：对话框填地址、放到哪、目录名，git clone --progress 在父目录里跑（git.RunStream，
// stderr 一段一段读进度），侧栏文件页顶上一行占位（转圈、进度字、✕、细进度条）；克隆完加进列表（空工作区时把它当工作区打开）。
// 克隆不是哪个仓库的 Op（仓库还没有），跟着窗口走（a.Clones）；凭据提示和超时钟同写命令（askpass.go）。

// Clone 是一个正在跑的克隆。
type Clone struct {
	URL    string
	Parent string // 父目录（git 在这里跑）
	Name   string // 目录名
	Dest   string // Parent/Name
	Sub    bool   // --recurse-submodules
	Text   string // 进度，如"接收对象 45%"
	Pct    int    // 这一段的百分比，没有为 -1

	cancel  context.CancelFunc
	existed bool // 开始前目标目录就在（空目录）：失败、取消时不删
	ask     *askOwner
}

// CloneDialog 是"克隆仓库"对话框（a.CloneDlg）。
type CloneDialog struct {
	Open      bool
	URL       string
	Parent    string
	Name      string
	Sub       bool
	Prefilled bool // 地址是从剪贴板填的（mygo 的输入框没有"打开时全选"，光标在末尾）

	auto   string // 上次从地址推出来的目录名
	edited bool   // 用户改过目录名：不再跟着地址变
}

// ReadClipboard 读剪贴板（打开克隆对话框时看里面是不是 git 地址）；测试里换掉。
var ReadClipboard = func() string { return mygo.Clipboard.ReadText() }

var reGitURL = regexp.MustCompile(`^(https?://|ssh://|git://|file://|[\w.-]+@[\w.-]+:)`)

// LooksGitURL 是 s 像一个 git 地址（打开对话框时剪贴板里的才预填）：一行、没有空白，协议头或 user@host: 开头，或者以 .git 结尾。
func LooksGitURL(s string) bool {
	if s == "" || len(s) > 2048 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	return reGitURL.MatchString(s) || strings.HasSuffix(s, ".git")
}

// CloneName 是地址对应的目录名（git clone 不给目录名时用的那个）：最后一段去掉尾部的 / 和 .git；推不出为空。
func CloneName(u string) string {
	s := strings.TrimSpace(u)
	if i := strings.IndexAny(s, "?#"); i >= 0 && strings.Contains(s, "://") {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" || s == "." || s == ".." || strings.HasPrefix(s, "-") {
		return ""
	}
	return s
}

var (
	reCloneStage = regexp.MustCompile(`^(?:remote: )?(Counting objects|Compressing objects|Enumerating objects|Receiving objects|Resolving deltas|Updating files|Checking out files):\s*(?:(\d+)%)?`)
	reCloneSub   = regexp.MustCompile(`^Submodule '([^']+)'`)
)

// CloneProgress 把 git clone --progress 的一段 stderr 换成占位行上的字和百分比（LC_ALL=C，英文的）；认不出的 ok 为 false。
func CloneProgress(s string) (text string, pct int, ok bool) {
	if strings.HasPrefix(s, "Cloning into ") {
		return i18n.T("连接中…"), -1, true
	}
	if m := reCloneSub.FindStringSubmatch(s); m != nil {
		return i18n.Tf("子模块 %s", m[1]), -1, true
	}
	m := reCloneStage.FindStringSubmatch(s)
	if m == nil {
		return "", 0, false
	}
	switch m[1] {
	case "Receiving objects":
		text = i18n.T("接收对象")
	case "Resolving deltas":
		text = i18n.T("解析差异")
	case "Updating files", "Checking out files":
		text = i18n.T("检出文件")
	default:
		text = i18n.T("整理对象")
	}
	pct = -1
	if m[2] != "" {
		pct, _ = strconv.Atoi(m[2])
		text += " " + m[2] + "%"
	}
	return text, pct, true
}

// OpenClone 打开克隆对话框：剪贴板里像 git 地址就预填，放到上次克隆的目录，没有就第一个仓库的父目录、再没有就家目录。
func (a *App) OpenClone() {
	d := CloneDialog{Open: true, Parent: tildeOf(a.cloneParent())}
	if s := strings.TrimSpace(ReadClipboard()); LooksGitURL(s) {
		d.URL, d.Prefilled = s, true
	}
	d.Sync()
	a.CloneDlg = d
}

func (a *App) cloneParent() string {
	if p := a.St.CloneDir; p != "" && isDir(p) {
		return p
	}
	for _, r := range a.Repos {
		if p := filepath.Dir(r.Root); isDir(p) {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// ChooseCloneParent 弹系统的选目录对话框选"放到"（对话框里的"选择…"）；对话框会阻塞，所以在 goroutine 里开。
func (a *App) ChooseCloneParent() {
	up, win, open := a.Update, a.Win, a.OpenDialog
	def := untilde(strings.TrimSpace(a.CloneDlg.Parent))
	go func() {
		paths, err := open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       i18n.T("克隆到"),
			Message:     i18n.T("选克隆下来的仓库放在哪个目录里"),
			ButtonLabel: i18n.T("选择"),
			DefaultPath: def,
			Directory:   true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		up(func() {
			if a.CloneDlg.Open {
				a.CloneDlg.Parent = tildeOf(paths[0])
			}
		})
	}()
}

// Sync 每帧调：用户没改过目录名就跟着地址推；改过（和上次推的不一样了）就不再动它。
func (d *CloneDialog) Sync() {
	if d.Name != d.auto {
		d.edited = true
	}
	if !d.edited {
		d.auto = CloneName(d.URL)
		d.Name = d.auto
	}
}

// Dest 是会克隆到的目录（绝对路径，~ 展开）；父目录或目录名空时为空。
func (d *CloneDialog) Dest() string {
	parent, name := untilde(strings.TrimSpace(d.Parent)), strings.TrimSpace(d.Name)
	if parent == "" || name == "" {
		return ""
	}
	return filepath.Join(parent, name)
}

// DestLabel 是说明行里的"克隆到 …"（家目录写成 ~）；Command 是会跑的命令。
func (d *CloneDialog) DestLabel() string {
	if p := d.Dest(); p != "" {
		return tildeOf(p)
	}
	return ""
}

func (d *CloneDialog) Command() string {
	s := "git clone"
	if d.Sub {
		s += " --recurse-submodules"
	}
	return s + " " + redactURL(strings.TrimSpace(d.URL)) + " " + strings.TrimSpace(d.Name)
}

// CloneErrs 是对话框里的红字：父目录不在、目录名不合法 / 已经有了而且不空 / 正在往那里克隆。空的不算错（按钮灰着就行）。
func (a *App) CloneErrs() (parentErr, nameErr string) {
	d := &a.CloneDlg
	parent, name := untilde(strings.TrimSpace(d.Parent)), strings.TrimSpace(d.Name)
	if parent != "" && !isDir(parent) {
		parentErr = i18n.T("没有这个目录")
	}
	switch {
	case name == "":
	case strings.ContainsAny(name, "/\\") || name == "." || name == "..":
		nameErr = i18n.T("目录名里不能有 /")
	case parentErr != "" || parent == "":
	case slices.ContainsFunc(a.Clones, func(c *Clone) bool { return c.Dest == d.Dest() }):
		nameErr = i18n.T("正在往这个目录克隆")
	case !emptyOrMissing(d.Dest()):
		nameErr = i18n.T("已经有这个目录了，而且不是空的")
	}
	return
}

// CloneReady 是能不能点"克隆"。
func (a *App) CloneReady() bool {
	d := &a.CloneDlg
	p, n := a.CloneErrs()
	return strings.TrimSpace(d.URL) != "" && d.Dest() != "" && p == "" && n == ""
}

// StartClone 按对话框里填的开始克隆（点了"克隆"或 ⏎）。
func (a *App) StartClone() {
	if !a.CloneReady() {
		return
	}
	d := a.CloneDlg
	a.CloneDlg = CloneDialog{}
	cl := &Clone{URL: strings.TrimSpace(d.URL), Parent: untilde(strings.TrimSpace(d.Parent)), Name: strings.TrimSpace(d.Name),
		Dest: d.Dest(), Sub: d.Sub, Text: i18n.T("连接中…"), Pct: -1}
	_, err := os.Stat(cl.Dest)
	cl.existed = err == nil
	args := []string{"clone", "--progress"}
	if cl.Sub {
		args = append(args, "--recurse-submodules")
	}
	args = append(args, "--", cl.URL, cl.Name)
	ctx, cancel := context.WithCancel(context.Background())
	cl.cancel = cancel
	up := a.Update
	cl.ask = a.askRegister(nil, cl.Name, up)
	env := askEnv(cl.ask)
	a.Clones = append(a.Clones, cl)
	go func() {
		var last time.Time
		out, err := git.RunStream(ctx, cl.Parent, env, cl.ask.keep, func(s string) {
			text, pct, ok := CloneProgress(s)
			if !ok || time.Since(last) < 100*time.Millisecond { // 进度一秒最多推 10 次
				return
			}
			last = time.Now()
			up(func() { cl.Text, cl.Pct = text, pct })
		}, args...)
		cancel()
		if err != nil && !cl.existed { // 开始前没有的目录是这次克隆建的：失败、取消都不留半个仓库（git 自己一般也会删）
			os.RemoveAll(cl.Dest)
		}
		up(func() {
			a.askDone(cl.ask)
			a.cloneDone(cl, out, err)
		})
	}()
}

// cloneDone 是克隆跑完（主线程）：成功的加进列表并选中，失败的出错框（带 git.Explain 的说明），取消的只 toast。
func (a *App) cloneDone(cl *Clone, out string, err error) {
	a.Clones = slices.DeleteFunc(a.Clones, func(c *Clone) bool { return c == cl })
	if cl.ask.canceled && err != nil {
		err = git.ErrCanceled
	}
	cmd := "$ git clone " + redactURL(cl.URL) + " " + cl.Name
	label := i18n.Tf("克隆 %s", cl.Name)
	switch {
	case errors.Is(err, git.ErrCanceled):
		a.Log(nil, cmd, canceledText(out, label, nil), true)
		a.Toast = i18n.Tf("已取消：%s", label)
	case err != nil:
		a.Log(nil, cmd, logText(out, err), true)
		a.Fail(i18n.T("克隆失败"), err)
	default:
		a.Log(nil, cmd, logText(out, nil), false)
		a.St.CloneDir = cl.Parent
		if a.WsPath == "" { // 空工作区没有可以加进去的工作区：把克隆下来的当工作区打开
			a.SaveState()
			a.OpenWorkspace(cl.Dest)
		} else if err := a.AddRepoDir(cl.Dest); err != nil {
			a.Fail(i18n.T("克隆好了，加进列表失败"), err)
			break
		}
		a.Toast = i18n.Tf("已克隆 %s", cl.Name)
	}
	a.writesStopped()
}

// CancelClone 取消一个克隆（占位行的 ✕、右键）：git 的进程组结束，建了一半的目录删掉。
func (a *App) CancelClone(cl *Clone) {
	if cl.cancel != nil {
		cl.cancel()
	}
}

// redactURL 是记进输出栏和日志的地址：https://user:token@host 里的密码 / 令牌换成 ***。
func redactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	if _, ok := u.User.Password(); !ok {
		return s
	}
	u.User = url.UserPassword(u.User.Username(), "***")
	return strings.Replace(u.String(), "%2A%2A%2A", "***", 1)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// emptyOrMissing 是 p 不存在，或者是个空目录（git clone 收的两种）。
func emptyOrMissing(p string) bool {
	ents, err := os.ReadDir(p)
	if err != nil {
		_, serr := os.Stat(p)
		return os.IsNotExist(serr)
	}
	return len(ents) == 0
}

// tildeOf 把家目录开头的路径写成 ~/…（框里显示）；untilde 反过来。
func tildeOf(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func untilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	if p != "" && !filepath.IsAbs(p) {
		return absPath(p)
	}
	return p
}
