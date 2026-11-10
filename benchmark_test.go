package univerify

import "testing"

func BenchmarkSign(b *testing.B) {
	key := make([]byte, 32)
	req := Request{AccessToken: "test-token", OpenID: "test-openid"}
	b.ReportAllocs()
	for b.Loop() {
		_ = signature(key, req)
	}
}
