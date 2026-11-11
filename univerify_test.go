package univerify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gtkit/httpc"
)

const successEnvelope = `{"code":0,"phoneNumber":"13800000000"}`

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
				return jsonResponse(200, successEnvelope), nil
			})
			client, err := New(cfg, WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatal("New sent a request")
			}
			if phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token", OpenID: "openid"}); err != nil || phone != "13800000000" {
				t.Fatalf("phone=%q err=%v", phone, err)
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

func (c *countingCodec) Unmarshal(b []byte, v any) error {
	c.unmarshals.Add(1)
	if c.decodeErr {
		return errors.New("private-phone-and-key")
	}
	return json.Unmarshal(b, v)
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
	for _, reader := range []io.Reader{strings.NewReader(successEnvelope), brokenReader{}} {
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
		var fields map[string]string
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Error(err)
			return
		}
		if len(fields) != 2 || fields["access_token"] != "token<&=中文\n\"\\" || fields["openid"] != "openid+/" {
			t.Errorf("request fields were changed or added: %v", fields)
			return
		}
		timestamp := r.Header.Get("X-UniVerify-Timestamp")
		if sign := r.Header.Get("X-UniVerify-Sign"); sign == "" || sign != signature([]byte(cfg.Secret), timestamp, body) {
			t.Error("header signature mismatch")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if _, err := io.WriteString(w, successEnvelope); err != nil {
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
	phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token<&=中文\n\"\\", OpenID: "openid+/"})
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

func TestResponseEnvelopeAndErrorPassthrough(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct {
		name, body, contentType, phone, code string
		status                               int
		wantKind                             error
		httpPassthrough                      bool
	}{
		{"full success", " \n" + successEnvelope + "\n", "application/json", "13800000000", "", 200, nil, false},
		{"provider failure", `{"code":50201,"message":"一键登录取号失败","upstream":{"stage":"exception","code":5000,"message":"获取手机号失败，请稍后重试。","hasPhoneNumber":false,"fields":["errMsg"]}}`, "application/json", "", "50201", 200, ErrUpstream, false},
		{"unknown business code", `{"code":41777,"message":"x"}`, "application/json", "", "41777", 200, ErrUpstream, false},
		{"success without phone", `{"code":0}`, "application/json", "", "", 200, ErrInvalidResponse, false},
		{"success with empty phone", `{"code":0,"phoneNumber":""}`, "application/json", "", "", 200, ErrInvalidResponse, false},
		{"missing code", `{"message":"x"}`, "application/json", "", "", 200, ErrInvalidResponse, false},
		{"string code", `{"code":"CUSTOM","other":true}`, "application/json", "", "", 200, ErrInvalidResponse, false},
		{"invalid JSON", "{", "application/json", "", "", 200, ErrInvalidResponse, false},
		{"binary", "\xff\x00\x01", "application/octet-stream", "", "", 200, ErrInvalidResponse, false},
		{"empty", "", "", "", "", 204, ErrInvalidResponse, false},
		{"unauthorized", `{"error":"original unauthorized"}`, "application/json", "", "", 401, nil, true},
		{"redirect", "original redirect", "text/plain", "", "", 307, nil, true},
		{"server failure", "<html>original upstream failure</html>", "text/html", "", "", 500, nil, true},
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
			phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "token", OpenID: "openid"})
			if phone != tc.phone {
				t.Fatalf("phone=%q, want %q", phone, tc.phone)
			}
			switch {
			case tc.httpPassthrough:
				httpErr, ok := errors.AsType[*httpc.HTTPError](err)
				if !ok || httpErr.StatusCode != tc.status || string(httpErr.Body) != tc.body || httpErr.Header.Get("X-Provider") != "original-header" {
					t.Fatalf("HTTP error not preserved: %v", err)
				}
			case tc.wantKind == nil:
				if err != nil {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, tc.wantKind) {
					t.Fatalf("want %v, got %v", tc.wantKind, err)
				}
				e, ok := errors.AsType[*Error](err)
				if !ok {
					t.Fatalf("want *Error, got %T", err)
				}
				if !bytes.Equal(e.Body, []byte(tc.body)) {
					t.Fatalf("body=%q, want %q", e.Body, tc.body)
				}
				if tc.code != "" {
					if e.Code != tc.code {
						t.Fatalf("code=%q: %v", e.Code, err)
					}
					wantMessage := fmt.Sprintf("%s code=%s", tc.wantKind, tc.code)
					if tc.body != "" {
						wantMessage += " body=" + tc.body
					}
					if err.Error() != wantMessage {
						t.Fatalf("message=%q, want %q", err.Error(), wantMessage)
					}
				}
				if tc.wantKind != nil && tc.body != "" && !strings.Contains(err.Error(), tc.body) {
					t.Fatalf("raw body missing from error string: %v", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("request retried")
			}
		})
	}
	client, err := New(cfg, WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, successEnvelope), nil
	})}), WithJSONCodec(&countingCodec{decodeErr: true}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
	if !errors.Is(err, ErrInvalidResponse) || strings.Contains(fmt.Sprint(err), "private-phone-and-key") {
		t.Fatalf("decode failure: %v", err)
	}
	if e, ok := errors.AsType[*Error](err); !ok || !bytes.Equal(e.Body, []byte(successEnvelope)) {
		t.Fatalf("decode failure body lost: %q", e.Body)
	}
	for _, status := range []int{200, 500} {
		client, err := New(cfg, WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(status, strings.Repeat("x", maxResponseBytes+1)), nil
		})}))
		if err != nil {
			t.Fatal(err)
		}
		phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
		if phone != "" || !errors.Is(err, httpc.ErrResponseTooLarge) {
			t.Fatalf("response limit lost: %v", err)
		}
	}
}

func TestInputAndCodecFailures(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return jsonResponse(200, successEnvelope), nil
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
	phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
	if err != errTestCodec || phone != "" {
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
	phone, err := client.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
	if phone != "" || !errors.Is(err, original) {
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
	phone, err := c.GetPhoneNumber(t.Context(), Request{AccessToken: "t", OpenID: "o"})
	httpErr, ok := errors.AsType[*httpc.HTTPError](err)
	if !ok || httpErr.StatusCode != 307 || phone != "" {
		t.Fatalf("redirect response lost: %q %v", phone, err)
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
		if len(fields) != 2 {
			return jsonResponse(400, "{}"), nil
		}
		if r.Header.Get("X-UniVerify-Sign") != signature([]byte(cfg.Secret), r.Header.Get("X-UniVerify-Timestamp"), body) {
			t.Error("cross-request body/signature corruption")
		}
		return jsonResponse(200, successEnvelope), nil
	})}
	c, err = New(cfg, WithHTTPClient(hc), WithJSONCodec(codec))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			phone, err := c.GetPhoneNumber(t.Context(), Request{AccessToken: fmt.Sprint(i), OpenID: "o"})
			if err != nil || phone != "13800000000" {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if codec.marshals.Load() != 32 || codec.unmarshals.Load() != 32 {
		t.Fatal("concurrent codec counts")
	}
}
