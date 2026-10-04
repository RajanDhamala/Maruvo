package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

const ContractVersion = "1"

type commandError struct {
	Code    string
	Message string
}

func (e *commandError) Error() string { return e.Message }

func InvalidArgument(message string) error {
	return &commandError{Code: "INVALID_ARGUMENT", Message: message}
}

func WriteError(out io.Writer, err error) error {
	failure := struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		HTTPStatus int    `json:"http_status,omitempty"`
	}{Code: "INTERNAL", Message: err.Error()}

	var (
		command  *commandError
		response *api.Error
		network  net.Error
		path     *os.PathError
	)
	switch {
	case errors.As(err, &command):
		failure.Code = command.Code
	case errors.Is(err, context.Canceled):
		failure.Code = "CANCELLED"
	case errors.Is(err, context.DeadlineExceeded):
		failure.Code = "DEADLINE_EXCEEDED"
	case errors.As(err, &response):
		failure.HTTPStatus = response.StatusCode
		switch response.StatusCode {
		case 400, 422:
			failure.Code = "INVALID_ARGUMENT"
		case 401:
			failure.Code = "UNAUTHENTICATED"
		case 403:
			failure.Code = "FORBIDDEN"
		case 404:
			failure.Code = "NOT_FOUND"
		case 409:
			failure.Code = "CONFLICT"
		case 429:
			failure.Code = "RATE_LIMITED"
		default:
			if response.StatusCode >= 500 {
				failure.Code = "UNAVAILABLE"
			}
		}
	case errors.As(err, &network):
		failure.Code = "UNAVAILABLE"
	case errors.As(err, &path):
		failure.Code = "IO_ERROR"
	}

	return json.NewEncoder(out).Encode(map[string]any{
		"contract_version": ContractVersion,
		"error":            failure,
	})
}
