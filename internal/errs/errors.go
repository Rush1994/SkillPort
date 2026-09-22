// Package errs exposes only curated messages, never remote bodies or secrets.
package errs

import "fmt"

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string           { return e.Message }
func New(code int, message string) error { return &Error{code, message} }
func Wrap(code int, message string, err error) error {
	if err == nil {
		return nil
	}
	return New(code, message)
}
func Validation(rule, path string) error { return New(6, fmt.Sprintf("%s: %s", rule, path)) }
