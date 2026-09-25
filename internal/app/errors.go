package app

import (
	"errors"
	"net/http"
)

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func E(status int, code string, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func ErrorResponse(err error) (int, map[string]any) {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Status, map[string]any{"error": appErr.Code, "message": appErr.Message}
	}
	return http.StatusInternalServerError, map[string]any{"error": "internal_error", "message": "internal server error"}
}

func isStoreNotFound(err error) bool {
	return errors.Is(err, ErrStoreNotFound)
}

func isAppStatus(err error, status int) bool {
	var appErr *Error
	return errors.As(err, &appErr) && appErr.Status == status
}
