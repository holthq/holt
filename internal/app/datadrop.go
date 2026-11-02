package app

import (
	"strings"

	"github.com/egoist/mygo/transfer"

	"github.com/lemoeade/grove/internal/docs"
)

// 这个文件是别的应用拖进来的东西：访达、备忘录、浏览器拖到可编辑的文件标签上和终端的一格上。
// 文件树自己的行拖到编辑器 / 终端见 treedrag.go（那是进程里的值，不走这里）。不写磁盘。
//
// 两处都也接文件列表：访达的拖放板上除了 file URL 可能还有文件名的纯文字，只接文字的话 mygo 按格式把整个拖放交给这里，
// 访达拖来的文件就被当成文字插进去了；接了文件列表，带文件的照文件的规矩办。

// DataDrop 是编辑器和终端登记的放的地方：文件、URL、文字，只给复制（别的应用按着 ⌘ 拖成移动时也只是复制，源那边的不会删）。
var DataDrop = transfer.DropOptions{
	Formats:    []transfer.Format{transfer.FileList, transfer.URIList, transfer.Text},
	Operations: transfer.Copy,
}

// ExitedDrop 是 shell 退出了的终端格登记的：只接文件（照窗口的规矩打开），文字没人接。
// 不登记的话访达的文件落到 mygo 终端视图自己的 DroppedFiles，写进没有 shell 的终端，什么都看不到。
var ExitedDrop = transfer.DropOptions{
	Formats:    []transfer.Format{transfer.FileList},
	Operations: transfer.Copy,
}

// droppedText 是拖来的文字：有 Text 取 Text，没有就取 URL（一行一个）。
func droppedText(d transfer.Data) string {
	if b, err := d.Read(transfer.Text); err == nil {
		return string(b)
	}
	if urls, err := d.URLs(); err == nil {
		return strings.Join(urls, "\n")
	}
	return ""
}

// DropOnDoc 是别的应用拖到可编辑的文件标签 d 上松手：带文件的照访达拖进窗口的规矩开（OpenArgs）；
// 别的取文字插在光标处（和粘贴一样替换选中的，多个光标每个都插；mygo 的编辑器没有"点 → 位置"，落点做不了），键盘给这一组。
// 只读的标签界面那边不登记。
func (a *App) DropOnDoc(d *docs.Doc, drop transfer.Drop) {
	if paths, _ := drop.Data.Files(); len(paths) > 0 {
		a.OpenArgs(paths, "")
		return
	}
	text := droppedText(drop.Data)
	if text == "" || d.Buf == nil || d.St.ReadOnly {
		return
	}
	if g := a.GroupOf(d); g != nil {
		a.FocusGroup(g)
	}
	d.St.Insert(text)
	a.FocusDoc = true
}

// DropOnTerm 是别的应用拖到终端的一格 p 上松手，都不回车，键盘给这一格：带文件的贴引号过的绝对路径（ShellFiles，
// 和文件树拖来的写法一样）；别的贴文字（程序要的话是括号粘贴，不会当命令跑）。
// shell 退出了（或还没起来）的格子：带文件的照访达拖进窗口的规矩开（OpenArgs），文字不接。
func (a *App) DropOnTerm(r *Repo, p *TermPane, drop transfer.Drop) {
	paths, _ := drop.Data.Files()
	if p.Term == nil || p.Exited {
		if len(paths) > 0 {
			a.OpenArgs(paths, "")
		}
		return
	}
	if len(paths) > 0 {
		p.Term.PasteData(ShellFiles(paths))
	} else if text := droppedText(drop.Data); text != "" {
		p.Term.Paste(text)
	} else {
		return
	}
	a.FocusPane(r, p)
	a.FocusTermTab(r.tabOf(p))
}

// ShellFiles 是拖到终端的文件：文字是 shellJoin（和文件树拖来的一样）；读剪贴板的程序（mode 5522）
// 另外拿到文件列表（照 mygo 终端自己接文件时的拼法：第一项带文件列表、URL 和全部的文字）。
func ShellFiles(paths []string) transfer.Data {
	text := transfer.Bytes(transfer.Text, []byte(shellJoin(paths)))
	d, err := transfer.FileData(paths...)
	if err != nil {
		return transfer.New(transfer.NewItem(text))
	}
	items := d.Items()
	first, _ := items[0].Read(transfer.FileList)
	items[0] = transfer.NewItem(transfer.Bytes(transfer.FileList, first), transfer.Bytes(transfer.URIList, first), text)
	return transfer.New(items...)
}
