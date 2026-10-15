// Package control implements the approved M1 JSON-RPC 2.0 line protocol.
package control

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/server"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// EOF also shuts down the gateway: the owning shell has gone away.
func Serve(in io.Reader, out io.Writer, port int, snapshots ...func() server.Health) error {
	health := server.Ready
	if len(snapshots) > 0 {
		health = snapshots[0]
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		var req request
		resp := response{JSONRPC: "2.0", ID: json.RawMessage("null")}
		var stop bool
		if !json.Valid(scanner.Bytes()) {
			resp.Error = &rpcError{-32700, "Parse error"}
		} else if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" || !validID(req.ID) {
			resp.Error = &rpcError{-32600, "Invalid Request"}
		} else {
			if len(req.ID) > 0 {
				resp.ID = req.ID
			}
			if len(req.Params) > 0 && !bytes.Equal(bytes.TrimSpace(req.Params), []byte("{}")) && !bytes.Equal(bytes.TrimSpace(req.Params), []byte("[]")) {
				resp.Error = &rpcError{-32602, "Methods take no parameters"}
			} else {
				switch req.Method {
				case "gateway.info":
					resp.Result = map[string]int{"port": port}
				case "gateway.health":
					resp.Result = health()
				case "gateway.shutdown":
					resp.Result = map[string]bool{"ok": true}
					stop = true
				default:
					resp.Error = &rpcError{-32601, "Method not found"}
				}
			}
			// JSON-RPC notifications never receive a response.
			if len(req.ID) == 0 {
				if stop {
					return nil
				}
				continue
			}
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	return scanner.Err()
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	var v any
	if json.Unmarshal(id, &v) != nil {
		return false
	}
	switch v.(type) {
	case nil, string, float64:
		return true
	default:
		return false
	}
}
