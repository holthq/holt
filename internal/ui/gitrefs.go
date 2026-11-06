package ui

import (
	"slices"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// refDialog 是分支改名、新建 / 删除标签、远程改名 / 添加共用的对话框（app.RefDlg，照"新建分支"做，dialogFrame）：
// 标题、一句说明、一两个输入框（删标签是"同时删除远程上的"勾选和红按钮）、底下是会跑的命令。
func refDialog(a *app.App, c *wui.Context) {
	d := &a.RefDlg
	r := d.Repo
	if r == nil || a.RepoFor(r.Root) != r {
		a.RefDlg = app.RefDialog{}
		return
	}
	name := strings.TrimSpace(d.Name)
	var title, note, button, nameErr string
	var cmd []string
	var ok bool
	var run func()
	switch d.Kind {
	case app.DlgRenameBranch:
		title, button = i18n.Tf("重命名分支 %s", d.Old), i18n.T("改名")
		note = i18n.T("只改本地分支名；有上游的话上游会取消，推送时在 origin 上新建。")
		nameErr = refNameErr(name, d.Old, slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return !b.Remote && b.Name == name }))
		ok = name != "" && name != d.Old && nameErr == ""
		cmd = []string{"git branch -m", d.Old, name}
		run = func() { a.RenameBranch(r, d.Old, name) }
	case app.DlgNewTag:
		title, button = i18n.T("新建标签"), i18n.T("新建")
		note = i18n.Tf("打在 %s 上；写了信息是附注标签，不写是轻量标签。", tagFrom(r, d.From))
		nameErr = refNameErr(name, "", slices.ContainsFunc(r.Tags, func(x git.Tag) bool { return x.Name == name }))
		ok = name != "" && nameErr == ""
		cmd = []string{"git tag", name}
		if strings.TrimSpace(d.Text) != "" {
			cmd = []string{"git tag -a", name, "-m …"}
		}
		if d.From != "" {
			cmd = append(cmd, d.From)
		}
		run = func() { a.NewTag(r, name, d.Text, d.From) }
	case app.DlgDeleteTag:
		title, button = i18n.Tf("删除标签 %s？", d.Old), i18n.T("删除标签")
		note = i18n.T("只删本地的（git tag -d）；推送过的话远程上还在，要一起删就勾上下面那项。")
		if app.TagRemote(r) == "" { // 没有远程就没有那个勾选框
			note = i18n.T("只删本地的（git tag -d）。")
		}
		ok = true
		cmd = []string{"git tag -d", d.Old}
		if rm := app.TagRemote(r); d.Also && rm != "" {
			cmd = append(cmd, "&& git push", rm, "--delete", "refs/tags/"+d.Old)
		}
		run = func() { a.DeleteTag(r, d.Old, d.Also) }
	case app.DlgRenameRemote:
		title, button = i18n.Tf("远程 %s 改名", d.Old), i18n.T("改名")
		note = i18n.Tf("远程分支的本地引用（%d 个）和用到它的上游设置 git 会一起改。", app.RemoteRefs(r, d.Old))
		nameErr = refNameErr(name, d.Old, app.HasRemote(r, name))
		ok = name != "" && name != d.Old && nameErr == ""
		cmd = []string{"git remote rename", d.Old, name}
		run = func() { a.RenameRemote(r, d.Old, name) }
	case app.DlgAddRemote:
		title, button = i18n.T("添加远程"), i18n.T("添加")
		note = i18n.Tf("给 %s 加一个远程，加好后接着抓取它。", r.Name)
		nameErr = refNameErr(name, "", app.HasRemote(r, name))
		url := strings.TrimSpace(d.Text)
		ok = name != "" && nameErr == "" && url != ""
		cmd = []string{"git remote add", name, url}
		run = func() { a.AddRemote(r, name, url) }
	}
	submit := func() {
		if ok {
			d.Open = false
			run()
		}
	}
	o := dialogOpts{Title: title, Note: note, Hint: button}
	if len(a.Repos) > 1 && d.Kind != app.DlgAddRemote { // 添加远程的说明里有仓库名
		o.Repo = r.Name
	}
	kind := btnPrimary
	if d.Kind == app.DlgDeleteTag { // 没有输入框：⏎ 直接删，按钮是红的
		o.Kind, o.Enter, kind = dlgDanger, submit, btnDanger
	}
	dialogFrame(c, &d.Open, o, func() {
		switch d.Kind {
		case app.DlgDeleteTag:
			if rm := app.TagRemote(r); rm != "" {
				wui.Checkbox(c, &d.Also, i18n.Tf("同时删除 %s 上的", rm)).FontSize(12).
					Tooltip(i18n.Tf("本地删掉后再跑 git push %s --delete", rm))
			}
		case app.DlgNewTag:
			dlgField(c, i18n.T("标签名"), nameErr, func() {
				if dlgInput(c, wui.TextInput(c, &d.Name).Placeholder(i18n.T("如 v1.2.0")).Label(i18n.T("标签名")).AutoFocus()).Submitted() {
					submit()
				}
			})
			dlgField(c, i18n.T("信息"), "", func() {
				if dlgInput(c, wui.TextInput(c, &d.Text).Placeholder(i18n.T("可不填")).Label(i18n.T("标签信息"))).Submitted() {
					submit()
				}
			})
		case app.DlgAddRemote:
			// 名字默认填了 origin 时光标直接在地址框
			dlgField(c, i18n.T("名字"), nameErr, func() {
				in := dlgInput(c, wui.TextInput(c, &d.Name).Placeholder("origin").Label(i18n.T("远程名")))
				if !d.Prefilled {
					in.AutoFocus()
				}
			})
			dlgField(c, i18n.T("地址"), "", func() {
				in := dlgInput(c, wui.TextInput(c, &d.Text).Placeholder("git@github.com:me/repo.git").Label(i18n.T("远程地址")))
				if d.Prefilled {
					in.AutoFocus()
				}
				if in.Submitted() {
					submit()
				}
			})
		default:
			label := i18n.T("新的名字")
			dlgField(c, label, nameErr, func() {
				if dlgInput(c, wui.TextInput(c, &d.Name).Label(label).AutoFocus()).Submitted() {
					submit()
				}
			})
		}
		if ok {
			dlgCommand(c, strings.Join(cmd, " "))
		}
	}, func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			d.Open = false
		}
		if dlgButton(c, button, kind, !ok, d.Kind == app.DlgDeleteTag) {
			submit()
		}
	})
	if !a.RefDlg.Open {
		a.RefDlg = app.RefDialog{}
	}
}

// refNameErr 是名字框下面的红字：已经有了、或 git 一定不收的字符；空、和原来一样不算错（按钮灰着就行）。
func refNameErr(name, old string, exists bool) string {
	switch {
	case name == "" || name == old:
		return ""
	case exists:
		return i18n.T("已经有这个名字了")
	case strings.ContainsAny(name, " ~^:?*[\\") || strings.HasPrefix(name, "-") || strings.Contains(name, "..") ||
		strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, "."):
		return i18n.T("名字里不能有空格和 ~^:?*[\\，不能以 - 开头、以 / . .lock 结尾，不能有 ..")
	}
	return ""
}

// tagFrom 是"打在 … 上"的那个："main 的 HEAD"或"提交 abc1234"。
func tagFrom(r *app.Repo, from string) string {
	if from != "" {
		return i18n.Tf("提交 %s", from)
	}
	if b, ok := app.CurrentBranch(r); ok {
		return i18n.Tf("%s 的 HEAD", b.Name)
	}
	return "HEAD"
}
