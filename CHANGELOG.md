# Changelog

## [v0.5.0] - 2026-11-10

### Changed

- 签名协议对齐 Apifox 参照：HMAC-SHA256 覆盖 `timestamp + "\n" + 原始请求正文`，经 `X-UniVerify-Timestamp` / `X-UniVerify-Sign` 请求头传递；请求正文不再携带 sign 字段，配置简化为 `Config{Endpoint, Secret}`。
- `GetPhoneNumber` 返回 `(string, error)`：解析云函数响应，成功（`code == 0`）直接返回顶层 `phoneNumber`；`code != 0` 归类 `ErrUpstream`（业务码进入 `Error.Code`），响应无法解码、缺 code 或成功但缺手机号归类 `ErrInvalidResponse`。
- 响应级错误经 `Error.Body` 字段透传完整原始响应正文，供调用方排障；`Error` 字符串输出包含类别、错误码与原始正文，是否记录及脱敏由调用方决定。
- 签名向量、Go 协议测试与 README 同步为 header 签名与新响应格式；取号失败统一返回 `{"code":50201,"message":"一键登录取号失败","upstream":{...}}`。

## [v0.4.0] - 2026-11-05

### Added

- 新增基于 httpc v1.8.0 的 Go 一键登录取号客户端。
- 新增默认服务空间域名及 FunctionPath 配置，保留完整 Endpoint 优先覆盖。
- 默认使用 Go encoding/json，支持 JSONCodec 实例注入。
- 新增配置校验、分类错误、可执行示例和 Go 本地协议测试。
- 新增固定工具版本的 GitHub Actions CI，以及仅在同提交 CI 成功后创建 Release 的版本标签工作流。

### Changed

- Go 包名统一为 `univerify`，同步测试、示例、文档和错误/脱敏输出前缀。调用方保持 import 路径，改用 `univerify` 限定符；目录、module、公开 API 结构及签名协议不变。

### Security

- 使用 HMAC-SHA256 和时间戳校验，限制请求/响应大小，禁用重定向与应用层重试。
- 共享密钥使用服务端环境变量配置，错误与格式化输出脱敏。
- Redis 限流和凭证去重由业务侧负责；未实现云端时间窗内的严格防重放。

本地验证：普通测试、构建、vet、lint、modern 差异检查与全量 race 通过，语句覆盖率 97.9%。覆盖外部 Example 和本地 Go 协议测试；未验证真实 App/运营商取号。
