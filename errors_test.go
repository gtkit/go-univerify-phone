package univerify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSafeErrorsAndFormatting(t *testing.T) {
	t.Parallel()
	for _, value := range []any{Request{AccessToken: "private-token", OpenID: "private-openid"}, Client{key: []byte("private-key")}, transportFailure(errors.New("private-key"))} {
		if strings.Contains(fmt.Sprintf("%v %+v %#v", value, value, value), "private-") {
			t.Fatal("sensitive format output")
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := transportFailure(cause)
		if !errors.Is(err, cause) || !errors.Is(err, ErrUnavailable) {
			t.Fatal("cause missing")
		}
	}
	var e *Error
	if e.Unwrap() != nil || e.Is(ErrUpstream) || e.Error() == "" {
		t.Fatal("nil Error methods")
	}
}
