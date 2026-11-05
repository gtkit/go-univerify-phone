package univerifygo

import (
	"encoding/base64"
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
		Key       string `json:"key"`
		KeyID     string `json:"key_id"`
		Timestamp string `json:"timestamp"`
		Body      string `json:"body"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			key, err := base64.StdEncoding.DecodeString(v.Key)
			if err != nil {
				t.Fatal(err)
			}
			if got := signature(key, v.KeyID, v.Timestamp, []byte(v.Body)); got != v.Signature {
				t.Fatalf("signature=%s", got)
			}
		})
	}
}
