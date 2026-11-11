package univerify

import (
	"errors"
	"strings"
)

var (
	// ErrInvalidArgument 表示配置、选项或调用参数不合法。
	ErrInvalidArgument = errors.New("univerify: invalid argument")
	// ErrEncoding 表示请求编码结果不是有效 JSON 或超过大小限制；编码器自身错误直接透传。
	ErrEncoding = errors.New("univerify: encode request failed")
	// ErrUnauthorized 表示云函数返回 HTTP 401 或 403。
	ErrUnauthorized = errors.New("univerify: unauthorized")
	// ErrUpstream 表示云函数未能取得运营商手机号。
	ErrUpstream = errors.New("univerify: phone provider failed")
	// ErrUnavailable 表示传输失败、取消、超时或云函数不可用，不代表请求未被处理。
	ErrUnavailable = errors.New("univerify: unavailable")
	// ErrInvalidResponse 表示响应不符合约定或超过大小限制。
	ErrInvalidResponse = errors.New("univerify: invalid response")
)

// Error 携带完整的错误信息。使用 errors.Is 判断类别，errors.AsType[*Error] 读取详情。
// 响应级错误保留原始正文（Body）并进入字符串输出，调用方日志与脱敏策略自行决定如何记录。
// 取消和超时等传输错误直接透传，仍可由 errors.Is 判断。
type Error struct {
	_    struct{} // 强制使用字段名初始化。
	Code string   // 错误码：云函数业务码（十进制字符串）或本包内部错误标识。
	Body []byte   // 响应原始正文；本包内部错误（配置、编码等）为 nil。
	kind error
}

// Error 返回包含全部错误信息的消息：类别、错误码和响应原始正文（Body）；
// 正文可能含上游原始文案，调用方自行决定如何记录与脱敏。
func (e *Error) Error() string {
	if e == nil || e.kind == nil {
		return "univerify: unspecified error"
	}
	parts := []string{e.kind.Error()}
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
	}
	if len(e.Body) > 0 {
		parts = append(parts, "body="+string(e.Body))
	}
	return strings.Join(parts, " ")
}

// GoString 避免调试格式输出内部字段。
func (e *Error) GoString() string { return e.Error() }

// Is 支持按包级错误类别判断。
func (e *Error) Is(target error) bool { return e != nil && target == e.kind }

func failure(kind error, code string) *Error { return &Error{kind: kind, Code: code} }
