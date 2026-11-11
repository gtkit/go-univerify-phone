package univerify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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
	_ struct{} // 防止两个字符串被位置初始化调换。
	//AppID       string   `json:"app_id"`
	AccessToken string `json:"access_token"` // App 返回的 access_token，最多 4096 字节。
	OpenID      string `json:"openid"`       // App 返回的 openid，最多 1024 字节。
}

// String 返回脱敏描述。
func (Request) String() string { return "univerify.Request{[redacted]}" }

// GoString 返回脱敏调试描述。
func (r Request) GoString() string { return r.String() }

// Client 是可并发复用的取号客户端。构造后不修改其配置；注入的 Transport 和 JSONCodec
// 也必须支持并发。Client 不管理业务 Redis、用户库或 token。
type Client struct {
	endpoint string
	key      []byte        // 密钥 secret
	timeout  time.Duration // 超时
	http     *httpc.Client // HTTP 客户端
	codec    JSONCodec     // JSON 编码器
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
		return nil, failure(ErrInvalidArgument, "INVALID_CONFIG_SECRET")
	}

	// 解析选项。
	o := options{timeout: 5 * time.Second, codec: nativeJSON{}}
	for _, opt := range opts {
		if opt == nil {
			return nil, failure(ErrInvalidArgument, "INVALID_OPTION")
		}
		if err := opt(&o); err != nil {
			return nil, err
		}
	}

	// 构建 HTTP 客户端选项。
	httpOpts := make([]httpc.Option, 0, 5)
	if o.client != nil {
		httpOpts = append(httpOpts, httpc.WithHTTPClient(o.client))
	}
	httpOpts = append(httpOpts,
		httpc.WithTimeout(o.timeout),
		httpc.WithoutRedirect(),
		httpc.WithMaxResponseBytes(maxResponseBytes),
		httpc.WithHTTPStatusError(),
	)

	return &Client{
		endpoint: endpoint,
		key:      []byte(cfg.Secret),
		timeout:  o.timeout,
		http:     httpc.New(httpOpts...),
		codec:    o.codec,
	}, nil
}

// resolveEndpoint 校验并解析端点。
func resolveEndpoint(cfg Config) (string, error) {
	endpoint := cfg.Endpoint
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") {
		return "", failure(ErrInvalidArgument, "INVALID_ENDPOINT")
	}
	return endpoint, nil
}

// GetPhoneNumber 调用云函数取号，成功时返回手机号。
// 响应格式：成功 {"code":0,"phoneNumber":"..."}；
// 业务失败 {"code":50201,"message":"一键登录取号失败","upstream":{...}}（HTTP 仍为 2xx）。
// code != 0 返回 kind 为 ErrUpstream 的 *Error，Code 为业务码十进制字符串，完整原始正文经 Body 透传；
// 响应无法解码、缺 code 或成功但缺手机号返回 ErrInvalidResponse，同样携带原始正文。
// Error 字符串输出类别、业务码与原始正文；正文可能含上游原始文案，调用方自行决定记录与脱敏。
// 请求编码、传输和读取错误直接返回；非 2xx 返回 httpc.HTTPError，正文仍会保留。
// 默认超时与响应大小限制仍然生效，不自动重试。
func (c *Client) GetPhoneNumber(ctx context.Context, req Request) (string, error) {
	if c == nil || c.http == nil || c.codec == nil || ctx == nil {
		return "", failure(ErrInvalidArgument, "INVALID_CLIENT_OR_CONTEXT")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !validCredential(req.AccessToken, maxTokenBytes) || !validCredential(req.OpenID, maxOpenIDBytes) {
		return "", failure(ErrInvalidArgument, "INVALID_CREDENTIAL")
	}
	encoded, err := c.codec.Marshal(signedRequest{
		AccessToken: req.AccessToken,
		OpenID:      req.OpenID,
	})
	if err != nil {
		return "", err
	}
	body := bytes.Clone(encoded)
	if len(body) > maxRequestBytes || !utf8.Valid(body) || !json.Valid(body) {
		return "", failure(ErrEncoding, "INVALID_ENCODED_REQUEST")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// 签名覆盖 timestamp + "\n" + 原始请求正文。
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	headers := map[string]string{
		"Content-Type":          "application/json",
		"X-UniVerify-Timestamp": timestamp,
		"X-UniVerify-Sign":      signature(c.key, timestamp, body),
	}

	data, _, err := c.http.RequestRaw(requestCtx, http.MethodPost, c.endpoint, headers, httpc.RawBody(body))
	if err != nil {
		return "", err
	}

	return c.decodePhoneNumber(data)
}

// successCode 是云函数响应信封中表示取号成功的业务码。
const successCode = 0

// decodePhoneNumber 解析云函数响应并提取手机号；所有响应级错误都经 Body 携带完整原始正文。
func (c *Client) decodePhoneNumber(data []byte) (string, error) {
	var resp cloudResponse
	if err := c.codec.Unmarshal(data, &resp); err != nil {
		return "", &Error{kind: ErrInvalidResponse, Code: "INVALID_RESPONSE_UNMARSHAL", Body: data}
	}
	if resp.Code == nil {
		return "", &Error{kind: ErrInvalidResponse, Code: "MISSING_RESPONSE_CODE", Body: data}
	}
	if *resp.Code != successCode {
		return "", &Error{kind: ErrUpstream, Code: strconv.Itoa(*resp.Code), Body: data}
	}
	if resp.PhoneNumber == "" {
		return "", &Error{kind: ErrInvalidResponse, Code: "MISSING_PHONE_NUMBER", Body: data}
	}
	return resp.PhoneNumber, nil
}

// cloudResponse 是云函数响应；成功 {"code":0,"phoneNumber":"..."}，
// 失败 {"code":非0,"message":"...","upstream":{...}}。
// message 与 upstream 不逐字段解析，失败时完整正文经 Error.Body 透传给调用方。
type cloudResponse struct {
	Code        *int   `json:"code"`        // 业务码，指针用于区分缺失与 0。
	PhoneNumber string `json:"phoneNumber"` // 成功时的手机号。
}

// signedRequest 是请求的 JSON 编码结构体；签名不在正文，走 X-UniVerify-Sign 头。
type signedRequest struct {
	AccessToken string `json:"access_token"`
	OpenID      string `json:"openid"`
}
