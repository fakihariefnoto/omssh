package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/archive"
	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/version"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// `omssh mcp` is the same file manager as a Model Context Protocol server over
// stdio. There is no listener and no network: the client that started the process
// is the only one that can talk to it, and the safety model is the CLI's —
// --allow-write, allowlists, batch semantics and the audit log.

// protocolVersion is the MCP revision this server speaks. It is a date, not a
// range: a client that wants another one says so and the handshake still works.
const protocolVersion = "2024-11-05"

func newMCPCmd(opts *Options, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server over stdio (for AI agents)",
		Long: "Speaks the Model Context Protocol on stdin and stdout.\n\n" +
			"Reads are free; writes need --allow-write, and --allow-host and\n" +
			"--allow-path narrow what is reachable. rm returns a plan unless --yes.\n" +
			"Nothing prompts: this process has no terminal.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			// An MCP client cannot answer a host-key question; batch semantics are
			// the only ones that can run here without hanging.
			opts.Batch = true
			return ServeMCP(context.Background(), opts, os.Stdin, stdout, stderr)
		},
	}
}

// rpcRequest is one JSON-RPC 2.0 call or notification.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcError is a protocol-level error, not a tool failure.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeMCP runs the server until the input closes.
func ServeMCP(ctx context.Context, opts *Options, in io.Reader, out, errOut io.Writer) error {
	cfg, _, err := loadConfig(opts)
	if err != nil {
		return err
	}
	server := &mcpServer{opts: opts, cfg: cfg, out: out}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request rpcRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			server.respond(nil, nil, &rpcError{Code: -32700, Message: "parse error"})
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil //nolint:nilerr // a cancelled context is a clean shutdown
		}
		if request.ID == nil {
			continue // a notification, such as notifications/initialized
		}
		server.handle(request)
	}
	return scanner.Err()
}

// mcpServer holds the configuration and the output, so each call does not reload
// either.
type mcpServer struct {
	opts *Options
	cfg  config.Config
	out  io.Writer
}

func (s *mcpServer) handle(request rpcRequest) {
	switch request.Method {
	case "initialize":
		s.respond(request.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "omssh", "version": version.String()},
		}, nil)
	case "ping":
		s.respond(request.ID, map[string]any{}, nil)
	case "tools/list":
		s.respond(request.ID, map[string]any{"tools": mcpTools()}, nil)
	case "tools/call":
		s.callTool(request)
	default:
		s.respond(request.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + request.Method})
	}
}

func (s *mcpServer) respond(id json.RawMessage, result any, rpcErr *rpcError) {
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return
	}
	_, _ = s.out.Write(append(encoded, '\n'))
}

// toolResult is the MCP content shape: text, or text that happens to be JSON.
func toolResult(text string, failed bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": failed,
	}
}

func (s *mcpServer) callTool(request rpcRequest) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.respond(request.ID, nil, &rpcError{Code: -32602, Message: "invalid params"})
		return
	}
	text, failed := s.runTool(params.Name, params.Arguments)
	s.respond(request.ID, toolResult(text, failed), nil)
}

