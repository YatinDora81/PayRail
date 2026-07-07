package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/payrail/go/internal/middleware"
)

type Error struct {
	Status  int
	Code    string
	Message string
	Details any // optional
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(msg string) *Error {
	return NewError(http.StatusBadRequest, "bad_request", msg)
}

func NotFound(msg string) *Error {
	return NewError(http.StatusNotFound, "not_found", msg)
}

func Conflict(msg string) *Error {
	return NewError(http.StatusConflict, "conflict", msg)
}

func ConflictWith(code, msg string, details any) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: msg, Details: details}
}

func Unprocessable(msg string) *Error {
	return NewError(http.StatusUnprocessableEntity, "unprocessable", msg)
}

func Internal() *Error {
	return NewError(http.StatusInternalServerError, "internal_error", "something went wrong")
}

func TraceID(r *http.Request) string {
	return middleware.GetRequestId(r.Context())
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": payload})
}

func WriteError(w http.ResponseWriter, traceId string, err error) {
	apiErr, ok := err.(*Error)
	if !ok {
		apiErr = Internal()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apiErr.Status)
	body := map[string]any{
		"code":    apiErr.Code,
		"message": apiErr.Message,
		"traceId": traceId,
	}

	if apiErr.Details != nil {
		body["details"] = apiErr.Details
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

func Decode(w http.ResponseWriter, r *http.Request, dst any) error {

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return BadRequest("invalid json body")
	}
	return nil
}
