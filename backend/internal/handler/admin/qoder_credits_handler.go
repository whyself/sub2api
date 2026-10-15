package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *QoderOAuthHandler) CreditsAccounts(c *gin.Context) {
	items, err := h.oauth.ListCreditsAccounts(c.Request.Context())
	if err != nil {
		response.Error(c, 500, "无法读取 Qoder 账号列表")
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *QoderOAuthHandler) AccountCredits(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "账号编号无效")
		return
	}
	account, err := h.admin.GetAccount(c.Request.Context(), id)
	if err != nil || account == nil || !account.IsQoder() || account.IsShadow() {
		response.Error(c, 404, "找不到原生 Qoder 账号")
		return
	}
	row, err := h.oauth.AccountCredits(c.Request.Context(), account, c.Query("refresh") == "1")
	if err != nil {
		response.Error(c, 502, "无法查询 Qoder 额度")
		return
	}
	response.Success(c, row)
}
