package secretscan

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// 样例里的“密钥”都在运行时按格式随机生成，源码里不出现形似真密钥的字面量，
// 免得代码平台的密钥扫描与本仓库自己的扫描把测试文件当成泄露。
var rng = rand.New(rand.NewSource(20261017))

func pick(charset string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}

const (
	upper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lower  = "abcdefghijklmnopqrstuvwxyz"
	digits = "0123456789"
	alnum  = upper + lower + digits
	b32    = upper + "234567"
	urlb64 = alnum + "_-"
	hexs   = "0123456789abcdef"
)

func uuidLike() string {
	return pick(hexs, 8) + "-" + pick(hexs, 4) + "-" + pick(hexs, 4) + "-" + pick(hexs, 4) + "-" + pick(hexs, 12)
}

func TestRulesLoad(t *testing.T) {
	if n := RuleCount(); n < 200 {
		t.Fatalf("应加载 gitleaks 的两百多条规则，得到 %d", n)
	}
}

// 计划 2.2 命中样例集：每条都必须命中，且规则编号符合预期。
func TestTruePositives(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + pick(alnum+"+/", 64) + "\n" + pick(alnum+"+/", 64) + "\n-----END RSA PRIVATE KEY-----"
	jwt := "ey" + pick(alnum, 30) + ".ey" + pick(alnum, 40) + "." + pick(urlb64, 43)
	cases := []struct {
		name, text, rule string
	}{
		{"AWS", "aws configure 时用的是 " + "AKIA" + pick(b32, 16), "aws-access-token"},
		{"GitHub", "export GH=" + "ghp_" + pick(alnum, 36), "github-pat"},
		{"GitLab", "token: " + "glpat-" + pick(alnum, 20), "gitlab-pat"},
		{"Slack", "SLACK=" + "xoxb-" + pick(digits, 12) + "-" + pick(digits, 12) + "-" + pick(alnum, 24), "slack-bot-token"},
		{"Stripe", "stripe " + "sk_live_" + pick(alnum, 24) + " ", "stripe-access-token"},
		{"Google", "key=" + "AIza" + pick(urlb64, 35) + " ", "gcp-api-key"},
		{"OpenAI", "OPENAI=" + "sk-proj-" + pick(urlb64, 74) + "T3BlbkFJ" + pick(urlb64, 74), "openai-api-key"},
		{"Anthropic", "ANTHROPIC_API_KEY=" + "sk-ant-api03-" + pick(urlb64, 93) + "AA", "anthropic-api-key"},
		{"Telegram 带关键词", "COHIVE_TELEGRAM_BOT_TOKEN=" + pick(digits, 10) + ":A" + pick(urlb64, 34), "telegram-bot-api-token"},
		{"Telegram 裸令牌", "bot " + pick(digits, 10) + ":AA" + pick(urlb64, 33) + " 返回 401", "telegram-bot-token"},
		{"私钥", "id_rsa 内容：\n" + pem, "private-key"},
		{"JWT", "Authorization: Bearer " + jwt, "jwt"},
		{"通用赋值", "DB_PASSWORD=" + pick(alnum, 6) + "9" + pick(alnum, 18), "generic-api-key"},
		// 十六进制的真密钥不能被“标识、摘要”的放行误放过：键名是 token、secret 时照样拦
		{"十六进制访问令牌", "access_token: " + pick(hexs, 40), "generic-api-key"},
		{"十六进制客户端密钥", "client_secret = \"" + pick(hexs, 32) + "\"", "generic-api-key"},
		{"Cohive 令牌", "credentials.json 里是 " + "cohive_" + pick(urlb64, 43), "cohive-token"},
		{"Cohive 设备密钥", "\"device_secret\": \"" + "cohive_dev_" + pick(urlb64, 43) + "\"", "cohive-token"},
	}
	for _, c := range cases {
		got := Scan(Field{Name: "evidence", Text: c.text})
		found := false
		for _, f := range got {
			if f.RuleID == c.rule {
				found = true
			}
		}
		if !found {
			t.Errorf("%s：应命中规则 %s，得到 %+v", c.name, c.rule, got)
		}
	}
}

