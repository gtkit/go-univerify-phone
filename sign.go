package univerify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxTokenBytes    = 4096
	maxOpenIDBytes   = 1024
	maxRequestBytes  = 32 << 10
	maxResponseBytes = 16 << 10
)

func signature(key []byte, req Request) string {
	// 👉 在这里维护【参与签名的字段列表】，顺序无关，后面会排序
	kv := map[string]string{
		"access_token": req.AccessToken,
		"openid":       req.OpenID,
		//"app_id":       "__UNI__5B021A7",
		//"provider":     "univerify",
	}

	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte('&')
		}
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(kv[k])
	}
	message := buf.String()

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func validCredential(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value)
}
