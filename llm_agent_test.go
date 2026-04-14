package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQwenClientEnabled(t *testing.T) {
	c := &qwenClient{apiKey: ""}
	if c.Enabled() {
		t.Fatal("expected disabled when api key is empty")
	}
}

func TestAgentRun(t *testing.T) {
	root := t.TempDir()
	s := &server{root: root}

	var mu sync.Mutex
	idx := 0
	responses := []string{
		`{"choices":[{"message":{"content":"{\"type\":\"tool_call\",\"tool\":\"list_dir\",\"arguments\":{\"path\":\".\"}}"}}]}`,
		`{"choices":[{"message":{"content":"{\"type\":\"final\",\"answer\":\"done\"}"}}]}`,
	}

	c := &qwenClient{apiKey: "k", base: "http://test", model: "qwen3.5-plus"}
	c.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		body := responses[idx]
		idx++
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})}
	s.llm = c

	res, err := s.toolAgentRun("check files", 4)
	if err != nil {
		t.Fatalf("toolAgentRun error: %v", err)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "final: done") {
		t.Fatalf("unexpected result: %+v", res)
	}
}
