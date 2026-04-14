package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const protocolVersion = "2024-11-05"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
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

type toolCallResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type server struct {
	root string
	llm  *qwenClient
}

type agentAction struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if configured := strings.TrimSpace(os.Getenv("MCP_FS_ROOT")); configured != "" {
		root = configured
	}

	s := &server{
		root: root,
		llm:  newQwenClientFromEnv(),
	}
	if err := s.serve(os.Stdin, os.Stdout); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func (s *server) serve(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	for {
		payload, err := readFramedMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		var req request
		if err := json.Unmarshal(payload, &req); err != nil {
			_ = writeFramedMessage(out, response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}

		resp, shouldRespond := s.handle(req)
		if shouldRespond {
			if err := writeFramedMessage(out, resp); err != nil {
				return err
			}
		}
	}
}

func readFramedMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		const prefix = "Content-Length:"
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(prefix)) {
			_, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length header: %w", err)
			}
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeFramedMessage(w io.Writer, msg any) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func (s *server) handle(req request) (response, bool) {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"serverInfo": map[string]any{
				"name":    "interview-fs-agent-server",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		}
		return resp, true
	case "notifications/initialized":
		return response{}, false
	case "tools/list":
		resp.Result = map[string]any{"tools": s.tools()}
		return resp, true
	case "tools/call":
		result, err := s.callTool(req.Params)
		if err != nil {
			resp.Result = toolCallResult{Content: []content{{Type: "text", Text: err.Error()}}, IsError: true}
			return resp, true
		}
		resp.Result = result
		return resp, true
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
		return resp, true
	}
}

func (s *server) tools() []tool {
	return []tool{
		{
			Name:        "list_dir",
			Description: "List entries in a directory under sandbox root.",
			InputSchema: objSchema([]string{"path"}, map[string]any{"path": strSchema("directory path relative to root")}),
		},
		{
			Name:        "read_file",
			Description: "Read UTF-8 text file content.",
			InputSchema: objSchema([]string{"path"}, map[string]any{"path": strSchema("file path relative to root")}),
		},
		{
			Name:        "write_file",
			Description: "Write UTF-8 content to file. Creates parent directories.",
			InputSchema: objSchema([]string{"path", "content"}, map[string]any{"path": strSchema("target file path"), "content": strSchema("new file content")}),
		},
		{
			Name:        "find_text",
			Description: "Recursively search text in files from a directory.",
			InputSchema: objSchema([]string{"path", "query"}, map[string]any{"path": strSchema("start directory"), "query": strSchema("search string"), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20}}),
		},
		{
			Name:        "apply_patch",
			Description: "Replace text in a file with safety check.",
			InputSchema: objSchema([]string{"path", "old", "new"}, map[string]any{"path": strSchema("target file path"), "old": strSchema("text to replace"), "new": strSchema("replacement text")}),
		},
		{
			Name:        "stat",
			Description: "Show file or directory metadata.",
			InputSchema: objSchema([]string{"path"}, map[string]any{"path": strSchema("path relative to root")}),
		},
		{
			Name:        "agent_plan",
			Description: "Generate an executable action plan for a coding goal.",
			InputSchema: objSchema([]string{"goal"}, map[string]any{"goal": strSchema("high-level objective"), "path": strSchema("working directory; default '.'")}),
		},
		{
			Name:        "agent_execute",
			Description: "Execute a multi-step action plan with step-by-step logs.",
			InputSchema: objSchema([]string{"actions"}, map[string]any{"actions": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "dry_run": map[string]any{"type": "boolean", "default": false}}),
		},
		{
			Name:        "agent_run",
			Description: "Use Qwen 3.5 model to autonomously call filesystem tools and finish a task.",
			InputSchema: objSchema([]string{"task"}, map[string]any{"task": strSchema("task to complete"), "max_steps": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "default": 8}}),
		},
	}
}

func objSchema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func strSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func (s *server) callTool(raw json.RawMessage) (toolCallResult, error) {
	var req struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return toolCallResult{}, errors.New("invalid tools/call params")
	}
	switch req.Name {
	case "list_dir":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		return s.toolListDir(path)
	case "read_file":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		return s.toolReadFile(path)
	case "write_file":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		content, err := argString(req.Arguments, "content")
		if err != nil {
			return toolCallResult{}, err
		}
		return s.toolWriteFile(path, content)
	case "find_text":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		query, err := argString(req.Arguments, "query")
		if err != nil {
			return toolCallResult{}, err
		}
		limit := argInt(req.Arguments, "limit", 20)
		return s.toolFindText(path, query, limit)
	case "apply_patch":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		oldText, err := argString(req.Arguments, "old")
		if err != nil {
			return toolCallResult{}, err
		}
		newText, err := argString(req.Arguments, "new")
		if err != nil {
			return toolCallResult{}, err
		}
		return s.toolApplyPatch(path, oldText, newText)
	case "stat":
		path, err := argString(req.Arguments, "path")
		if err != nil {
			return toolCallResult{}, err
		}
		return s.toolStat(path)
	case "agent_plan":
		goal, err := argString(req.Arguments, "goal")
		if err != nil {
			return toolCallResult{}, err
		}
		path := argStringDefault(req.Arguments, "path", ".")
		return s.toolAgentPlan(goal, path)
	case "agent_execute":
		actions, err := argActions(req.Arguments, "actions")
		if err != nil {
			return toolCallResult{}, err
		}
		dryRun := argBool(req.Arguments, "dry_run", false)
		return s.toolAgentExecute(actions, dryRun)
	case "agent_run":
		task, err := argString(req.Arguments, "task")
		if err != nil {
			return toolCallResult{}, err
		}
		maxSteps := argInt(req.Arguments, "max_steps", 8)
		return s.toolAgentRun(task, maxSteps)
	default:
		return toolCallResult{}, fmt.Errorf("unknown tool: %s", req.Name)
	}
}

