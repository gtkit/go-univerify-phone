package univerify

import (
	"encoding/json"
	"net/http"
	"reflect"
	"time"
)

// JSONCodec 负责请求序列化，实现必须支持并发调用并遵守 JSON 字段标签。
// 返回字节在 Marshal 返回后须可安全复制；GetPhoneNumber 不解码响应。
type JSONCodec interface {
	// Marshal 返回尊重 JSON 字段标签的编码结果。
	Marshal(v any) ([]byte, error)
	// Unmarshal 保留现有接口兼容性，GetPhoneNumber 不调用此方法。
	Unmarshal(data []byte, v any) error
}

type nativeJSON struct{}

func (nativeJSON) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (nativeJSON) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

type options struct {
	timeout time.Duration
	client  *http.Client
	codec   JSONCodec
}

// Option 配置 Client。选项在 New 时执行，不能在构造后修改 Client。
type Option func(*options) error

// WithTimeout 设置包含排队、网络和读取的总预算，默认 5 秒；必须大于零。
// 调用方 context 的更早期限始终优先。
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) error {
		if timeout <= 0 {
			return failure(ErrInvalidArgument, "INVALID_TIMEOUT")
		}
		o.timeout = timeout
		return nil
	}
}

// WithHTTPClient 复用调用方的传输配置。client 值会被复制，不接管其 Transport 生命周期。
// 本包仍固定总超时、禁用重定向和 Cookie Jar；Transport 必须支持并发且不得并发修改。
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) error {
		if client == nil {
			return failure(ErrInvalidArgument, "INVALID_HTTP_CLIENT")
		}
		clone := *client
		clone.Jar = nil
		o.client = &clone
		return nil
	}
}

// WithJSONCodec 注入 JSON 实例，默认使用 encoding/json。nil 和 typed-nil 均返回错误。
// 实例由调用方持有，必须支持并发；本包不会关闭或更改它。
func WithJSONCodec(codec JSONCodec) Option {
	return func(o *options) error {
		if codec == nil {
			return failure(ErrInvalidArgument, "INVALID_JSON_CODEC")
		}
		v := reflect.ValueOf(codec)
		switch v.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if v.IsNil() {
				return failure(ErrInvalidArgument, "INVALID_JSON_CODEC")
			}
		}
		o.codec = codec
		return nil
	}
}
