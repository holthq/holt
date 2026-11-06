package app

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/holthq/holt/internal/askpass"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是 git / ssh 要凭据时弹的框（helper 和套接字见 internal/askpass）。整个进程一个套接字，第一条写命令前起；
// 每条写命令（和克隆）登记一个随机 id 放进它的环境，helper 来问时按 id 找到是哪个窗口的哪条命令，在那个窗口里排一个
// CredReq（一次弹一个，a.Cred[0]）。答了回给 helper；取消回不 ok，git 失败，但只 toast "已取消"（Op.ask.canceled）。
// 等用户的时候命令的超时钟停着（git.Keep 的 Hold / Release）。回答不记进输出栏和日志文件，输出栏只记"凭据提示：主机"。

// asks 是进程里唯一的套接字和登记了的命令（id → askOwner）。
var asks struct {
	mu     sync.Mutex
	srv    *askpass.Server
	failed bool // 起不来（记过一次日志）：之后的命令照旧用 git 包默认的 GIT_ASKPASS=true
	owners map[string]*askOwner
}

// askpassExe 是脚本 exec 的程序：应用自己（main 第一行分出 askpass 模式）。测试里是测试程序，TestMain 同样分出去。
var askpassExe = os.Executable

// askOwner 是登记了的一条写命令或克隆。
type askOwner struct {
	id       string
	app      *App
	up       func(func()) // 那个窗口的 Update（在主线程上取的）
	repo     *Repo        // 克隆时为 nil
	name     string       // 框上写的仓库名（克隆时是目录名）
	keep     *git.Keep
	canceled bool // 用户在凭据框里点了取消（主线程）
}

// askServer 是套接字，第一次用时起；起不来返回 nil。
func askServer() *askpass.Server {
	asks.mu.Lock()
	defer asks.mu.Unlock()
	if asks.srv != nil || asks.failed {
		return asks.srv
	}
	exe, err := askpassExe()
	if err == nil {
		asks.srv, err = askpass.Listen(os.TempDir(), exe, askHandle)
	}
	if err != nil {
		asks.failed = true
		log.Printf("凭据提示的套接字：%v", err)
	}
	return asks.srv
}

// AskpassClose 在退出时删掉套接字和脚本（main 里调；没起过就什么都不做）。
func AskpassClose() {
	asks.mu.Lock()
	defer asks.mu.Unlock()
	if asks.srv != nil {
		asks.srv.Close()
		asks.srv = nil
	}
}

// askHandle 在套接字的 goroutine 上：按 id 找到命令，停住它的超时钟，回那个窗口的主线程排进凭据框。
// 不认识的 id（命令已经结束、别的进程乱连）一律回不 ok。
func askHandle(req askpass.Request, reply func(askpass.Reply)) {
	asks.mu.Lock()
	o := asks.owners[req.ID]
	asks.mu.Unlock()
	if o == nil {
		reply(askpass.Reply{})
		return
	}
	o.keep.Hold()
	o.up(func() {
		o.app.credAsk(o, req.Prompt, func(rep askpass.Reply) {
			reply(rep)
			o.keep.Release()
		})
	})
}

// askRegister 登记一条要跑的命令（主线程）；跑完调 askDone。
func (a *App) askRegister(r *Repo, name string, up func(func())) *askOwner {
	b := make([]byte, 12)
	rand.Read(b)
	o := &askOwner{id: hex.EncodeToString(b), app: a, up: up, repo: r, name: name, keep: &git.Keep{}}
	asks.mu.Lock()
	if asks.owners == nil {
		asks.owners = map[string]*askOwner{}
	}
	asks.owners[o.id] = o
	asks.mu.Unlock()
	return o
}

// askDone 是命令跑完了（主线程）：注销 id，还排着的这条命令的凭据框撤掉。
func (a *App) askDone(o *askOwner) {
	if o == nil {
		return
	}
	asks.mu.Lock()
	delete(asks.owners, o.id)
	asks.mu.Unlock()
	a.Cred = slices.DeleteFunc(a.Cred, func(q *CredReq) bool {
		if q.owner == o {
			q.reply(askpass.Reply{})
			return true
		}
		return false
	})
}

