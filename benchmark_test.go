package univerifygo

import "testing"

func BenchmarkSign(b *testing.B) {
	key := make([]byte, 32)
	body := []byte(`{"access_token":"test-token","openid":"test-openid"}`)
	b.ReportAllocs()
	for b.Loop() {
		_ = signature(key, "test", "1793872800000", body)
	}
}
