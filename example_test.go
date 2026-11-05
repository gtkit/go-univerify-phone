package univerifygo_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/gtkit/go-univerify-phone"
)

// 应用可用已有实例实现相同接口；本示例继续使用原生 JSON。
type exampleJSON struct{}

func (exampleJSON) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (exampleJSON) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func ExampleNew() {
	_, err := univerifygo.New(univerifygo.Config{})
	fmt.Println(errors.Is(err, univerifygo.ErrInvalidArgument))
	// Output: true
}

func ExampleClient_GetPhoneNumber() {
	// 仅供本地示例的合成密钥，不是生产配置。
	const envName = "UNIVERIFY_EXAMPLE_KEY"
	previous, existed := os.LookupEnv(envName)
	if err := os.Setenv(envName, base64.StdEncoding.EncodeToString(make([]byte, 32))); err != nil {
		fmt.Println(err)
		return
	}
	defer func() {
		var err error
		if existed {
			err = os.Setenv(envName, previous)
		} else {
			err = os.Unsetenv(envName)
		}
		if err != nil {
			fmt.Println(err)
		}
	}()
	// 这里仅演示本地 TLS 响应，不会请求真实 uniCloud 或运营商。
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"code":"OK","phoneNumber":"13800000000"}`); err != nil {
			fmt.Println("response failed")
		}
	}))
	defer server.Close()
	client, err := univerifygo.New(univerifygo.Config{
		Endpoint: server.URL, KeyID: "example", SigningKeyEnv: envName,
	}, univerifygo.WithHTTPClient(server.Client()), univerifygo.WithTimeout(5*time.Second), univerifygo.WithJSONCodec(exampleJSON{}))
	if err != nil {
		fmt.Println(err)
		return
	}
	phone, err := client.GetPhoneNumber(context.Background(), univerifygo.Request{AccessToken: "example-token", OpenID: "example-openid"})
	if err != nil {
		fmt.Println(err)
		return
	}
	// 业务侧在此用 phone 完成用户登录/注册；生产日志不要打印手机号。
	fmt.Println(phone != "")
	// Output: true
}
