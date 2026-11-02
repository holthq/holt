package app

import (
	"path/filepath"
	"runtime/debug"
	"slices"
	"time"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/search"
)

// 这个文件是搜索结果跟着磁盘变：监听报来文件变了、保存了开着的文件（含自动保存），结果里有的那些文件
// 过 FollowDelay 重读重搜，换进结果（splice）。结果外面新配上的文件不管，摘要那一行的"重新搜索"整个再搜。

// FollowDelay 是文件变了之后等多久重搜结果里的这些文件：这段时间里又变的并成一次。
const FollowDelay = 300 * time.Millisecond

// SearchFilesChanged 是仓库 r 里 dirs 底下（仓库内路径，文件或目录；full 是整个仓库）的文件变了：
// 结果里有的这些文件记下来，FollowDelay 后重搜。正在搜、出错了、还没搜的不管（搜完的就是新的）。
func (a *App) SearchFilesChanged(r *Repo, dirs []string, full bool) {
	s := &a.Search
	if !s.Done || s.Running || s.matcher == nil || s.Res.Err != nil {
		return
	}
	ms := s.Res.Matches
	for i := 0; i < len(ms); {
		_, j := s.fileRange(i)
		m := ms[i]
		if m.Root == r.Root && (full || slices.ContainsFunc(dirs, func(d string) bool { return files.Under(m.Path, d) })) {
			if s.follow == nil {
				s.follow = map[fileKey]bool{}
			}
			s.follow[fileKey{m.Root, m.Path}] = true
		}
		i = j
	}
	a.followLater()
}

// followLater 在记了要重搜的文件、又没有在等或在读的时候起一个 FollowDelay 的定时器。
func (a *App) followLater() {
	s := &a.Search
	if len(s.follow) == 0 || s.followTimer != nil || s.following {
		return
	}
	gen, up := s.gen, a.Update
	s.followTimer = time.AfterFunc(FollowDelay, func() { up(func() { a.followSearch(gen) }) })
}

// stopFollow 是开始新的一次搜索：等着重搜的文件丢掉（新的结果就是新读的），在读的那次回来也作废（gen 变了）。
func (s *SearchState) stopFollow() {
	if s.followTimer != nil {
		s.followTimer.Stop()
		s.followTimer = nil
	}
	s.follow, s.following = nil, false
}

// followSearch 在后台重读重搜记下的文件，读完回主线程 searchFollowed。
func (a *App) followSearch(gen int) {
	s := &a.Search
	if s.gen != gen {
		return
	}
	s.followTimer = nil
	keys := s.follow
	s.follow = nil
	if len(keys) == 0 || !s.Done || s.matcher == nil {
		return
	}
	repos := map[string]string{} // 仓库根 → 名字，从原来的结果里拿
	for _, m := range s.Res.Matches {
		repos[m.Root] = m.Repo
	}
	m, limit, ver, up := s.matcher, s.resQ.MaxSize(), s.ver, a.Update
	s.following = true
	go func() {
		fresh := make(map[fileKey][]search.Match, len(keys))
		var buf []byte
		for k := range keys {
			ms := m.FindFile(filepath.Join(k.root, filepath.FromSlash(k.path)), limit, &buf)
			for i := range ms {
				ms[i].Repo, ms[i].Root, ms[i].Path = repos[k.root], k.root, k.path
			}
			fresh[k] = ms
		}
		up(func() { a.searchFollowed(gen, ver, fresh) })
		if len(keys) > 100 { // 整个仓库变了（Full）时结果里的文件全读一遍：和搜完一样收一次
			debug.FreeOSMemory()
		}
	}()
}

// searchFollowed 把重搜的结果换进来：和原来一样的文件不动（不然 ver 变了，问着的"全部替换？"会作废）；
// 读的时候结果或排除变了（替换了、排除了）就按现在的样子再读一次。排除了的几处 (line, col) 还配得上就留着。
func (a *App) searchFollowed(gen, ver int, fresh map[fileKey][]search.Match) {
	s := &a.Search
	if s.gen != gen {
		return
	}
	s.following = false
	if s.ver != ver {
		if s.follow == nil {
			s.follow = map[fileKey]bool{}
		}
		for k := range fresh {
			s.follow[k] = true
		}
		a.followLater()
		return
	}
	old := map[fileKey][]search.Match{}
	ms := s.Res.Matches
	for i := 0; i < len(ms); {
		_, j := s.fileRange(i)
		old[fileKey{ms[i].Root, ms[i].Path}] = ms[i:j]
		i = j
	}
	changed := map[fileKey][]search.Match{}
	for k, f := range fresh {
		if o, ok := old[k]; ok && !slices.Equal(o, f) {
			changed[k] = f
		}
	}
	if len(changed) > 0 {
		for k := range s.dismissed {
			f, ok := changed[fileKey{k.root, k.path}]
			if ok && !slices.ContainsFunc(f, func(m search.Match) bool { return m.Line == k.line && m.Col == k.col }) {
				delete(s.dismissed, k)
			}
		}
		s.splice(changed)
		if c := s.resQ.MatchCap(); len(s.Res.Matches) > c {
			// 截在上限的最后一个文件重读出来可能更多：照旧只留前 c 处
			s.Res.Matches, s.Res.Truncated = s.Res.Matches[:c], true
			s.splice(nil) // 重算文件数和行
		}
	}
	a.followLater()
}
