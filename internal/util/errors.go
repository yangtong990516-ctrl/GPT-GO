package util

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// HTTPError maps a domain error to an HTTP status code and a `{code,message}`
// detail payload, mirroring main.py's exception handlers.
//
//	MongoUnavailableError  -> 503 {"code":"mongodb_unavailable", ...}
//	DuplicateResourceError -> 409 {"code":"duplicate_resource", ...}
//	ResourceNotFoundError  -> 404 {"code":"resource_not_found", ...}
type HTTPError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *HTTPError) Error() string { return e.Message }

// Error codes shared across handlers.
const (
	CodeInternal     = "internal_error"
	CodeInvalidBody  = "invalid_body"
	CodeNotFound     = "resource_not_found"
	CodeDuplicate    = "duplicate_resource"
	CodeMongoDown    = "mongodb_unavailable"
	CodeInvalidEmail = "invalid_email"
)

// InternalError builds the generic 500 error.
func InternalError() *HTTPError {
	return &HTTPError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "内部错误"}
}

// InvalidBody builds the 422 invalid-JSON-body error.
func InvalidBody() *HTTPError {
	return &HTTPError{Status: http.StatusUnprocessableEntity, Code: CodeInvalidBody, Message: "invalid JSON body"}
}

// MongoUnavailable builds the 503 MongoDB-unavailable error.
func MongoUnavailable(msg string) *HTTPError {
	return &HTTPError{Status: http.StatusServiceUnavailable, Code: CodeMongoDown, Message: msg}
}

// DuplicateResource builds the 409 duplicate-resource error.
func DuplicateResource(msg string) *HTTPError {
	return &HTTPError{Status: http.StatusConflict, Code: CodeDuplicate, Message: msg}
}

// ResourceNotFound builds the 404 resource-not-found error.
func ResourceNotFound(msg string) *HTTPError {
	return &HTTPError{Status: http.StatusNotFound, Code: CodeNotFound, Message: msg}
}

// WriteError writes err to the gin context as `{"detail": {code,message}}`.
// It maps a *HTTPError via errors.As; anything else becomes a 500 internal error.
// This is the single error-to-response entry point for all handlers.
func WriteError(c *gin.Context, err error) {
	var he *HTTPError
	if errors.As(err, &he) {
		c.JSON(he.Status, gin.H{"detail": gin.H{"code": he.Code, "message": he.Message}})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"detail": gin.H{"code": CodeInternal, "message": "内部错误"}})
}

// NowUTC returns the current time in UTC, the unified time base for the service.
func NowUTC() time.Time {
	return time.Now().UTC()
}
