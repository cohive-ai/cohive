# 第一阶段·里程碑四：登录码、cohive login 与 cohive init 实现计划

**目标：** 成员私聊机器人 `/login` 领取一次性登录码，在自己电脑上 `cohive login` 换取令牌存入本机凭据文件，再 `cohive init` 把 MCP 配置、确认规则、`/cohive` 技能写进项目。令牌不出现在聊天记录和项目文件里。

**依据：** [docs/plan.md](../../plan.md) 第 5.2 节；2026-10-15 与 lee 确认的方案

## 全局约束

- 登录码：一次性、10 分钟有效、只存哈希；格式 `XXXX-XXXX`，字母表去掉易混字符（约 40 位熵）。
- 只在**私聊**里发登录码；群里发 `/login` 只提示改为私聊。
- 成员与 Telegram 账号按**用户编号**绑定（用户名可改、可被他人注册）；用户名仍用于群里 @。
- 一个 Telegram 账号绑定多个工作空间时，`/login` 列出工作空间，用 `/login <工作空间名>` 选择。
- 换取接口不需要令牌，用登录码鉴权；全局失败次数限流（每分钟 30 次）防暴力尝试。
- 管理员命令行签发令牌保留，作为备用。
- 项目配置文件不含任何秘密：MCP 一律配置为本机命令 `cohive mcp-proxy`，由它读取 `~/.config/cohive/credentials.json`（权限 600）后转发。
- `cohive init` 只改动自己管理的部分（JSON 里的 `cohive` 条目、TOML 里带标记的块），重复执行结果相同。

## 设计

**数据（迁移 0005）：** `members.telegram_user_id bigint`（加索引）；新表 `login_codes(id, member_id, code_hash bytea unique, expires_at, used_at, created_at)`。

**服务端：**
- `core.IssueLoginCode(ctx, telegramUserID, workspace) → LoginCodeResult{Code, Member, Workspace, Choices}`
- `core.ExchangeLoginCode(ctx, code, tool) → (token, Identity)`：单条 UPDATE 原子消费。
- `POST /api/v1/login {code, tool}` → `{token, member, workspace}`；不在令牌鉴权中间件之内。
- 机器人：`/login`（仅私聊），`/whoami` 增加用户编号。
- 管理命令：`member set --telegram-id <编号>`。

**命令行 `cohive`（cmd/cohive）：**

```
cohive login --server https://cohive.example.com --code ABCD-EFGH --tool codex
cohive init [--server …] [--workspace …] [--tools claude-code,codex]
cohive mcp-proxy --server … --workspace … --tool …     # 供编程工具调用，不需要手动运行
cohive status                                           # 列出本机保存的登录
```

`cohive init` 写入：
- Claude Code：`.mcp.json` 的 `cohive` 条目（`command: cohive`、`args: [mcp-proxy, …]`）；`.claude/settings.json` 的权限规则；`.claude/skills/cohive/SKILL.md`
- Codex：`.codex/config.toml` 中 `# >>> cohive` 与 `# <<< cohive` 之间的块；`.agents/skills/cohive/SKILL.md`

**转发器：** 从标准输入逐行读取 JSON-RPC 消息，带令牌 POST 到 `<server>/mcp`，把响应（JSON 或 SSE 的 data 行）逐行写回标准输出；通知类消息（无 id）返回 202 时不输出。

## 任务

1. 迁移 0005、成员 Telegram 用户编号、管理命令与 `/whoami` 输出编号。
2. 登录码签发与换取（业务层），含过期、重复使用、错误码、工作空间选择。
3. 换取接口与失败限流；机器人 `/login`。
4. `cohive login` 与本机凭据文件。
5. `cohive mcp-proxy` 转发器（与真实服务端联调测试）。
6. `cohive init`（内嵌技能文件，合并而非覆盖，可重复执行）。
7. 指南更新；真实环境实测：私聊机器人领码 → login → init → Claude Code 与 Codex 经转发器走通。
