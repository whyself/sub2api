package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQoderQueueFailoverRetainsClientRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
				StatusCode:      429,
				ResponseHeaders: http.Header{"Retry-After": {"30"}},
				ResponseBody:    []byte(`{"error":{"type":"rate_limit_error","code":"10605","message":"queued"}}`),
			}, false)
			require.Equal(t, http.StatusTooManyRequests, rec.Code)
			require.Equal(t, "30", rec.Header().Get("Retry-After"))
			require.Contains(t, rec.Body.String(), "rate_limit_error")
		})
	}
}
