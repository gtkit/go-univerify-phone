package univerify_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gtkit/go-univerify-phone"
)

// 应用可用已有实例实现相同接口；本示例继续使用原生 JSON。
type exampleJSON struct{}

func (exampleJSON) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (exampleJSON) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func ExampleNew() {
	_, err := univerify.New(univerify.Config{})
	fmt.Println(errors.Is(err, univerify.ErrInvalidArgument))
	// Output: true
}

func ExampleClient_GetPhoneNumber() {
	// 这里仅演示本地 TLS 响应，不会请求真实 uniCloud 或运营商。
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"code":0,"phoneNumber":"13800000000"}`); err != nil {
			fmt.Println("response failed")
		}
	}))
	defer server.Close()
	client, err := univerify.New(univerify.Config{
		Endpoint: server.URL, Secret: "synthetic-example-secret",
	}, univerify.WithHTTPClient(server.Client()), univerify.WithTimeout(5*time.Second), univerify.WithJSONCodec(exampleJSON{}))
	if err != nil {
		fmt.Println(err)
		return
	}
	data, err := client.GetPhoneNumber(context.Background(), univerify.Request{AccessToken: "example-token", OpenID: "example-openid"})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(data))
	// Output: {"code":0,"phoneNumber":"13800000000"}
}
