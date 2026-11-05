# go-univerify-phone

通过配套的 uniCloud URL 化云函数，将 uni 一键登录的 `access_token`、`openid` 换成手机号。Go 包只负责取号；业务服务负责 Redis 限流、凭证去重、用户注册/登录和业务 token。

当前代码版本为 `v0.3.0`，尚未创建发布 tag。module 为 `github.com/gtkit/go-univerify-phone`，实际包名为 `univerifygo`，要求 Go 1.27.3。

## 安装与初始化

版本发布后，按实际发布版本安装：

```sh
go get github.com/gtkit/go-univerify-phone
```

唯一直接依赖是 `github.com/gtkit/httpc v1.8.0`。默认 JSON 使用 Go 原生 `encoding/json`，没有 Redis、JWT 或数据库依赖。

Go 服务通过私有环境变量注入一个安全随机生成的 32 字节密钥，值为标准 Base64。不要把实际密钥放入 App、源码、示例或日志；云端使用相同 KeyID/密钥。

```go
import (
    "context"
    "time"

    "github.com/gtkit/go-univerify-phone"
)

// 在应用启动时构造并复用，不在每次请求里创建。
func newPhoneClient() (*univerifygo.Client, error) {
    return univerifygo.New(univerifygo.Config{
        FunctionPath:  "/univerify-phone",
        KeyID:         "current",
        SigningKeyEnv: "UV_SIGNING_KEY",
    }, univerifygo.WithTimeout(5*time.Second))
}

// ctx、accessToken 和 openID 来自已校验并通过限流/去重的业务请求。
func getPhone(ctx context.Context, client *univerifygo.Client, accessToken, openID string) (string, error) {
    return client.GetPhoneNumber(ctx, univerifygo.Request{
        AccessToken: accessToken,
        OpenID:      openID,
    })
}
```

省略 `Endpoint` 时，默认使用 `https://env-00jy6yowysc0.dev-hz.cloudbasefunction.cn`，与必填的 `FunctionPath` 组合。上例实际请求 `/univerify-phone`；你可以将路径改为 `phone-login` 或 `/phone-login`。默认域名属于指定服务空间，其他服务空间请显式设置完整 `Endpoint`，例如 `https://example.invalid/your-function`。

非空 `Endpoint` 优先于 `FunctionPath`，保持已有完整地址用法；两者均由服务端配置，不接受 App 指定。`FunctionPath` 只接受纯路径，禁止绝对 URL、双斜杠开头、query、fragment、userinfo 和非法转义；完整 `Endpoint` 仍禁止 userinfo、query 和 fragment。

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

