package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/gtkit/go-univerify-phone"
)

func main() {
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		for _, line := range []string{
			"用法：设置以下环境变量后运行 go run ./examples",
			"  UNIVERIFY_ENDPOINT      云函数 HTTPS 地址",
			"  UNIVERIFY_SECRET        与云函数一致的原始密钥",
			"  UNIVERIFY_ACCESS_TOKEN  App 授权返回的 access_token",
			"  UNIVERIFY_OPENID        App 授权返回的 openid",
		} {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return
			}
		}
	}
	flag.Parse()

	client, err := univerify.New(univerify.Config{
		Endpoint: os.Getenv("UNIVERIFY_ENDPOINT"),
		Secret:   os.Getenv("UNIVERIFY_SECRET"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败：", err)
		os.Exit(1)
	}

	phone, err := client.GetPhoneNumber(context.Background(), univerify.Request{
		AccessToken: os.Getenv("UNIVERIFY_ACCESS_TOKEN"),
		OpenID:      os.Getenv("UNIVERIFY_OPENID"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(phone)
}
