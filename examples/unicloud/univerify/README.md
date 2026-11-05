# 配套 uniCloud 云函数

本函数只处理 Go 服务发来的签名取号请求。App 先把 `access_token`、`openid` 提交给 Go；不要把云端共享密钥放入 App。

## 部署步骤

1. 在自己的 uniCloud 服务空间创建普通云函数，将本目录的 `index.js` 和 `package.json` 放入其函数目录。测试文件、README 和 `.env.example` 不需要上传。
2. 保留 package.json 的 `uni-cloud-verify` 扩展声明，并确认账号已经开通一键登录、应用配置正确。函数使用 Node 内置 `node:crypto`，不需要 npm 依赖；选择支持该模块的云端 Node 运行时。
3. 在控制台配置下表环境变量，使用私有配置注入实际值，不上传包含真实密钥的 `.env`。
4. 将云函数启用 URL 化，取得 HTTPS 地址并配置到 Go 的 `Config.Endpoint`。本地测试中的地址与凭证都是合成值，不能直接用于云端。
5. 在一键登录服务中配置服务空间白名单。白名单与函数验签作用不同，不能互相代替。
6. 在获得真实测试许可后，从 App 完成一次授权，经 Go 业务限流/去重调用，核对手机号和业务登录结果。本仓库尚未执行部署或真实取号。

## 环境变量

| 变量 | 用途、默认与约束 |
| --- | --- |
| `UV_APP_ID` | 一键登录所属 DCloud AppID；必填，无默认；不接受空值或前后空白；不能从 HTTP 请求覆盖 |
| `UV_SIGNING_KEYS` | JSON 对象，KeyID 对应标准 Base64 密钥；必填，无默认；1–2 个条目；KeyID 为 1–64 个 ASCII 字母/数字/下划线/连字符；每把密钥解码后恰好 32 字节 |

配置形状示例（占位符不能直接用于取号）：

```json
{"current":"<base64-of-32-random-bytes>"}
```

Go 的 `Config.KeyID` 选择同名条目，`Config.SigningKeyEnv` 指向 Go 进程内保存相同 Base64 密钥的变量。不要混用不同 App/环境的密钥。

## 请求与响应

只接受 POST + application/json。处理纯文本或 Base64 编码的 URL 化事件，不允许从 `callFunction` 直接传两个未签名字段绕过校验。请求头忽略名称大小写，多值或重复安全头拒绝。

原始正文大小不超过 32 KiB，access_token/openid 分别不超过 4096/1024 UTF-8 字节。正文只包含这两个字符串。签名协议详见根 README；函数在调用官方 getPhoneNumber 前校验格式、配置、HMAC-SHA256 与 `[-5000,60000]` 毫秒时间差。

| HTTP 状态 | code | 含义 |
| --- | --- | --- |
| 200 | OK | phoneNumber 为校验后的手机号 |
| 400 | INVALID_REQUEST | 正文、Base64、UTF-8 或凭证字段非法 |
| 401 | UNAUTHORIZED | 安全头、密钥编号、签名或时间戳不合法 |
| 405 | METHOD_NOT_ALLOWED | 非 POST |
| 413 | PAYLOAD_TOO_LARGE | 正文超限 |
| 415 | UNSUPPORTED_MEDIA_TYPE | 非 JSON 或不支持的字符集 |
| 502 | UPSTREAM_ERROR | 官方取号异常或返回值无效，可带数字 providerCode |
| 503 | UNAVAILABLE | 服务端环境变量缺失或无效 |

函数返回 uniCloud 集成响应，所有响应包含 `Cache-Control: no-store`。错误不含上游原始消息、凭证或手机号；本函数不打印业务日志。Go 应仅在 HTTP 200 和 code=OK 同时成立后使用手机号。

函数不自动重试，不保存手机号、不接入 Redis。**同一合法签名在有效时间窗内仍可再次调用取号接口**；这项剩余风险已明确保留。Go 的凭证去重仅保护经过 Go 的请求。

## 本地验证

从仓库根执行 `node --test examples/unicloud/univerify/index.test.js`，使用 Node 自带测试模块与合成取号替身。根目录 Go 测试还会启动一个临时本地 Node 入口，以 TLS 请求完成两端联通。它们不证明真实运营商或指定服务空间可用。

实际服务空间的云函数管理页用于核对 URL 化、运行时、扩展与环境变量；其登录态页面内容没有被当作已验证的配置依据。

参考：[官方取号说明](https://doc.dcloud.net.cn/uniCloud/uni-login/dev.html)、[URL 化事件与集成响应](https://doc.dcloud.net.cn/uniCloud/http)、[云函数环境变量](https://doc.dcloud.net.cn/uniCloud/cf-env-variables.html)。