// 构造时使用：univerifygo.WithJSONCodec(applicationJSON{})
```

实例必须支持并发、遵守 JSON 字段标签，不得修改响应输入或在返回后修改请求编码缓冲区。包复制一次编码结果后签名，并通过 `httpc.RawBody` 原样发送，不再由 httpc 编码。响应由同一实例解码，随后检查 HTTP 状态、协议 code 和手机号。nil/typed-nil 实例返回 `ErrInvalidArgument`。

## 配置和 API

| 项目 | 默认值与约束 |
| --- | --- |
| `Config.Endpoint` | 可选的完整 HTTPS 地址；非空时优先并忽略 FunctionPath；留空则使用默认服务空间域名和 FunctionPath |
| `Config.FunctionPath` | Endpoint 留空时必填；支持带或不带前导斜杠；不预设函数路径 |
| `Config.KeyID` | 必填，云端配置的密钥编号，1–64 个 ASCII 字母、数字、下划线或连字符 |
| `Config.SigningKeyEnv` | 必填，保存 Base64 密钥的环境变量名；缺失、空值、非规范 Base64 或解码后非 32 字节均失败 |
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
  → 配套 URL 化云函数验签/校验时间窗
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

云端文件位于 [examples/unicloud/univerify](examples/unicloud/univerify/README.md)。必须部署配套实现；它与官方示例的签名串不同。

请求正文只含 `access_token`、`openid`。签名覆盖以下五项，以 LF 连接且无末尾换行：

```text
univerify-go/v1
POST
<key_id>
<Unix 毫秒时间戳>
<原始 JSON 字节的 SHA256 小写 hex>
```

使用共享密钥计算 HMAC-SHA256，小写 hex 放入 `X-Univerify-Signature`；另传 `X-Univerify-Key-Id`、`X-Univerify-Timestamp`。云函数以恒定时间比较签名，只接受 `-5000 <= 云端时间 - 请求时间 <= 60000` 毫秒。双方应同步时钟。

成功响应为 HTTP 200 与 `{"code":"OK","phoneNumber":"..."}`。其余情况不会返回成功手机号。取号接口没有由本包承诺的幂等性；传输失败不代表运营商没有处理或扣费。

本包禁止自动重定向和应用层重试，限制请求正文 32 KiB、响应解压后 16 KiB，凭证上限分别为 4096/1024 字节。格式化 Client/Request 时脱敏，但显式 JSON 序列化 Request 必然包含请求凭证，业务不能记录这些正文。

本期不使用云端 Redis 或 nonce。**签名可防止无密钥者构造请求，时间戳只能缩短重放窗口；时间窗内已签名的请求仍可被直接重放给云函数。** Go 入口的 Redis 不能拦截此绕行请求。需要严格云端防重放时，应另行设计共享一次性记录。

密钥轮换：云端先加入新 KeyID → Go 用新密钥构造新 Client → 旧请求排空并经过有效期后移除旧 KeyID。云端最多同时配置两把。泄露时立即撤销对应密钥，不关闭验签维持流量。

## 错误处理

| 判断 | 含义 |
| --- | --- |
| `errors.Is(err, ErrInvalidArgument)` | 配置、选项、凭证或云端请求参数无效 |
| `errors.Is(err, ErrEncoding)` | JSON 请求编码失败或返回非法 JSON/超大正文 |
| `errors.Is(err, ErrUnauthorized)` | 签名、KeyID 或时间戳不被云端接受 |
| `errors.Is(err, ErrUpstream)` | 云函数取号失败 |
| `errors.Is(err, ErrUnavailable)` | 传输、取消、超时或云函数配置不可用 |
| `errors.Is(err, ErrInvalidResponse)` | 状态与正文不一致、缺手机号、类型错误或响应超限 |

取消和超时同时支持 `errors.Is(err, context.Canceled)`、`errors.Is(err, context.DeadlineExceeded)`。`errors.AsType[*univerifygo.Error](err)` 可读取安全的 HTTP 状态、协议 code 和可选的数字 ProviderCode。

原始 transport/codec 错误可能包含敏感信息，因此不对外保留这些原始消息；只保留明确的取消/超时原因。错误类别不能用来推断“可以安全重试”。日志只记录 request ID、错误类别、状态及耗时，不记录凭证、签名、密钥或手机号。

## 验证与发布状态

普通本地验证入口（不会调用真实运营商）：

```sh
go test -trimpath -vet=off -count=1 -timeout=2m ./...
node --test examples/unicloud/univerify/index.test.js
go build -trimpath ./...
```

测试包括原生/注入 JSON、签名向量、TLS、取消、并发、响应边界和 Go → 本地 Node 云函数入口。Node 侧使用取号替身；实际服务空间、App 真机及运营商尚未验证。

默认域名增量经单独授权复跑，vet、lint（0 issues）、modern（无差异）和全量 race 均已通过，当前语句覆盖率为 97.9%。各次实际结果见实施计划。刻意验证 nil context 的测试有一处附带理由的单行 SA1012 豁免。未部署、未创建发布 tag，也未实现业务侧 Redis 或用户系统。

技术约束见 [DESIGN.md](DESIGN.md)，执行状态见 [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md)。官方依据：[一键登录](https://doc.dcloud.net.cn/uniCloud/uni-login/dev.html)、[云函数 URL 化](https://doc.dcloud.net.cn/uniCloud/http)、[环境变量](https://doc.dcloud.net.cn/uniCloud/cf-env-variables.html)。
