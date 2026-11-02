// Package history 是本地历史：每次保存留一份文件内容，删错、改错了能拿回来。
// 一个文件一个目录 <状态目录>/history/<Key(绝对路径)>/：index.json 记条目（时间、大小、内容的 sha、为什么记），
// 每条的内容是一个文件 <t>（写进磁盘的原始字节，编码照文件的）；“改名”条只是时间线上的标记，不另存内容。
// 没有总索引：清理、“从本地历史恢复文件”时把目录走一遍。纯文件操作，不碰界面；什么时候记、右栏时间线、
// 恢复在 app/localhist.go 和 ui。
package history

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxSnapshot = 256 << 10        // 比这大的文件不记
	MaxEntries  = 50               // 每个文件最多几条，多了删最早的
	MaxTotal    = 200 << 20        // 所有文件加起来最多多大，清理时从最早的删起
	DefaultDays = 30               // 保留天数的默认（设置里存 0）
	MergeWindow = 10 * time.Second // 和上一条“保存”相隔不到这么久就替换它（自动保存不刷出几十条）
	indexName   = "index.json"
)

// 条目为什么记：保存、保存前磁盘上的那份、恢复前、改名（标记）。
const (
	WhySave    = "save"
	WhyDisk    = "disk"
	WhyRestore = "restore"
	WhyRename  = "rename"
)

// Entry 是一条。T 是 unix 毫秒，同一个文件里严格递增，也是内容文件的名字。
type Entry struct {
	T    int64  `json:"t"`
	Size int64  `json:"size"`
	SHA  string `json:"sha"`
	Why  string `json:"why"`
	From string `json:"from,omitempty"` // 改名条：改名前的绝对路径
}

// Time 是 T 的时间。
func (e Entry) Time() time.Time { return time.UnixMilli(e.T) }

// Index 是一个文件的 index.json：Path 是现在的绝对路径，Entries 按时间升序。
type Index struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

// Latest 是最新一条，没有条目时 ok 为假。
func (x Index) Latest() (Entry, bool) {
	if len(x.Entries) == 0 {
		return Entry{}, false
	}
	return x.Entries[len(x.Entries)-1], true
}

// Store 是一个状态目录下的本地历史。方法可以从几个 goroutine 调（保存在界面线程，清理在后台）。
type Store struct {
	Dir   string           // <状态目录>/history
	Now   func() time.Time // 测试换掉它；nil 是 time.Now
	Limit int64            // 总量上限，0 是 MaxTotal（测试改小）
	mu    sync.Mutex
}

