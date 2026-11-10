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
		Name        string `json:"name"`
		Secret      string `json:"secret"`
		AccessToken string `json:"access_token"`
		OpenID      string `json:"openid"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			mac := hmac.New(sha256.New, []byte(v.Secret))
			if _, err := mac.Write([]byte(v.Message)); err != nil {
				t.Fatal(err)
			}
			want := hex.EncodeToString(mac.Sum(nil))
			got := signature([]byte(v.Secret), Request{AccessToken: v.AccessToken, OpenID: v.OpenID})
			if got != want {
				t.Fatalf("signature=%s, want %s", got, want)
			}
		})
	}
}
