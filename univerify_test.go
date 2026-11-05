package univerify

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func testConfig(t testing.TB) Config {
	t.Helper()
	t.Setenv("UV_TEST_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	return Config{Endpoint: "https://example.invalid/verify", KeyID: "test", SigningKeyEnv: "UV_TEST_KEY"}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestDefaultDomainAndEndpointOverride(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct{ endpoint, path, want string }{
		{"", "/univerify-phone", "https://env-00jy6yowysc0.dev-hz.cloudbasefunction.cn/univerify-phone"},
		{"", "phone-login", "https://env-00jy6yowysc0.dev-hz.cloudbasefunction.cn/phone-login"},
		{"", "/auth/phone", "https://env-00jy6yowysc0.dev-hz.cloudbasefunction.cn/auth/phone"},
		{"", "/phone%20login", "https://env-00jy6yowysc0.dev-hz.cloudbasefunction.cn/phone%20login"},
		{"https://override.invalid/custom", "https://ignored.invalid/?x=1", "https://override.invalid/custom"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			cfg.Endpoint, cfg.FunctionPath = tc.endpoint, tc.path
			calls := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tc.want {
					t.Errorf("URL=%q, want %q", r.URL.String(), tc.want)
				}
				return jsonResponse(200, `{"code":"OK","phoneNumber":"13800000000"}`), nil
			})
			client, err := New(cfg, WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatal("New sent a request")
			}
			if _, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token", OpenID: "openid"}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
	for _, path := range []string{"", "https://other.invalid/fn", "//other.invalid/fn", "///other.invalid/fn", "/fn?x=1", "/fn?", "/fn#fragment", "/fn#", "/bad%"} {
		cfg.Endpoint, cfg.FunctionPath = "", path
		if _, err := New(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("accepted path %q: %v", path, err)
		}
	}
}

func TestNewValidation(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct {
		name string
		edit func(*Config)
		opts []Option
	}{
		{name: "http", edit: func(c *Config) { c.Endpoint = "http://example.invalid" }},
		{name: "userinfo", edit: func(c *Config) { c.Endpoint = "https://user:secret@example.invalid" }},
		{name: "query", edit: func(c *Config) { c.Endpoint += "?token=secret" }},
		{name: "empty query", edit: func(c *Config) { c.Endpoint += "?" }},
		{name: "fragment", edit: func(c *Config) { c.Endpoint += "#" }},
		{name: "bad url", edit: func(c *Config) { c.Endpoint = "://" }},
		{name: "empty host", edit: func(c *Config) { c.Endpoint = "https:///verify" }},
		{name: "key id", edit: func(c *Config) { c.KeyID = "bad\nkey" }},
		{name: "missing env", edit: func(c *Config) { c.SigningKeyEnv = "UV_MISSING_TEST_KEY" }},
		{name: "zero timeout", opts: []Option{WithTimeout(0)}},
		{name: "nil client", opts: []Option{WithHTTPClient(nil)}},
		{name: "nil option", opts: []Option{nil}},
		{name: "nil codec", opts: []Option{WithJSONCodec(nil)}},
		{name: "typed nil codec", opts: []Option{WithJSONCodec((*countingCodec)(nil))}},
	}
	t.Setenv("UV_MISSING_TEST_KEY", "")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			if tc.edit != nil {
				tc.edit(&c)
			}
			if _, err := New(c, tc.opts...); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("want invalid argument, got %v", err)
			}
		})
	}
	for _, value := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 32)) + "\n"} {
		t.Setenv("UV_TEST_KEY", value)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bad key accepted: %v", err)
		}
	}
}

type countingCodec struct {
	marshals   atomic.Int32
	unmarshals atomic.Int32
	encodeErr  bool
	decodeErr  bool
	raw        []byte
}

func (c *countingCodec) Marshal(v any) ([]byte, error) {
	c.marshals.Add(1)
	if c.encodeErr {
		return nil, errors.New("private-token-and-key")
	}
	if c.raw != nil {
		return c.raw, nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	return b, err
}

type trackedBody struct {
	reader io.Reader
	closed bool
}

func (b *trackedBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *trackedBody) Close() error               { b.closed = true; return nil }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("private-response") }

func TestResponseClosedAndEncodingBounds(t *testing.T) {
	cfg := testConfig(t)
	for _, reader := range []io.Reader{strings.NewReader(`{"code":"OK","phoneNumber":"13800000000"}`), brokenReader{}} {
		body := &trackedBody{reader: reader}
		client, err := New(cfg, WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
		})}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
		if !body.closed {
			t.Fatal("response body not closed")
		}
		if _, broken := reader.(brokenReader); broken && !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	for _, raw := range [][]byte{[]byte("{"), {255}, []byte(strings.Repeat(" ", maxRequestBytes+1)), []byte("")} {
		client, err := New(cfg, WithJSONCodec(&countingCodec{raw: raw}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"}); !errors.Is(err, ErrEncoding) {
			t.Fatal(err)
		}
	}
}

