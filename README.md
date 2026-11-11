# go-univerify-phone

通过 uniCloud URL 化云函数，将 uni 一键登录的 `access_token`、`openid` 换成手机号。Go 包只负责取号；业务服务负责 Redis 限流、凭证去重、用户注册/登录和业务 token。

当前版本为 `v0.4.0`。module 为 `github.com/gtkit/go-univerify-phone`，实际包名为 `univerify`，要求 Go 1.27.3。调用方保持 import 路径，使用 `univerify` 访问本包 API，无需指定导入别名。

## 安装与初始化

安装指定版本：

```sh
go get github.com/gtkit/go-univerify-phone@v0.4.0
```

唯一直接依赖是 `github.com/gtkit/httpc v1.8.0`。默认 JSON 使用 Go 原生 `encoding/json`，没有 Redis、JWT 或数据库依赖。

Go 服务与云函数共享同一个原始密钥（非空 UTF-8 字符串），由服务端私有配置注入。不要把实际密钥放入 App、源码、示例或日志。

```go
import (
    "context"
    "time"

    "github.com/gtkit/go-univerify-phone"
)

// 在应用启动时构造并复用，不在每次请求里创建。
func newPhoneClient(endpoint, secret string) (*univerify.Client, error) {
    return univerify.New(univerify.Config{
        Endpoint: endpoint, // URL 化云函数的完整 HTTPS 地址
        Secret:   secret,   // 与云函数一致的原始密钥
    }, univerify.WithTimeout(5*time.Second))
}

// ctx、accessToken 和 openID 来自已校验并通过限流/去重的业务请求。
func getPhone(ctx context.Context, client *univerify.Client, accessToken, openID string) (string, error) {
    return client.GetPhoneNumber(ctx, univerify.Request{
        AccessToken: accessToken,
        OpenID:      openID,
    })
}
```

`Endpoint` 是云函数 URL 化后的完整 HTTPS 地址，由服务端配置，不接受 App 指定；禁止 userinfo、query 和 fragment。

## JSON 实例注入

已有 JSON 实例只需满足下列接口，即可使用 `WithJSONCodec(instance)` 注入：

```go
type JSONCodec interface {
    Marshal(v any) ([]byte, error)
    Unmarshal(data []byte, v any) error
}
```

例如以下适配器仍采用 Go 原生 JSON，可以替换为应用已有的实现：

```go
import "encoding/json"

type applicationJSON struct{}

func (applicationJSON) Marshal(v any) ([]byte, error) {
    return json.Marshal(v)
}
func (applicationJSON) Unmarshal(data []byte, v any) error {
    return json.Unmarshal(data, v)
}

// 构造时使用：univerify.WithJSONCodec(applicationJSON{})
```

实例必须支持并发、遵守 JSON 字段标签，不得修改响应输入或在返回后修改请求编码缓冲区。包复制一次编码结果后签名，并通过 `httpc.RawBody` 原样发送，不再由 httpc 编码。响应由同一实例解码，随后检查 HTTP 状态、协议 code 和手机号。nil/typed-nil 实例返回 `ErrInvalidArgument`。

## 配置和 API

| 项目 | 默认值与约束 |
| --- | --- |
| `Config.Endpoint` | 必填的完整 HTTPS 地址；禁止 userinfo、query 和 fragment；由服务端配置，不接受 App 指定 |
| `Config.Secret` | 必填，与云函数一致的非空 UTF-8 原始密钥；来自服务端私有配置 |
| `WithTimeout` | 默认 5 秒；必须为正；父 context 更早的取消或期限优先 |
| `WithHTTPClient` | 默认由 httpc 创建；可注入 `*http.Client`，其值被复制，Transport 共享；仍禁止重定向与 Cookie Jar，仍设置本包超时 |
| `WithJSONCodec` | 默认 encoding/json；可注入满足接口且并发安全的实例；不允许 nil |

Client 构造后可被多个 goroutine 复用。注入的 Transport/JSONCodec 也必须并发安全，不能在请求期间修改。本包不关闭调用方的 Transport，不暴露内部可变 client。

`New` 不发送网络请求、不建立 Redis 连接、不启动后台任务。`GetPhoneNumber` 要求非 nil context；返回手机号或错误，不自动重试。

## 完整调用链路

```text
App：uni.login(provider: univerify)
  → access_token + openid
  → HTTPS 请求 Go 业务登录接口
Go 业务：校验 → 限流/额度 → Redis 原子凭证去重
  → client.GetPhoneNumber
Go 包：httpc POST + HMAC-SHA256 + 时间戳
  → URL 化云函数验签/校验时间窗
  → uniCloud.getPhoneNumber
  → 手机号返回 Go
Go 业务：查询/注册用户 → 检查账号状态 → 提交事务 → 签发业务 token
  → App 按结果关闭授权界面
```

App 成功取得两个凭证只代表授权步骤完成，不代表业务登录成功。App 请求完成后使用其平台支持的方法关闭授权界面；取消、网络失败和不支持一键登录时使用业务已有处理方式。共享密钥永远不进入 App。

业务侧接入顺序：

1. 限制请求体并检查字段，从受信代理链获取 IP，结合业务已验证的设备/会话和全局额度限流。
2. 使用现有 Redis 原子 `SET ... NX EX ...` 占用凭证；不要先 GET 再 SET。键可采用业务前缀加 `SHA256(JSON([应用标识, access_token, openid]))`，不存原始凭证。
3. 只有占用成功者调用本包。重复请求由业务返回“正在处理或已使用”；Redis 故障时拒绝进入取号流程。
4. 取号成功后执行现有用户事务和账号检查，成功提交后才签发 token。手机号不能由 App 自报，也不要把 `openid` 直接当作业务账号 ID。
5. 取号超时或失败后保留去重标记直至 TTL，不自动释放再重试；客户端需要时重新获取凭证。

