package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// PathToURI 是磁盘路径对应的 file:// 地址（LSP 用的那种）。
func PathToURI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	p := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// URIToPath 是 file:// 地址对应的磁盘路径；别的协议、解不开的返回空。
func URIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		return ""
	}
	if u.Host != "" && u.Host != "localhost" {
		p = "//" + u.Host + p
	}
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}
