package docs

import (
	"github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是交互式变基向导标签里排的计划（只是数据；算信息、写 todo、跑命令在 app/rebase.go）。
// 顺序和 git 的 todo 一样：最老的在上面。

// RebaseAction 是交互式变基里一步做什么。
type RebaseAction int

const (
	RebasePick   RebaseAction = iota // 保留
	RebaseReword                     // 保留，改信息
	RebaseSquash                     // 并入上一条，信息合在一起
	RebaseFixup                      // 并入上一条，丢掉这一条的信息
	RebaseDrop                       // 丢掉
	RebaseEdit                       // 停在这里改
)

// RebaseActions 是菜单里列的顺序。
var RebaseActions = []RebaseAction{RebasePick, RebaseReword, RebaseSquash, RebaseFixup, RebaseDrop, RebaseEdit}

var rebaseWords = [...]string{"pick", "reword", "squash", "fixup", "drop", "edit"}

// String 是 git 里的叫法（pick、reword…），界面上的按钮也写它。
func (x RebaseAction) String() string { return rebaseWords[x] }

// Text 是一句说明。
func (x RebaseAction) Text() string {
	switch x {
	case RebaseReword:
		return i18n.T("保留，改提交信息")
	case RebaseSquash:
		return i18n.T("并入上一条，信息合在一起")
	case RebaseFixup:
		return i18n.T("并入上一条，丢掉这一条的信息")
	case RebaseDrop:
		return i18n.T("丢掉这个提交")
	case RebaseEdit:
		return i18n.T("停在这里改，改完在 Git 栏的横幅里继续")
	}
	return i18n.T("保留")
}

// Melds 是并入上一条（squash / fixup）。
func (x RebaseAction) Melds() bool { return x == RebaseSquash || x == RebaseFixup }

// HasMsg 是这一步下面有信息输入框（reword / squash）。
func (x RebaseAction) HasMsg() bool { return x == RebaseReword || x == RebaseSquash }

// RebaseStep 是计划里的一步。
type RebaseStep struct {
	Commit git.Commit
	Action RebaseAction
	Pushed bool // 推送过：做完要强制推送
	Orig   int  // 开向导时排在第几（重置用）

	// Msg 是 reword / squash 的信息输入框。没改过（Edited 为 false）时每帧跟着默认的走（Def）：
	// reword 是原信息，squash 是上一条（连着并入的几条合在一起）和这一条的信息
	Msg    string
	Def    string
	Edited bool
}

// RebasePlan 是一个交互式变基向导标签里的计划。
type RebasePlan struct {
	Base  string // 最老那条的父提交；最老那条是根提交时为空（--root）
	Head  string // 开向导时的 HEAD：仓库的 HEAD 变了，计划就过时了
	Steps []RebaseStep
	Sel   int
	List  ui.ListState // 向导的列表

	Running bool     // 点了开始、还在跑
	Stopped bool     // git 停下了（冲突、edit、exec 失败），横幅接手；向导只剩"关闭"
	Reason  string   // 停下的原因（"有冲突"之类）
	Ended   bool     // 停下以后在横幅里继续完了或放弃了
	Files   []string // 这次写的临时文件（todo、信息），变基结束后删
}

// Message 是提交的完整信息（标题，有正文时空一行接正文）。
func Message(c git.Commit) string {
	if c.Body == "" {
		return c.Subject
	}
	return c.Subject + "\n\n" + c.Body
}

// Defaults 算每个输入框的默认信息，没改过的换成它；用户改过的（和上次的默认不一样了）记下 Edited，以后不再跟。
// 每帧建向导之前调，改了顺序、动作后 squash 的默认信息跟着变。
func (p *RebasePlan) Defaults() {
	group := "" // 这一组（保留的那条和并进来的）到现在的信息
	for i := range p.Steps {
		s := &p.Steps[i]
		if !s.Edited && s.Def != "" && s.Msg != s.Def {
			s.Edited = true
		}
		own := Message(s.Commit)
		switch s.Action {
		case RebasePick, RebaseEdit:
			group = own
			continue
		case RebaseFixup, RebaseDrop:
			continue
		case RebaseReword:
			s.Def = own
		case RebaseSquash:
			s.Def = own
			if group != "" {
				s.Def = group + "\n\n" + own
			}
		}
		if !s.Edited {
			s.Msg = s.Def
		}
		group = s.Msg
	}
}

// Reset 把计划换回开向导时的样子：全是 pick、按原来的顺序、信息清空。
func (p *RebasePlan) Reset() {
	steps := make([]RebaseStep, len(p.Steps))
	for _, s := range p.Steps {
		steps[s.Orig] = RebaseStep{Commit: s.Commit, Pushed: s.Pushed, Orig: s.Orig}
	}
	p.Steps, p.Sel = steps, 0
}

// Changed 是计划和开向导时不一样了（挪过、动作不是 pick、信息改了）。
func (p *RebasePlan) Changed() bool {
	for i, s := range p.Steps {
		if s.Orig != i || s.Action != RebasePick {
			return true
		}
	}
	return false
}

// Move 把第 i 步挪 by 格（-1 往上），选中跟着它；挪不动返回 false。
func (p *RebasePlan) Move(i, by int) bool {
	j := i + by
	if i < 0 || i >= len(p.Steps) || j < 0 || j >= len(p.Steps) {
		return false
	}
	p.Steps[i], p.Steps[j] = p.Steps[j], p.Steps[i]
	p.Sel = j
	return true
}
