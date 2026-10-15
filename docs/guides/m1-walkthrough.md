# 里程碑一操作指南：用 Claude Code 和 Codex 扮演两位成员走通闭环

这份指南带你在本机起一个 Cohive 协作服务，用 Claude Code 扮演前端开发 `lee-claude`、Codex 扮演订单服务负责人 `lee-codex`，人工走一遍：

> 起草 → **确认发出** → **确认接单** → 开始修复 → 请求验证 → **确认关闭**

加粗的三步会弹出确认，必须由你点头。

## 1. 启动服务

需要 Go 1.25、Node 20 以上、pnpm 和 Docker。

```bash
make db-up
```

```bash
make build
```

```bash
export COHIVE_DATABASE_URL='postgres://cohive:cohive@localhost:55432/cohive?sslmode=disable'
```

```bash
./bin/cohive-server serve
```

`make build` 会先构建看板，再编译服务端。启动后：

- 看板：`http://localhost:8787/`
- MCP：`http://localhost:8787/mcp`

内网其他机器访问时，把 `localhost` 换成本机内网地址。

## 看板

浏览器打开看板地址，粘贴任一成员的令牌登录。看板只读，看到的范围和这个成员在编程工具里一致，别人的草稿看不到。

- **谁手上有球**：按人分列，每张单出现在“现在该谁动手”的那一列。比如待验证的单在发起人那一列，待接单的在负责人那一列。轮到你的单子带琥珀色左边条。每 5 秒自动刷新，单子换到另一个人手上时会短暂高亮。
- **全部问题单**：按关键词和状态筛选，显示最近更新的 50 张。
- **详情**：字段、复现脚本，以及每一步是谁用哪个工具做的。

建议走第 5 节流程时，在浏览器里开着看板，观察单子在两列之间移动。

看板不加载任何外部资源，断网的内网也能打开。改看板代码时用 `make web-dev` 在 5173 端口热更新，接口会转发到本机 8787 端口。

## 2. 创建成员、令牌并登记服务

另开一个终端，设置同样的 `COHIVE_DATABASE_URL`，然后：

```bash
./bin/cohive-server member add --name lee-claude --display "Lee（Claude Code）"
```

```bash
./bin/cohive-server member add --name lee-codex --display "Lee（Codex）"
```

```bash
./bin/cohive-server service add --name order-api --owner lee-codex
```

```bash
./bin/cohive-server token issue --member lee-claude --tool claude-code
```

```bash
./bin/cohive-server token issue --member lee-codex --tool codex
```

令牌只显示一次，分别记下来。丢了就用 `token revoke` 吊销后重新签发。

## 3. 配置 Claude Code（lee-claude）

在你要用的项目目录里：

1. 复制 [examples/claude-code/.mcp.json](../../examples/claude-code/.mcp.json) 到项目根目录。
2. 把 [examples/claude-code/.claude/settings.json](../../examples/claude-code/.claude/settings.json) 的内容合并进项目的 `.claude/settings.json`。
3. 复制技能：把 [examples/skills/cohive](../../examples/skills/cohive) 整个目录复制到项目的 `.claude/skills/cohive`，之后在 Claude Code 里用 `/cohive` 或 `/cohive 12` 处理问题单。
4. 启动前设置令牌：`export COHIVE_TOKEN=<lee-claude 的令牌>`；经公网访问时再设 `export COHIVE_URL=https://你的域名/mcp`。
5. **项目目录必须被 Claude Code 信任过**（第一次在该目录交互式启动时接受信任提示）。没有信任的目录里，设置中的 `allow` 规则会被忽略，连只读工具都要逐个确认；`ask` 规则不受影响，照常生效。

## 4. 配置 Codex（lee-codex）

在另一个项目目录里：

1. 复制 [examples/codex/.codex/config.toml](../../examples/codex/.codex/config.toml) 到项目的 `.codex/config.toml`。
2. 复制技能：把 [examples/skills/cohive](../../examples/skills/cohive) 整个目录复制到项目的 `.agents/skills/cohive`，之后在 Codex 里用 `$cohive` 或 `$cohive 12` 处理问题单。
3. 启动前设置令牌：`export COHIVE_TOKEN=<lee-codex 的令牌>`。
4. **项目必须被 Codex 信任过**，否则不会读取项目级 `.codex/config.toml`。可以用 `codex mcp list` 在项目目录里确认是否出现 `cohive`。

群通知与公网访问的配置见 [群通知与公网访问](group-notify.md)。

## 5. 走一遍

| 步骤 | 在哪个工具里说 | 预期 |
|---|---|---|
| 1 | Claude Code：“联调发现下单接口返回 500，帮我查重后起草一张 Cohive 问题单给 order-api，附复现脚本。” | 调用 `cohive_search`、`cohive_draft_issue`，不弹确认 |
| 2 | Claude Code：“发出去吧。” | 调用 `cohive_submit_issue`，**弹出确认** |
| 3 | Codex：“看看 Cohive 里有什么待办。” | 调用 `cohive_inbox`，汇总要点和初筛意见 |
| 4 | Codex：“接单。” | 调用 `cohive_accept_issue`，**弹出确认** |
| 5 | Codex：“开始修，修好了请求验证，合并请求是 !12。” | 调用 `cohive_update_issue`、`cohive_request_verification`，不弹确认 |
| 6 | Claude Code：“看看待办，跑一下复现脚本，没问题就关掉。” | 调用 `cohive_inbox`，运行脚本，调用 `cohive_close_issue`，**弹出确认** |

全程的动态可以在任一工具里用 `cohive_get_issue` 查看，每条都记录了是哪个人的哪个工具做的。

## 6. 实测记录（2026-10-14）

在无界面模式（`claude -p`、`codex exec`）下，用本指南的配置做了自动化实测：

| 项目 | 结果 |
|---|---|
| Claude Code 只读工具与起草 | 直接放行，负责人按服务登记自动带上 |
| Claude Code 发出（未确认） | **被拦截**，问题单保持草稿 |
| Claude Code 命令行 `--allowedTools` 能否绕过 `ask` | 不能，项目设置的 `ask` 规则优先 |
| Codex 收件箱 | 直接放行，并给出初筛意见（指出缺少环境与日志） |
| Codex 接单（未确认） | **被拦截**：“MCP tool call requires approval, but approval policy is never” |
| Claude Code 关闭（未确认） | **被拦截** |
| 模拟确认后的完整闭环 | 走通，6 条事件的成员与工具全部正确 |

实测中发现并已修正：智能体把“修复后应返回的退出码”误解成“当前退出码”，字段已改名为 `repro_expected_exit_code` 并补充说明。

交互模式下的弹窗确认，需要你按第 5 节亲自走一遍确认。
