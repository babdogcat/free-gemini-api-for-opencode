package gemini

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// ToolCall represents an OpenAI-compatible function tool call
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function"
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // stringified JSON object
}

// ArrangedOutput contains the parsed, cleaned, and structured result of Gemini's response
type ArrangedOutput struct {
	CleanText        string     `json:"clean_text"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	IsToolCall       bool       `json:"is_tool_call"`
	FinishReason     string     `json:"finish_reason"` // "tool_calls" or "stop"
}

// FormatToolsInstruction creates a clear, structured system prompt injecting tools into Gemini
func FormatToolsInstruction(tools []map[string]any) string {
	if len(tools) == 0 {
		return ""
	}

	toolsJSON, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		bytes, _ := json.Marshal(tools)
		toolsJSON = bytes
	}

	var sb strings.Builder
	sb.WriteString("\n[AVAILABLE TOOLS]\n")
	sb.WriteString("You have access to the following tools that you can invoke:\n")
	sb.WriteString("```json\n")
	sb.Write(toolsJSON)
	sb.WriteString("\n```\n\n")
	sb.WriteString("[TOOL CALL INSTRUCTIONS]\n")
	sb.WriteString("1. If you need to call a tool to answer the user's request, reply ONLY with one or more `<tool_call>` blocks in this exact format:\n")
	sb.WriteString("<tool_call>\n")
	sb.WriteString(`{"name": "<tool_name>", "arguments": {<json_arguments>}}` + "\n")
	sb.WriteString("</tool_call>\n")
	sb.WriteString("2. You can invoke multiple tools by outputting multiple `<tool_call>` blocks consecutively.\n")
	sb.WriteString("3. Ensure the \"arguments\" object strictly follows the schema parameters (types, names, requirements).\n")
	sb.WriteString("4. If no tool is needed, or after receiving tool results, respond directly in natural language without any `<tool_call>` tags.\n")
	sb.WriteString("5. Do not invent tools that are not in the list.\n")
	sb.WriteString("6. Never emit an empty arguments object ({}). Every property listed as required in the tool's schema MUST be present with a real value.\n")
	sb.WriteString("\n[EXAMPLE]\n")
	sb.WriteString("<tool_call>\n")
	sb.WriteString(`{"name": "shell", "arguments": {"command": "ls -la"}}` + "\n")
	sb.WriteString("</tool_call>\n\n")

	return sb.String()
}

var (
	thoughtRegex  = regexp.MustCompile(`(?s)<(?:thought|thinking)>([\s\S]*?)</(?:thought|thinking)>`)
	toolCallRegex = regexp.MustCompile(`(?s)<tool_call>([\s\S]*?)</tool_call>`)
	jsonCodeBlock = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")
)