func argString(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing argument: %s", key)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("argument %s must be non-empty string", key)
	}
	return s, nil
}

func argStringDefault(args map[string]any, key, def string) string {
	v, ok := args[key]
	if !ok {
		return def
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func argInt(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	f, ok := v.(float64)
	if !ok {
		return def
	}
	n := int(f)
	if n < 1 {
		return def
	}
	if n > 200 {
		n = 200
	}
	return n
}

func argBool(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func argActions(args map[string]any, key string) ([]agentAction, error) {
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("missing argument: %s", key)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var actions []agentAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, errors.New("actions must be an array of {tool, arguments}")
	}
	if len(actions) == 0 {
		return nil, errors.New("actions must not be empty")
	}
	return actions, nil
}

func (s *server) resolve(rel string) (string, error) {
	clean := filepath.Clean(rel)
	root := filepath.Clean(s.root)
	full := filepath.Join(root, clean)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", errors.New("path escapes sandbox root")
	}
	return full, nil
}

func (s *server) toolListDir(rel string) (toolCallResult, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return toolCallResult{}, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return toolCallResult{}, err
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	sort.Strings(lines)
	return textResult(strings.Join(lines, "\n")), nil
}

func (s *server) toolReadFile(rel string) (toolCallResult, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return toolCallResult{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return toolCallResult{}, err
	}
	return textResult(string(b)), nil
}

func (s *server) toolWriteFile(rel, c string) (toolCallResult, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return toolCallResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return toolCallResult{}, err
	}
	if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
		return toolCallResult{}, err
	}
	return textResult("ok"), nil
}

func (s *server) toolStat(rel string) (toolCallResult, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return toolCallResult{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return toolCallResult{}, err
	}
	payload, _ := json.MarshalIndent(map[string]any{
		"name":        info.Name(),
		"size":        info.Size(),
		"mode":        info.Mode().String(),
		"isDirectory": info.IsDir(),
		"modifiedAt":  info.ModTime().UTC().Format(time.RFC3339),
	}, "", "  ")
	return textResult(string(payload)), nil
}

func (s *server) toolFindText(rel, query string, limit int) (toolCallResult, error) {
	start, err := s.resolve(rel)
	if err != nil {
		return toolCallResult{}, err
	}
	var matches []string
	err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if len(matches) >= limit {
			return io.EOF
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.Contains(bytes.ToLower(data), bytes.ToLower([]byte(query))) {
			relPath, _ := filepath.Rel(s.root, path)
			matches = append(matches, relPath)
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return toolCallResult{}, err
	}
	if len(matches) == 0 {
		return textResult("no matches"), nil
	}
	sort.Strings(matches)
	return textResult(strings.Join(matches, "\n")), nil
}

func (s *server) toolApplyPatch(rel, oldText, newText string) (toolCallResult, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return toolCallResult{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return toolCallResult{}, err
	}
	oldCount := strings.Count(string(data), oldText)
	if oldCount == 0 {
		return toolCallResult{}, errors.New("target text not found")
	}
	if oldCount > 1 {
		return toolCallResult{}, fmt.Errorf("target text is ambiguous: %d matches", oldCount)
	}
	updated := strings.Replace(string(data), oldText, newText, 1)
	if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
		return toolCallResult{}, err
	}
	return textResult("patched"), nil
}

func (s *server) toolAgentPlan(goal, rel string) (toolCallResult, error) {
	_, err := s.resolve(rel)
	if err != nil {
		return toolCallResult{}, err
	}
	plan := []agentAction{
		{Tool: "list_dir", Arguments: map[string]any{"path": rel}},
		{Tool: "find_text", Arguments: map[string]any{"path": rel, "query": keywords(goal), "limit": 20}},
	}
	payload, _ := json.MarshalIndent(map[string]any{
		"goal":    goal,
		"summary": "Inspect workspace, locate relevant files, then use write/apply_patch to implement changes.",
		"actions": plan,
	}, "", "  ")
	return textResult(string(payload)), nil
}

func keywords(goal string) string {
	fields := strings.Fields(goal)
	if len(fields) == 0 {
		return "TODO"
	}
	if len(fields) > 4 {
		fields = fields[:4]
	}
	return strings.Join(fields, " ")
}

func (s *server) toolAgentExecute(actions []agentAction, dryRun bool) (toolCallResult, error) {
	var logs []string
	for i, action := range actions {
		if strings.HasPrefix(action.Tool, "agent_") {
			return toolCallResult{}, errors.New("agent_execute cannot call agent_* tools recursively")
		}
		logs = append(logs, fmt.Sprintf("step %d: %s", i+1, action.Tool))
		if dryRun {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"name": action.Tool, "arguments": action.Arguments})
		result, err := s.callTool(raw)
		if err != nil {
			logs = append(logs, "error: "+err.Error())
			break
		}
		for _, c := range result.Content {
			logs = append(logs, "output: "+firstLine(c.Text, 120))
		}
	}
	return textResult(strings.Join(logs, "\n")), nil
}

func (s *server) toolAgentRun(task string, maxSteps int) (toolCallResult, error) {
	if s.llm == nil || !s.llm.Enabled() {
		return toolCallResult{}, errors.New("qwen is not configured, set QWEN_API_KEY (and optional QWEN_BASE_URL/QWEN_MODEL)")
	}
	return s.llm.Run(task, maxSteps, s)
}

func firstLine(s string, n int) string {
	line := strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
	if len(line) <= n {
		return line
	}
	return line[:n] + "..."
}

func textResult(text string) toolCallResult {
	return toolCallResult{Content: []content{{Type: "text", Text: text}}}
}