func (c *countingCodec) Unmarshal(b []byte, v any) error {
	c.unmarshals.Add(1)
	if c.decodeErr {
		return errors.New("private-phone-and-key")
	}
	return json.Unmarshal(b, v)
}

func TestSignedTLSAndCodec(t *testing.T) {
	cfg := testConfig(t)
	codec := &countingCodec{}
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("wrong method/content type")
		}
		if !strings.Contains(string(body), "\n") {
			t.Error("custom codec body was re-encoded")
		}
		digest := sha256.Sum256(body)
		message := strings.Join([]string{"univerify-go/v1", "POST", "test", r.Header.Get("X-Univerify-Timestamp"), hex.EncodeToString(digest[:])}, "\n")
		mac := hmac.New(sha256.New, make([]byte, 32))
		if _, err := mac.Write([]byte(message)); err != nil {
			t.Error(err)
			return
		}
		if r.Header.Get("X-Univerify-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature mismatch")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if _, err := io.WriteString(w, `{"code":"OK","phoneNumber":"13800000000"}`); err != nil {
			t.Error(err)
		}
	}))
	server.StartTLS()
	t.Cleanup(server.Close)
	cfg.Endpoint = server.URL
	underlying := server.Client()
	originalTimeout := underlying.Timeout
	client, err := New(cfg, WithHTTPClient(underlying), WithJSONCodec(codec))
	if err != nil {
		t.Fatal(err)
	}
	phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token<&=中文", OpenID: "openid"})
	if err != nil || phone != "13800000000" {
		t.Fatalf("phone=%q err=%v", phone, err)
	}
	if codec.marshals.Load() != 1 || codec.unmarshals.Load() != 1 || calls.Load() != 1 {
		t.Fatal("codec/request count mismatch")
	}
	if underlying.Timeout != originalTimeout || underlying.CheckRedirect != nil {
		t.Fatal("caller client mutated")
	}
}

func TestResponseAndFailureClassification(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct {
		name, body, contentType string
		status                  int
		want                    error
	}{
		{"success", `{"code":"OK","phoneNumber":"+8613800000000"}`, "application/json", 200, nil},
		{"unauthorized", `{"code":"UNAUTHORIZED"}`, "application/json", 401, ErrUnauthorized},
		{"upstream", `{"code":"UPSTREAM_ERROR","providerCode":5000}`, "application/json", 502, ErrUpstream},
		{"unavailable", `{"code":"UNAVAILABLE"}`, "application/json", 503, ErrUnavailable},
		{"input", `{"code":"INVALID_REQUEST"}`, "application/json", 400, ErrInvalidArgument},
		{"large input", `{"code":"PAYLOAD_TOO_LARGE"}`, "application/json", 413, ErrInvalidArgument},
		{"media", `{"code":"UNSUPPORTED_MEDIA_TYPE"}`, "application/json", 415, ErrInvalidArgument},
		{"missing phone", `{"code":"OK"}`, "application/json", 200, ErrInvalidResponse},
		{"only plus", `{"code":"OK","phoneNumber":"+"}`, "application/json", 200, ErrInvalidResponse},
		{"invalid phone", `{"code":"OK","phoneNumber":"abc"}`, "application/json", 200, ErrInvalidResponse},
		{"wrong status", `{"code":"OK","phoneNumber":"13800000000"}`, "application/json", 401, ErrInvalidResponse},
		{"wrong code type", `{"code":0,"phoneNumber":"13800000000"}`, "application/json", 200, ErrInvalidResponse},
		{"null", `null`, "application/json", 200, ErrInvalidResponse},
		{"empty", "", "application/json", 200, ErrInvalidResponse},
		{"html", "<html>private</html>", "text/html", 200, ErrInvalidResponse},
		{"trailing value", `{"code":"OK","phoneNumber":"13800000000"}{}`, "application/json", 200, ErrInvalidResponse},
		{"charset", `{}`, "application/json;charset=gbk", 200, ErrInvalidResponse},
		{"too large", strings.Repeat(" ", maxResponseBytes+1), "application/json", 200, ErrInvalidResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				r := jsonResponse(tc.status, tc.body)
				r.Header.Set("Content-Type", tc.contentType)
				return r, nil
			})}
			c, err := New(cfg, WithHTTPClient(hc))
			if err != nil {
				t.Fatal(err)
			}
			phone, err := c.GetPhoneNumber(t.Context(), Request{AccessToken: "token", OpenID: "openid"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if tc.want != nil && phone != "" {
				t.Fatal("phone returned on failure")
			}
			if tc.name == "upstream" {
				e, ok := errors.AsType[*Error](err)
				if !ok || e.ProviderCode == nil || *e.ProviderCode != 5000 {
					t.Fatal("provider code lost")
				}
			}
			if calls.Load() != 1 {
				t.Fatal("request retried")
			}
		})
	}
}

