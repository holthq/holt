package workspace

import (
	"bytes"
	"maps"
	"slices"
)

// Store 是一个进程里唯一的一份 State：几个窗口（各自一个 app.App）共用一个 state.json，谁存都经过它，
// 只改自己那部分（自己工作区的会话、布局、几张最近的表），别的窗口的会话不会被盖掉。
// 只在主线程上用（窗口的回调、Update 里），不加锁。
type Store struct {
	dir  string
	st   State
	last []byte // 上次写进去的；存的内容没变就不写
}

// NewStore 是存在 dir 里、现在是 st 的 Store（dir 为空就不写盘）。
func NewStore(dir string, st State) *Store {
	return &Store{dir: dir, st: st.Clone()}
}

// Dir 是 state.json 所在的目录。
func (s *Store) Dir() string { return s.dir }

// State 是现在的一份拷贝（新开窗口用它）。
func (s *Store) State() State { return s.st.Clone() }

// Save 让 fn 改现在的状态，再写进 state.json；和上次写的一样就不写。changed 说这次写没写。
func (s *Store) Save(fn func(st *State)) (changed bool, err error) {
	fn(&s.st)
	if s.dir == "" {
		return false, nil
	}
	data := encode(s.st)
	if bytes.Equal(data, s.last) {
		return false, nil
	}
	if err := write(s.dir, data); err != nil {
		return false, err
	}
	s.last = data
	return true, nil
}

// Clone 是 st 的拷贝：表都是新的，改了不影响 st；会话本身是共用的（会话存了就不再改，换成新的）。
func (st State) Clone() State {
	st.Added, st.Hidden = slices.Clone(st.Added), slices.Clone(st.Hidden)
	st.RecentWs, st.Recent, st.CommitMsgs = slices.Clone(st.RecentWs), slices.Clone(st.Recent), slices.Clone(st.CommitMsgs)
	st.SearchHistory, st.TaskHistory = slices.Clone(st.SearchHistory), slices.Clone(st.TaskHistory)
	st.Windows = slices.Clone(st.Windows)
	st.Sessions = maps.Clone(st.Sessions)
	return st
}
