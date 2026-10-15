package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type QoderOAuthHandler struct {
	oauth *service.QoderService
	admin service.AdminService
}

func NewQoderOAuthHandler(oauth *service.QoderService, admin service.AdminService) *QoderOAuthHandler {
	return &QoderOAuthHandler{oauth: oauth, admin: admin}
}
func (h *QoderOAuthHandler) owner(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录管理员账号")
		return 0, false
	}
	return subject.UserID, true
}
func qoderAdminError(c *gin.Context, err error) {
	var provider *qoder.Error
	if errors.As(err, &provider) {
		status := provider.Status
		if status == 401 || status == 403 {
			status = http.StatusBadGateway
		}
		response.Error(c, status, provider.Message)
		return
	}
	response.Error(c, http.StatusBadGateway, "Qoder 授权或凭证更新失败，请稍后重试")
}

type qoderBeginRequest struct {
	Name               string         `json:"name"`
	Notes              *string        `json:"notes"`
	AccountID          int64          `json:"account_id"`
	ProxyID            *int64         `json:"proxy_id"`
	GroupIDs           []int64        `json:"group_ids"`
	Concurrency        int            `json:"concurrency"`
	Priority           int            `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier"`
	LoadFactor         *int           `json:"load_factor"`
	Extra              map[string]any `json:"extra"`
	ExpiresAt          *int64         `json:"expires_at"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired"`
}

func (h *QoderOAuthHandler) Begin(c *gin.Context) {
	owner, ok := h.owner(c)
	if !ok {
		return
	}
	var req qoderBeginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "授权参数无效")
		return
	}
	if req.AccountID == 0 && strings.TrimSpace(req.Name) == "" {
		response.BadRequest(c, "请输入账号名称")
		return
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 2
	}
	input := service.CreateAccountInput{Name: strings.TrimSpace(req.Name), Notes: req.Notes, ProxyID: req.ProxyID, GroupIDs: req.GroupIDs, Concurrency: req.Concurrency, Priority: req.Priority, RateMultiplier: req.RateMultiplier, LoadFactor: req.LoadFactor, Extra: req.Extra, ExpiresAt: req.ExpiresAt, AutoPauseOnExpired: req.AutoPauseOnExpired}
	result, err := h.oauth.Begin(c.Request.Context(), owner, input, req.AccountID)
	if err != nil {
		qoderAdminError(c, err)
		return
	}
	response.Success(c, result)
}

type qoderSessionRequest struct {
	SessionID string `json:"session_id" binding:"required"`
}

func (h *QoderOAuthHandler) Poll(c *gin.Context) {
	owner, ok := h.owner(c)
	if !ok {
		return
	}
	var req qoderSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少授权会话编号")
		return
	}
	result, err := h.oauth.Poll(c.Request.Context(), owner, req.SessionID)
	if err != nil {
		qoderAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func (h *QoderOAuthHandler) Cancel(c *gin.Context) {
	owner, ok := h.owner(c)
	if !ok {
		return
	}
	var req qoderSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少授权会话编号")
		return
	}
	result, err := h.oauth.Cancel(owner, req.SessionID)
	if err != nil {
		qoderAdminError(c, err)
		return
	}
	response.Success(c, result)
}
func (h *QoderOAuthHandler) Commit(c *gin.Context) {
	owner, ok := h.owner(c)
	if !ok {
		return
	}
	var req qoderSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少授权会话编号")
		return
	}
	result, err := h.oauth.Commit(c.Request.Context(), owner, req.SessionID, h.admin.CreateAccount)
	if err != nil {
		qoderAdminError(c, err)
		return
	}
	response.Success(c, result)
}
