package univerify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"unicode/utf8"
)

const (
	maxTokenBytes    = 4096
	maxOpenIDBytes   = 1024
	maxRequestBytes  = 32 << 10
	maxResponseBytes = 16 << 10
)

func signature(key []byte, req Request) string {
	// 按字段名排序后用原始值拼接，与官方 POST 示例一致。
	message := "access_token=" + req.AccessToken + "&openid=" + req.OpenID
	mac := hmac.New(sha256.New, key)
	// hash.Hash.Write 对所有输入均返回 nil error。
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func validCredential(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value)
}
