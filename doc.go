// Package univerify 通过带 HMAC-SHA256 签名的 HTTPS 请求调用配套 uniCloud 云函数，
// 将 uni 一键登录凭证换取为手机号。
//
// Client 可并发复用，默认使用 encoding/json，支持注入 JSONCodec。
// 本包不重试取号、不跟随重定向、不管理 Redis 或业务登录。
// 调用方应先完成限流和凭证去重，随后按自身规则注册或登录并签发业务 token。
// 时间戳限制请求有效期；没有云端一次性存储时不能阻止时间窗内的直接重放。
package univerify
