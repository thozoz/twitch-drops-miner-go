package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/sourcegraph/jsonrpc2"
)

type routingHandler struct {
	h Handler
}

func (r routingHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	switch req.Method {
	case MethodStatus:
		res, err := r.h.Status(ctx)
		if err != nil {
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInternalError, Message: err.Error()})
			return
		}
		_ = conn.Reply(ctx, req.ID, res)

	case MethodPriority:
		var p PriorityParams
		if req.Params != nil {
			if err := json.Unmarshal(*req.Params, &p); err != nil {
				_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams, Message: err.Error()})
				return
			}
		}
		res, err := r.h.Priority(ctx, p)
		if err != nil {
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInternalError, Message: err.Error()})
			return
		}
		_ = conn.Reply(ctx, req.ID, res)

	case MethodShutdown:
		var p ShutdownParams
		if req.Params != nil {
			if err := json.Unmarshal(*req.Params, &p); err != nil {
				_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams, Message: err.Error()})
				return
			}
		}
		res, err := r.h.Shutdown(ctx, p)
		if err != nil {
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInternalError, Message: err.Error()})
			return
		}
		_ = conn.Reply(ctx, req.ID, res)

	case MethodGetLogs:
		var p GetLogsParams
		if req.Params != nil {
			if err := json.Unmarshal(*req.Params, &p); err != nil {
				_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams, Message: err.Error()})
				return
			}
		}
		res, err := r.h.GetLogs(ctx, p)
		if err != nil {
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInternalError, Message: err.Error()})
			return
		}
		_ = conn.Reply(ctx, req.ID, res)

	case MethodStreamLogs:
		var p GetLogsParams
		if req.Params != nil {
			if err := json.Unmarshal(*req.Params, &p); err != nil {
				_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams, Message: err.Error()})
				return
			}
		}
		_ = conn.Reply(ctx, req.ID, map[string]string{"status": "subscribed"})
		go func() {
			_ = r.h.StreamLogs(ctx, conn, p)
		}()

	default:
		_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeMethodNotFound,
			Message: fmt.Sprintf("method not found: %s", req.Method),
		})
	}
}

// Serve accepts incoming connections on ln, dispatching JSON-RPC 2.0 requests to h until ctx is canceled.
func Serve(ctx context.Context, ln net.Listener, h Handler) error {
	var wg sync.WaitGroup

	// Ensure listener is closed on context cancellation
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			// If listener was closed or network error when shutting down
			if errors.Is(err, net.ErrClosed) {
				wg.Wait()
				return nil
			}
			return err
		}

		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			jc := jsonrpc2.NewConn(ctx, jsonrpc2.NewPlainObjectStream(c), routingHandler{h: h})
			defer func() {
				_ = jc.Close()
				_ = c.Close()
			}()
			select {
			case <-jc.DisconnectNotify():
			case <-ctx.Done():
			}
		}(conn)
	}
}
