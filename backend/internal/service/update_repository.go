package service

import (
	"os"
	"strings"
)

// updateRepository 允许自部署分支绑定自己的发布仓库，默认行为仍使用原上游。
func updateRepository() string {
	value := strings.TrimSpace(os.Getenv("SUB2API_UPDATE_REPOSITORY"))
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return githubRepo
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return githubRepo
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.') {
				return githubRepo
			}
		}
	}
	return value
}
