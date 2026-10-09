package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
)

type qoderResponseBody struct {
	*io.PipeReader
	cancel context.CancelFunc
}

func (b *qoderResponseBody) Close() error                           { b.cancel(); return b.PipeReader.Close() }
func (s *OpenAIGatewayService) SetQoderService(value *QoderService) { s.qoderService = value }

// Forward 在共享网关出站点将账号凭证转换为原生请求，再恢复为标准响应。
func (s *QoderService) Forward(ctx context.Context, account *Account, raw []byte, stream bool) (*http.Response, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return qoderHTTPError(&qoder.Error{Status: 400, Message: "请求格式无效"}), nil
	}
	modelName, _ := request["model"].(string)
	if modelName == "" {
		modelName = "auto"
	}
	models, err := s.Models(ctx, account)
	if err != nil {
		return qoderHTTPError(err), nil
	}
	model, err := qoder.ResolveModel(models, modelName)
	if err != nil {
		return qoderHTTPError(err), nil
	}
	body, err := qoder.PrepareChat(raw, model)
	if err != nil {
		return qoderHTTPError(err), nil
	}
	constraint, err := qoder.ApplyToolConstraint(body)
	if err != nil {
		return qoderHTTPError(err), nil
	}
	collector := &qoder.Collector{}
	run := func(callCtx context.Context, emit func(map[string]any) error) error {
		for attempt := 0; attempt < 2; attempt++ {
			fresh, err := s.CurrentAccount(callCtx, account, attempt > 0)
			if err != nil {
				return err
			}
			client, err := s.client(callCtx, fresh.ProxyID)
			if err != nil {
				return err
			}
			emitted := false
			var buffered []map[string]any
			bufferBytes := 0
			err = client.ChatStream(callCtx, qoder.CredentialsFromMap(fresh.Credentials), body, func(chunk map[string]any) error {
				if constraint.Buffered || !stream {
					data, _ := json.Marshal(chunk)
					bufferBytes += len(data)
					if bufferBytes > 16<<20 {
						return &qoder.Error{Status: 502, Message: "上游响应超过缓冲大小限制"}
					}
					buffered = append(buffered, chunk)
					return nil
				}
				emitted = true
				return emit(chunk)
			})
			if err != nil {
				if attempt == 0 && !emitted && qoder.IsUnauthorized(err) {
					continue
				}
				return err
			}
			if constraint.Buffered || !stream {
				collector = &qoder.Collector{}
				for _, chunk := range buffered {
					if err := collector.Add(chunk); err != nil {
						return err
					}
				}
				if err := constraint.Validate(collector); err != nil {
					return err
				}
				for _, chunk := range buffered {
					if err := emit(chunk); err != nil {
						return err
					}
				}
			}
			return nil
		}
		return errors.New("Qoder 授权失效，请重新授权")
	}
	if !stream {
		if err := run(ctx, func(map[string]any) error { return nil }); err != nil {
			return qoderHTTPError(err), nil
		}
		result := collector.Completion(modelName)
		raw, _ := json.Marshal(result)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw))}, nil
	}
	callCtx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	first := make(chan error, 1)
	go func() {
		started := false
		err := run(callCtx, func(chunk map[string]any) error {
			data, err := qoder.MarshalChunk(chunk, modelName)
			if err != nil {
				return err
			}
			if !started {
				first <- nil
				started = true
			}
			_, err = writer.Write(data)
			return err
		})
		if !started {
			first <- err
			if err == nil {
				err = &qoder.Error{Status: 502, Message: "上游没有有效数据"}
			}
		}
		if err == nil {
			_, err = writer.Write([]byte("data: [DONE]\n\n"))
		}
		_ = writer.CloseWithError(err)
	}()
	select {
	case err := <-first:
		if err != nil {
			cancel()
			_ = reader.Close()
			return qoderHTTPError(err), nil
		}
	case <-ctx.Done():
		cancel()
		_ = reader.Close()
		return nil, ctx.Err()
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Cache-Control": {"no-cache"}}, Body: &qoderResponseBody{PipeReader: reader, cancel: cancel}, ContentLength: -1}, nil
}
func qoderHTTPError(err error) *http.Response {
	status := 502
	var provider *qoder.Error
	if errors.As(err, &provider) && provider.Status >= 400 && provider.Status <= 599 {
		status = provider.Status
	}
	message := "Qoder 请求失败"
	if provider != nil {
		message = provider.Message
	}
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "qoder_upstream_error", "message": message}})
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw))}
}