// runTool dispatches one tool. A failure is a tool result with isError, not a
// protocol error: the agent should see why and decide what to do.
func (s *mcpServer) runTool(name string, args map[string]any) (string, bool) {
	str := func(key string) string {
		value, _ := args[key].(string)
		return strings.TrimSpace(value)
	}
	placeName, path := str("place"), vfs.Clean(str("path"))
	if placeName == "" {
		placeName = "local"
	}

	switch name {
	case "places":
		lines := make([]string, 0, 1+len(s.cfg.Cloud.Profiles))
		lines = append(lines, "local\tlocal disk")
		for profile, entry := range s.cfg.Cloud.Profiles {
			lines = append(lines, profile+"\t"+entry.Provider+"\t"+entry.Bucket)
		}
		return strings.Join(lines, "\n"), false

	case "ls", "stat", "get":
		target, err := s.open(placeName, path, false)
		if err != nil {
			return err.Error(), true
		}
		defer target.done()
		switch name {
		case "ls":
			entries, err := target.fsys.ReadDir(path)
			if err != nil {
				return err.Error(), true
			}
			rows := make([]map[string]any, 0, len(entries))
			for _, entry := range entries {
				row := map[string]any{"name": entry.Name(), "dir": entry.IsDir()}
				if info, err := entry.Info(); err == nil {
					row["size"] = info.Size()
					row["modified"] = info.ModTime().UTC().Format(time.RFC3339)
				}
				rows = append(rows, row)
			}
			encoded, _ := json.Marshal(map[string]any{"place": placeName, "path": path, "entries": rows})
			return string(encoded), false
		case "stat":
			info, err := target.fsys.Stat(path)
			if err != nil {
				return err.Error(), true
			}
			encoded, _ := json.Marshal(map[string]any{"place": placeName, "path": path,
				"name": info.Name(), "dir": info.IsDir(), "size": info.Size(),
				"modified": info.ModTime().UTC().Format(time.RFC3339)})
			return string(encoded), false
		default: // get
			file, err := target.fsys.Open(path)
			if err != nil {
				return err.Error(), true
			}
			defer func() { _ = file.Close() }()
			data, err := io.ReadAll(io.LimitReader(file, 4<<20))
			if err != nil {
				return err.Error(), true
			}
			if utf8.Valid(data) {
				return string(data), false
			}
			return "base64:" + base64.StdEncoding.EncodeToString(data), false
		}

	case "put":
		target, err := s.open(placeName, path, true)
		if err != nil {
			return err.Error(), true
		}
		defer target.done()
		content := str("text")
		data := []byte(content)
		if strings.HasPrefix(content, "base64:") {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(content, "base64:"))
			if err != nil {
				return "invalid base64: " + err.Error(), true
			}
			data = decoded
		}
		if parent := target.fsys.Dir(path); parent != "/" && parent != "." {
			if err := target.fsys.MkdirAll(parent, 0o755); err != nil {
				return err.Error(), true
			}
		}
		writer, err := target.fsys.Create(path)
		if err != nil {
			return err.Error(), true
		}
		written, copyErr := writer.Write(data)
		closeErr := writer.Close()
		if copyErr == nil {
			copyErr = closeErr
		}
		audit("put", placeName, path, int64(written), copyErr)
		if copyErr != nil {
			return copyErr.Error(), true
		}
		return fmt.Sprintf("wrote %d bytes to %s:%s", written, placeName, path), false

	case "mkdir":
		target, err := s.open(placeName, path, true)
		if err != nil {
			return err.Error(), true
		}
		defer target.done()
		if err := target.fsys.MkdirAll(path, 0o755); err != nil {
			audit("mkdir", placeName, path, 0, err)
			return err.Error(), true
		}
		audit("mkdir", placeName, path, 0, nil)
		return "created " + placeName + ":" + path, false

	case "copy":
		srcPlace, srcPath := str("src_place"), vfs.Clean(str("src_path"))
		dstPlace, dstPath := str("dst_place"), vfs.Clean(str("dst_path"))
		if srcPlace == "" {
			srcPlace = placeName
		}
		if dstPlace == "" {
			dstPlace = srcPlace
		}
		if srcPath == "" || dstPath == "" {
			return "copy needs src_path and dst_path", true
		}
		if err := guard(s.opts, srcPlace, srcPath, false); err != nil {
			return err.Error(), true
		}
		dst, err := s.open(dstPlace, dstPath, true)
		if err != nil {
			return err.Error(), true
		}
		defer dst.done()
		src, err := s.open(srcPlace, srcPath, false)
		if err != nil {
			return err.Error(), true
		}
		defer src.done()
		bytes, files, err := copyTree(src.fsys, srcPath, dst.fsys, dstPath)
		audit("copy", dstPlace, dstPath, bytes, err)
		if err != nil {
			return err.Error(), true
		}
		return fmt.Sprintf("copied %d %s (%d bytes) to %s:%s", files,
			pluralWord(files, "file", "files"), bytes, dstPlace, dstPath), false

	case "extract":
		dstPlace, dstPath := str("dst_place"), vfs.Clean(str("dst_path"))
		if dstPlace == "" {
			dstPlace = placeName
		}
		if dstPath == "" {
			dstPath = "/"
		}
		src, err := s.open(placeName, path, false)
		if err != nil {
			return err.Error(), true
		}
		defer src.done()
		dst, err := s.open(dstPlace, dstPath, true)
		if err != nil {
			return err.Error(), true
		}
		defer dst.done()
		count, err := archive.Extract(context.Background(), src.fsys, path, dst.fsys, dstPath, nil)
		audit("extract", dstPlace, dstPath, 0, err)
		if err != nil {
			return err.Error(), true
		}
		return fmt.Sprintf("extracted %d %s into %s:%s", count,
			pluralWord(count, "entry", "entries"), dstPlace, dstPath), false

	case "rm":
		target, err := s.open(placeName, path, true)
		if err != nil {
			return err.Error(), true
		}
		defer target.done()
		if !s.opts.Yes {
			return "planned: remove " + placeName + ":" + path + "\nadd --yes to the server command to perform it", false
		}
		if err := target.fsys.RemoveAll(path); err != nil {
			audit("rm", placeName, path, 0, err)
			return err.Error(), true
		}
		audit("rm", placeName, path, 0, nil)
		return "removed " + placeName + ":" + path, false

	default:
		return "unknown tool: " + name, true
	}
}

