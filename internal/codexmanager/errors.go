package codexmanager

import (
	"context"
	"errors"
	"net"
	"net/http"
)

const (
	CodeBadRequest          = "bad_request"
	CodeAccountNotFound     = "account_not_found"
	CodeUpstreamUnavailable = "upstream_unavailable"
	CodeUpstreamTimeout     = "upstream_timeout"
	CodeUpstreamRejected    = "upstream_rejected"
	CodeInternalError       = "internal_error"
)

var (
	ErrInvalidPagination   = errors.New("invalid pagination")
	ErrInvalidAccountID    = errors.New("invalid accountId")
	ErrAccountNotFound     = errors.New("account not found")
	ErrUpstreamUnavailable = errors.New("codex-manager upstream unavailable")
)

type CodedError struct {
	HTTPStatus int
	Code       string
	Message    string
	Retryable  bool
}

func (e *CodedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func NewCodedError(httpStatus int, code, message string, retryable bool) *CodedError {
	return &CodedError{
		HTTPStatus: httpStatus,
		Code:       code,
		Message:    message,
		Retryable:  retryable,
	}
}

func MapError(err error) (int, Envelope[any]) {
	if err == nil {
		return http.StatusOK, Success[any](nil)
	}

	var codedErr *CodedError
	if errors.As(err, &codedErr) {
		httpStatus := codedErr.HTTPStatus
		if httpStatus == 0 {
			httpStatus = http.StatusInternalServerError
		}
		return httpStatus, Failure(codedErr.Code, codedErr.Message, codedErr.Retryable)
	}

	switch {
	case errors.Is(err, ErrInvalidPagination), errors.Is(err, ErrInvalidAccountID):
		return http.StatusBadRequest, Failure(CodeBadRequest, err.Error(), false)
	case errors.Is(err, ErrAccountNotFound):
		return http.StatusNotFound, Failure(CodeAccountNotFound, ErrAccountNotFound.Error(), false)
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, Failure(CodeUpstreamTimeout, "codex-manager upstream request timed out", true)
	case errors.Is(err, ErrUpstreamUnavailable):
		return http.StatusServiceUnavailable, Failure(CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return http.StatusServiceUnavailable, Failure(CodeUpstreamTimeout, "codex-manager upstream request timed out", true)
	}

	return http.StatusInternalServerError, Failure(CodeInternalError, "internal server error", false)
}
