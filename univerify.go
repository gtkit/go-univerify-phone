package univerify

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gtkit/httpc"
)

// Config 提供服务器端配置。Endpoint 和 Secret 必填，无默认值。
type Config struct {
	_        struct{} // 强制外部使用字段名初始化。
	Endpoint string   // URL 化云函数的完整 HTTPS 地址；禁止用户信息、query 和 fragment。
	Secret   string   `json:"-"` // 与云函数一致的非空 UTF-8 原始密钥，由调用方私有配置提供。
}

// String 返回不包含密钥的配置描述。
func (Config) String() string { return "univerify.Config{[redacted]}" }

// GoString 返回不包含密钥的调试描述。
func (c Config) GoString() string { return c.String() }

// Request 包含 App 授权返回的原始凭证；不得记录到日志。
type Request struct {
	_           struct{} // 防止两个字符串被位置初始化调换。
	AccessToken string   `json:"access_token"` // App 返回的 access_token，最多 4096 字节。
	OpenID      string   `json:"openid"`       // App 返回的 openid，最多 1024 字节。
}

// String 返回脱敏描述。
func (Request) String() string { return "univerify.Request{[redacted]}" }

// GoString 返回脱敏调试描述。
func (r Request) GoString() string { return r.String() }

// Client 是可并发复用的取号客户端。构造后不修改其配置；注入的 Transport 和 JSONCodec
// 也必须支持并发。Client 不管理业务 Redis、用户库或 token。
type Client struct {
	endpoint string
	key      []byte
	timeout  time.Duration
	http     *httpc.Client
	codec    JSONCodec
}

// String 返回不包含内部配置或密钥的描述。
func (Client) String() string { return "univerify.Client{[redacted]}" }

// GoString 返回脱敏调试描述。
func (c Client) GoString() string { return c.String() }

// New 校验配置并创建客户端；不发送网络请求，不启动后台任务。
// 默认超时 5 秒、响应上限 16 KiB、原生 JSON、禁止重定向及 Cookie。
// 配置或选项无效时返回 ErrInvalidArgument。
func New(cfg Config, opts ...Option) (*Client, error) {
	endpoint, err := resolveEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Secret == "" || !utf8.ValidString(cfg.Secret) {
		return nil, failure(ErrInvalidArgument, "INVALID_CONFIG")
	}
	o := options{timeout: 5 * time.Second, codec: nativeJSON{}}
	for _, opt := range opts {
		if opt == nil {
			return nil, failure(ErrInvalidArgument, "INVALID_OPTION")
		}
		if err := opt(&o); err != nil {
			return nil, err
		}
	}
	httpOpts := []httpc.Option{}
	if o.client != nil {
		httpOpts = append(httpOpts, httpc.WithHTTPClient(o.client))
	}
	httpOpts = append(httpOpts, httpc.WithTimeout(o.timeout), httpc.WithoutRedirect(), httpc.WithMaxResponseBytes(maxResponseBytes), httpc.WithHTTPStatusError())
	return &Client{
		endpoint: endpoint, key: []byte(cfg.Secret), timeout: o.timeout,
		http: httpc.New(httpOpts...), codec: o.codec,
	}, nil
}

func resolveEndpoint(cfg Config) (string, error) {
	endpoint := cfg.Endpoint
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") {
		return "", failure(ErrInvalidArgument, "INVALID_ENDPOINT")
	}
	return endpoint, nil
}

// GetPhoneNumber 返回云函数响应的原始正文，不解析业务 code 或手机号。
// 请求编码、传输和读取错误直接返回；非 2xx 返回 httpc.HTTPError，正文仍会保留。
// 默认超时与响应大小限制仍然生效，不自动重试。
func (c *Client) GetPhoneNumber(ctx context.Context, req Request) ([]byte, error) {
	if c == nil || c.http == nil || c.codec == nil || ctx == nil {
		return nil, failure(ErrInvalidArgument, "INVALID_CLIENT_OR_CONTEXT")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validCredential(req.AccessToken, maxTokenBytes) || !validCredential(req.OpenID, maxOpenIDBytes) {
		return nil, failure(ErrInvalidArgument, "INVALID_CREDENTIAL")
	}
	encoded, err := c.codec.Marshal(signedRequest{
		AccessToken: req.AccessToken,
		OpenID:      req.OpenID,
		Sign:        signature(c.key, req),
	})
	if err != nil {
		return nil, err
	}
	body := bytes.Clone(encoded)
	if len(body) > maxRequestBytes || !utf8.Valid(body) || !json.Valid(body) {
		return nil, failure(ErrEncoding, "INVALID_ENCODED_REQUEST")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	data, _, err := c.http.RequestRaw(requestCtx, http.MethodPost, c.endpoint, nil, httpc.RawBody(body))
	return data, err
}

type signedRequest struct {
	AccessToken string `json:"access_token"`
	OpenID      string `json:"openid"`
	Sign        string `json:"sign"`
}