// askEnv 是带凭据提示的写命令的环境：git（HTTPS）和 ssh（口令、主机指纹）都问我们的脚本。
// GIT_TERMINAL_PROMPT=0 照旧（git 包默认的）；套接字起不来就是 nil（照旧 GIT_ASKPASS=true，要凭据就失败）。
func askEnv(o *askOwner) []string {
	s := askServer()
	if s == nil || o == nil {
		return nil
	}
	return []string{"GIT_ASKPASS=" + s.Script, "SSH_ASKPASS=" + s.Script, "SSH_ASKPASS_REQUIRE=force",
		askpass.EnvSock + "=" + s.Sock, askpass.EnvID + "=" + o.id}
}

// CredReq 是一个等用户回答的凭据提示。
type CredReq struct {
	askpass.Prompt
	Repo   string // 框上写的仓库名（克隆时是目录名）
	User   string // 用户名（Username；Password 时是提示里带的，只显示）
	Pass   string // 密码 / 令牌 / 口令
	Answer string // 认不出的提示的回答

	owner *askOwner
	reply func(askpass.Reply)
}

// credKeep 是 HTTPS 用户名框里一起填的密码记多久：git 问完用户名紧接着问密码，记着就不再弹一次。只在内存里，用一次就忘。
const credKeep = 2 * time.Minute

// credPass 是记着的密码。
type credPass struct {
	pass  string
	until time.Time
}

func credKey(host, user string) string { return host + "\x00" + user }

// credAsk 是一个提示到了（主线程）：记着密码的直接回，否则排进凭据框。
func (a *App) credAsk(o *askOwner, text string, reply func(askpass.Reply)) {
	asks.mu.Lock()
	live := asks.owners[o.id] == o
	asks.mu.Unlock()
	if !live || o.canceled { // 命令已经结束；或者这条命令里已经取消过一次（ssh 换一种方式再问），不再弹
		reply(askpass.Reply{})
		return
	}
	p := askpass.Classify(text)
	what := p.Host
	switch {
	case p.Key != "":
		what = p.Key
	case what == "":
		what = p.Title()
	}
	a.Log(o.repo, i18n.T("凭据提示"), what, false)
	if p.Kind == askpass.Password {
		k := credKey(p.Host, p.User)
		if c, ok := a.credCache[k]; ok {
			delete(a.credCache, k)
			if time.Now().Before(c.until) {
				reply(askpass.Reply{OK: true, Text: c.pass})
				return
			}
		}
	}
	a.Cred = append(a.Cred, &CredReq{Prompt: p, Repo: o.name, User: p.User, owner: o, reply: reply})
}

// AnswerCred 回答最前面的凭据框：ok 为 false 是取消（这条命令失败，只 toast"已取消"）。
func (a *App) AnswerCred(ok bool) {
	if len(a.Cred) == 0 {
		return
	}
	q := a.Cred[0]
	a.Cred = slices.Delete(a.Cred, 0, 1)
	if !ok {
		q.owner.canceled = true
		q.reply(askpass.Reply{})
		return
	}
	text := q.Pass
	switch q.Kind {
	case askpass.Username:
		text = strings.TrimSpace(q.User)
		if q.Pass != "" {
			if a.credCache == nil {
				a.credCache = map[string]credPass{}
			}
			a.credCache[credKey(q.Host, text)] = credPass{q.Pass, time.Now().Add(credKeep)}
		}
	case askpass.HostKey:
		text = "yes"
	case askpass.Other:
		text = q.Answer
	}
	q.Pass, q.Answer = "", ""
	q.reply(askpass.Reply{OK: true, Text: text})
}

// CredReady 是最前面的凭据框能不能点确定：用户名框要有用户名，HTTPS 密码 / 令牌框要有字；口令和认不出的可以空着回。
func CredReady(q *CredReq) bool {
	switch q.Kind {
	case askpass.Username:
		return strings.TrimSpace(q.User) != ""
	case askpass.Password:
		return q.Pass != ""
	}
	return true
}
