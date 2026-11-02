package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// Reorder 把工作区文件 path 的 folders 按 roots（绝对路径）的先后重排：指向 roots 里的那几项互相换位置，
// 别的（不在 roots 里的、认不出的）留在原来的位置。只换 folders 数组里那几段原文，每一项自己的写法（"name" 之类）、
// 文件里别的字段、注释、缩进都原样不动；顺序本来就对时不写。写的是临时文件再换上去（符号链接写到它指的文件，权限位不变）。
func Reorder(path string, roots []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := reorderFolders(data, filepath.Dir(path), roots)
	if err != nil || out == nil {
		return err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(real), "."+filepath.Base(real)+".*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(out)
	if err == nil {
		err = tmp.Chmod(fi.Mode().Perm())
	}
	if err2 := tmp.Close(); err == nil {
		err = err2
	}
	if err == nil {
		err = os.Rename(tmp.Name(), real)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// errFolders 是 folders 找不到或不是数组：不认识的写法不动它。
var errFolders = errors.New("工作区文件里的 folders 不是认得的写法")

// reorderFolders 是 Reorder 对文件内容 data 做的（base 是相对路径的起点）：返回换好的内容，不用换是 nil。
func reorderFolders(data []byte, base string, roots []string) ([]byte, error) {
	s := scanner{data: data}
	s.space()
	if !s.eat('{') {
		return nil, errFolders
	}
	start := -1
	for {
		s.space()
		if s.eat('}') || s.done() {
			break
		}
		key, ok := s.str()
		if !ok {
			return nil, errFolders
		}
		s.space()
		if !s.eat(':') {
			return nil, errFolders
		}
		s.space()
		if key == "folders" {
			start = s.i
		}
		if !s.value() {
			return nil, errFolders
		}
		s.space()
		s.eat(',')
	}
	if start < 0 || data[start] != '[' {
		return nil, errFolders
	}
	// 数组里每一项的原文 [from, to)
	type item struct{ from, to, slot int }
	var items []item
	s.i = start + 1
	for {
		s.space()
		if s.eat(']') {
			break
		}
		if s.done() {
			return nil, errFolders
		}
		from := s.i
		if !s.value() {
			return nil, errFolders
		}
		items = append(items, item{from: from, to: s.i, slot: -1})
		s.space()
		s.eat(',')
	}
	// 认出每一项指向 roots 里的哪一个（同一个出现两次只认第一个）
	var slots []int // 指向 roots 的那些项，按在文件里的先后
	seen := map[int]bool{}
	for i, it := range items {
		var f struct {
			Path string `json:"path"`
		}
		text := data[it.from:it.to]
		if (json.Unmarshal(text, &f) != nil && json.Unmarshal(stripLineComments(text), &f) != nil) || f.Path == "" {
			continue
		}
		p := f.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		if k := slices.Index(roots, filepath.Clean(p)); k >= 0 && !seen[k] {
			seen[k] = true
			items[i].slot = k
			slots = append(slots, i)
		}
	}
	// 这些位置按 roots 的先后放上它们的原文
	order := slices.Clone(slots)
	slices.SortStableFunc(order, func(x, y int) int { return items[x].slot - items[y].slot })
	if slices.Equal(order, slots) {
		return nil, nil
	}
	var out bytes.Buffer
	at := 0
	for n, i := range slots {
		out.Write(data[at:items[i].from])
		j := order[n]
		out.Write(data[items[j].from:items[j].to])
		at = items[i].to
	}
	out.Write(data[at:])
	return out.Bytes(), nil
}

// scanner 是认 .code-workspace（带注释、末尾逗号的 JSON）的一个小扫描器：只认得出一个值从哪到哪，不解析。
type scanner struct {
	data []byte
	i    int
}

func (s *scanner) done() bool { return s.i >= len(s.data) }

func (s *scanner) eat(c byte) bool {
	if !s.done() && s.data[s.i] == c {
		s.i++
		return true
	}
	return false
}

// space 跳过空白和注释（// 到行末、/* … */）。
func (s *scanner) space() {
	for !s.done() {
		switch c := s.data[s.i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			s.i++
		case bytes.HasPrefix(s.data[s.i:], []byte("//")):
			if n := bytes.IndexByte(s.data[s.i:], '\n'); n >= 0 {
				s.i += n + 1
			} else {
				s.i = len(s.data)
			}
		case bytes.HasPrefix(s.data[s.i:], []byte("/*")):
			if n := bytes.Index(s.data[s.i+2:], []byte("*/")); n >= 0 {
				s.i += n + 4
			} else {
				s.i = len(s.data)
			}
		default:
			return
		}
	}
}

// str 读一个字符串，返回它的值（认不出的转义原样留着，只用来比 key）。
func (s *scanner) str() (string, bool) {
	from := s.i
	if !s.skipStr() {
		return "", false
	}
	var v string
	if json.Unmarshal(s.data[from:s.i], &v) != nil {
		return string(s.data[from+1 : s.i-1]), true
	}
	return v, true
}

func (s *scanner) skipStr() bool {
	if !s.eat('"') {
		return false
	}
	for !s.done() {
		switch s.data[s.i] {
		case '\\':
			s.i += 2
		case '"':
			s.i++
			return true
		default:
			s.i++
		}
	}
	return false
}

// value 跳过一个值：字符串、对象 / 数组（里面的字符串和注释照认）、数字和 true / false / null。
func (s *scanner) value() bool {
	if s.done() {
		return false
	}
	switch s.data[s.i] {
	case '"':
		return s.skipStr()
	case '{', '[':
		depth := 0
		for !s.done() {
			s.space()
			if s.done() {
				break
			}
			switch s.data[s.i] {
			case '"':
				if !s.skipStr() {
					return false
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
			s.i++
			if depth == 0 {
				return true
			}
		}
		return false
	}
	from := s.i
	for !s.done() && !bytes.ContainsRune([]byte(",]} \t\r\n/"), rune(s.data[s.i])) {
		s.i++
	}
	return s.i > from
}
