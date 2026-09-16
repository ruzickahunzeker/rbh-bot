package ipc

import (
	"context"
	"encoding/json"
	"net/http"
)

type contextKey string

const requestIDKey contextKey = "request_id"

type ErrorResponse struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id,omitempty"`
	} `json:"error"`
}

func RequestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestID := req.Header.Get(HeaderRequestID)
		if requestID == "" {
			requestID, _ = randomHex(16)
		}
		w.Header().Set(HeaderRequestID, requestID)
		ctx := context.WithValue(req.Context(), requestIDKey, requestID)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func RequestID(req *http.Request) string {
	if req == nil {
		return ""
	}
	value, _ := req.Context().Value(requestIDKey).(string)
	return value
}

func WriteError(w http.ResponseWriter, status int, code, message, requestID string) {
	var response ErrorResponse
	response.Error.Code = code
	response.Error.Message = message
	response.Error.RequestID = requestID
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
