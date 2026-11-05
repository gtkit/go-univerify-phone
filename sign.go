package univerify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

const (
	maxTokenBytes    = 4096
	maxOpenIDBytes   = 1024
	maxRequestBytes  = 32 << 10
	maxResponseBytes = 16 << 10
)

func signature(key []byte, keyID, timestamp string, body []byte) string {
	digest := sha256.Sum256(body)
	message := "univerify-go/v1\nPOST\n" + keyID + "\n" + timestamp + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, key)
	// hash.Hash.Write 对所有输入均返回 nil error。
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func decodeKey(value string) ([]byte, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != value {
		return nil, failure(ErrInvalidArgument, "INVALID_SIGNING_KEY")
	}
	return key, nil
}

func validKeyID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func validCredential(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value)
}

func validPhone(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	digits := strings.TrimPrefix(value, "+")
	if digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
