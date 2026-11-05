package univerifygo

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidArgument 表示配置、选项或调用参数不合法。
	ErrInvalidArgument = errors.New("univerifygo: invalid argument")
	// ErrEncoding 表示注入的 JSON 实例无法序列化请求。
	ErrEncoding = errors.New("univerifygo: encode request failed")
	// ErrUnauthorized 表示云函数拒绝签名或时间戳。
	ErrUnauthorized = errors.New("univerifygo: unauthorized")
	// ErrUpstream 表示云函数未能取得运营商手机号。
	ErrUpstream = errors.New("univerifygo: phone provider failed")
	// ErrUnavailable 表示传输失败、取消、超时或云函数不可用，不代表请求未被处理。
	ErrUnavailable = errors.New("univerifygo: unavailable")
	// ErrInvalidResponse 表示响应不符合约定或超过大小限制。
	ErrInvalidResponse = errors.New("univerifygo: invalid response")
)

// Error 提供安全的错误详情。使用 errors.Is 判断类别，errors.AsType[*Error] 读取详情。
// 不保留原始正文或任意传输/codec 错误；取消和超时仍可由 errors.Is 判断。
type Error struct {
	_            struct{} // 强制使用字段名初始化。
	StatusCode   int      // 收到的 HTTP 状态；未收到响应时为 0。
	Code         string   // 本包或配套云函数的安全错误码，不包含上游原始消息。
	ProviderCode *int64   // 可选的运营商数字错误码；未提供时为 nil。
	kind         error
	cause        error
}

// Error 返回不包含凭证或上游原始文案的消息。
func (e *Error) Error() string {
	if e == nil || e.kind == nil {
		return "univerifygo: unspecified error"
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s (HTTP %d)", e.kind, e.StatusCode)
	}
	return e.kind.Error()
}

// GoString 避免调试格式输出内部字段。
func (e *Error) GoString() string { return e.Error() }

// Is 支持按包级错误类别判断。
func (e *Error) Is(target error) bool { return e != nil && target == e.kind }

// Unwrap 只返回经过筛选的取消或超时原因。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func failure(kind error, code string) *Error { return &Error{kind: kind, Code: code} }
