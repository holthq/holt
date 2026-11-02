package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// 红的确认框（AskDanger）只给丢数据、改远程、改历史的那几样：
// 删分支 / 删远程 / 删远程分支 / 丢弃改动 / 重置 --hard / 强制推送 / 移除工作树 / 放弃变基·合并·拣选 / 删除贮藏；
// 还有：保存时覆盖别人在磁盘上的改动、换编码重新打开丢掉没保存的、撤销 / 修改推送过的提交；
// 恢复默认设置、设置文档改过了还要换回现在的设置。
// 清除一个文件的 / 全部的本地历史、恢复删掉的文件时那个位置又有了文件（覆盖）。
// 删标签走自己的表单（ui.refDialog 的 dlgDanger）。其余的确认都是普通的 Ask。
// 按源码数：哪个函数里调了 AskDanger（重置是 ask = a.AskDanger 那一下），多了少了都报。
func TestConfirmDanger(t *testing.T) {
	want := []string{
		"AbortOngoing", "ApplyStashFiles", "ClearAllLocalHistory", "ClearLocal", "Commit", "DeleteBranch", "DeleteRemote", "DeleteRemoteBranch",
		"Discard", "DiscardAll", "DiscardChanges", "DiscardHunk", "DiscardLines",
		"PushForce", "RegenSettingsDoc", "ReopenWithEncoding", "ResetSettings", "ResetTo", "RestoreDeleted", "Save", "StashDrop", "UndoCommit",
		"deleteBranch", "removeWorktree",
	}
	files, _ := filepath.Glob("*.go")
	fs := token.NewFileSet()
	var got []string
	asks := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fs, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name.Name == "AskDanger" {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if s, ok := n.(*ast.SelectorExpr); ok {
					switch s.Sel.Name {
					case "AskDanger":
						if !slices.Contains(got, fd.Name.Name) {
							got = append(got, fd.Name.Name)
						}
					case "Ask":
						asks++
					}
				}
				return true
			})
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("调 AskDanger 的函数\n  有 %v\n  该 %v", got, want)
	}
	if asks < 30 {
		t.Errorf("普通的 Ask 只数到 %d 处，源码读错了？", asks)
	}
}