// New 是 dir 下的本地历史（目录第一次记的时候才建）。
func New(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Key 是绝对路径的目录名：sha256 的前 12 个十六进制。
func Key(abs string) string {
	h := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(h[:6])
}

// Sum 是内容的 sha：sha256 的前 16 个十六进制。
func Sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

func (s *Store) dirOf(abs string) string { return filepath.Join(s.Dir, Key(abs)) }

// Load 读 abs 的条目；没有记过是空的 Index（Path 是 abs）。
func (s *Store) Load(abs string) Index {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, _ := s.load(s.dirOf(abs))
	if x.Path == "" {
		x.Path = abs
	}
	return x
}

func (s *Store) load(dir string) (Index, error) {
	var x Index
	b, err := os.ReadFile(filepath.Join(dir, indexName))
	if err != nil {
		return x, err
	}
	err = json.Unmarshal(b, &x)
	return x, err
}

// save 写 index.json（临时文件 + 改名）；没有条目了整个目录删掉。
func (s *Store) save(dir string, x Index) error {
	if len(x.Entries) == 0 {
		return os.RemoveAll(dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(x)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, indexName), b)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Record 给 abs 记一条内容 data（why 是 WhySave / WhyDisk / WhyRestore）。和最新一条内容一样不记；
// 保存和上一条保存相隔不到 MergeWindow 就替换那条。大于 MaxSnapshot 不记。返回记了没有。
func (s *Store) Record(abs string, data []byte, why string) (bool, error) {
	if len(data) > MaxSnapshot {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.record(abs, data, why)
}

func (s *Store) record(abs string, data []byte, why string) (bool, error) {
	dir := s.dirOf(abs)
	x, _ := s.load(dir)
	x.Path = abs
	sum := Sum(data)
	t := s.now().UnixMilli()
	if last, ok := x.Latest(); ok {
		if last.SHA == sum {
			return false, nil
		}
		if why == WhySave && last.Why == WhySave && t-last.T < MergeWindow.Milliseconds() {
			os.Remove(filepath.Join(dir, strconv.FormatInt(last.T, 10)))
			x.Entries = x.Entries[:len(x.Entries)-1]
		}
	}
	if last, ok := x.Latest(); ok && t <= last.T {
		t = last.T + 1
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	if err := writeAtomic(filepath.Join(dir, strconv.FormatInt(t, 10)), data); err != nil {
		return false, err
	}
	x.Entries = append(x.Entries, Entry{T: t, Size: int64(len(data)), SHA: sum, Why: why})
	s.trim(dir, &x)
	return true, s.save(dir, x)
}

// trim 删到最多 MaxEntries 条（从最早的删），找不到内容的改名条一起删。
func (s *Store) trim(dir string, x *Index) {
	for len(x.Entries) > MaxEntries {
		os.Remove(filepath.Join(dir, strconv.FormatInt(x.Entries[0].T, 10)))
		x.Entries = x.Entries[1:]
	}
	for i := 0; i < len(x.Entries); i++ {
		if resolve(*x, i) < 0 {
			x.Entries = slices.Delete(x.Entries, i, i+1)
			i--
		}
	}
}

// resolve 是第 i 条的内容在哪一条：不是改名条就是它自己；改名条是它前面最近的、sha 一样的有内容的那条，没有是 -1。
func resolve(x Index, i int) int {
	e := x.Entries[i]
	if e.Why != WhyRename {
		return i
	}
	for j := i - 1; j >= 0; j-- {
		if c := x.Entries[j]; c.Why != WhyRename && c.SHA == e.SHA {
			return j
		}
	}
	return -1
}

// BeforeWrite 在写 abs 之前调：磁盘上有这个文件、而且还没记过它现在的内容（没有条目，或最新一条不是它），
// 先把磁盘上的记一条 WhyDisk——第一次保存、别的程序改过的那份都留得住。
func (s *Store) BeforeWrite(abs string) (bool, error) {
	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxSnapshot {
		return false, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	x, _ := s.load(s.dirOf(abs))
	if last, ok := x.Latest(); ok && last.SHA == Sum(data) {
		return false, nil
	}
	return s.record(abs, data, WhyDisk)
}

// ContentPath 是条目 e 的内容文件；改名条指向改名时最新的那份（resolve）。
func (s *Store) ContentPath(abs string, e Entry) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.dirOf(abs)
	if e.Why != WhyRename {
		return filepath.Join(dir, strconv.FormatInt(e.T, 10)), nil
	}
	x, err := s.load(dir)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(x.Entries, func(o Entry) bool { return o.T == e.T })
	if i < 0 || resolve(x, i) < 0 {
		return "", fs.ErrNotExist
	}
	return filepath.Join(dir, strconv.FormatInt(x.Entries[resolve(x, i)].T, 10)), nil
}

// Content 是条目 e 的内容。
func (s *Store) Content(abs string, e Entry) ([]byte, error) {
	p, err := s.ContentPath(abs, e)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Rename 跟着应用里的重命名 / 移动：old 的目录改名成 new 的，Path 改掉，追加一条改名标记。old 没记过什么都不做；
// new 已经有记录（以前有过同名文件）就把两边的条目按时间合起来。
func (s *Store) Rename(old, new string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	od, nd := s.dirOf(old), s.dirOf(new)
	x, err := s.load(od)
	if err != nil || len(x.Entries) == 0 {
		return nil
	}
	last, _ := x.Latest()
	if y, err := s.load(nd); err == nil && len(y.Entries) > 0 {
		// 两边都有：old 的内容文件挪过去，条目合起来（同一毫秒的往后挪一毫秒）
		for i := range x.Entries {
			e := &x.Entries[i]
			t := e.T
			for slices.ContainsFunc(y.Entries, func(o Entry) bool { return o.T == t }) {
				t++
			}
			if e.Why != WhyRename {
				if err := os.Rename(filepath.Join(od, strconv.FormatInt(e.T, 10)), filepath.Join(nd, strconv.FormatInt(t, 10))); err != nil {
					return err
				}
			}
			e.T = t
			y.Entries = append(y.Entries, *e)
		}
		slices.SortFunc(y.Entries, func(a, b Entry) int { return cmp.Compare(a.T, b.T) })
		x = y
		os.RemoveAll(od)
	} else {
		os.RemoveAll(nd)
		if err := os.Rename(od, nd); err != nil {
			return err
		}
	}
	x.Path = new
	t := s.now().UnixMilli()
	if l, ok := x.Latest(); ok && t <= l.T {
		t = l.T + 1
	}
	x.Entries = append(x.Entries, Entry{T: t, Size: last.Size, SHA: last.SHA, Why: WhyRename, From: old})
	s.trim(nd, &x)
	return s.save(nd, x)
}

// Move 跟着应用里的重命名 / 移动：文件就是 Rename；目录（dir）是它下面记过的每个文件各 Rename 一次（要走一遍 history 目录）。
// 返回挪了几个文件的历史，出错的接着挪别的、返回第一个错。
func (s *Store) Move(old, new string, dir bool) (int, error) {
	if !dir {
		if len(s.Load(old).Entries) == 0 {
			return 0, nil
		}
		return 1, s.Rename(old, new)
	}
	var olds []string
	for _, x := range s.All() {
		if strings.HasPrefix(x.Path, old+string(filepath.Separator)) {
			olds = append(olds, x.Path)
		}
	}
	var first error
	for _, p := range olds {
		if err := s.Rename(p, new+p[len(old):]); err != nil && first == nil {
			first = err
		}
	}
	return len(olds), first
}

// Delete 删 abs 的一条（按 T）。指着它的内容的改名条（没有别的同样内容的）一起删。
func (s *Store) Delete(abs string, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.dirOf(abs)
	x, err := s.load(dir)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(x.Entries, func(e Entry) bool { return e.T == t })
	if i < 0 {
		return nil
	}
	os.Remove(filepath.Join(dir, strconv.FormatInt(t, 10)))
	x.Entries = slices.Delete(x.Entries, i, i+1)
	s.trim(dir, &x)
	return s.save(dir, x)
}

// Clear 删掉 abs 的全部本地历史。
func (s *Store) Clear(abs string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(s.dirOf(abs))
}

// ClearAll 删掉整个 history 目录。
func (s *Store) ClearAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(s.Dir)
}

// All 是记过的所有文件（走一遍目录；读不出 index 的跳过），按最新一条的时间降序。
func (s *Store) All() []Index {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.all()
}

func (s *Store) all() []Index {
	ents, _ := os.ReadDir(s.Dir)
	var out []Index
	for _, d := range ents {
		if !d.IsDir() {
			continue
		}
		if x, err := s.load(filepath.Join(s.Dir, d.Name())); err == nil && len(x.Entries) > 0 && x.Path != "" {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b Index) int {
		la, _ := a.Latest()
		lb, _ := b.Latest()
		return cmp.Compare(lb.T, la.T)
	})
	return out
}

// Deleted 是记过、但磁盘上已经没有的文件（“从本地历史恢复文件…”列的），按最新一条的时间降序。
func (s *Store) Deleted() []Index {
	var out []Index
	for _, x := range s.All() {
		if _, err := os.Lstat(x.Path); errors.Is(err, fs.ErrNotExist) {
			out = append(out, x)
		}
	}
	return out
}

// Usage 是占了多少字节、记了几个文件。
func (s *Store) Usage() (bytes int64, files int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != s.Dir {
				files++
			}
			return nil
		}
		if fi, err := d.Info(); err == nil {
			bytes += fi.Size()
		}
		return nil
	})
	return bytes, files
}

// Prune 是启动后在后台跑的清理：删超过 days 天的条目（days ≤ 0 是 DefaultDays）、目录里没被条目引用的文件，
// 总量还超过 Limit（MaxTotal）就从最早的条目删起。没有条目的目录整个删掉。返回删了几条。
func (s *Store) Prune(days int) (int, error) {
	if days <= 0 {
		days = DefaultDays
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	type item struct {
		dir  string
		e    Entry
		size int64
	}
	var removed int
	var items []item
	var total int64
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	for _, d := range ents {
		dir := filepath.Join(s.Dir, d.Name())
		if !d.IsDir() {
			os.Remove(dir)
			continue
		}
		x, err := s.load(dir)
		if err != nil {
			os.RemoveAll(dir) // 读不出 index：内容找不回路径，留着也没用
			continue
		}
		n := len(x.Entries)
		x.Entries = slices.DeleteFunc(x.Entries, func(e Entry) bool { return e.T < cut })
		s.trim(dir, &x)
		removed += n - len(x.Entries)
		keep := map[string]bool{indexName: true}
		for _, e := range x.Entries {
			keep[strconv.FormatInt(e.T, 10)] = true
		}
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if !keep[f.Name()] {
				os.RemoveAll(filepath.Join(dir, f.Name()))
			}
		}
		if err := s.save(dir, x); err != nil {
			return removed, err
		}
		for _, e := range x.Entries {
			if e.Why != WhyRename {
				items = append(items, item{dir, e, e.Size})
				total += e.Size
			}
		}
	}
	limit := s.Limit
	if limit <= 0 {
		limit = MaxTotal
	}
	if total <= limit {
		return removed, nil
	}
	slices.SortFunc(items, func(a, b item) int { return cmp.Compare(a.e.T, b.e.T) })
	for _, it := range items {
		if total <= limit {
			break
		}
		x, err := s.load(it.dir)
		if err != nil {
			continue
		}
		i := slices.IndexFunc(x.Entries, func(e Entry) bool { return e.T == it.e.T })
		if i < 0 {
			continue
		}
		os.Remove(filepath.Join(it.dir, strconv.FormatInt(it.e.T, 10)))
		x.Entries = slices.Delete(x.Entries, i, i+1)
		s.trim(it.dir, &x)
		if err := s.save(it.dir, x); err != nil {
			return removed, err
		}
		total -= it.size
		removed++
	}
	return removed, nil
}
