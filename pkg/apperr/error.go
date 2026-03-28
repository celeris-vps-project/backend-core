package apperr

import (
	"errors"
	"fmt"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// AppError is a structured application error that carries a machine-readable
// code and the suggested HTTP status code. App-layer services return *AppError
// so the interface layer can map them to HTTP responses without knowing
// business logic.
type AppError struct {
	Code       string // machine-readable code (e.g. "PROVIDER_NOT_FOUND")
	Message    string // human-readable message for debugging
	HTTPStatus int    // suggested HTTP status code
}

func (e *AppError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// ── Convenience constructors ───────────────────────────────────────────

// ErrBadRequest creates a 400 error.
func ErrBadRequest(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, HTTPStatus: consts.StatusBadRequest}
}

// ErrNotFound creates a 404 error.
func ErrNotFound(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, HTTPStatus: consts.StatusNotFound}
}

// ErrUnprocessable creates a 422 error.
func ErrUnprocessable(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, HTTPStatus: consts.StatusUnprocessableEntity}
}

// ErrInternal creates a 500 error.
func ErrInternal(msg string) *AppError {
	return &AppError{Code: CodeInternalError, Message: msg, HTTPStatus: consts.StatusInternalServerError}
}

// ── Handler helper ─────────────────────────────────────────────────────

// HandleErr writes a JSON error response based on the error type.
// If the error is an *AppError, its Code and HTTPStatus are used.
// Otherwise, a generic 500 Internal Server Error is returned.
func HandleErr(c *hz_app.RequestContext, err error) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		c.JSON(appErr.HTTPStatus, Resp(appErr.Code, appErr.Message))
		return
	}
	// Fallback: unknown error → 500
	c.JSON(consts.StatusInternalServerError, Resp(CodeInternalError, err.Error()))
}
