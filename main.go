package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type server struct {
	root string
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if configured := strings.TrimSpace(os.Getenv("MCP_FS_ROOT")); configured != "" {
		root = configured
	}

	srv := &server{root: root}
	if err := srv.serve(os.Stdin, os.Stdout); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func (s *server) serve(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(jsonRPCResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}

		resp := s.handle(req)
		if len(req.ID) == 0 {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}

	return scanner.Err()
}

func (s *server) handle(req jsonRPCRequest) jsonRPCResponse {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    "filesystem-mcp-server",
				"version": "0.1.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		}
	case "notifications/initialized":
		resp.ID = nil
	case "tools/list":
		resp.Result = map[string]any{"tools": s.tools()}
	case "tools/call":
		result, err := s.callTool(req.Params)
		if err != nil {
			resp.Result = toolCallResult{
				Content: []content{{Type: "text", Text: err.Error()}},
				IsError: true,
			}
			return resp
		}
		resp.Result = result
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}

	return resp
}

func (s *server) tools() []tool {
	return []tool{
		{
			Name:        "list_dir",
			Description: "List entries in a directory under the sandbox root.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Directory path relative to MCP_FS_ROOT."},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "read_file",
			Description: "Read a text file under the sandbox root.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "File path relative to MCP_FS_ROOT."},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Write a UTF-8 text file under the sandbox root.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "stat",
			Description: "Show metadata for a file or directory under the sandbox root.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (s *server) callTool(raw json.RawMessage) (toolCallResult, error) {
	var req struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return toolCallResult{}, errors.New("invalid tool call parameters")
	}

	switch req.Name {
	case "list_dir":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		entries, err := s.listDir(path)
		if err != nil {
			return toolCallResult{}, err
		}
		return textResult(strings.Join(entries, "\n")), nil
	case "read_file":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		data, err := s.readFile(path)
		if err != nil {
			return toolCallResult{}, err
		}
		return textResult(data), nil
	case "write_file":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		content, err := argString(req.Arguments, "content")
		if err != nil {
			return toolCallResult{}, err
		}
		if err := s.writeFile(path, content); err != nil {
			return toolCallResult{}, err
		}
		return textResult("ok"), nil
	case "stat":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		info, err := s.stat(path)
		if err != nil {
			return toolCallResult{}, err
		}
		b, _ := json.MarshalIndent(info, "", "  ")
		return textResult(string(b)), nil
	default:
		return toolCallResult{}, fmt.Errorf("unknown tool: %s", req.Name)
	}
}

func argString(args map[string]any, key string) (string, error) {
	value, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing argument: %s", key)
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("argument must be string: %s", key)
	}
	return s, nil
}

func (s *server) resolve(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("path cannot be empty")
	}
	clean := filepath.Clean(rel)
	full := filepath.Join(s.root, clean)
	rootClean := filepath.Clean(s.root)
	if full != rootClean && !strings.HasPrefix(full, rootClean+string(os.PathSeparator)) {
		return "", errors.New("path escapes sandbox root")
	}
	return full, nil
}

func (s *server) listDir(rel string) ([]string, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	return out, nil
}

func (s *server) readFile(rel string) (string, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *server) writeFile(rel, data string) error {
	p, err := s.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(data), 0o644)
}

func (s *server) stat(rel string) (map[string]any, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":        info.Name(),
		"size":        info.Size(),
		"mode":        info.Mode().String(),
		"isDirectory": info.IsDir(),
		"modifiedAt":  info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func textResult(text string) toolCallResult {
	return toolCallResult{Content: []content{{Type: "text", Text: text}}}
}