// ArrangeGeminiOutput analyzes, cleans, parses, and structures raw Gemini output
func ArrangeGeminiOutput(raw string) *ArrangedOutput {
	res := &ArrangedOutput{
		FinishReason: "stop",
	}

	text := raw

	// 1. Extract and separate Thinking / Reasoning blocks (<thought> or <thinking>)
	if matches := thoughtRegex.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		var thoughts []string
		for _, m := range matches {
			if len(m) > 1 {
				t := strings.TrimSpace(m[1])
				if t != "" {
					thoughts = append(thoughts, t)
				}
			}
		}
		if len(thoughts) > 0 {
			res.ReasoningContent = strings.Join(thoughts, "\n\n")
		}
		text = thoughtRegex.ReplaceAllString(text, "")
	}

	text = strings.TrimSpace(text)

	// 2. Extract Tool Calls from <tool_call> tags
	toolCallMatches := toolCallRegex.FindAllStringSubmatch(text, -1)
	if len(toolCallMatches) > 0 {
		for _, m := range toolCallMatches {
			if len(m) > 1 {
				payload := strings.TrimSpace(m[1])
				tc, ok := parseSingleToolCall(payload)
				if ok {
					res.ToolCalls = append(res.ToolCalls, tc)
				}
			}
		}
		if len(res.ToolCalls) > 0 {
			// The model sometimes picks the right tool but drops the payload,
			// which makes the client reject the call ("Invalid arguments ...
			// Missing key"). If the reply carries a fenced command, use it.
			backfillEmptyToolArgs(res.ToolCalls, text)
			res.IsToolCall = true
			res.FinishReason = "tool_calls"
			clean := toolCallRegex.ReplaceAllString(text, "")
			res.CleanText = strings.TrimSpace(clean)
			return res
		}
	}

	// 3. Fallback: Check if response contains a direct JSON tool call inside code block or raw JSON
	if strings.Contains(text, `"name"`) && (strings.Contains(text, `"arguments"`) || strings.Contains(text, `"parameters"`)) {
		// Try code block first
		if cbMatches := jsonCodeBlock.FindAllStringSubmatch(text, -1); len(cbMatches) > 0 {
			for _, m := range cbMatches {
				if len(m) > 1 {
					payload := strings.TrimSpace(m[1])
					tc, ok := parseSingleToolCall(payload)
					if ok {
						res.ToolCalls = append(res.ToolCalls, tc)
					}
				}
			}
		}
		// If still none, try entire text as JSON
		if len(res.ToolCalls) == 0 {
			tc, ok := parseSingleToolCall(text)
			if ok {
				res.ToolCalls = append(res.ToolCalls, tc)
			}
		}

		if len(res.ToolCalls) > 0 {
			backfillEmptyToolArgs(res.ToolCalls, text)
			res.IsToolCall = true
			res.FinishReason = "tool_calls"
			res.CleanText = ""
			return res
		}
	}

	// 4. Clean up any raw internal artifacts or trailing disclaimers
	res.CleanText = sanitizeGeminiContent(text)
	return res
}

func parseSingleToolCall(payload string) (ToolCall, bool) {
	payload = strings.TrimSpace(payload)
	// Remove outer backticks if any
	payload = strings.TrimPrefix(payload, "```json")
	payload = strings.TrimPrefix(payload, "```")
	payload = strings.TrimSuffix(payload, "```")
	payload = strings.TrimSpace(payload)

	obj, decodable := decodeJSONObject(payload)
	if !decodable {
		return ToolCall{}, false
	}

	// Some models wrap the call in an OpenAI-style {"function": {...}} object.
	if fn, ok := obj["function"].(map[string]any); ok {
		merged := map[string]any{}
		for k, v := range fn {
			merged[k] = v
		}
		if n, exists := obj["name"]; exists {
			if _, has := merged["name"]; !has {
				merged["name"] = n
			}
		}
		obj = merged
	}

	// Extract tool name
	name := ""
	if n, ok := obj["name"].(string); ok && n != "" {
		name = n
	} else if n, ok := obj["tool"].(string); ok && n != "" {
		name = n
	} else if n, ok := obj["function"].(string); ok && n != "" {
		name = n
	}

	if name == "" {
		return ToolCall{}, false
	}

	// Extract arguments
	argsStr := stringifyArgs(obj["arguments"])
	if argsStr == "" {
		argsStr = stringifyArgs(obj["parameters"])
	}
	if argsStr == "" {
		// Flat form: {"name":"shell","command":"ls -la"} - everything that is
		// not metadata is treated as an argument.
		flat := map[string]any{}
		for k, v := range obj {
			switch k {
			case "name", "tool", "function", "id", "type", "arguments", "parameters":
				continue
			}
			flat[k] = v
		}
		if len(flat) > 0 {
			if b, err := json.Marshal(flat); err == nil {
				argsStr = string(b)
			}
		}
	}

	return ToolCall{
		ID:   "call_" + strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
		Type: "function",
		Function: ToolCallFunction{
			Name:      name,
			Arguments: repairArgsJSON(argsStr),
		},
	}, true
}

var (
	smartQuoteReplacer = strings.NewReplacer(
		"\u201c", "\"", "\u201d", "\"", "\u2018", "'", "\u2019", "'",
	)
	trailingCommaJSON = regexp.MustCompile(`,\s*([}\]])`)
	codeFenceAny      = regexp.MustCompile("(?s)```(?:bash|sh|zsh|shell|console|cmd|powershell)?\\s*\\n(.*?)```")
)