去重 TTL、限额和 Redis 库号由业务配置。签名的 60 秒时限不是运营商凭证有效期；TTL 应覆盖实际凭证可使用期和业务重试窗口。Redis 标记不是用户数据库事务，注册唯一性仍由现有数据库约束保证。本仓库没有业务服务源码，因此上述业务步骤是接入要求，并未在本包中实现。

## 签名、云函数与安全边界

请求正文只含 `access_token`、`openid`，签名放在请求头：

| 请求头 | 含义 |
| --- | --- |
| `X-UniVerify-Timestamp` | 十进制 Unix 秒时间戳 |
| `X-UniVerify-Sign` | HMAC-SHA256 签名的小写 hex |

签名原文是 `timestamp + "\n" + 原始请求正文`。云函数以恒定时间比较签名，并校验时间窗；双方应同步时钟。

响应为 HTTP 200 加 JSON：成功 `{"code":0,"phoneNumber":"..."}`；取号失败 `{"code":50201,"message":"一键登录取号失败","upstream":{...}}`。`GetPhoneNumber` 成功时直接返回手机号；`code != 0` 归类为 `ErrUpstream`，业务码进入 `Error.Code`，完整原始正文经 `Error.Body` 透传；响应无法解码、缺 code 或成功但缺手机号归类为 `ErrInvalidResponse`，同样携带原始正文。取号接口没有由本包承诺的幂等性；传输失败不代表运营商没有处理或扣费。

本包禁止自动重定向和应用层重试，限制请求正文 32 KiB、响应解压后 16 KiB，凭证上限分别为 4096/1024 字节。格式化 Client/Request 时脱敏，但显式 JSON 序列化 Request 必然包含请求凭证，业务不能记录这些正文。

本期不使用云端 Redis 或 nonce。**签名可防止无密钥者构造请求，时间戳只能缩短重放窗口；时间窗内已签名的请求仍可被直接重放给云函数。** Go 入口的 Redis 不能拦截此绕行请求。需要严格云端防重放时，应另行设计共享一次性记录。

更换密钥时先更新云端环境变量和 Go 配置并重建 Client，等待旧实例退出后即不再接受旧签名；泄露时立即撤销对应密钥，不关闭验签维持流量。

## 错误处理

| 判断 | 含义 |
| --- | --- |
| `errors.Is(err, ErrInvalidArgument)` | 配置、选项、凭证或云端请求参数无效 |
| `errors.Is(err, ErrEncoding)` | JSON 请求编码失败或返回非法 JSON/超大正文 |
| `errors.Is(err, ErrUnauthorized)` | 签名或时间戳不被云端接受 |
| `errors.Is(err, ErrUpstream)` | 云函数取号失败 |
| `errors.Is(err, ErrUnavailable)` | 传输、取消、超时或云函数配置不可用 |
| `errors.Is(err, ErrInvalidResponse)` | 状态与正文不一致、缺手机号、类型错误或响应超限 |

取消和超时同时支持 `errors.Is(err, context.Canceled)`、`errors.Is(err, context.DeadlineExceeded)`。`errors.AsType[*univerify.Error](err)` 可读取错误码 `Code`（云函数业务码的十进制字符串或本包内部错误标识）与错误响应的原始正文 `Body`（响应级错误时为响应字节，供调用方排障；成功路径不出现）。

`Error` 的字符串输出包含原始正文（`body=...` 后缀），上游原始文案会随之进入任何 `err.Error()`/`%v` 输出路径；调用方自行决定记录与脱敏策略。原始 transport/codec 错误的私有消息仍然不对外保留；取消和超时原因除外。错误类别不能用来推断“可以安全重试”。日志建议只记录 request ID、错误类别、状态及耗时，凭证、签名、密钥或手机号不应进入日志。

## 验证与发布状态

普通本地验证入口（不会调用真实运营商）：

```sh
go test -trimpath -vet=off -count=1 -timeout=2m ./...
go build -trimpath ./...
```

测试包括原生/注入 JSON、签名向量、TLS、取消、并发和响应边界。实际服务空间、App 真机及运营商尚未验证。

本次版本经单独授权验证，vet、lint（0 issues）、modern（无差异）和全量 race 均已通过，语句覆盖率为 97.9%。刻意验证 nil context 的测试有一处附带理由的单行 SA1012 豁免。业务侧 Redis 和用户系统由接入服务实现。

版本记录见 [CHANGELOG.md](CHANGELOG.md)。官方依据：[一键登录](https://doc.dcloud.net.cn/uniCloud/uni-login/dev.html)、[云函数 URL 化](https://doc.dcloud.net.cn/uniCloud/http)、[环境变量](https://doc.dcloud.net.cn/uniCloud/cf-env-variables.html)。

GitHub Actions 在 main 更新和 PR 时运行模块、格式、静态检查、Go 测试及安全扫描，覆盖率下限为 80%。Go 版本取自 go.mod，检查工具固定版本。发布时先确认该提交的 CI 成功，再推送与 version.go 一致的附注版本标签；发布工作流会读取 CHANGELOG 的对应版本段落创建 GitHub Release，无需配置个人访问令牌。
