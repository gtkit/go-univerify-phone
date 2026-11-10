package univerify

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
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

	"github.com/gtkit/httpc"
)

func testConfig(t testing.TB) Config {
	t.Helper()
	return Config{Endpoint: "https://example.invalid/verify", Secret: "test-secret"}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestEndpointAddress(t *testing.T) {
	cfg := testConfig(t)
	for _, endpoint := range []string{"https://example.invalid/verify", "https://example.invalid/auth/phone%20login"} {
		t.Run(endpoint, func(t *testing.T) {
			cfg.Endpoint = endpoint
			calls := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != endpoint {
					t.Errorf("URL=%q, want %q", r.URL.String(), endpoint)
				}
				return jsonResponse(200, `{"code":0,"phoneNumber":"13800000000"}`), nil
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
		{name: "empty endpoint", edit: func(c *Config) { c.Endpoint = "" }},
		{name: "empty secret", edit: func(c *Config) { c.Secret = "" }},
		{name: "invalid secret", edit: func(c *Config) { c.Secret = "\xff" }},
		{name: "zero timeout", opts: []Option{WithTimeout(0)}},
		{name: "nil client", opts: []Option{WithHTTPClient(nil)}},
		{name: "nil option", opts: []Option{nil}},
		{name: "nil codec", opts: []Option{WithJSONCodec(nil)}},
		{name: "typed nil codec", opts: []Option{WithJSONCodec((*countingCodec)(nil))}},
	}
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
	for _, secret := range []string{"s", "not-base64", "密钥🙂", strings.Repeat("x", 100)} {
		cfg.Secret = secret
		if _, err := New(cfg); err != nil {
			t.Fatalf("valid raw secret rejected: %v", err)
		}
	}
}

var errTestCodec = errors.New("private-token-and-key")
var errTestRead = errors.New("private-response")

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
		return nil, errTestCodec
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

func (brokenReader) Read([]byte) (int, error) { return 0, errTestRead }

func TestResponseClosedAndEncodingBounds(t *testing.T) {
	cfg := testConfig(t)
	for _, reader := range []io.Reader{strings.NewReader(`{"code":0,"phoneNumber":"13800000000"}`), brokenReader{}} {
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
		if _, broken := reader.(brokenReader); broken && !errors.Is(err, errTestRead) {
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
	codec := &countingCodec{decodeErr: true}
	const want = " \n{\"code\":0,\"message\":\"原样\",\"phoneNumber\":\"13800000000\",\"extra\":{\"value\":1}}\n"
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
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-univerify-") {
				t.Error("custom signature header sent")
			}
		}
		if !strings.Contains(string(body), "\n") {
			t.Error("custom codec body was re-encoded")
		}
		var fields map[string]string
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Error(err)
			return
		}
		if len(fields) != 3 || fields["access_token"] != "token<&=中文\n\"\\" || fields["openid"] != "openid+/" {
			t.Errorf("request fields were changed or added")
			return
		}
		mac := hmac.New(sha256.New, []byte(cfg.Secret))
		if _, err := mac.Write([]byte("access_token=" + fields["access_token"] + "&openid=" + fields["openid"])); err != nil {
			t.Error(err)
			return
		}
		if fields["sign"] != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("official signature mismatch")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if _, err := io.WriteString(w, want); err != nil {
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
	data, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token<&=中文\n\"\\", OpenID: "openid+/"})
	if err != nil || string(data) != want {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if codec.marshals.Load() != 1 || codec.unmarshals.Load() != 0 || calls.Load() != 1 {
		t.Fatal("codec/request count mismatch")
	}
	if underlying.Timeout != originalTimeout || underlying.CheckRedirect != nil {
		t.Fatal("caller client mutated")
	}
}

func TestResponseAndErrorPassthrough(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct {
		name, body, contentType string
		status                  int
	}{
		{"full success", " \n{\"code\":0,\"phoneNumber\":\"13800000000\",\"message\":\"原文\",\"extra\":{\"a\":1}}\n", "application/json", 200},
		{"provider failure", "{\"code\":5000,\"message\":\"provider original error\",\"detail\":[1,2]}", "application/json", 200},
		{"string code", "{\"code\":\"CUSTOM\",\"other\":true}", "application/json", 200},
		{"invalid JSON", "{", "application/json", 200},
		{"binary", "\xff\x00\x01", "application/octet-stream", 200},
		{"empty", "", "", 204},
		{"unauthorized", "{\"error\":\"original unauthorized\"}", "application/json", 401},
		{"redirect", "original redirect", "text/plain", 307},
		{"server failure", "<html>original upstream failure</html>", "text/html", 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client, err := New(cfg, WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				resp := jsonResponse(tc.status, tc.body)
				resp.Header.Set("Content-Type", tc.contentType)
				resp.Header.Set("X-Provider", "original-header")
				return resp, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			data, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token", OpenID: "openid"})
			if string(data) != tc.body {
				t.Fatalf("body changed: %q", data)
			}
			if tc.status < 200 || tc.status >= 300 {
				httpErr, ok := errors.AsType[*httpc.HTTPError](err)
				if !ok || httpErr.StatusCode != tc.status || string(httpErr.Body) != tc.body || httpErr.Header.Get("X-Provider") != "original-header" {
					t.Fatalf("HTTP error not preserved: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("request retried")
			}
		})
	}
	for _, status := range []int{200, 500} {
		client, err := New(cfg, WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(status, strings.Repeat("x", maxResponseBytes+1)), nil
		})}))
		if err != nil {
			t.Fatal(err)
		}
		data, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
		if data != nil || !errors.Is(err, httpc.ErrResponseTooLarge) {
			t.Fatalf("response limit lost: %v", err)
		}
	}
}

