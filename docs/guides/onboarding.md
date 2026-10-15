# 新成员接入

新成员接入只需要三步，全程令牌不会出现在聊天记录或项目文件里。

## 管理员先做的事（每位成员一次）

1. 在工作空间里添加成员：

   ```bash
   ./bin/cohive-server member add --workspace payments --name ann --display "Ann"
   ```

2. 请成员私聊机器人发 `/whoami`，机器人会回复其 Telegram 用户名、用户编号，以及一条可直接复制的绑定命令：

   ```bash
   ./bin/cohive-server member set --workspace payments --name ann --telegram ann_dev --telegram-id 7012345678
   ```

   - 用户编号用来确认“私聊领登录码的是本人”（用户名可以改，也可能被别人注册，所以不用它鉴权）。
   - 用户名用来在群通知里 @ 到人。
   - 同一个工作空间里，一个 Telegram 账号只能绑定一个成员；同一个人在不同工作空间可以分别绑定。
   - **请确认用户编号来自本人和机器人的私聊**。如果有人把自己的编号报成别人的，就能冒领那位成员的身份。

## 成员自己做的三步

**1. 安装 cohive 命令行**（编程工具要能在 PATH 里找到它）：

```bash
make install-cli
```

**2. 私聊机器人发 `/login`**，拿到一次性登录码（10 分钟有效，只能用一次），在自己电脑上执行机器人给出的命令：

```bash
cohive login --server https://cohive.example.com --code ABCD-EFGH --tool codex
```

用 Claude Code 就把 `--tool` 换成 `claude-code`；两个工具都用，就领两次码、登录两次。加入了多个工作空间时，机器人会让你用 `/login 工作空间名` 选择。

令牌保存在 `~/.config/cohive/credentials.json`，只有你本人可读。`cohive status` 可以查看已登录的工作空间（不显示令牌）。

**3. 在项目目录执行**：

```bash
cohive init
```

它会写入：

| 工具 | 文件 |
|---|---|
| Claude Code | `.mcp.json`（`cohive` 条目）、`.claude/settings.json`（确认规则）、`.claude/skills/cohive/SKILL.md` |
| Codex | `.codex/config.toml`（`# >>> cohive` 与 `# <<< cohive` 之间的一段）、`.agents/skills/cohive/SKILL.md` |

- 只改动自己管理的部分，原有配置保留；重复执行结果相同。
- 这些文件**不含令牌**：编程工具通过本机命令 `cohive mcp-proxy` 连接协作服务，令牌由它从本机凭据文件读取。所以可以放心提交进仓库，团队共用；每个人用自己的登录。
- 第一次在 Claude Code、Codex 里打开项目时，会询问是否信任项目、是否启用 `cohive` 这个 MCP 服务，都选同意。没有信任的项目里，确认规则以外的放行规则不会生效。
- `.claude/settings.json` 里的 `enabledMcpjsonServers` 会随仓库共享：以后有人改动 `.mcp.json` 里 cohive 的启动命令，其他成员不会再被单独询问。和“提交者能改钩子”是同一级别的信任，审查合并请求时留意这两个文件的改动。
- `.mcp.json` 和 `.codex/config.toml` 里有协作服务地址，公开仓库要注意。
- `cohive init` 不会跟随符号链接写入；原有配置类型不对（例如 `permissions.allow` 不是数组）时会报错而不是覆盖。

之后在 Claude Code 里用 `/cohive`、在 Codex 里用 `$cohive` 查看和处理分给你的问题单。

## 常见问题

- **登录码提示无效、已使用或已过期**：私聊机器人重新发 `/login` 领新码。
- **编程工具里提示“Cohive 令牌无效或已吊销”**：令牌被管理员吊销了，重新领码登录即可。
- **机器人回复“还没有绑定”**：把回复里的那行命令发给管理员执行。
- **没有 Telegram 的成员**：管理员用 `cohive-server token issue` 签发令牌，通过可靠渠道交给成员；成员把令牌从标准输入传给 `cohive login`（不会进 shell 历史），之后照常 `cohive init`：

  ```bash
  pbpaste | cohive login --server https://cohive.example.com --token-stdin
  ```

- **电脑丢失或令牌泄露**：请管理员用 `cohive-server token list` 找到对应令牌并 `token revoke`；重新领码登录会签发新令牌，但不会自动吊销旧的。
- **重新领码**：新码签发后，之前没用掉的码立即作废，同一成员同时只有一个有效码。
