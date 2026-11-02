package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// 这个文件是标签和远程：分支菜单里的"标签"、仓库菜单里的"远程"，都只读。

// Tag 是一个标签。Hash 是它指着的提交：附注标签是解开一层的 *objectname，轻量标签就是它自己指着的。
type Tag struct {
	Name      string
	Hash      string
	Date      time.Time // creatordate：附注标签是打标签的时间，轻量标签是那个提交的时间
	Annotated bool
}

// MaxTags 是最多读几个标签（新的在前）。
const MaxTags = 200

// ReadTags 列出标签，新的在前，最多 MaxTags 个。
func ReadTags(ctx context.Context, root string) ([]Tag, error) {
	out, err := RunCtx(ctx, root, "for-each-ref", "--sort=-creatordate", "--count="+strconv.Itoa(MaxTags),
		"--format=%(refname:short)%1f%(objectname)%1f%(*objectname)%1f%(creatordate:iso-strict)%1f%(objecttype)", "refs/tags")
	if err != nil {
		return nil, err
	}
	var ts []Tag
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) < 5 {
			continue
		}
		t := Tag{Name: f[0], Hash: f[1], Annotated: f[4] == "tag"}
		if f[2] != "" {
			t.Hash = f[2]
		}
		t.Date, _ = time.Parse(time.RFC3339, f[3])
		ts = append(ts, t)
	}
	return ts, nil
}

// Remote 是一个远程：名字和抓取地址；推送地址和抓取的不一样时是 Push，一样时为空。
type Remote struct {
	Name, URL, Push string
}

// ReadRemotes 列出远程（git remote -v，按名字排），读不了时为空。
func ReadRemotes(ctx context.Context, root string) []Remote {
	out, err := RunCtx(ctx, root, "remote", "-v")
	if err != nil {
		return nil
	}
	var rs []Remote
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		name, rest, ok := strings.Cut(line, "\t")
		i := strings.LastIndex(rest, " (")
		if !ok || i < 0 {
			continue
		}
		u, kind := rest[:i], rest[i+2:]
		if len(rs) == 0 || rs[len(rs)-1].Name != name {
			rs = append(rs, Remote{Name: name})
		}
		r := &rs[len(rs)-1]
		switch kind {
		case "fetch)":
			r.URL = u
		case "push)":
			r.Push = u
		}
	}
	for i := range rs {
		if rs[i].Push == rs[i].URL {
			rs[i].Push = ""
		}
	}
	return rs
}

// RangeCount 是 base..HEAD 的提交数（重置 / 变基到 base 时会离开当前分支或被重写的那些），
// pushed 是其中已经在某个远程分支里的（之后要强制推送）。只读的两次 rev-list --count。
func RangeCount(ctx context.Context, root, base string) (n, pushed int, err error) {
	if n, err = Count(ctx, root, base+"..HEAD"); err != nil {
		return 0, 0, err
	}
	local, err := Count(ctx, root, base+"..HEAD", "--not", "--remotes")
	if err != nil {
		return 0, 0, err
	}
	return n, n - local, nil
}

// Count 是 rev-list --count 数出来的提交数（只读），revs 是范围和别的参数。
func Count(ctx context.Context, root string, revs ...string) (int, error) {
	out, err := RunCtx(ctx, root, append(append([]string{"rev-list", "--count"}, revs...), "--")...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
