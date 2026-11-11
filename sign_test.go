package univerify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestSignatureVectors(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/signatures.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name      string `json:"name"`
		Secret    string `json:"secret"`
		Timestamp string `json:"timestamp"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			// 协议约定：签名原文是 timestamp + "\n" + 原始正文，向量按同一拼接计算期望值。
			message := v.Timestamp + "\n" + v.Body
			mac := hmac.New(sha256.New, []byte(v.Secret))
			if _, err := mac.Write([]byte(message)); err != nil {
				t.Fatal(err)
			}
			want := hex.EncodeToString(mac.Sum(nil))
			got := signature([]byte(v.Secret), v.Timestamp, []byte(v.Body))
			if got != want {
				t.Fatalf("signature=%s, want %s", got, want)
			}
		})
	}
}