func TestInputAndCodecFailures(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return jsonResponse(200, `{"code":0,"phoneNumber":"13800000000"}`), nil
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
	codec := &countingCodec{encodeErr: true}
	client, err := New(cfg, WithHTTPClient(hc), WithJSONCodec(codec))
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
	if err != errTestCodec || data != nil {
		t.Fatalf("codec error changed: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("encoding failure sent a request")
	}
}

func TestTransportErrorPassthrough(t *testing.T) {
	original := errors.New("original transport error")
	client, err := New(testConfig(t), WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, original
	})}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
	if data != nil || !errors.Is(err, original) {
		t.Fatalf("transport cause lost: %v", err)
	}
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("transport type lost: %v", err)
	}
	if _, ok := errors.AsType[*Error](err); ok {
		t.Fatal("transport error replaced by SDK error")
	}
	if !strings.Contains(err.Error(), original.Error()) {
		t.Fatal("transport message changed")
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
		if !errors.Is(err, context.DeadlineExceeded) {
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
	data, err := c.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
	httpErr, ok := errors.AsType[*httpc.HTTPError](err)
	if !ok || httpErr.StatusCode != 307 || string(data) != "{}" {
		t.Fatalf("redirect response lost: %v", err)
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
		var fields map[string]string
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, err
		}
		want := signature([]byte(cfg.Secret), Request{AccessToken: fields["access_token"], OpenID: fields["openid"]})
		if fields["sign"] != want {
			t.Error("cross-request body/signature corruption")
		}
		return jsonResponse(200, `{"code":0,"phoneNumber":"13800000000"}`), nil
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
	if codec.marshals.Load() != 32 || codec.unmarshals.Load() != 0 {
		t.Fatal("concurrent codec counts")
	}
}

func TestGoNodeProtocol(t *testing.T) {
	cfg := testConfig(t)
	const bridge = `
const http = require('node:http');
const {main} = require('./examples/unicloud/univerify/index.js');
globalThis.uniCloud = {getPhoneNumber: async (args) => {
  if (args.appid !== 'test-app' || args.provider !== 'univerify') throw new Error('bad provider args');
  return {code: 0, phoneNumber: '13800000000'};
}};
const server = http.createServer(async (req, res) => {
  res.setHeader('Content-Type', 'application/json');
  try {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const bytes = Buffer.concat(chunks);
    const encoded = req.url === '/base64';
    const result = await main({httpMethod: req.method, headers: req.headers,
      isBase64Encoded: encoded, body: bytes.toString(encoded ? 'base64' : 'utf8')});
    res.writeHead(200); res.end(JSON.stringify(result));
  } catch {
    res.writeHead(500); res.end('{}');
  }
});
server.listen(0, '127.0.0.1', () => process.stdout.write('http://127.0.0.1:' + server.address().port + '\n'));
process.stdin.resume();
process.stdin.on('end', () => { server.close(); server.closeAllConnections(); });
`
	processCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
	cmd := exec.CommandContext(processCtx, "node", "-e", bridge)
	cmd.Env = append(os.Environ(), "UV_APP_ID=test-app", "UV_SECRET="+cfg.Secret)
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
		data, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token<&=中文", OpenID: "标识"})
		if err != nil || string(data) != "{\"code\":0,\"phoneNumber\":\"13800000000\"}" {
			t.Fatalf("Go/Node %s: %q, %v", path, data, err)
		}
	}
	cfg.Endpoint = server.URL
	cfg.Secret = "different-secret"
	client, err := New(cfg, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token", OpenID: "openid"})
	httpErr, ok := errors.AsType[*httpc.HTTPError](err)
	if !ok || httpErr.StatusCode != 500 || string(data) != "{}" {
		t.Fatalf("Go/Node error response changed: %q %v", data, err)
	}
}

func TestCloudFunctionExample(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "node", "--test", "examples/unicloud/univerify/index.test.js")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cloud function tests: %v\n%s", err, output)
	}
}
