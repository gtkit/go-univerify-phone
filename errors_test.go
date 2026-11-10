package univerify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLocalErrorsAndFormatting(t *testing.T) {
	t.Parallel()
	for _, value := range []any{Config{Endpoint: "https://example.invalid", Secret: "private-secret"}, Request{AccessToken: "private-token", OpenID: "private-openid"}, Client{key: []byte("private-key")}, failure(ErrInvalidArgument, "INVALID_ARGUMENT")} {
		if strings.Contains(fmt.Sprintf("%v %+v %#v", value, value, value), "private-") {
			t.Fatal("sensitive format output")
		}
	}
	encoded, err := json.Marshal(Config{Endpoint: "https://example.invalid", Secret: "private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-secret") {
		t.Fatal("secret leaked in JSON")
	}
	var e *Error
	if e.Unwrap() != nil || e.Is(ErrUpstream) || e.Error() == "" {
		t.Fatal("nil Error methods")
	}
}
