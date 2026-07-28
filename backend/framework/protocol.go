package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

const FrameworkVersion = "0.5.0"
const MaxRequestBytes = 4 * 1024 * 1024

const (
	streamRequestMeta = "stream"
	streamWindowMeta  = "stream_window"
)

type Request struct {
	ID     string            `json:"id"`
	Method string            `json:"method"`
	Params json.RawMessage   `json:"params,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

type Response struct {
	ID     string         `json:"id"`
	Result any            `json:"result"`
	Error  *RPCError      `json:"error,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
	Stream *StreamFrame   `json:"stream,omitempty"`
}

type StreamFrame struct {
	Sequence int64     `json:"sequence"`
	Kind     string    `json:"kind"`
	Progress *Progress `json:"progress,omitempty"`
}

type Progress struct {
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
	Message   string `json:"message,omitempty"`
	Unit      string `json:"unit,omitempty"`
}

type RPCError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code, message string) *RPCError {
	return &RPCError{Code: code, Message: message}
}

func NewErrorWithData(code, message string, data map[string]any) *RPCError {
	return &RPCError{Code: code, Message: message, Data: data}
}

type Context struct {
	context.Context
	Request Request
	Trace   []string
	scope   *Scope
	stream  *StreamWriter
}

func NewContext(parent context.Context, request Request) *Context {
	return NewContextWithScope(parent, request, nil)
}

func NewContextWithScope(parent context.Context, request Request, scope *Scope) *Context {
	return &Context{Context: parent, Request: request, scope: scope}
}

func (ctx *Context) Scope() *Scope {
	if ctx == nil {
		return nil
	}
	return ctx.scope
}

func BindParams[T any](ctx *Context) (T, error) {
	var value T
	if len(ctx.Request.Params) == 0 {
		return value, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(ctx.Request.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, NewError("invalid_params", "The request parameters are invalid.")
	}
	return value, nil
}

func AsRPCError(err error) *RPCError {
	return renderFrameworkException(err)
}
