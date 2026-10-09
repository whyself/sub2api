package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) SetQoderService(value *QoderService) { s.qoderService = value }
func (s *AccountTestService) testQoderAccount(c *gin.Context, account *Account, model, prompt string) error {
	if s.qoderService == nil {
		return s.sendErrorAndEnd(c, "Qoder 测试服务未配置")
	}
	if model == "" {
		model = "qwen3.8-flash"
	}
	if prompt == "" {
		prompt = "请只回复：连接成功。"
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	body, _ := json.Marshal(map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": prompt}}, "max_tokens": 256, "stream": true})
	response, err := s.qoderService.Forward(c.Request.Context(), account, body, true)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Qoder 接口返回 HTTP %d", response.StatusCode))
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[5:])
		if payload == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) == nil {
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					s.sendEvent(c, TestEvent{Type: "content", Text: choice.Delta.Content})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return s.sendErrorAndEnd(c, "Qoder 流读取失败")
	}
	if !done {
		return s.sendErrorAndEnd(c, "Qoder 流未正常结束")
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
