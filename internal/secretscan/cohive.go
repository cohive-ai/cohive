package secretscan

import (
	"regexp"
	"strings"
)

// Cohive 在 gitleaks 默认规则之外追加的规则与放行。每一条都有对应的命中或误报样例测试。

// addCohive 追加 Cohive 自己的令牌规则，并给通用规则补问题单场景的放行。
func (rs *ruleset) addCohive() error {
	// 成员令牌 cohive_ 与设备密钥 cohive_dev_，后接 32 字节的 URL 安全 base64（43 位）。
	// 智能体最可能误贴的就是本机凭据文件里的这两样。
	token, err := regexp.Compile(`(cohive_(?:dev_)?[A-Za-z0-9_-]{43})(?:[^A-Za-z0-9_-]|$)`)
	if err != nil {
		return err
	}
	rs.rules = append(rs.rules, &rule{id: "cohive-token", re: token, keywords: []string{"cohive_"}})

	// Telegram 机器人令牌：gitleaks 的规则要求附近有 telegr 字样，日志里常见的是裸令牌
	// （机器人编号 + 冒号 + 以 AA 开头的 35 位）。Cohive 的群通知就靠它，单独补一条。
	bot, err := regexp.Compile(`(?:^|[^0-9A-Za-z])([0-9]{8,10}:AA[A-Za-z0-9_-]{33})(?:[^A-Za-z0-9_-]|$)`)
	if err != nil {
		return err
	}
	rs.rules = append(rs.rules, &rule{id: "telegram-bot-token", re: bot, keywords: []string{":aa"}})

	extra := []*allowlist{
		{
			// 键名明显是标识或摘要、值是 UUID 或纯十六进制时，不是密钥：
			// request_id=<uuid>、commit_sha: <40 位>、checksum=<64 位> 之类。
			target: "match",
			and:    true,
			regexes: []*regexp.Regexp{regexp.MustCompile(
				`(?i)(?:^|[^a-z])(?:[a-z]+[_.-])*(?:id|uuid|guid|commit|sha(?:1|256|512)?|hash|digest|checksum|etag|nonce|request|trace|span|correlation)` +
					`[\s'"]{0,3}(?:=|:|=>)[\s'"\x60]{0,5}(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{7,128})(?:[\s'"\x60;,]|$)`)},
		},
		{
			// 明显的占位值：已脱敏标记、变量引用、示例值
			regexes: []*regexp.Regexp{regexp.MustCompile(
				`(?i)^(?:<[^>]*>|\[[^\]]*\]|\(redacted\)|redacted|[*x•]{3,}|changeme|change[_-]?me|your[_-]?(?:api[_-]?)?(?:key|token|secret)[_-]?here|example[_-]?(?:key|token|secret)|dummy[_-]?(?:key|token|secret)|placeholder|已脱敏|已隐藏|略)$`)},
		},
	}
	for _, r := range rs.rules {
		if r.id == genericRule {
			r.allowlists = append(r.allowlists, extra...)
		}
	}
	return nil
}

// categories 把常见规则编号前缀映射为中文类别，按前缀长度从长到短匹配。
var categories = []struct{ prefix, label string }{
	{"cohive-token", "Cohive 令牌"},
	{"generic-api-key", "通用密钥赋值"},
	{"private-key", "私钥"},
	{"jwt", "JWT 令牌"},
	{"aws", "AWS 密钥"},
	{"github", "GitHub 令牌"},
	{"gitlab", "GitLab 令牌"},
	{"bitbucket", "Bitbucket 凭据"},
	{"slack", "Slack 令牌"},
	{"stripe", "Stripe 密钥"},
	{"gcp", "Google Cloud 凭据"},
	{"google", "Google 密钥"},
	{"openai", "OpenAI 密钥"},
	{"anthropic", "Anthropic 密钥"},
	{"telegram", "Telegram 机器人令牌"},
	{"azure", "Azure 凭据"},
	{"alibaba", "阿里云密钥"},
	{"tencent", "腾讯云密钥"},
	{"npm", "npm 令牌"},
	{"pypi", "PyPI 令牌"},
	{"docker", "Docker 凭据"},
	{"heroku", "Heroku 密钥"},
	{"twilio", "Twilio 密钥"},
	{"sendgrid", "SendGrid 密钥"},
	{"mailgun", "Mailgun 密钥"},
	{"discord", "Discord 令牌"},
	{"hashicorp", "HashiCorp 令牌"},
	{"vault", "Vault 令牌"},
	{"cloudflare", "Cloudflare 密钥"},
	{"digitalocean", "DigitalOcean 令牌"},
	{"huggingface", "Hugging Face 令牌"},
	{"sentry", "Sentry 令牌"},
	{"shopify", "Shopify 令牌"},
	{"square", "Square 令牌"},
	{"sourcegraph", "Sourcegraph 令牌"},
}

func category(id string) string {
	best := ""
	label := "密钥或令牌"
	for _, c := range categories {
		if strings.HasPrefix(id, c.prefix) && len(c.prefix) > len(best) {
			best, label = c.prefix, c.label
		}
	}
	return label
}
