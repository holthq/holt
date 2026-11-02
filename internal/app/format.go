package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
)

// 外部格式化：格式化文档（⇧⌥F、编辑器右键、命令面板）和保存时格式化（设置"格式化"页）都是把缓冲区的
// UTF-8 文字喂给一条命令（stdin → stdout），输出和原文按行比，只把改了的地方一步套上（docs.LineEdits，收窄到不同的那几个字，一步撤销），
// 光标、折叠、滚动留着。命令在 login shell 里跑（Shell 设置或 $SHELL，-lc），Homebrew 那些装的工具就在 PATH 上；
// 工作目录是仓库根（仓库外的文件：文件所在的目录），FormatTimeout 超时。跑的时候文字又改了就不套。
// 不自带格式化器，也不走 LSP：命令都是外部的，没装就提示装上或在设置里改。

// DefaultFormatters 是有内置默认命令的语言，key 是 editor.Language.Name；{file} 换成文件的绝对路径（ShellQuote 过）。
var DefaultFormatters = map[string]string{
	"Go":         "gofmt",
	"Rust":       "rustfmt --edition 2021",
	"Python":     "ruff format - || black -q -",
	"JavaScript": prettier,
	"TypeScript": prettier,
	"JSON":       prettier,
	"CSS":        prettier,
	"HTML":       prettier,
	"YAML":       prettier,
	"Markdown":   prettier,
	"C":          clangFormat,
	"C++":        clangFormat,
	"Shell":      "shfmt",
	"Swift":      "swift-format",
	"Zig":        "zig fmt --stdin",
	"Lua":        "stylua -",
}

const (
	prettier    = "prettier --stdin-filepath {file}"
	clangFormat = "clang-format --assume-filename={file}"
)

// FormatterLangs 是设置里格式化表先列出来的语言（都有内置默认），按这个顺序；设置里加过的别的语言排在后面。
var FormatterLangs = []string{"Go", "Rust", "Python", "JavaScript", "TypeScript", "JSON", "CSS", "HTML", "YAML", "Markdown",
	"C", "C++", "Shell", "Swift", "Zig", "Lua"}

// FormatOff 是设置里"这种语言不格式化"的写法。
const FormatOff = "-"

// FormatTimeout 是一次格式化最多等多久；测试里调短。
var FormatTimeout = 10 * time.Second

// FormatterOf 是语言 lang 现在的格式化命令（设置里改过的，没改是内置默认；{file} 还没换）；"" 为没有或关掉了。
func (a *App) FormatterOf(lang string) string {
	cmd := strings.TrimSpace(a.St.Settings.Formatters[lang])
	if cmd == "" {
		cmd = DefaultFormatters[lang]
	}
	if cmd == FormatOff {
		return ""
	}
	return cmd
}

