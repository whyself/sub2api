package admin

import "github.com/Wei-Shaw/sub2api/internal/service"

func (h *AccountHandler) SetQoderService(value *service.QoderService) {
	h.qoderService = value
	if h.accountTestService != nil {
		h.accountTestService.SetQoderService(value)
	}
}
