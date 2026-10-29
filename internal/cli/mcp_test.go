package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runMCP writes requests to the server and returns the decoded responses.
func runMCP(t *testing.T, opts *Options, requests ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	if err := ServeMCP(context.Background(), opts, in, &out, io.Discard); err != nil {
		t.Fatalf("ServeMCP: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	responses := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("response is not JSON: %v\n%s", err, line)
		}
		responses = append(responses, response)
	}
	return responses
}

func mcpOptions(t *testing.T) *Options {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return &Options{ConfigPath: filepath.Join(dir, "config.yaml"), err: io.Discard}
}

// The handshake, and the list an agent reads before it calls anything.
func TestMCPInitializeAndTools(t *testing.T) {
	responses := runMCP(t, mcpOptions(t),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(responses) != 2 {
		t.Fatalf("responses = %d, want 2", len(responses))
	}
	result := responses[0]["result"].(map[string]any)
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Error("the server should advertise tools")
	}
	tools := responses[1]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"places", "ls", "stat", "get", "put", "mkdir", "rm"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

// A local call works with no account and no network; a write without
// --allow-write is refused by the same guard the CLI uses.
func TestMCPLocalTools(t *testing.T) {
	opts := mcpOptions(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(source, []byte("hello mcp"), 0o644); err != nil {
		t.Fatal(err)
	}
	lsRequest := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ls","arguments":{"place":"local","path":"` + dir + `"}}}`
	getRequest := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get","arguments":{"place":"local","path":"` + source + `"}}}`
	putRequest := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"put","arguments":{"place":"local","path":"` + filepath.Join(dir, "b.txt") + `","text":"nope"}}}`

	responses := runMCP(t, opts, lsRequest, getRequest, putRequest)
	if len(responses) != 3 {
		t.Fatalf("responses = %d, want 3", len(responses))
	}
	content := func(response map[string]any) string {
		result := response["result"].(map[string]any)
		return result["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	if text := content(responses[0]); !strings.Contains(text, "a.txt") {
		t.Errorf("ls = %s", text)
	}
	if text := content(responses[1]); text != "hello mcp" {
		t.Errorf("get = %q", text)
	}
	if text := content(responses[2]); !strings.Contains(text, "--allow-write") {
		t.Errorf("a write without permission = %q", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err == nil {
		t.Error("the refused put must not create a file")
	}

	// With the run's permission, the same call writes.
	opts.AllowWrite = true
	responses = runMCP(t, opts, putRequest)
	if text := content(responses[0]); !strings.Contains(text, "wrote 4 bytes") {
		t.Errorf("allowed put = %q", text)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "b.txt")); err != nil || string(body) != "nope" {
		t.Errorf("file = %q, %v", body, err)
	}
}

// rm is a plan unless the server was started with --yes.
func TestMCPRemovalIsAPlan(t *testing.T) {
	opts := mcpOptions(t)
	opts.AllowWrite = true
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rm","arguments":{"place":"local","path":"` + path + `"}}}`
	responses := runMCP(t, opts, request)
	text := responses[0]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "planned") {
		t.Fatalf("rm without --yes = %q", text)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the plan must not remove the file")
	}

	opts.Yes = true
	runMCP(t, opts, request)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("--yes should remove it, stat err = %v", err)
	}
}

// Copy is a tool the agent can compose: one file or a whole tree, in any
// direction, through the same places the CLI uses.
func TestMCPCopyTree(t *testing.T) {
	opts := mcpOptions(t)
	opts.AllowWrite = true
	root := t.TempDir()
	src := filepath.Join(root, "site")
	if err := os.MkdirAll(filepath.Join(src, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.html"), []byte("<html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "css", "app.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "copy")
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"copy","arguments":{` +
		`"src_place":"local","src_path":"` + src + `","dst_place":"local","dst_path":"` + dst + `"}}}`
	responses := runMCP(t, opts, request)
	text := responses[0]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "copied 2 files") {
		t.Fatalf("copy = %q", text)
	}
	if body, err := os.ReadFile(filepath.Join(dst, "css", "app.css")); err != nil || string(body) != "body{}" {
		t.Errorf("copied css = %q, %v", body, err)
	}
}

// Every MCP call is recorded for the History screen.
func TestMCPCallsAreRecorded(t *testing.T) {
	opts := mcpOptions(t)
	dir := t.TempDir()
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ls","arguments":{"place":"local","path":"` + dir + `"}}}`
	runMCP(t, opts, request)
	body, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "omssh", "mcp.jsonl"))
	if err != nil {
		t.Fatalf("mcp log: %v", err)
	}
	if !strings.Contains(string(body), `"tool":"ls"`) || !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("the call should be logged:\n%s", body)
	}
}
