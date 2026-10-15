// 原生协议排查工具，仅输出验证结果，不输出账号凭证。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
)

func main() {
	path := flag.String("credentials-file", "", "授权数据文件路径")
	modelName := flag.String("model", "qwen3.8-flash", "测试模型")
	flag.Parse()
	if *path == "" {
		fmt.Fprintln(os.Stderr, "请指定授权数据文件")
		os.Exit(1)
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		fail("无法读取授权数据")
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		fail("授权数据格式无效")
	}
	credentials := qoder.CredentialsFromMap(data)
	if credentials.UserID == "" {
		if user, ok := data["user"].(map[string]any); ok {
			credentials.UserID = fmt.Sprint(user["id"])
			credentials.Name, _ = user["name"].(string)
			credentials.Email, _ = user["email"].(string)
			credentials.OrganizationID, _ = user["organization_id"].(string)
			credentials.OrganizationName, _ = user["organization_name"].(string)
			credentials.UserType = "personal_standard"
		}
	}
	client, err := qoder.NewClient("")
	if err != nil {
		fail("无法创建协议客户端")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	models, err := client.Models(ctx, credentials)
	if err != nil {
		fail("模型查询失败：" + err.Error())
	}
	model, err := qoder.ResolveModel(models, *modelName)
	if err != nil {
		fail(err.Error())
	}
	body, err := qoder.PrepareChat([]byte(`{"messages":[{"role":"user","content":"请只回复：原生连接成功。"}],"max_tokens":128}`), model)
	if err != nil {
		fail(err.Error())
	}
	collector := &qoder.Collector{}
	started := time.Now()
	if err := client.ChatStream(ctx, credentials, body, collector.Add); err != nil {
		fail("原生聊天失败：" + err.Error())
	}
	result := map[string]any{"模型数量": len(models), "回复": collector.Content, "正常结束": collector.Finish != "", "耗时秒": time.Since(started).Seconds()}
	output, _ := json.Marshal(result)
	fmt.Println(string(output))
}
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
