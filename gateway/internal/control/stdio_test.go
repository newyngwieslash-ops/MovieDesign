package control

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocol(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"gateway.info"}
{"jsonrpc":"2.0","id":"health","method":"gateway.health"}
{"jsonrpc":"2.0","method":"gateway.health"}
not-json
{"jsonrpc":"2.0","id":2,"method":"missing"}
{"jsonrpc":"2.0","id":3,"method":"gateway.info","params":{"extra":1}}
{"jsonrpc":"2.0","id":true,"method":"gateway.info"}
{"jsonrpc":"2.0","id":4,"method":"gateway.shutdown"}
{"jsonrpc":"2.0","id":5,"method":"gateway.info"}
`
	var out bytes.Buffer
	if err := Serve(strings.NewReader(input), &out, 8001); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("responses=%s", out.String())
	}
	var responses []map[string]any
	for _, line := range lines {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, r)
	}
	if responses[0]["result"].(map[string]any)["port"] != float64(8001) {
		t.Fatal(responses[0])
	}
	if responses[1]["id"] != "health" || responses[1]["result"].(map[string]any)["status"] != "ok" {
		t.Fatal(responses[1])
	}
	for i, code := range []float64{-32700, -32601, -32602, -32600} {
		if responses[i+2]["error"].(map[string]any)["code"] != code {
			t.Fatal(responses[i+2])
		}
	}
	if responses[6]["result"].(map[string]any)["ok"] != true {
		t.Fatal(responses[6])
	}
	if err := Serve(strings.NewReader(""), &out, 8001); err != nil {
		t.Fatal(err)
	}
	if err := Serve(strings.NewReader(strings.Repeat("x", 65537)), &out, 8001); err == nil {
		t.Fatal("oversized frame accepted")
	}
}
