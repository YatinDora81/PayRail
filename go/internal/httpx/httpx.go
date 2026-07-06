package httpx

import "net/http"

type Error struct{
	Status int
	Code string
	Message string
	Details any // optional
}

func (e *Error)Error()string{
	return e.Code + ": " + e.Message
}

func NewError(status int , code, message string) *Error{
	return &Error{Status: status , Code: code , Message: message}
}

func BadRequest(msg string) *Error{
	return NewError(http.StatusBadRequest , "bad_request" , msg)
}

func NotFound(msg string) *Error{
	return NewError(http.StatusNotFound , "not_found" , msg)
}

func Conflict(msg string) *Error{
	return NewError(http.StatusConflict , "conflict" , msg)
}

func ConflictWith(code , msg string , details any) *Error{
	return &Error{Status: http.StatusConflict , Code: code , Message: msg , Details: details}
}

func Unprocessable(msg string) *Error{
	return NewError(http.StatusUnprocessableEntity , "unprocessable" , msg)
}

func Internal()*Error{
	return NewError(http.StatusInternalServerError , "internal_error" , "something went wrong")
}