// ExtraFormatterLangs 是设置里另加了一行的语言（不在 FormatterLangs 里），按名字排。
func (a *App) ExtraFormatterLangs() []string {
	var out []string
	for l := range a.St.Settings.Formatters {
		if !slices.Contains(FormatterLangs, l) {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

// CanFormat 是 d 能不能格式化：读完了的可编辑文件，不是大文件。有没有命令另说（formatCommand）。
func CanFormat(d *docs.Doc) bool {
	return d != nil && d.Kind == docs.File && d.Buf != nil && !d.Loading && d.Editable() && !d.Big
}

// formatCommand 是给 d 跑的命令（{file} 已换好）；没有时 cmd 为空、why 是为什么。
func (a *App) formatCommand(d *docs.Doc) (cmd, why string) {
	name := i18n.T("纯文本")
	if l := d.Buf.Language(); l != nil {
		name = l.Name
	}
	if strings.TrimSpace(a.St.Settings.Formatters[name]) == FormatOff {
		return "", i18n.Tf("%s 的格式化在设置里关掉了", name)
	}
	if cmd = a.FormatterOf(name); cmd == "" {
		return "", i18n.Tf("没有 %s 的格式化命令（设置里加）", name)
	}
	return strings.ReplaceAll(cmd, "{file}", ShellQuote(d.Abs)), ""
}

// formatOutcome 是一次格式化的结果。
type formatOutcome int

const (
	formatNone    formatOutcome = iota // 没有命令，没跑
	formatGone                         // 跑完时标签已经关了：什么都不做
	formatFailed                       // 命令失败、超时、没找到
	formatStale                        // 跑的时候文字又改了，没套
	formatSame                         // 已经是格式好的
	formatApplied                      // 套上了
)

// formatDoc 在后台格式化 d，回到主线程时文字没再改就套上，然后 then(结果, 给人看的话)（主线程上调）。
func (a *App) formatDoc(d *docs.Doc, then func(res formatOutcome, msg string)) {
	cmd, why := a.formatCommand(d)
	if cmd == "" {
		then(formatNone, why)
		return
	}
	d.Formatting = true
	buf, ver, text := d.Buf, d.Buf.Version(), d.Buf.String()
	dir := d.Root
	if dir == "" {
		dir = filepath.Dir(d.Abs)
	}
	r := a.RepoOf(d)
	ctx := context.Background()
	if r != nil {
		ctx = r.ctx
	}
	shell, title := a.shell(), i18n.Tf("格式化 %s", d.Title)
	up := a.Update
	go func() {
		out, msg, detail := runFormatter(ctx, shell, dir, cmd, text)
		up(func() {
			d.Formatting = false
			switch {
			case a.GroupOf(d) == nil:
				then(formatGone, "")
			case msg != "":
				a.Log(r, title+" · $ "+cmd, detail, true)
				then(formatFailed, msg)
			case d.Buf != buf || buf.Version() != ver:
				then(formatStale, i18n.T("文件改了，没套用格式化"))
			case out == text:
				then(formatSame, i18n.T("已经是格式好的"))
			default:
				buf.Edit(docs.LineEdits(text, out)...)
				then(formatApplied, "")
			}
		})
	}()
}

// shell 是跑格式化命令的 shell：设置里的 Shell，没设是 $SHELL。
func (a *App) shell() string {
	if s := a.St.Settings.Shell; s != "" {
		return s
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// notFound 从 shell 的报错里认出没装的命令，一种一种试（zsh 的 "zsh:1: command not found: x" 也能被 bash 那种从开头配上）：
// zsh 的 "command not found: x"、fish 的 "Unknown command: x"、bash / sh 的 "x: command not found"。
var notFound = []*regexp.Regexp{
	regexp.MustCompile(`command not found: (\S+)`),
	regexp.MustCompile(`Unknown command:? (\S+)`),
	regexp.MustCompile(`(\S+): command not found`),
}

// missing 是 stderr 里报的第一个没找到的命令；没报为空。
func missing(stderr string) string {
	for _, re := range notFound {
		if m := re.FindStringSubmatch(stderr); m != nil {
			return strings.Trim(m[1], "'\"`")
		}
	}
	return ""
}

// toolRun 是 runTool 跑一条外部命令的结果。
type toolRun struct {
	stdout, stderr    string // stderr 去掉了头尾空白
	exit              int    // 退出码；没跑起来、超时、取消时为 -1
	timeout, canceled bool
	missing           string // 命令没装（退出码 127 或 shell 报了没找到）：没找到的那个命令
	err               error  // 别的没跑起来的原因
}

// runTool 在 login shell（shell -lc）里、dir 下跑 cmd，stdin 进标准输入，最多等 timeout；格式化和检查共用。
// 超时、ctx 取消时连同它起的子进程（同一个进程组）一起杀掉。
func runTool(ctx context.Context, shell, dir, cmd, stdin string, timeout time.Duration) toolRun {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(ctx, shell, "-lc", cmd)
	c.Dir = dir
	c.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second
	err := c.Run()
	res := toolRun{stdout: stdout.String(), stderr: strings.TrimSpace(stderr.String()), exit: -1}
	var exit *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.timeout = true
	case ctx.Err() != nil:
		res.canceled = true
	case errors.As(err, &exit):
		res.exit = exit.ExitCode()
		if res.missing = missing(res.stderr); res.missing == "" && res.exit == 127 {
			res.missing, _, _ = strings.Cut(strings.TrimSpace(cmd), " ")
		}
	case err != nil:
		res.err = err
	default:
		res.exit = 0
	}
	return res
}

// runFormatter 跑 cmd：text 进 stdin，stdout 是格式化好的文字（\r\n 换成 \n）。失败时 msg 是给人看的一句、
// detail 是 stderr 全文（记进输出栏）。
func runFormatter(ctx context.Context, shell, dir, cmd, text string) (out, msg, detail string) {
	res := runTool(ctx, shell, dir, cmd, text, FormatTimeout)
	detail = res.stderr
	first, _, _ := strings.Cut(detail, "\n")
	switch {
	case res.timeout:
		return "", i18n.Tf("格式化失败：%d 秒还没跑完", int(FormatTimeout/time.Second)), detail
	case res.canceled:
		return "", i18n.T("格式化失败：取消了"), detail
	case res.missing != "":
		return "", i18n.Tf("没找到 %s：装上，或在设置里改命令", res.missing), detail
	case res.err != nil:
		return "", i18n.Tf("格式化失败：%s", res.err.Error()), res.err.Error()
	case res.exit != 0:
		if first == "" {
			first = i18n.Tf("退出码 %d", res.exit)
		}
		return "", i18n.Tf("格式化失败：%s", first), detail
	case res.stdout == "" && text != "": // 什么都没输出就当没跑成：不能把整个文件清空
		return "", i18n.T("格式化失败：命令什么都没输出"), detail
	}
	return strings.ReplaceAll(res.stdout, "\r\n", "\n"), "", ""
}

// Format 是"格式化文档"（⇧⌥F、编辑器右键、命令面板）：结果在提示里说，套上了不另说。
func (a *App) Format(d *docs.Doc) {
	if !CanFormat(d) || d.Formatting {
		return
	}
	a.formatDoc(d, func(res formatOutcome, msg string) {
		if res == formatSame || res == formatApplied {
			d.FormatWarned = false
		}
		if msg != "" {
			a.Toast = msg
		}
	})
}

// saveFormatted 保存 d：开着"保存时格式化"、这种语言有命令时先在后台格式化，回来套上（文字没再改的话）再整理、写；
// 格式化失败照样存，提示一次（这个标签格式化成功之前不再提示）。done（可以为 nil）在写完或没写成之后调，带着 WriteDoc 的错误；
// 跑完时标签已经关了就不写，done(nil)。
func (a *App) saveFormatted(d *docs.Doc, done func(error)) {
	finish := func(note string) {
		err := a.WriteDoc(d, true)
		if err == nil && note != "" {
			a.Toast = i18n.Tf("已保存 %s · %s", d.Title, note)
		}
		if done != nil {
			done(err)
		}
	}
	if !a.St.Settings.FormatOnSave || !CanFormat(d) {
		finish("")
		return
	}
	a.formatDoc(d, func(res formatOutcome, msg string) {
		switch res {
		case formatGone:
			if done != nil {
				done(nil)
			}
		case formatFailed:
			warn := !d.FormatWarned
			d.FormatWarned = true
			if !warn {
				msg = ""
			}
			finish(msg)
		case formatStale:
			finish(msg)
		case formatSame, formatApplied:
			d.FormatWarned = false
			finish("")
		default:
			finish("")
		}
	})
}

// SaveEach 是 ⌥⌘S：所有组里改过的标签一个一个存，开着保存时格式化的一个一个格式化（前一个写完才跑下一个）；
// 有一个没存上就停。退出确认里的"全部保存"用 SaveAll，不等格式化。
func (a *App) SaveEach() {
	todo := slices.Clone(a.AllDocs())
	var next func(error)
	next = func(err error) {
		if err != nil {
			return
		}
		for len(todo) > 0 {
			d := todo[0]
			todo = todo[1:]
			if d.Modified() && d.Editable() && !d.Formatting {
				a.saveFormatted(d, next)
				return
			}
		}
	}
	next(nil)
}
