package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestErrorCodes(t *testing.T) {
	for _, test := range []struct {
		err    error
		code   string
		status int
	}{
		{InvalidArgument("missing version"), "INVALID_ARGUMENT", 0},
		{&api.Error{StatusCode: 401, Message: "expired token"}, "UNAUTHENTICATED", 401},
		{&api.Error{StatusCode: 403, Message: "wrong reviewer"}, "FORBIDDEN", 403},
		{&api.Error{StatusCode: 409, Message: "stale version"}, "CONFLICT", 409},
		{&api.Error{StatusCode: 503, Message: "offline"}, "UNAVAILABLE", 503},
		{fmt.Errorf("waiting: %w", context.DeadlineExceeded), "DEADLINE_EXCEEDED", 0},
		{context.Canceled, "CANCELLED", 0},
		{errors.New("unexpected failure"), "INTERNAL", 0},
	} {
		var out bytes.Buffer
		if err := WriteError(&out, test.err); err != nil {
			t.Fatal(err)
		}

		var result struct {
			Version string `json:"contract_version"`
			Error   struct {
				Code       string `json:"code"`
				Message    string `json:"message"`
				HTTPStatus int    `json:"http_status"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Version != ContractVersion ||
			result.Error.Code != test.code || result.Error.HTTPStatus != test.status || result.Error.Message != test.err.Error() {
			t.Fatalf("lost failure details: %s (%v)", out.String(), err)
		}
	}
}
