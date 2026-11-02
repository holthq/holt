package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 这个文件是 blame 装订线读的东西：`git blame --porcelain`（只读）。porcelain 里每个提交的头
// （author、author-mail、author-time、author-tz、summary、previous、boundary、filename）只在它第一次出现时给一次，
// 之后同一个提交的行只有 "<hash> <orig> <final> [<n>]" 一行，按哈希查。

// ZeroHash 是 blame 里"未提交"的行（工作区里改了还没提交）的哈希。
const ZeroHash = "0000000000000000000000000000000000000000"

// BlameMaxBytes / BlameMaxLines 是肯 blame 的最大文件：再大就不算（git blame 要把整个历史走一遍）。
const (
	BlameMaxBytes = 1 << 20
	BlameMaxLines = 20000
)

// ErrBlameTooBig 是文件太大不算 blame。
var ErrBlameTooBig = errors.New("blame: too big")

// Blame 是一个文件每一行最后是哪次提交改的。
type Blame struct {
	Lines   []BlameLine             // Lines[i] 是第 i 行（从 0 起）
	Commits map[string]*BlameCommit // 按完整哈希
	Hash    string                  // blame 的是哪一版：工作树为空
}

// BlameLine 是一行：哪次提交、在那次提交里是第几行（从 1 起，git 的写法）。
type BlameLine struct {
	Hash string
	Orig int
}

// BlameCommit 是 blame 里出现的一次提交。未提交的（ZeroHash）Author、Email 为空。
type BlameCommit struct {
	Hash, Short   string
	Author, Email string
	Time          time.Time
	Summary       string
	Path          string // 这个文件在那次提交里叫什么（改过名时和现在的不一样）
	Previous      string // 这个文件的上一版在哪次提交里，没有（第一次加进来）为空
	PrevPath      string // 上一版里它叫什么（改过名时和现在的不一样）
	Boundary      bool   // 历史的边界（根提交，或 blame 的范围外）
}

// Uncommitted 是还没提交的改动。
func (c *BlameCommit) Uncommitted() bool { return c.Hash == ZeroHash }

// BlameSeg 是连着几行是同一次提交改的：[Start, End)。
type BlameSeg struct {
	Start, End int
	Commit     *BlameCommit
}

// Segments 把连续同一个提交的行归成一段。
func (b *Blame) Segments() []BlameSeg {
	var segs []BlameSeg
	for i, l := range b.Lines {
		if n := len(segs); n > 0 && segs[n-1].Commit.Hash == l.Hash {
			segs[n-1].End = i + 1
			continue
		}
		segs = append(segs, BlameSeg{Start: i, End: i + 1, Commit: b.Commits[l.Hash]})
	}
	return segs
}

// Span 是这个文件里提交的时间范围（不算未提交的），好按新旧给每一段染色；没有提交时为零值。
func (b *Blame) Span() (oldest, newest time.Time) {
	for _, c := range b.Commits {
		if c.Uncommitted() {
			continue
		}
		if oldest.IsZero() || c.Time.Before(oldest) {
			oldest = c.Time
		}
		if c.Time.After(newest) {
			newest = c.Time
		}
	}
	return oldest, newest
}

// ReadBlame 读 rel 的 blame（只读）：commit 为空时是工作树里磁盘上的那份（改了没提交的行是 ZeroHash），
// 否则是那次提交里的版本。
func ReadBlame(ctx context.Context, root, commit, rel string) (*Blame, error) {
	args := []string{"blame", "--porcelain"}
	if commit != "" {
		args = append(args, commit)
	}
	out, err := RunCtx(ctx, root, append(args, "--", rel)...)
	if err != nil {
		return nil, err
	}
	b, err := ParseBlame(out)
	if b != nil {
		b.Hash = commit
	}
	return b, err
}

// ParseBlame 解析 `git blame --porcelain` 的输出。
func ParseBlame(out []byte) (*Blame, error) {
	b := &Blame{Commits: map[string]*BlameCommit{}}
	var cur *BlameCommit // 正在读的那一组的提交
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var tz string
	var at int64
	finish := func() { // 一个提交的头读完了（到了内容行）：算时间
		if cur != nil && cur.Time.IsZero() && at != 0 {
			cur.Time = time.Unix(at, 0).In(zone(tz))
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "\t"): // 这一行的内容：一组结束
			finish()
			cur, at, tz = nil, 0, ""
		case cur == nil:
			f := strings.Fields(line)
			if len(f) < 3 || len(f[0]) < 40 {
				return nil, errors.New("blame: bad header: " + line)
			}
			orig, _ := strconv.Atoi(f[1])
			c := b.Commits[f[0]]
			if c == nil {
				c = &BlameCommit{Hash: f[0], Short: Short(f[0])}
				b.Commits[f[0]] = c
			}
			cur = c
			b.Lines = append(b.Lines, BlameLine{Hash: f[0], Orig: orig})
		default:
			key, val, _ := strings.Cut(line, " ")
			switch key {
			case "author":
				if !cur.Uncommitted() {
					cur.Author = val
				}
			case "author-mail":
				if !cur.Uncommitted() {
					cur.Email = strings.TrimSuffix(strings.TrimPrefix(val, "<"), ">")
				}
			case "author-time":
				at, _ = strconv.ParseInt(val, 10, 64)
			case "author-tz":
				tz = val
			case "summary":
				cur.Summary = val
			case "previous":
				cur.Previous, cur.PrevPath, _ = strings.Cut(val, " ")
			case "boundary":
				cur.Boundary = true
			case "filename":
				if cur.Path == "" {
					cur.Path = val
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

// zone 是 author-tz（"+0800"）的时区，认不出时是本地时间。
func zone(tz string) *time.Location {
	if len(tz) != 5 || (tz[0] != '+' && tz[0] != '-') {
		return time.Local
	}
	h, err1 := strconv.Atoi(tz[1:3])
	m, err2 := strconv.Atoi(tz[3:5])
	if err1 != nil || err2 != nil {
		return time.Local
	}
	off := h*3600 + m*60
	if tz[0] == '-' {
		off = -off
	}
	return time.FixedZone(tz, off)
}
