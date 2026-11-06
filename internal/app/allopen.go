package app

import (
	"log"
	"slices"
	"time"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/workspace"
)

// allOpenLogf 是“全开”那行日志打到哪儿（测试换掉）。
var allOpenLogf = log.Printf

// AllOpenWait 是开始搜之后至少等多久才打“全开”那行（shell 起好）；测试调小。
var AllOpenWait = 5 * time.Second

// AllOpen 是量内存用的“全开”（WORKBENCH_ALLOPEN=搜索词，量内存用）：仓库都读完后
// 再开两个 Diff 标签（头两项未提交的改动），侧栏换到搜索搜 q，再切回原来的标签，当前仓库的终端标签都起 shell；
// 会话里的标签、底栏、终端标签照 state.json 恢复。搜完、且开始搜过了 AllOpenWait 后日志里一行：
// 全开：N 个标签（M 个 Diff），搜 "q" k 处（用时 t ms，搜了 f 个文件、跳过 s 个[、到上限]），n 个 shell。
// 标签和 Diff 数的是所有组里的（会话里恢复的 Diff 也算：仓库没有未提交的改动时量的种子自己带两个提交的 Diff）。
func (a *App) AllOpen(q string) {
	up := a.Update
	var try func()
	try = func() {
		for _, r := range a.Repos {
			if !r.Loaded {
				time.AfterFunc(200*time.Millisecond, func() { up(try) }) // 计时器上别读 a 的字段
				return
			}
		}
		cur, n := a.CurrentDoc(), 0
		for _, r := range a.Repos {
			for _, ch := range r.Status.Changes {
				if n < 2 {
					a.OpenDiff(r, ch, true)
					n++
				}
			}
		}
		if i := slices.Index(a.Docs, cur); i >= 0 {
			a.SelectDoc(i)
		}
		if r := a.Current(); r != nil {
			for _, t := range a.TermTabs(r) {
				for _, p := range t.Panes() {
					a.EnsurePane(r, p)
				}
			}
		}
		a.ShowSidebar(workspace.ModeSearch)
		a.Search.Query.Text = q
		a.SearchNow()
		started := time.Now()
		var report func()
		report = func() {
			s := &a.Search
			if s.Running || !s.Done || time.Since(started) < AllOpenWait {
				time.AfterFunc(100*time.Millisecond, func() { up(report) })
				return
			}
			res := s.Res
			tabs, diffs := a.AllDocs(), 0
			for _, d := range tabs {
				if d.Kind == docs.Diff {
					diffs++
				}
			}
			allOpenLogf("全开：%d 个标签（%d 个 Diff），搜 %q %d 处（用时 %d ms，搜了 %d 个文件、跳过 %d 个%s），%d 个 shell",
				len(tabs), diffs, q, len(res.Matches), res.Elapsed.Milliseconds(), res.Searched, res.Skipped,
				map[bool]string{true: "、到上限"}[res.Truncated], a.StartedPanes()) // 字面量都在 allOpenLogf 的参数里：日志，TestCatalog 不收
		}
		time.AfterFunc(100*time.Millisecond, func() { up(report) })
	}
	try()
}