// copyTree copies a file or a whole directory tree, creating the shape on the
// other side. It is the MCP counterpart of the interface's paste.
func copyTree(srcFS vfs.FS, srcPath string, dstFS vfs.FS, dstPath string) (bytes int64, files int, err error) {
	info, err := srcFS.Stat(srcPath)
	if err != nil {
		return 0, 0, err
	}
	if !info.IsDir() {
		written, err := copyOneFile(srcFS, srcPath, dstFS, dstPath)
		return written, 1, err
	}
	if err := dstFS.MkdirAll(dstPath, 0o755); err != nil {
		return 0, 0, err
	}
	base := strings.TrimSuffix(srcPath, "/")
	err = fs.WalkDir(srcFS, srcPath, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, base), "/")
		if rel == "" {
			return nil
		}
		target := dstFS.Join(dstPath, rel)
		if entry.IsDir() {
			return dstFS.MkdirAll(target, 0o755)
		}
		written, err := copyOneFile(srcFS, p, dstFS, target)
		if err != nil {
			return err
		}
		bytes += written
		files++
		return nil
	})
	return bytes, files, err
}

func copyOneFile(srcFS vfs.FS, srcPath string, dstFS vfs.FS, dstPath string) (int64, error) {
	if parent := dstFS.Dir(dstPath); parent != "/" && parent != "." {
		if err := dstFS.MkdirAll(parent, 0o755); err != nil {
			return 0, err
		}
	}
	in, err := srcFS.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	out, err := dstFS.Create(dstPath)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// open applies the safety model before a filesystem is handed out.
func (s *mcpServer) open(placeName, path string, write bool) (*place, error) {
	if err := guard(s.opts, placeName, path, write); err != nil {
		return nil, err
	}
	return openPlace(s.opts, s.cfg, placeName)
}

// mcpTools is the contract: every tool says whether it reads or writes.
func mcpTools() []map[string]any {
	placeProp := map[string]any{"type": "string", "description": "local, or a cloud profile name"}
	pathProp := map[string]any{"type": "string", "description": "absolute path inside the place"}
	object := func(props map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	return []map[string]any{
		{"name": "places", "description": "List the places this server can touch (reads).",
			"inputSchema": object(map[string]any{})},
		{"name": "ls", "description": "List a directory (reads).",
			"inputSchema": object(map[string]any{"place": placeProp, "path": pathProp}, "path")},
		{"name": "stat", "description": "Describe one path (reads).",
			"inputSchema": object(map[string]any{"place": placeProp, "path": pathProp}, "path")},
		{"name": "get", "description": "Read a file as text, or as base64:… when binary (reads).",
			"inputSchema": object(map[string]any{"place": placeProp, "path": pathProp}, "path")},
		{"name": "put", "description": "Write a file; needs --allow-write on the server (writes).",
			"inputSchema": object(map[string]any{"place": placeProp, "path": pathProp,
				"text": map[string]any{"type": "string", "description": "contents, or base64:…"}}, "path", "text")},
		{"name": "mkdir", "description": "Create a directory; needs --allow-write (writes).",
			"inputSchema": object(map[string]any{"place": placeProp, "path": pathProp}, "path")},
		{"name": "copy", "description": "Copy a file or tree between places; needs --allow-write (writes).",
			"inputSchema": object(map[string]any{
				"src_place": placeProp, "src_path": pathProp,
				"dst_place": placeProp, "dst_path": pathProp,
			}, "src_path", "dst_path")},
		{"name": "extract", "description": "Extract an archive into a place; needs --allow-write (writes).",
			"inputSchema": object(map[string]any{"place": placeProp, "path": pathProp,
				"dst_place": placeProp, "dst_path": pathProp}, "path")},
		{"name": "rm", "description": "Remove a file or tree; returns a plan unless the server runs with --yes (writes).",
			"inputSchema": object(map[string]any{"place": placeProp, "path": pathProp}, "path")},
	}
}
