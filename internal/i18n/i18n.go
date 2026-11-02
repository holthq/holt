// Package i18n 是界面文案表。代码里文案直接写中文，显示前过一下 T / Tf（带数的 Tn）：界面是中文时原样返回，
// 是英文时查 en.json。zh-CN.json 是代码里的全部中文文案（catalog_test.go 用 -update 从代码里收集，
// 并检查 en.json 每条都有、占位符对得上）；en.json 先是机翻。
package i18n

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// Langs 是界面能用的语言："" 中文、"en" 英文（和设置里存的一样）。
var Langs = []string{"", "en"}

//go:embed en.json
var enJSON []byte

var (
	lang   atomic.Value // string
	enOnce sync.Once
	en     map[string]string
)

// Set 换界面语言：""（或不认识的）是中文，"en" 是英文。
func Set(l string) {
	if l != "en" {
		l = ""
	}
	lang.Store(l)
}

// Lang 是现在的界面语言："" 中文、"en" 英文。
func Lang() string {
	l, _ := lang.Load().(string)
	return l
}

// Locale 是交给 mygo 的语言（它自己的文案：编辑器的查找条、系统菜单、对话框按钮）。
func Locale() string {
	if Lang() == "en" {
		return "en"
	}
	return "zh-Hans"
}

// T 是中文文案 s 在当前语言里的样子；表里没有就原样返回。
func T(s string) string {
	if Lang() != "en" {
		return s
	}
	if t, ok := English()[s]; ok && t != "" {
		return t
	}
	return s
}

// List 把几项连成一句里的列举：中文用 "、"，英文用 ", "。
func List(items []string) string {
	if Lang() == "en" {
		return strings.Join(items, ", ")
	}
	return strings.Join(items, "、")
}

// Tf 是 fmt.Sprintf(T(format), args...)。
func Tf(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// Tn 是带数的 Tf：英文里数是 1 时换单数的写法（"1 file"），中文同 Tf。
// 格式串里只有一个 %d 时，它是 1 就先找 en.json 里的 format+"|1"；有两个以上 %d 时看头两个：
// "|1|" 是第一个是 1、"||1" 是第二个是 1、"|1|1" 是都是 1（都是 1 而没有 "|1|1" 的依次退到 "|1|"、"||1"）。
// 没有对得上的变体就用 format 本身的译文。
func Tn(format string, args ...any) string {
	if Lang() != "en" {
		return fmt.Sprintf(format, args...)
	}
	for _, k := range PluralKeys(format, args...) {
		if t, ok := English()[format+k]; ok && t != "" {
			return fmt.Sprintf(t, args...)
		}
	}
	return Tf(format, args...)
}

// PluralKeys 是 Tn 按 args 要依次找的变体后缀（见 Tn）；数都不是 1 时为空。
func PluralKeys(format string, args ...any) []string {
	var ns []int64
	arg := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		j := i + 1
		for j < len(format) && strings.IndexByte("+-# 0123456789.", format[j]) >= 0 {
			j++
		}
		if j == len(format) {
			break
		}
		i = j
		if format[j] == '%' {
			continue
		}
		if format[j] == 'd' && arg < len(args) {
			ns = append(ns, intOf(args[arg]))
		}
		arg++
	}
	switch {
	case len(ns) == 1 && ns[0] == 1:
		return []string{"|1"}
	case len(ns) < 2:
		return nil
	case ns[0] == 1 && ns[1] == 1:
		return []string{"|1|1", "|1|", "||1"}
	case ns[0] == 1:
		return []string{"|1|"}
	case ns[1] == 1:
		return []string{"||1"}
	}
	return nil
}

// intOf 是 %d 的参数的值（不是整数的当 0）。
func intOf(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case int32:
		return int64(x)
	case uint:
		return int64(x)
	case uint64:
		return int64(x)
	case uint32:
		return int64(x)
	}
	return 0
}

// English 是英文表（中文 → 英文）。
func English() map[string]string {
	enOnce.Do(func() {
		if err := json.Unmarshal(enJSON, &en); err != nil {
			panic("i18n: en.json: " + err.Error())
		}
	})
	return en
}
