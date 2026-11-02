package workspace

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// 这个文件是设置的 JSON：编辑器区的 settings.json 文档（app.OpenSettingsDoc）、导出 / 导入的文件都用。
// 存在 state.json 里的照旧是 omitempty 的那份（SaveState）；这里写的是全部键，零值也写，默认值一眼看全。

// settingsFields 是 Settings 的字段和它们的 json 名，照结构体的顺序。
var settingsFields = func() []settingsField {
	t := reflect.TypeFor[Settings]()
	out := make([]settingsField, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		out = append(out, settingsField{i, name})
	}
	return out
}()

type settingsField struct {
	index int
	key   string
}

// SettingsKeys 是设置的所有键（json 名），照结构体的顺序。
func SettingsKeys() []string {
	keys := make([]string, len(settingsFields))
	for i, f := range settingsFields {
		keys[i] = f.key
	}
	return keys
}

// SettingsJSON 是 s 的 JSON：所有键都写，零值也写（"editorSize": 0；map、切片空着写 {}、[]），
// 键照结构体的顺序，两格缩进，末尾换行。
func SettingsJSON(s Settings) []byte {
	v := reflect.ValueOf(s)
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range settingsFields {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(f.key)
		b.Write(key)
		b.WriteByte(':')
		x := v.Field(f.index)
		switch {
		case x.Kind() == reflect.Map && x.Len() == 0:
			b.WriteString("{}")
		case x.Kind() == reflect.Slice && x.Len() == 0:
			b.WriteString("[]")
		default:
			data, _ := json.Marshal(x.Interface()) // 设置里只有字符串、数字、布尔和它们的表，不会出错
			b.Write(data)
		}
	}
	b.WriteByte('}')
	var out bytes.Buffer
	_ = json.Indent(&out, b.Bytes(), "", "  ")
	out.WriteByte('\n')
	return out.Bytes()
}

// SettingsError 是设置的 JSON 哪里不对：Line、Col 从 1 起（Col 按字算）。Kind 是 "syntax"（Msg 是 encoding/json 的原话）、
// "unknown"（不认识的键 Key）、"type"（键 Key 的值类型不对：Want 是要的、Got 是写的，都是 "string" "number" "bool" "array" "object" 之一）、
// "trailing"（对象后面还有东西）、"notObject"（最外面不是对象）。
type SettingsError struct {
	Line, Col int
	Kind      string
	Key       string
	Want, Got string
	Msg       string
}

func (e *SettingsError) Error() string {
	switch e.Kind {
	case "unknown":
		return "unknown key " + e.Key
	case "type":
		return e.Key + ": want " + e.Want + ", got " + e.Got
	}
	return cmp.Or(e.Msg, e.Kind)
}

// ParseSettings 严格地解设置的 JSON（设置文档保存时）：不认识的键（终端配置、网页风格表里的也算）、值的类型不对、
// 最外面不是对象、对象后面还有东西都报错（*SettingsError，带行列）。没 Clamp。
func ParseSettings(data []byte) (Settings, error) {
	var s Settings
	if err := objectStart(data); err != nil {
		return s, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Settings{}, decodeError(data, err, dec.InputOffset())
	}
	end := dec.InputOffset()
	if _, err := dec.Token(); err != io.EOF {
		e := &SettingsError{Kind: "trailing"}
		e.Line, e.Col = lineCol(data, nextNonSpace(data, end))
		return Settings{}, e
	}
	return s, nil
}

// ReadSettingsFile 宽松地解导入的文件：有 "settings" 键（导出的文件、整份 state.json）取它，不然整个对象当设置；
// 不认识的键、值的类型不对的键跳过，记在 ignored 里（照文件里的顺序）。只有不是 JSON 对象时报错（*SettingsError）。没 Clamp。
func ReadSettingsFile(data []byte) (s Settings, ignored []string, err error) {
	if err := objectStart(data); err != nil {
		return s, nil, err
	}
	var top map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&top); err != nil {
		return s, nil, decodeError(data, err, dec.InputOffset())
	}
	body := data
	if raw, ok := top["settings"]; ok && objectStart(raw) == nil {
		body = raw
	}
	keys := settingsKeySet()
	var all map[string]json.RawMessage
	_ = json.Unmarshal(body, &all)
	keep := map[string]json.RawMessage{}
	for _, k := range objectKeys(body) {
		v := all[k]
		var probe Settings
		if !keys[k] || json.Unmarshal(append(append([]byte(`{"`+k+`":`), v...), '}'), &probe) != nil {
			ignored = append(ignored, k)
			continue
		}
		keep[k] = v
	}
	clean, _ := json.Marshal(keep)
	_ = json.Unmarshal(clean, &s)
	return s, ignored, nil
}

