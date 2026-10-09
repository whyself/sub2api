package qoder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func quotaFixture(raw string) map[string]any {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	_ = decoder.Decode(&result)
	return result
}

func TestQoderCreditsPreserveFractionsAndSeparateSharedQuota(t *testing.T) {
	raw := quotaFixture(`{"usageType":"credits","expiresAt":1792771200000,"userQuota":{"total":100.5,"used":0.25,"remaining":100.25},"addOnQuota":{"total":10,"used":0.125,"remaining":9.875},"orgResourcePackage":{"used":15,"remaining":500,"cap":50,"available":true}}`)
	result, err := ParseCredits(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Subscription.Remaining != 100.25 || len(result.OtherPools) != 2 || result.OtherPools[0].Remaining != 9.875 {
		t.Fatal("小数额度被截断或附加来源丢失")
	}
	if result.OtherPools[1].Total != nil || result.OtherPools[0].ExpiresAt != nil {
		t.Fatal("共享上限被当成总额，或订阅时间被误贴给资源包")
	}
	if result.PeriodEndAt == nil || result.PeriodEndAt.UnixMilli() != 1792771200000 {
		t.Fatal("毫秒时间解析错误")
	}
}

func TestQoderCreditsRejectUnknownOrMissingAmounts(t *testing.T) {
	for _, raw := range []string{
		`{"usageType":"tokens","userQuota":{"total":100,"used":0,"remaining":100}}`,
		`{"usageType":"credits","userQuota":{"total":100,"used":0}}`,
		`{"usageType":"credits","userQuota":{"total":100,"used":0,"remaining":"NaN"}}`,
		`{"usageType":"credits","userQuota":{"total":100,"used":0,"remaining":-1}}`,
	} {
		if _, err := ParseCredits(quotaFixture(raw)); err == nil {
			t.Fatal("未知计量方式或缺失额度被当成有效余额")
		}
	}
	zero, err := ParseCredits(quotaFixture(`{"usageType":"credits","userQuota":{"total":0,"used":0,"remaining":0}}`))
	if err != nil || zero.Subscription.Remaining != 0 {
		t.Fatal("真实零余额应可显示")
	}
}

func TestQoderCreditsUseBearerAndNeverExposeUpstreamSecrets(t *testing.T) {
	client := &Client{OpenAPIURL: OpenAPI, HTTP: testDoer(func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || req.Header.Get("Authorization") != "Bearer 测试访问令牌" || strings.Contains(req.URL.String(), "测试访问令牌") {
			t.Fatal("额度查询的令牌传递方式错误")
		}
		body := `{"usageType":"credits","userQuota":{"total":50,"used":1.5,"remaining":48.5},"access_token":"上游回显秘密"}`
		status := 200
		if req.URL.Path == "/api/v2/user/plan" {
			status = 503
			body = `{"error":"上游回显秘密"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	result, err := client.Credits(context.Background(), Credentials{AccessToken: "测试访问令牌"})
	if err != nil || result.Subscription.Remaining != 48.5 || result.Warning == "" {
		t.Fatal("套餐查询失败不应丢弃已确认的余额")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "秘密") || strings.Contains(string(raw), "访问令牌") {
		t.Fatal("网页响应包含了秘密字段")
	}
}
