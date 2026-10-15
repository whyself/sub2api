package qoder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ToolConstraint struct {
	Choice   string
	Function string
	Single   bool
	Buffered bool
}

// ApplyToolConstraint 补充上游未原生执行的工具选择语义；结果仍需严格校验。
func ApplyToolConstraint(body map[string]any) (ToolConstraint, error) {
	c := ToolConstraint{Choice: "auto"}
	instruction := ""
	if value, ok := body["parallel_tool_calls"]; ok {
		single, valid := value.(bool)
		if !valid {
			return c, &Error{400, "parallel_tool_calls 必须是布尔值"}
		}
		c.Single = !single
	}
	if choice := body["tool_choice"]; choice != nil {
		switch v := choice.(type) {
		case string:
			c.Choice = v
		case map[string]any:
			c.Choice = "function"
			function, ok := v["function"].(map[string]any)
			if !ok || v["type"] != "function" {
				return c, &Error{400, "指定工具格式无效"}
			}
			c.Function, _ = function["name"].(string)
		default:
			return c, &Error{400, "tool_choice 取值不受支持"}
		}
	}
	tools, ok := body["tools"].([]any)
	if body["tools"] != nil && !ok {
		return c, &Error{400, "tools 必须为列表"}
	}
	for _, tool := range tools {
		entry, ok := tool.(map[string]any)
		if !ok || entry["type"] != "function" {
			return c, &Error{400, "当前只支持标准 function 工具"}
		}
		function, ok := entry["function"].(map[string]any)
		if !ok || function["name"] == nil {
			return c, &Error{400, "工具缺少名称"}
		}
	}
	switch c.Choice {
	case "auto":
	case "none":
		// 工具结果历史仍需对应定义；保留定义，使用指令和结果校验禁止新调用。
		instruction = "本次回答禁止发起工具调用，请根据已有信息直接回答。"
	case "required":
		if len(tools) == 0 {
			return c, &Error{400, "强制工具调用需要提供 tools"}
		}
		instruction = "本次回答必须调用已提供的工具，不能用普通文本代替工具调用。"
	case "function":
		selected := make([]any, 0, 1)
		for _, tool := range tools {
			entry := tool.(map[string]any)
			if entry["function"].(map[string]any)["name"] == c.Function {
				selected = append(selected, tool)
			}
		}
		if c.Function == "" || len(selected) != 1 {
			return c, &Error{400, "指定的工具没有唯一匹配的定义"}
		}
		body["tools"] = selected
		instruction = "本次回答必须调用工具 " + c.Function + "，不能用普通文本代替工具调用。"
	default:
		return c, &Error{400, "tool_choice 取值不受支持"}
	}
	if c.Single {
		instruction += "本次回答最多发起一个工具调用；其他工具应在收到结果后再决定是否调用。"
	}
	c.Buffered = instruction != ""
	if c.Buffered {
		var systems []string
		remaining := make([]any, 0)
		for _, message := range body["messages"].([]any) {
			entry := message.(map[string]any)
			if entry["role"] == "system" {
				text, ok := entry["content"].(string)
				if !ok {
					return c, &Error{400, "工具约束需要纯文本系统消息"}
				}
				systems = append(systems, text)
			} else {
				remaining = append(remaining, entry)
			}
		}
		systems = append(systems, instruction)
		body["messages"] = append([]any{map[string]any{"role": "system", "content": strings.Join(systems, "\n\n")}}, remaining...)
	}
	delete(body, "parallel_tool_calls")
	return c, nil
}

type ToolCall struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}
type Collector struct {
	Content, Reasoning string
	Tools              map[int]*ToolCall
	Usage              map[string]any
	Finish             string
}

func (c *Collector) Add(chunk map[string]any) error {
	if usage, ok := chunk["usage"].(map[string]any); ok {
		c.Usage = usage
	}
	choices, _ := chunk["choices"].([]any)
	for _, value := range choices {
		choice, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if index, ok := choice["index"].(float64); ok && index != 0 {
			return &Error{502, "上游返回了不支持的多项生成"}
		}
		if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
			c.Finish = finish
		}
		delta, _ := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			c.Content += content
		}
		if reasoning, ok := delta["reasoning_content"].(string); ok {
			c.Reasoning += reasoning
		}
		tools, _ := delta["tool_calls"].([]any)
		for _, tool := range tools {
			entry, ok := tool.(map[string]any)
			if !ok {
				continue
			}
			if c.Tools == nil {
				c.Tools = make(map[int]*ToolCall)
			}
			index := 0
			if number, ok := entry["index"].(float64); ok {
				index = int(number)
			}
			call := c.Tools[index]
			if call == nil {
				call = &ToolCall{Index: index}
				c.Tools[index] = call
			}
			if id, ok := entry["id"].(string); ok && id != "" {
				call.ID = id
			}
			function, _ := entry["function"].(map[string]any)
			if name, ok := function["name"].(string); ok {
				call.Name += name
			}
			if arguments, ok := function["arguments"].(string); ok {
				call.Arguments += arguments
			}
		}
	}
	return nil
}
func (c ToolConstraint) Validate(result *Collector) error {
	if c.Choice == "none" && len(result.Tools) > 0 {
		return &Error{502, "模型没有遵守禁止工具调用的要求"}
	}
	if (c.Choice == "required" || c.Choice == "function") && len(result.Tools) == 0 {
		return &Error{502, "模型没有满足强制工具调用要求"}
	}
	if c.Single && len(result.Tools) > 1 {
		return &Error{502, "模型没有遵守禁止并行调用的要求"}
	}
	for _, tool := range result.Tools {
		if c.Function != "" && tool.Name != c.Function {
			return &Error{502, "模型调用了未指定的工具"}
		}
	}
	return nil
}
func (c *Collector) Completion(model string) map[string]any {
	message := map[string]any{"role": "assistant", "content": c.Content}
	if c.Reasoning != "" {
		message["reasoning_content"] = c.Reasoning
	}
	if len(c.Tools) > 0 {
		tools := make([]any, 0, len(c.Tools))
		indices := make([]int, 0, len(c.Tools))
		for index := range c.Tools {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		for _, index := range indices {
			tool := c.Tools[index]
			tools = append(tools, map[string]any{"id": tool.ID, "type": "function", "function": map[string]any{"name": tool.Name, "arguments": tool.Arguments}})
		}
		message["tool_calls"] = tools
	}
	finish := c.Finish
	if finish == "" {
		finish = "stop"
	}
	id, _ := RandomHex(16)
	return map[string]any{"id": "chatcmpl-" + id, "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": c.Usage}
}
func MarshalChunk(chunk map[string]any, model string) ([]byte, error) {
	chunk["model"] = model
	raw, err := json.Marshal(chunk)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("data: %s\n\n", raw)), nil
}
