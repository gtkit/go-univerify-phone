package univerifygo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gtkit/httpc"
)

const defaultDomain = "https://env-00jy6yowysc0.dev-hz.cloudbasefunction.cn"

// Config 提供服务器端配置。KeyID、SigningKeyEnv 以及 Endpoint/FunctionPath 之一必填。
// 密钥不会通过公开字段传入。
type Config struct {
	_             struct{} // 强制外部使用字段名初始化。
	Endpoint      string   // 完整 HTTPS 地址，优先于 FunctionPath；禁止用户信息、query 和 fragment。
	FunctionPath  string   // Endpoint 留空时必填，使用默认服务空间域名；支持有或没有前导斜杠。
	KeyID         string   // 云端配置的密钥编号，1–64 个 ASCII 字母、数字、下划线或连字符。
	SigningKeyEnv string   // 保存标准 Base64 编码的 32 字节密钥的环境变量名称，无默认值。
}

// Request 包含 App 授权返回的原始凭证；不得记录到日志。
type Request struct {
	_           struct{} // 防止两个字符串被位置初始化调换。
	AccessToken string   `json:"access_token"` // App 返回的 access_token，最多 4096 字节。
	OpenID      string   `json:"openid"`       // App 返回的 openid，最多 1024 字节。
}

// String 返回脱敏描述。
func (Request) String() string { return "univerifygo.Request{[redacted]}" }

// GoString 返回脱敏调试描述。
func (r Request) GoString() string { return r.String() }

// Client 是可并发复用的取号客户端。构造后不修改其配置；注入的 Transport 和 JSONCodec
// 也必须支持并发。Client 不管理业务 Redis、用户库或 token。
type Client struct {
	endpoint string
	keyID    string
	key      []byte
	timeout  time.Duration
	http     *httpc.Client
	codec    JSONCodec
}

// String 返回不包含内部配置或密钥的描述。
func (Client) String() string { return "univerifygo.Client{[redacted]}" }

// GoString 返回脱敏调试描述。
func (c Client) GoString() string { return c.String() }

// New 校验配置并创建客户端；不发送网络请求，不启动后台任务。
// 默认超时 5 秒、响应上限 16 KiB、原生 JSON、禁止重定向及 Cookie。
// 配置、选项或环境变量密钥无效时返回 ErrInvalidArgument。
func New(cfg Config, opts ...Option) (*Client, error) {
	endpoint, err := resolveEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	if !validKeyID(cfg.KeyID) || cfg.SigningKeyEnv == "" || strings.ContainsAny(cfg.SigningKeyEnv, "=\x00") {
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
	encoded, ok := os.LookupEnv(cfg.SigningKeyEnv)
	if !ok {
		return nil, failure(ErrInvalidArgument, "MISSING_SIGNING_KEY")
	}
	key, err := decodeKey(encoded)
	if err != nil {
		return nil, err
	}
	httpOpts := []httpc.Option{}
	if o.client != nil {
		httpOpts = append(httpOpts, httpc.WithHTTPClient(o.client))
	}
	httpOpts = append(httpOpts, httpc.WithTimeout(o.timeout), httpc.WithoutRedirect(), httpc.WithMaxResponseBytes(maxResponseBytes))
	return &Client{
		endpoint: endpoint, keyID: cfg.KeyID, key: key, timeout: o.timeout,
		http: httpc.New(httpOpts...), codec: o.codec,
	}, nil
}

func resolveEndpoint(cfg Config) (string, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		path, err := url.Parse(cfg.FunctionPath)
		if err != nil || path.IsAbs() || path.Host != "" || path.User != nil || path.Path == "" ||
			path.RawQuery != "" || path.ForceQuery || path.Fragment != "" ||
			strings.Contains(cfg.FunctionPath, "#") || strings.HasPrefix(cfg.FunctionPath, "//") {
			return "", failure(ErrInvalidArgument, "INVALID_FUNCTION_PATH")
		}
		endpoint = defaultDomain + "/" + strings.TrimPrefix(path.EscapedPath(), "/")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") {
		return "", failure(ErrInvalidArgument, "INVALID_ENDPOINT")
	}
	return endpoint, nil
}

