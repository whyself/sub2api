package qoder

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

type CreditPool struct {
	Kind      string     `json:"kind"`
	Remaining float64    `json:"remaining"`
	Used      float64    `json:"used"`
	Total     *float64   `json:"total,omitempty"`
	Cap       *float64   `json:"cap,omitempty"`
	Available *bool      `json:"available,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type CreditsSnapshot struct {
	Unit         string       `json:"unit"`
	Plan         string       `json:"plan,omitempty"`
	UserType     string       `json:"user_type,omitempty"`
	Subscription CreditPool   `json:"subscription"`
	OtherPools   []CreditPool `json:"other_pools"`
	PeriodEndAt  *time.Time   `json:"period_end_at,omitempty"`
	Exceeded     bool         `json:"exceeded"`
	Prorated     bool         `json:"prorated"`
	FetchedAt    time.Time    `json:"fetched_at"`
	Warning      string       `json:"warning,omitempty"`
}

func creditNumber(value any) (float64, bool) {
	var n float64
	var err error
	switch v := value.(type) {
	case json.Number:
		n, err = v.Float64()
	case float64:
		n = v
	case string:
		n, err = strconv.ParseFloat(v, 64)
	default:
		return 0, false
	}
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}

func creditTime(value any) *time.Time {
	if text, ok := value.(string); ok {
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	n, ok := creditNumber(value)
	if !ok || n <= 0 || n > 1e15 {
		return nil
	}
	parsed := time.Unix(int64(n), 0)
	if n > 1e11 {
		parsed = time.UnixMilli(int64(n))
	}
	parsed = parsed.UTC()
	return &parsed
}

func parseCreditPool(kind string, object map[string]any, requireTotal bool) (CreditPool, error) {
	pool := CreditPool{Kind: kind}
	var ok bool
	pool.Remaining, ok = creditNumber(object["remaining"])
	if !ok {
		return pool, &Error{502, "额度响应缺少有效剩余数值"}
	}
	pool.Used, ok = creditNumber(object["used"])
	if !ok {
		return pool, &Error{502, "额度响应缺少有效已用数值"}
	}
	if value, exists := object["total"]; exists || requireTotal {
		total, valid := creditNumber(value)
		if !valid {
			return pool, &Error{502, "额度响应缺少有效总额"}
		}
		pool.Total = &total
	}
	if cap, valid := creditNumber(object["cap"]); valid {
		pool.Cap = &cap
	}
	if available, exists := object["available"].(bool); exists {
		pool.Available = &available
	}
	pool.ExpiresAt = creditTime(object["expiresAt"])
	if pool.ExpiresAt == nil {
		pool.ExpiresAt = creditTime(object["expireAt"])
	}
	return pool, nil
}

// ParseCredits 只解析明确返回的额度；资源包与共享池不合并到订阅余额。
func ParseCredits(raw map[string]any) (*CreditsSnapshot, error) {
	unit, _ := raw["usageType"].(string)
	if !strings.EqualFold(strings.TrimSpace(unit), "credits") {
		return nil, &Error{502, "当前账号未返回 Credits 计量额度"}
	}
	subscription, ok := raw["userQuota"].(map[string]any)
	if !ok {
		return nil, &Error{502, "额度响应缺少订阅额度"}
	}
	pool, err := parseCreditPool("subscription", subscription, true)
	if err != nil {
		return nil, err
	}
	result := &CreditsSnapshot{Unit: "credits", Subscription: pool, OtherPools: make([]CreditPool, 0), FetchedAt: time.Now().UTC(), PeriodEndAt: creditTime(raw["expiresAt"])}
	result.UserType, _ = raw["userType"].(string)
	result.Exceeded, _ = raw["isQuotaExceeded"].(bool)
	result.Prorated, _ = raw["isPlanQuotaProrated"].(bool)
	for _, field := range []struct{ name, kind string }{{"addOnQuota", "addon"}, {"orgResourcePackage", "shared"}} {
		value := raw[field.name]
		if value == nil {
			continue
		}
		object, valid := value.(map[string]any)
		if !valid {
			return nil, &Error{502, "附加额度结构无法识别"}
		}
		additional, err := parseCreditPool(field.kind, object, field.kind == "addon")
		if err != nil {
			// 保留可确认的订阅额度，同时明确附加来源未成功解析。
			result.Warning = "部分附加额度暂无法查询，请核对官方用量页。"
			continue
		}
		result.OtherPools = append(result.OtherPools, additional)
	}
	return result, nil
}

func (c *Client) Credits(ctx context.Context, credentials Credentials) (*CreditsSnapshot, error) {
	var quota map[string]any
	if err := c.jsonCall(ctx, "/api/v2/quota/usage", nil, credentials.AccessToken, &quota); err != nil {
		return nil, err
	}
	result, err := ParseCredits(quota)
	if err != nil {
		return nil, err
	}
	var plan map[string]any
	if err := c.jsonCall(ctx, "/api/v2/user/plan", nil, credentials.AccessToken, &plan); err != nil {
		result.Warning = "套餐信息暂无法查询，额度已成功获取。"
		return result, nil
	}
	result.Plan, _ = plan["plan_tier_name"].(string)
	// 年付订阅服务期可能长于月度额度周期，不用套餐到期日覆盖额度刷新日。
	return result, nil
}
