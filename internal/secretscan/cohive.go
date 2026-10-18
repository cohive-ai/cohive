package secretscan

import (
	"regexp"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
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
				`(?i)^(?:<[^>]*>|\[[^\]]*\]|\(redacted\)|redacted|[*x•]{3,}|changeme|change[_-]?me|your[_-]?(?:api[_-]?)?(?:key|token|secret)[_-]?here|example[_-]?(?:key|token|secret)|dummy[_-]?(?:key|token|secret)|placeholder|已脱敏|已隐藏|略)$`)}, // i18n-ignore：匹配内容里写的中文占位词，不是展示文案
		},
	}
	for _, r := range rs.rules {
		if r.id == genericRule {
			r.allowlists = append(r.allowlists, extra...)
		}
	}
	return nil
}

// categories 把常见规则编号前缀映射为类别的文案键（语言包里的名称），按前缀长度从长到短匹配。
var categories = []struct{ prefix, key string }{
	{"cohive-token", "secretscan.rule.cohive_token"},
	{"generic-api-key", "secretscan.rule.generic_api_key"},
	{"private-key", "secretscan.rule.private_key"},
	{"jwt", "secretscan.rule.jwt"},
	{"aws", "secretscan.rule.aws"},
	{"github", "secretscan.rule.github"},
	{"gitlab", "secretscan.rule.gitlab"},
	{"bitbucket", "secretscan.rule.bitbucket"},
	{"slack", "secretscan.rule.slack"},
	{"stripe", "secretscan.rule.stripe"},
	{"gcp", "secretscan.rule.gcp"},
	{"google", "secretscan.rule.google"},
	{"openai", "secretscan.rule.openai"},
	{"anthropic", "secretscan.rule.anthropic"},
	{"telegram", "secretscan.rule.telegram"},
	{"azure", "secretscan.rule.azure"},
	{"alibaba", "secretscan.rule.alibaba"},
	{"tencent", "secretscan.rule.tencent"},
	{"npm", "secretscan.rule.npm"},
	{"pypi", "secretscan.rule.pypi"},
	{"docker", "secretscan.rule.docker"},
	{"heroku", "secretscan.rule.heroku"},
	{"twilio", "secretscan.rule.twilio"},
	{"sendgrid", "secretscan.rule.sendgrid"},
	{"mailgun", "secretscan.rule.mailgun"},
	{"discord", "secretscan.rule.discord"},
	{"hashicorp", "secretscan.rule.hashicorp"},
	{"vault", "secretscan.rule.vault"},
	{"cloudflare", "secretscan.rule.cloudflare"},
	{"digitalocean", "secretscan.rule.digitalocean"},
	{"huggingface", "secretscan.rule.huggingface"},
	{"sentry", "secretscan.rule.sentry"},
	{"shopify", "secretscan.rule.shopify"},
	{"square", "secretscan.rule.square"},
	{"sourcegraph", "secretscan.rule.sourcegraph"},
}

// CategoryIn 返回规则所属类别的名称，按指定语言；认不出的规则归为“密钥或令牌”。
func CategoryIn(l i18n.Lang, ruleID string) string {
	best := ""
	key := "secretscan.rule.default"
	for _, c := range categories {
		if strings.HasPrefix(ruleID, c.prefix) && len(c.prefix) > len(best) {
			best, key = c.prefix, c.key
		}
	}
	return l.T(key)
}

// category 是固定中文的旧函数，调用方迁移到 CategoryIn 后删除。
func category(id string) string { return CategoryIn(i18n.ZhCN, id) }
