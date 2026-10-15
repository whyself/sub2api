package service

import "testing"

func TestUpdateRepositorySupportsForkAndRejectsPathTraversal(t *testing.T) {
	for _, test := range []struct{ value, expected string }{{"whyself/sub2api", "whyself/sub2api"}, {"", githubRepo}, {"../sub2api", githubRepo}, {"whyself/sub2api/extra", githubRepo}, {"https://github.com/whyself/sub2api", githubRepo}} {
		t.Setenv("SUB2API_UPDATE_REPOSITORY", test.value)
		if actual := updateRepository(); actual != test.expected {
			t.Fatalf("发布仓库校验错误：%s", actual)
		}
	}
}
