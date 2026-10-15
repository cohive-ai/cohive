// Package skill 内嵌 /cohive 技能文件，供 cohive init 写入项目。
package skill

import _ "embed"

// Cohive 是 /cohive（Claude Code）与 $cohive（Codex）共用的技能内容。
//
//go:embed SKILL.md
var Cohive []byte