// ChangedKeys 是 a 和 b 不一样的设置键（json 名，照结构体的顺序）；空的 map、切片和没有算一样。
func ChangedKeys(a, b Settings) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var out []string
	for _, f := range settingsFields {
		x, y := va.Field(f.index), vb.Field(f.index)
		if (x.Kind() == reflect.Map || x.Kind() == reflect.Slice) && x.Len() == 0 && y.Len() == 0 {
			continue
		}
		if !reflect.DeepEqual(x.Interface(), y.Interface()) {
			out = append(out, f.key)
		}
	}
	return out
}

func settingsKeySet() map[string]bool {
	m := make(map[string]bool, len(settingsFields))
	for _, f := range settingsFields {
		m[f.key] = true
	}
	return m
}

// objectKeys 是 JSON 对象 data 最外面一层的键，照文件里的顺序（重复的只算一次）。
func objectKeys(data []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		k, _ := t.(string)
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return keys
		}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

// objectStart 检查 data 最外面是不是一个对象（{ 开头）：不是就是 notObject 错误，位置在第一个字上。
func objectStart(data []byte) error {
	i := nextNonSpace(data, 0)
	if i < int64(len(data)) && data[i] == '{' {
		return nil
	}
	e := &SettingsError{Kind: "notObject"}
	e.Line, e.Col = lineCol(data, i)
	return e
}

// decodeError 把 encoding/json 的错误换成带行列的 *SettingsError；off 是解码器停下的位置（不认识的键从这往回找）。
func decodeError(data []byte, err error, off int64) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	e := &SettingsError{Kind: "syntax", Msg: err.Error()}
	switch {
	case errors.As(err, &syn):
		e.Line, e.Col = lineCol(data, max(0, syn.Offset-1))
		e.Msg = strings.TrimPrefix(syn.Error(), "json: ")
	case errors.As(err, &typ):
		e.Kind, e.Key, e.Got = "type", typ.Field, jsonKind(typ.Value)
		e.Want = goKind(typ.Type)
		if i := strings.LastIndex(typ.Field, "."); i >= 0 {
			e.Key = typ.Field[i+1:]
		}
		e.Line, e.Col = lineCol(data, valueStart(data, typ.Offset, e.Key))
	case errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		e.Msg = "unexpected end of JSON input"
		e.Line, e.Col = lineCol(data, int64(len(data)))
	default:
		if k, ok := strings.CutPrefix(err.Error(), `json: unknown field "`); ok {
			k = strings.TrimSuffix(k, `"`)
			e.Kind, e.Key = "unknown", k
			at := bytes.LastIndex(data[:min(off, int64(len(data)))], []byte(`"`+k+`"`))
			e.Line, e.Col = lineCol(data, int64(max(at, 0)))
		}
	}
	return e
}

// valueStart 是 UnmarshalTypeError.Offset（读完那个值之后）往回找到的那个值的开头：往回找到键 key，它后面冒号后的第一个字
// （不直接往回找冒号：值是 "a:b" 这样的字符串时会找到字符串里面去）。
func valueStart(data []byte, off int64, key string) int64 {
	off = min(off, int64(len(data)))
	k := bytes.LastIndex(data[:off], []byte(`"`+key+`"`))
	if k < 0 {
		return off
	}
	i := bytes.IndexByte(data[k+len(key)+2:off], ':')
	if i < 0 {
		return off
	}
	return nextNonSpace(data, int64(k+len(key)+2+i+1))
}

func nextNonSpace(data []byte, i int64) int64 {
	for i < int64(len(data)) && strings.IndexByte(" \t\r\n", data[i]) >= 0 {
		i++
	}
	return i
}

// lineCol 是 data 里字节位置 off 的行、列（从 1 起，列按字算）。
func lineCol(data []byte, off int64) (int, int) {
	off = min(max(off, 0), int64(len(data)))
	before := data[:off]
	line := bytes.Count(before, []byte{'\n'}) + 1
	start := bytes.LastIndexByte(before, '\n') + 1
	return line, utf8.RuneCount(before[start:]) + 1
}

// jsonKind 是 UnmarshalTypeError.Value（"string"、"number 3"、"bool"、"array"、"object"）归成的种类。
func jsonKind(v string) string {
	if strings.HasPrefix(v, "number") {
		return "number"
	}
	return v
}

// goKind 是 Go 类型对应的 JSON 种类。
func goKind(t reflect.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	}
	return "number"
}
