package wire

import (
	"encoding/json"
	"strings"
)

// ParseBody inspects an intercepted request for a known LLM endpoint and extracts
// the assembled system prompt + tool names. It recognizes:
//   - Anthropic native:        POST .../v1/messages           (host api.anthropic.com)
//   - OpenAI/Codex:            POST .../chat/completions, .../responses
//   - Gemini API:              POST ...:generateContent,
//     ...:streamGenerateContent on generativelanguage.googleapis.com
//   - Bedrock (classic):       POST /model/{id}/invoke[-with-response-stream]
//     host bedrock-runtime.<region>.amazonaws.com
//     BODY = Anthropic Messages shape (anthropic_version,
//     system, tools, messages)
//   - Bedrock (aws-external):  POST .../v1/messages on aws-external-anthropic.*
//
// The Bedrock body is the same Anthropic Messages JSON, so the Anthropic parser
// handles all Anthropic-shaped variants.
func ParseBody(host, path string, body []byte) (Capture, bool) {
	switch {
	case isBedrockInvoke(host, path):
		c, ok := parseAnthropic(body)
		if ok {
			c.Endpoint = "bedrock/invoke"
		}
		return c, ok
	case strings.HasSuffix(path, "/v1/messages") || strings.HasSuffix(path, "/messages"):
		return parseAnthropic(body)
	case strings.HasSuffix(path, "/chat/completions"):
		return parseOpenAIChat(body)
	case strings.HasSuffix(path, "/responses"):
		return parseOpenAIResponses(body)
	case host == "generativelanguage.googleapis.com" && isGeminiGenerateContent(path):
		return parseGeminiGenerateContent(body)
	default:
		return Capture{}, false
	}
}

func isGeminiGenerateContent(path string) bool {
	return strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")
}

func isBedrockInvoke(host, path string) bool {
	if !strings.Contains(host, "bedrock-runtime") && !strings.Contains(host, "aws-external-anthropic") {
		return false
	}
	return strings.Contains(path, "/model/") && strings.Contains(path, "invoke") ||
		strings.HasSuffix(path, "/v1/messages")
}

func finalize(c Capture, allText string) (Capture, bool) {
	c.AllText = allText
	return c, true
}

// Anthropic Messages shape (also the Bedrock invoke body).
func parseAnthropic(body []byte) (Capture, bool) {
	var req struct {
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return Capture{}, false
	}
	c := Capture{Endpoint: "anthropic/messages"}
	c.SystemPrompt = decodeAnthropicSystem(req.System)
	all := c.SystemPrompt
	for _, m := range req.Messages {
		all += "\n" + decodeContent(m.Content)
	}
	for _, t := range req.Tools {
		if t.Name != "" {
			c.ToolNames = append(c.ToolNames, t.Name)
		}
	}
	return finalize(c, all)
}

func decodeAnthropicSystem(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func parseOpenAIChat(body []byte) (Capture, bool) {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return Capture{}, false
	}
	c := Capture{Endpoint: "openai/chat.completions"}
	var sys, all []string
	for _, m := range req.Messages {
		txt := decodeContent(m.Content)
		all = append(all, txt)
		if m.Role == "system" || m.Role == "developer" {
			sys = append(sys, txt)
		}
	}
	c.SystemPrompt = strings.Join(sys, "\n")
	for _, t := range req.Tools {
		switch {
		case t.Function.Name != "":
			c.ToolNames = append(c.ToolNames, t.Function.Name)
		case t.Name != "":
			c.ToolNames = append(c.ToolNames, t.Name)
		}
	}
	return finalize(c, strings.Join(all, "\n"))
}

func parseOpenAIResponses(body []byte) (Capture, bool) {
	var req struct {
		Instructions string `json:"instructions"`
		Input        []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"input"`
		Tools []struct {
			Name     string `json:"name"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return Capture{}, false
	}
	c := Capture{Endpoint: "openai/responses"}
	var sys, all []string
	if req.Instructions != "" {
		sys = append(sys, req.Instructions)
		all = append(all, req.Instructions)
	}
	for _, m := range req.Input {
		txt := decodeContent(m.Content)
		all = append(all, txt)
		if m.Role == "system" || m.Role == "developer" {
			sys = append(sys, txt)
		}
	}
	c.SystemPrompt = strings.Join(sys, "\n")
	for _, t := range req.Tools {
		switch {
		case t.Name != "":
			c.ToolNames = append(c.ToolNames, t.Name)
		case t.Function.Name != "":
			c.ToolNames = append(c.ToolNames, t.Function.Name)
		}
	}
	return finalize(c, strings.Join(all, "\n"))
}

// decodeContent handles string content and array-of-parts content.
func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var out []string
		for _, p := range parts {
			if p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func parseGeminiGenerateContent(body []byte) (Capture, bool) {
	var req struct {
		SystemInstruction geminiContent   `json:"systemInstruction"`
		Contents          []geminiContent `json:"contents"`
		Tools             []struct {
			FunctionDeclarations []struct {
				Name string `json:"name"`
			} `json:"functionDeclarations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return Capture{}, false
	}
	c := Capture{Endpoint: "google/generateContent"}
	c.SystemPrompt = req.SystemInstruction.text()
	var all []string
	if c.SystemPrompt != "" {
		all = append(all, c.SystemPrompt)
	}
	for _, content := range req.Contents {
		if txt := content.text(); txt != "" {
			all = append(all, txt)
		}
	}
	for _, tool := range req.Tools {
		for _, fn := range tool.FunctionDeclarations {
			if fn.Name != "" {
				c.ToolNames = append(c.ToolNames, fn.Name)
			}
		}
	}
	return finalize(c, strings.Join(all, "\n"))
}

type geminiContent struct {
	Parts []struct {
		Text string `json:"text"`
	} `json:"parts"`
}

func (c geminiContent) text() string {
	var parts []string
	for _, p := range c.Parts {
		if p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n")
}
