package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// oldName 是改名 Grove 之前的应用名：状态目录 ~/Library/Application Support/Workbench/、
// 日志目录 ~/Library/Logs/Workbench/ 都按它取。
const oldName = "Workbench"

// rename 是搬目录先试的那一步（测试换掉它，走拷贝那条路）。
var rename = os.Rename

// lineLogf 拼一行日志（日志这时还没接上，行先攒着交给 main）。是日志，不翻译：名字以 Logf 结尾，i18n 的 TestCatalog 不收；
// 行首的目录名也经它。
var lineLogf = fmt.Sprintf

// migrateDirs 把改名前的状态目录和日志目录搬到新名字 name 下，要在第一次取 mygo 的路径之前调
// （mygo.App.Path 会先建出空的新目录，那就搬不了了）。只在 name 是 Grove（打包的应用）时搬：
// go run / go test 的名字是可执行文件名 workbench，在不分大小写的盘上就是旧目录本身。
// 设了 WORKBENCH_STATE 或 WORKBENCH_LOGS（量内存、冒烟、手测用的另一份）什么都不搬。
// 新目录已经有了、旧的没有，都不动。返回给日志的几行（日志这时还没接上）。
func migrateDirs(name string) []string {
	if name != "Grove" || os.Getenv("WORKBENCH_STATE") != "" || os.Getenv("WORKBENCH_LOGS") != "" {
		return nil
	}
	var lines []string
	if conf, err := os.UserConfigDir(); err == nil {
		lines = append(lines, migrateDir(lineLogf("状态目录"), filepath.Join(conf, oldName), filepath.Join(conf, name))...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		logs := filepath.Join(home, "Library", "Logs")
		lines = append(lines, migrateDir(lineLogf("日志目录"), filepath.Join(logs, oldName), filepath.Join(logs, name))...)
	}
	return lines
}

// migrateDir 把目录 old 搬成 dir：先 rename（同一个盘上，一下就好）；不行就整个拷到 dir 旁边的临时目录、
// 拷完再改名成 dir，旧的留着；拷到一半出错删掉临时的，什么都不变。
func migrateDir(what, old, dir string) []string {
	if _, err := os.Lstat(dir); err == nil {
		return nil
	}
	if fi, err := os.Lstat(old); err != nil || !fi.IsDir() {
		return nil
	}
	err := rename(old, dir)
	if err == nil {
		return []string{lineLogf("%s从 %s 搬到 %s（改名 Grove）", what, old, dir)}
	}
	tmp := dir + ".migrating"
	os.RemoveAll(tmp)
	if cerr := copyTree(old, tmp); cerr != nil {
		os.RemoveAll(tmp)
		return []string{lineLogf("%s没搬成：改名 %v；拷贝 %v。用新的空目录，旧的 %s 还在", what, err, cerr, old)}
	}
	if rerr := os.Rename(tmp, dir); rerr != nil {
		os.RemoveAll(tmp)
		return []string{lineLogf("%s没搬成：改名 %v；拷完改名 %v。用新的空目录，旧的 %s 还在", what, err, rerr, old)}
	}
	return []string{lineLogf("%s从 %s 拷到 %s（改名 Grove；直接改名不行：%v），旧的留着", what, old, dir, err)}
}

// copyTree 把目录 src 拷成 dst（dst 不能已有）：目录、普通文件（权限照旧）、符号链接；套接字这类跳过
// （termd 的套接字拷过去也连不上）。
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(to, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		case info.Mode().IsRegular():
			return copyFile(p, to, info.Mode().Perm())
		}
		return nil
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
