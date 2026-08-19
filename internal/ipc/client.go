package ipc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sourcegraph/jsonrpc2"
)

type notifyHandler struct {
	onNotify func(method string, params json.RawMessage)
}

func (h notifyHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if req.Notif && h.onNotify != nil {
		var raw json.RawMessage
		if req.Params != nil {
			raw = *req.Params
		}
		h.onNotify(req.Method, raw)
	}
}

// Dial connects to a daemon socket/pipe and initializes a JSON-RPC 2.0 connection.
// An optional onNotify callback can be provided to handle server-pushed notifications.
func Dial(ctx context.Context, addr string, timeout time.Duration, onNotify func(method string, params json.RawMessage)) (*jsonrpc2.Conn, error) {
	conn, err := DialClient(ctx, addr, timeout)
	if err != nil {
		return nil, err
	}

	jc := jsonrpc2.NewConn(ctx, jsonrpc2.NewPlainObjectStream(conn), notifyHandler{onNotify: onNotify})
	return jc, nil
}

// Call is a helper wrapping conn.Call.
func Call(ctx context.Context, conn *jsonrpc2.Conn, method string, params, result any) error {
	return conn.Call(ctx, method, params, result)
}