// GetPhoneNumber 将原始授权凭证换成手机号，不自动重试。
// 调用方应先执行业务限流和凭证去重，再调用本方法。超时或传输失败不代表没有产生费用。
// 返回的 Error 支持包级类别判断；取消/超时还支持 errors.Is(err, context.Canceled/DeadlineExceeded)。
func (c *Client) GetPhoneNumber(ctx context.Context, req Request) (string, error) {
	if c == nil || c.http == nil || c.codec == nil || ctx == nil {
		return "", failure(ErrInvalidArgument, "INVALID_CLIENT_OR_CONTEXT")
	}
	if err := ctx.Err(); err != nil {
		return "", transportFailure(err)
	}
	if !validCredential(req.AccessToken, maxTokenBytes) || !validCredential(req.OpenID, maxOpenIDBytes) {
		return "", failure(ErrInvalidArgument, "INVALID_CREDENTIAL")
	}
	encoded, err := c.codec.Marshal(req)
	if err != nil {
		return "", failure(ErrEncoding, "ENCODE_ERROR")
	}
	body := bytes.Clone(encoded)
	if len(body) > maxRequestBytes || !utf8.Valid(body) || !json.Valid(body) {
		return "", failure(ErrEncoding, "INVALID_ENCODED_REQUEST")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	headers := map[string]string{
		"X-Univerify-Key-Id":    c.keyID,
		"X-Univerify-Timestamp": timestamp,
		"X-Univerify-Signature": signature(c.key, c.keyID, timestamp, body),
	}
	header, responseBody, status, err := c.http.RequestRawWithHeader(requestCtx, http.MethodPost, c.endpoint, headers, httpc.RawBody(body))
	if err != nil {
		if errors.Is(err, httpc.ErrResponseTooLarge) {
			return "", failure(ErrInvalidResponse, "RESPONSE_TOO_LARGE")
		}
		return "", transportFailure(err)
	}
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" ||
		(params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || !utf8.Valid(responseBody) || !json.Valid(responseBody) {
		return "", failure(ErrInvalidResponse, "INVALID_RESPONSE")
	}
	var result wireResponse
	if err := c.codec.Unmarshal(responseBody, &result); err != nil {
		return "", failure(ErrInvalidResponse, "DECODE_ERROR")
	}
	return result.phone(status)
}

type wireResponse struct {
	Code         string `json:"code"`
	PhoneNumber  string `json:"phoneNumber"`
	ProviderCode *int64 `json:"providerCode"`
}

func (r wireResponse) phone(status int) (string, error) {
	if status == http.StatusOK && r.Code == "OK" && validPhone(r.PhoneNumber) {
		return r.PhoneNumber, nil
	}
	kind := ErrInvalidResponse
	if r.PhoneNumber == "" {
		switch {
		case status == 400 && r.Code == "INVALID_REQUEST", status == 413 && r.Code == "PAYLOAD_TOO_LARGE", status == 415 && r.Code == "UNSUPPORTED_MEDIA_TYPE":
			kind = ErrInvalidArgument
		case status == 401 && r.Code == "UNAUTHORIZED":
			kind = ErrUnauthorized
		case status == 502 && r.Code == "UPSTREAM_ERROR":
			kind = ErrUpstream
		case status == 503 && r.Code == "UNAVAILABLE":
			kind = ErrUnavailable
		}
	}
	code := "INVALID_RESPONSE"
	if !errors.Is(kind, ErrInvalidResponse) {
		code = r.Code
	}
	return "", &Error{kind: kind, StatusCode: status, Code: code, ProviderCode: r.ProviderCode}
}

func transportFailure(err error) *Error {
	e := failure(ErrUnavailable, "TRANSPORT_ERROR")
	switch {
	case errors.Is(err, context.Canceled):
		e.cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		e.cause = context.DeadlineExceeded
	}
	return e
}