// normalizeToolPayload repairs the small, very common JSON mistakes models make
// when hand-writing a tool call: smart quotes and trailing commas.
func normalizeToolPayload(p string) string {
	p = smartQuoteReplacer.Replace(p)
	return trailingCommaJSON.ReplaceAllString(p, "$1")
}

// repairStrayQuotes escapes double quotes that appear *inside* a JSON string
// value instead of terminating it. Models routinely emit
//
//	{"command":"ffprobe "Some File.mp3" -show_format"}
//
// which is invalid JSON and used to be discarded wholesale, so the tool call
// reached the client as "{}" ("Invalid arguments ... Missing key").
func repairStrayQuotes(s string) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString && c == '\\' && i+1 < len(s) {
			b.WriteByte(c)
			i++
			b.WriteByte(s[i])
			continue
		}
		if c != '"' {
			b.WriteByte(c)
			continue
		}
		if !inString {
			inString = true
			b.WriteByte(c)
			continue
		}
		// Inside a string: a quote is a real terminator only when the next
		// meaningful character closes the value.
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
			j++
		}
		if j >= len(s) || s[j] == ',' || s[j] == '}' || s[j] == ']' || s[j] == ':' {
			inString = false
			b.WriteByte(c)
			continue
		}
		b.WriteString(`\"`)
	}
	return b.String()
}

// decodeJSONObject decodes a tool-call payload, applying progressively more
// forgiving repairs before giving up.
func decodeJSONObject(payload string) (map[string]any, bool) {
	normalized := normalizeToolPayload(payload)
	candidates := []string{payload, normalized, repairStrayQuotes(normalized)}
	for _, c := range candidates {
		var obj map[string]any
		if err := json.Unmarshal([]byte(c), &obj); err == nil && obj != nil {
			return obj, true
		}
	}
	return nil, false
}

// stringifyArgs turns an arguments value into the JSON-object string the
// OpenAI schema expects.
func stringifyArgs(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(val)
	default:
		if b, err := json.Marshal(val); err == nil {
			return string(b)
		}
	}
	return ""
}

// repairArgsJSON guarantees the arguments payload is decodable JSON, repairing
// the usual model mistakes (double encoding, smart quotes, trailing commas,
// stray inner quotes) before falling back to an empty object.
func repairArgsJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}
	// Arguments sometimes arrive double-encoded: "{\"command\":\"ls\"}"
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) > 1 {
		var inner string
		if err := json.Unmarshal([]byte(s), &inner); err == nil {
			s = strings.TrimSpace(inner)
		}
	}
	normalized := normalizeToolPayload(s)
	for _, c := range []string{s, normalized, repairStrayQuotes(normalized)} {
		var dummy any
		if err := json.Unmarshal([]byte(c), &dummy); err == nil {
			return c
		}
	}
	return "{}"
}

// backfillEmptyToolArgs rescues tool calls that arrived without arguments by
// lifting a fenced command out of the same reply.
func backfillEmptyToolArgs(calls []ToolCall, text string) {
	if len(calls) == 0 {
		return
	}
	fence := ""
	if m := codeFenceAny.FindStringSubmatch(text); len(m) > 1 {
		fence = strings.TrimSpace(m[1])
	}
	for i := range calls {
		args := strings.TrimSpace(calls[i].Function.Arguments)
		if args != "{}" && args != "" {
			continue
		}
		if fence == "" {
			continue
		}
		if b, err := json.Marshal(map[string]string{"command": fence}); err == nil {
			calls[i].Function.Arguments = string(b)
		}
	}
}

var (
	codeFenceWithQueryRegex = regexp.MustCompile(`(?m)^` + "```" + `([a-zA-Z0-9_-]+)\?[^\n]*$`)
	multiNewlineRegex       = regexp.MustCompile(`\n{3,}`)
)

func sanitizeGeminiContent(text string) string {
	cleaned := text
	// Remove null bytes
	cleaned = strings.ReplaceAll(cleaned, "\x00", "")
	// Clean Google Python Sandbox internal query strings: ```python?code_reference&code_event_index=1 -> ```python
	cleaned = codeFenceWithQueryRegex.ReplaceAllString(cleaned, "```$1")
	// Normalize excessive newlines
	cleaned = multiNewlineRegex.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned
}