func TestInputAndCodecFailures(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return jsonResponse(200, `{"code":"OK","phoneNumber":"13800000000"}`), nil
	})}
	c, err := New(cfg, WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []Request{{}, {AccessToken: "t"}, {OpenID: "o"}, {AccessToken: strings.Repeat("x", 4097), OpenID: "o"}, {AccessToken: "t", OpenID: strings.Repeat("x", 1025)}, {AccessToken: "\xff", OpenID: "o"}} {
		if _, err := c.GetPhoneNumber(t.Context(), req); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bad input: %v", err)
		}
	}
	//nolint:staticcheck // SA1012：刻意验证文档约定的 nil context 拒绝行为。
	if _, err := c.GetPhoneNumber(nil, Request{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	var zero Client
	if _, err := zero.GetPhoneNumber(t.Context(), Request{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent")
	}
	for _, encode := range []bool{true, false} {
		codec := &countingCodec{encodeErr: encode, decodeErr: !encode}
		client, err := New(cfg, WithHTTPClient(hc), WithJSONCodec(codec))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
		want := ErrInvalidResponse
		if encode {
			want = ErrEncoding
		}
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
		if strings.Contains(fmt.Sprintf("%+v %#v", err, err), "private-") {
			t.Fatal("codec error leaked")
		}
	}
}

func TestContextAndNoRetry(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	c, err := New(cfg, WithHTTPClient(hc), WithTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.GetPhoneNumber(ctx, Request{AccessToken: "t", OpenID: "o"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("cancelled call sent")
	}
	synctest.Test(t, func(t *testing.T) {
		_, err := c.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	})
	if calls.Load() != 1 {
		t.Fatal("timeout retried")
	}
}

func TestRedirectAndConcurrentCalls(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		resp := jsonResponse(307, `{}`)
		resp.Header.Set("Location", "https://example.invalid/stolen")
		return resp, nil
	})}
	c, err := New(cfg, WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("redirect followed")
	}
	codec := &countingCodec{}
	hc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		want := signature(make([]byte, 32), "test", r.Header.Get("X-Univerify-Timestamp"), body)
		if r.Header.Get("X-Univerify-Signature") != want {
			t.Error("cross-request body/signature corruption")
		}
		return jsonResponse(200, `{"code":"OK","phoneNumber":"13800000000"}`), nil
	})}
	c, err = New(cfg, WithHTTPClient(hc), WithJSONCodec(codec))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			_, err := c.GetPhoneNumber(t.Context(), Request{AccessToken: fmt.Sprint(i), OpenID: "o"})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if codec.marshals.Load() != 32 || codec.unmarshals.Load() != 32 {
		t.Fatal("concurrent codec counts")
	}
}

func TestGoNodeProtocol(t *testing.T) {
	cfg := testConfig(t)
	// Node 监听本地 HTTP，Go TLS 入口原样转发正文；实际执行配套云函数。
	const bridge = `
const http = require('node:http');
const {main} = require('./examples/unicloud/univerify/index.js');
globalThis.uniCloud = {getPhoneNumber: async (args) => {
  if (args.appid !== 'test-app' || args.provider !== 'univerify') throw new Error('bad provider args');
  return {code: 0, phoneNumber: '13800000000'};
}};
const server = http.createServer(async (req, res) => {
  try {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const bytes = Buffer.concat(chunks);
    const encoded = req.url === '/base64';
    const result = await main({httpMethod: req.method, headers: req.headers,
      isBase64Encoded: encoded, body: bytes.toString(encoded ? 'base64' : 'utf8')});
    res.writeHead(result.statusCode, result.headers);
    res.end(result.body);
  } catch {
    res.writeHead(500); res.end();
  }
});
server.listen(0, '127.0.0.1', () => process.stdout.write('http://127.0.0.1:' + server.address().port + '\n'));
process.stdin.resume();
process.stdin.on('end', () => { server.close(); server.closeAllConnections(); });
`
	processCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
	cmd := exec.CommandContext(processCtx, "node", "-e", bridge)
	cmd.Env = append(os.Environ(), "UV_APP_ID=test-app", "UV_SIGNING_KEYS="+`{"test":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stdin.Close(); err != nil {
			t.Error(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Error("Node bridge did not exit cleanly:", err)
		}
		cancel()
	})
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal("read Node bridge address:", err)
	}
	target, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target) }}
	server := httptest.NewUnstartedServer(proxy)
	server.StartTLS()
	t.Cleanup(server.Close)
	for _, path := range []string{"/plain", "/base64"} {
		cfg.Endpoint = server.URL + path
		client, err := New(cfg, WithHTTPClient(server.Client()), WithJSONCodec(&countingCodec{}))
		if err != nil {
			t.Fatal(err)
		}
		phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token<&=中文", OpenID: "标识"})
		if err != nil || phone != "13800000000" {
			t.Fatalf("Go/Node %s: %q, %v", path, phone, err)
		}
	}
	cfg.Endpoint = server.URL
	cfg.KeyID = "unknown"
	client, err := New(cfg, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token", OpenID: "openid"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Go/Node must reject unknown key: %v", err)
	}
}
