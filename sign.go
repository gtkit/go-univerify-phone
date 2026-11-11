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

// signature 按 timestamp + "\n" + rawBody 计算 HMAC-SHA256，返回小写 hex。
func signature(key []byte, timestamp string, rawBody []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

func validCredential(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value)
}