// 计划 2.2 误报样例集：都不应命中。
func TestFalsePositives(t *testing.T) {
	sha40 := pick(hexs, 40)
	sha256 := pick(hexs, 64)
	cases := []struct{ name, text string }{
		{"UUID", "订单 " + uuidLike() + " 创建失败"},
		{"request_id", "request_id=" + uuidLike() + " status=500"},
		{"trace_id JSON", `{"trace_id": "` + pick(hexs, 32) + `", "level": "error"}`},
		{"提交号", "commit " + sha40 + "\nAuthor: lee"},
		{"commit_sha 赋值", "commit_sha: " + sha40},
		{"镜像摘要", "image: registry.example.com/api@sha256:" + sha256},
		{"checksum", "checksum=" + sha256},
		{"go.sum", "github.com/jackc/pgx/v5 v5.11.0 h1:" + pick(alnum+"+/", 43) + "="},
		{"npm integrity", `"integrity": "sha512-` + pick(alnum+"+/", 86) + `=="`},
		{"已打码密码", "password: ******"},
		{"变量引用", "api_key=${API_KEY}\ntoken: $GITHUB_TOKEN"},
		{"脱敏占位", "Authorization: Bearer <REDACTED>\nsecret: [已脱敏]\napi_key = your-api-key-here"},
		{"Go 堆栈", "panic: runtime error: invalid memory address or nil pointer dereference\n" +
			"goroutine 1 [running]:\nmain.(*Server).handleToken(0xc000123456, {0x1029a2f40, 0xc0001b4000})\n\t/app/server.go:88 +0x1c4"},
		{"Python 堆栈", "Traceback (most recent call last):\n  File \"/srv/app/auth.py\", line 42, in refresh_token\n    raise KeyError('access_token')\nKeyError: 'access_token'"},
		{"内网地址", "连接 http://10.0.3.17:8080/api/v1/orders 超时；postgres://db.internal:5432/orders"},
		{"单号与版本", "问题单 #42 的版本已变化：你提供的是 3，当前是 4"},
		{"日志里的 token 计数", "max_tokens=4096 prompt_tokens=1532 completion_tokens=880"},
		{"etag", "ETag: \"" + pick(hexs, 32) + "\""},
		// 键名带 token、auth、api 等字样但其实是标识或摘要：gitleaks 的通用规则会报，靠 Cohive 追加的放行
		{"token_id", "token_id=" + uuidLike() + " revoked"},
		{"auth_request_id", `{"auth_request_id": "` + uuidLike() + `"}`},
		{"api_trace_id", "api_trace_id: " + pick(hexs, 32)},
		{"token 摘要", "session_token_sha256=" + pick(hexs, 64)},
		// 低熵的值不是密钥：靠熵阈值
		{"低熵赋值", "secret_key=aaaabbbbaaaabbbb12\npassword=1111111111ab"},
	}
	for _, c := range cases {
		if got := Scan(Field{Name: "evidence", Text: c.text}); len(got) > 0 {
			t.Errorf("%s：不应命中，得到 %+v", c.name, got)
		}
	}
}

func TestFieldLineAndDedupe(t *testing.T) {
	tok := "ghp_" + pick(alnum, 36)
	ev := "第一行\n第二行\n  GITHUB_TOKEN=" + tok + "\n第四行"
	got := Scan(Field{Name: "title", Text: "普通标题"}, Field{Name: "evidence", Text: ev}, Field{Name: "note", Text: tok + "\n" + tok})
	if len(got) != 3 {
		t.Fatalf("应有 3 处（evidence 第 3 行、note 第 1、2 行；通用规则与具体规则同行只报具体规则），得到 %+v", got)
	}
	want := []Finding{
		{Field: "evidence", Line: 3, RuleID: "github-pat"},
		{Field: "note", Line: 1, RuleID: "github-pat"},
		{Field: "note", Line: 2, RuleID: "github-pat"},
	}
	for i := range want {
		if got[i] != want[i] || got[i].CategoryIn(i18n.ZhCN) != "GitHub 令牌" {
			t.Errorf("第 %d 处应为 %+v，得到 %+v", i, want[i], got[i])
		}
	}
}

func TestCategoryFallback(t *testing.T) {
	if c := CategoryIn(i18n.ZhCN, "some-new-rule"); c != "密钥或令牌" {
		t.Fatalf("未登记类别的规则应显示通用说法，得到 %q", c)
	}
	if c := CategoryIn(i18n.ZhCN, "github-fine-grained-pat"); c != "GitHub 令牌" {
		t.Fatalf("前缀匹配：%q", c)
	}
}

// 结果结构里不存在密钥内容：只有字段、行号与规则。
func TestFindingCarriesNoSecret(t *testing.T) {
	tok := "glpat-" + pick(alnum, 20)
	for _, f := range Scan(Field{Name: "note", Text: "token " + tok}) {
		s := f.Field + f.RuleID + f.CategoryIn(i18n.ZhCN)
		if strings.Contains(s, tok[6:14]) {
			t.Fatalf("命中结果不应带密钥片段：%+v", f)
		}
	}
}
