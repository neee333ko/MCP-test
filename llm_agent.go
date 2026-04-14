package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type qwenClient struct {
	apiKey string
	base   string
	model  string
	http   *http.Client
}

type qwenResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type agentDecision struct {
	Type      string         `json:"type"`
	Tool      string         `json:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Answer    string         `json:"answer,omitempty"`
	Reason    string         `json:"reason,omitempty"`
}

func newQwenClientFromEnv() *qwenClient {
	apiKey := strings.TrimSpace(os.Getenv("QWEN_API_KEY"))
	base := strings.TrimSpace(os.Getenv("QWEN_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("QWEN_MODEL"))
	if base == "" {
		base = "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
	}
	if model == "" {
		model = "qwen3.5-plus"
	}
	return &qwenClient{apiKey: apiKey, base: base, model: model, http: http.DefaultClient}
}

func (c *qwenClient) Enabled() bool {
	return c != nil && c.apiKey != ""
}

func (c *qwenClient) Run(task string, maxSteps int, s *server) (toolCallResult, error) {
	if maxSteps < 1 {
		maxSteps = 1
	}
	if maxSteps > 20 {
		maxSteps = 20
	}
	obs := ""
	logs := []string{"task: " + task}
	for step := 1; step <= maxSteps; step++ {
		decision, raw, err := c.nextDecision(task, obs)
		if err != nil {
			return toolCallResult{}, err
		}
		logs = append(logs, fmt.Sprintf("step %d decision: %s", step, raw))
		switch decision.Type {
		case "final":
			if strings.TrimSpace(decision.Answer) == "" {
				return toolCallResult{}, errors.New("qwen final answer is empty")
			}
			logs = append(logs, "final: "+decision.Answer)
			return textResult(strings.Join(logs, "\n")), nil
		case "tool_call":
			if strings.HasPrefix(decision.Tool, "agent_") {
				return toolCallResult{}, errors.New("qwen tried to call agent_* tool recursively")
			}
			rawCall, _ := json.Marshal(map[string]any{"name": decision.Tool, "arguments": decision.Arguments})
			result, err := s.callTool(rawCall)
			if err != nil {
				obs = "tool error: " + err.Error()
				logs = append(logs, obs)
				continue
			}
			if len(result.Content) == 0 {
				obs = "tool returned empty content"
			} else {
				obs = result.Content[0].Text
			}
			logs = append(logs, "tool output: "+firstLine(obs, 240))
		default:
			return toolCallResult{}, fmt.Errorf("unsupported decision type: %s", decision.Type)
		}
	}
	return toolCallResult{}, errors.New("agent_run reached max steps without final answer")
}

func (c *qwenClient) nextDecision(task, observation string) (agentDecision, string, error) {
	if !c.Enabled() {
		return agentDecision{}, "", errors.New("qwen api key not configured")
	}
	systemPrompt := `You are an autonomous coding agent inside an MCP filesystem server.
Available tools: list_dir, read_file, write_file, stat, find_text, apply_patch.
Rules:
1) Return ONLY JSON.
2) For tool use: {"type":"tool_call","tool":"read_file","arguments":{"path":"README.md"},"reason":"..."}
3) When task completed: {"type":"final","answer":"..."}
4) Never call agent_* tools.`
	userPrompt := fmt.Sprintf("task: %s\nlast_observation: %s", task, observation)

	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.2,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.base, bytes.NewReader(body))
	if err != nil {
		return agentDecision{}, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return agentDecision{}, "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return agentDecision{}, "", fmt.Errorf("qwen http status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed qwenResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return agentDecision{}, "", err
	}
	if len(parsed.Choices) == 0 {
		return agentDecision{}, "", errors.New("qwen returned no choices")
	}
	raw := strings.TrimSpace(parsed.Choices[0].Message.Content)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var decision agentDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return agentDecision{}, raw, fmt.Errorf("qwen output is not valid JSON decision: %w", err)
	}
	return decision, raw, nil
}
