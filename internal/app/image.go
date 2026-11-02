package app

import (
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
)

// 图片（二）和十六进制视图：以十六进制查看的开关、用默认应用打开。缩放、拖动、棋盘格在 ui/image.go。

// CanHex 是 d 能不能切十六进制视图（view.hex）：读好了的文件或历史版本（文本、图片、二进制）；
// 改过没保存的文本不让切（切过去换缓冲区，改动就没了）；只有十六进制一种看法的二进制关不掉；同一个文件开着几份（镜像）不让切。
func (a *App) CanHex(d *docs.Doc) bool {
	if d == nil || d.Kind != docs.File && d.Kind != docs.Version || d.Loading || d.Modified() || d.HexOnly() || a.hasCopy(d) {
		return false
	}
	return d.Buf != nil || d.Bitmap != nil
}

// ToggleHex 在十六进制和文本 / 图片之间切换当前标签（view.hex、图片视图顶栏的“十六进制”、十六进制顶栏的“按文本打开”）：
// 换一个新的缓冲区重新读，十六进制只读前 docs.HexMax 字节。
func (a *App) ToggleHex() { a.SetHex(a.CurrentDoc(), !a.hexOn()) }

func (a *App) hexOn() bool { d := a.CurrentDoc(); return d != nil && d.Hex }

// SetHex 把 d 切成（on）或切出十六进制视图。
func (a *App) SetHex(d *docs.Doc, on bool) {
	if d == nil || d.Hex == on || !a.CanHex(d) {
		return
	}
	d.Hex, d.Preview, d.ShowBlame = on, false, false
	d.Buf, d.Bitmap = nil, nil // 换新的：光标、撤销不跨两种看法
	a.readDoc(d, true)
}

// OpenDefault 用默认应用打开 d 的文件（十六进制顶栏的链接）。
func (a *App) OpenDefault(d *docs.Doc) {
	if d == nil || d.Abs == "" {
		return
	}
	if err := a.ShowPath(d.Abs); err != nil {
		a.Toast = i18n.Tf("打不开 %s：%s", d.Title, err.Error())
	}
}
