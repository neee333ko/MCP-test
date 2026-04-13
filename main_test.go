package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBlocksTraversal(t *testing.T) {
	s := &server{root: "/tmp/sandbox"}
	_, err := s.resolve("../etc/passwd")
	if err == nil {
		t.Fatal("expected traversal to be blocked")
	}
}

func TestAgentExecuteDryRun(t *testing.T) {
	s := &server{root: t.TempDir()}
	actions := []agentAction{{Tool: "list_dir", Arguments: map[string]any{"path": "."}}}
	res, err := s.toolAgentExecute(actions, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "step 1") {
		t.Fatalf("unexpected output: %+v", res)
	}
}

func TestApplyPatch(t *testing.T) {
	root := t.TempDir()
	s := &server{root: root}
	p := filepath.Join(root, "a.txt")
	if err := os.WriteFile(p, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.toolApplyPatch("a.txt", "world", "mcp")
	if err != nil {
		t.Fatalf("patch failed: %v", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "hello mcp" {
		t.Fatalf("unexpected content: %s", string(b))
	}
}

func TestCallToolUnknown(t *testing.T) {
	s := &server{root: t.TempDir()}
	raw, _ := json.Marshal(map[string]any{"name": "nope", "arguments": map[string]any{}})
	_, err := s.callTool(raw)
	if err == nil {
		t.Fatal("expected unknown tool error")
	}
}
