package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是不开 termd 时终端的回滚：关窗口 / 退出 / 换工作区前（NoteTermDirs）把每个在应用进程里的格的回滚
// （Terminal.History：回滚和屏幕到光标那行的 VT 序列）写到 <StateDir>/termhist/<工作区哈希>/ 下一个文件，
// 文件名记进会话那一格（workspace.TermSplit.Hist）；下次这一格启动时读回来、喂给新终端（前面一行淡色的分隔），
// 读完就删。后台（termd）的格不写：会话本来就在。不定时写，全屏程序开着时（History 是空的）留着上一次写的。

// TermHistCap 是一格最多存多少回滚（从一行的开头截，留最后的）。
var TermHistCap = 256 << 10

// TermHistMax 是整个 termhist 目录的上限：超过了先删最老的。
var TermHistMax int64 = 20 << 20

// termHistRoot 是 <StateDir>/termhist；没有状态目录为空。
func (a *App) termHistRoot() string {
	if a.StateDir == "" {
		return ""
	}
	return filepath.Join(a.StateDir, "termhist")
}

// termHistDir 是这个工作区的回滚目录；空工作区（终端不记进会话）为空。
func (a *App) termHistDir() string {
	root, key := a.termHistRoot(), a.sessionKey()
	if root == "" || key == "" {
		return ""
	}
	return filepath.Join(root, termHistKey(key))
}

// termHistKey 是工作区（会话的键）在 termhist 下的目录名。
func termHistKey(ws string) string {
	h := sha256.Sum256([]byte(ws))
	return hex.EncodeToString(h[:8])
}

// trimHist 是 h 的最后 limit 字节以内、从一行的开头起的那段（不截在一行、一个转义序列中间）。
func trimHist(h []byte, limit int) []byte {
	if len(h) <= limit {
		return h
	}
	cut := len(h) - limit
	i := bytes.IndexByte(h[cut:], '\n')
	if i < 0 {
		return nil
	}
	return h[cut+i+1:]
}

// saveTermHist 把每个在应用进程里、启动过的格的回滚写进 termhist（关窗口 / 换工作区前，同步），再清掉用不着的文件。
func (a *App) saveTermHist() {
	dir := a.termHistDir()
	if dir == "" {
		return
	}
	start, n, size := time.Now(), 0, 0
	for _, r := range a.Repos {
		for _, t := range r.allTerms() {
			for _, p := range t.Panes() {
				if p.Term == nil || p.conn != nil {
					continue
				}
				h := trimHist(p.Term.History(), TermHistCap)
				if len(bytes.TrimSpace(h)) == 0 {
					continue // 空的、全屏程序开着：留着上一次的（没有就不记）
				}
				if err := os.MkdirAll(dir, 0o700); err != nil {
					log.Printf("终端回滚：%v", err)
					return
				}
				name := fmt.Sprintf("%d-%d.vt", time.Now().UnixMilli(), p.ID)
				if err := os.WriteFile(filepath.Join(dir, name), h, 0o600); err != nil {
					log.Printf("终端回滚：%v", err)
					continue
				}
				if p.hist != "" {
					os.Remove(filepath.Join(dir, p.hist))
				}
				p.hist = name
				n, size = n+1, size+len(h)
			}
		}
	}
	a.gcTermHist()
	if n > 0 {
		log.Printf("终端回滚：写了 %d 格、%d 字节，%v", n, size, time.Since(start).Round(time.Microsecond))
	}
}

// takeHist 读回这一格上次的回滚、删掉文件，返回要喂给新终端的（分隔行 + 回滚）；没有为 nil。
func (a *App) takeHist(p *TermPane) []byte {
	name := p.hist
	p.hist = ""
	dir := a.termHistDir()
	if name == "" || dir == "" {
		return nil
	}
	path := filepath.Join(dir, name)
	defer os.Remove(path)
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > int64(TermHistCap)+4096 {
		return nil
	}
	h, err := os.ReadFile(path)
	if err != nil || len(h) == 0 {
		return nil
	}
	var b bytes.Buffer
	b.WriteString("\x1b[0;2m── ")
	b.WriteString(i18n.Tf("上次的输出（%s）", fi.ModTime().Format("2006-01-02 15:04")))
	b.WriteString(" ──\x1b[0m\r\n")
	b.Write(h)
	b.WriteString("\x1b[0m")
	return b.Bytes()
}

// dropHist 删掉这一格还没读回去的回滚（关格、关标签、移除仓库）。
func (a *App) dropHist(name string) {
	if dir := a.termHistDir(); name != "" && dir != "" {
		os.Remove(filepath.Join(dir, name))
	}
}

// splitHists 是会话里一个仓库的终端记着的回滚文件名。
func splitHists(ts *workspace.Terms) []string {
	var out []string
	if ts == nil {
		return nil
	}
	for _, t := range ts.Tabs {
		for _, l := range t.Split.Leaves() {
			if l.Hist != "" {
				out = append(out, l.Hist)
			}
		}
	}
	return out
}

// gcTermHist 清 termhist：这个工作区的目录里格上没记着的文件、别的工作区的会话里没记的文件和没有会话的工作区的目录；
// 剩下的超过 TermHistMax 先删最老的。
func (a *App) gcTermHist() {
	root := a.termHistRoot()
	if root == "" {
		return
	}
	keep := map[string]bool{} // "<目录>/<文件>"
	mine := a.sessionKey()
	if mine != "" {
		d := termHistKey(mine)
		for _, r := range a.Repos {
			for _, t := range r.allTerms() {
				for _, p := range t.Panes() {
					if p.hist != "" {
						keep[d+"/"+p.hist] = true
					}
				}
			}
			for _, h := range splitHists(r.savedTerms) {
				keep[d+"/"+h] = true
			}
		}
	}
	for key, s := range a.store().State().Sessions {
		if key == mine || s == nil {
			continue
		}
		d := termHistKey(key)
		for _, ts := range s.Terms {
			for _, h := range splitHists(ts) {
				keep[d+"/"+h] = true
			}
		}
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	dirs, _ := os.ReadDir(root)
	for _, d := range dirs {
		sub := filepath.Join(root, d.Name())
		if !d.IsDir() {
			os.Remove(sub)
			continue
		}
		left := 0
		ents, _ := os.ReadDir(sub)
		for _, e := range ents {
			path := filepath.Join(sub, e.Name())
			fi, err := e.Info()
			if err != nil || !fi.Mode().IsRegular() || !keep[d.Name()+"/"+e.Name()] {
				os.RemoveAll(path)
				continue
			}
			files = append(files, file{path, fi.Size(), fi.ModTime()})
			total += fi.Size()
			left++
		}
		if left == 0 {
			os.Remove(sub)
		}
	}
	if total <= TermHistMax {
		return
	}
	slices.SortFunc(files, func(x, y file) int { return x.mod.Compare(y.mod) })
	for _, f := range files {
		if total <= TermHistMax {
			break
		}
		if err := os.Remove(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
			total -= f.size
		}
	}
}
