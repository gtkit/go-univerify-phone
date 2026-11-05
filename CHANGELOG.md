# Changelog

## [Unreleased]

## [v0.4.0] - 2026-11-05

### Added

- 新增基于 httpc v1.8.0 的 Go 一键登录取号客户端与配套 uniCloud 云函数。
- 新增默认服务空间域名及 FunctionPath 配置，保留完整 Endpoint 优先覆盖。
- 默认使用 Go encoding/json，支持 JSONCodec 实例注入。
- 新增配置校验、分类错误、可执行示例和 Go/Node 本地协议测试。

### Changed

- Go 包名统一为 `univerify`，同步测试、示例、文档和错误/脱敏输出前缀。调用方保持 import 路径，改用 `univerify` 限定符；目录、module、公开 API 结构及签名协议不变。

### Security

- 使用 HMAC-SHA256 和时间戳校验，限制请求/响应大小，禁用重定向与应用层重试。
- 共享密钥使用服务端环境变量配置，错误与格式化输出脱敏。
- Redis 限流和凭证去重由业务侧负责；未实现云端时间窗内的严格防重放。

本地验证：普通测试、构建、vet、lint、modern 差异检查与全量 race 通过，语句覆盖率 97.9%。覆盖外部 Example 和本地 Go/Node 协议测试；未验证真实 App/运营商取号。
