# DeepSeek Harness 手工接入（暂不正式支持）

[English](deepseek-harness.en.md)

[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)（命令名 `dsh`）是 DeepSeek 开源的编程智能体框架。**它还在开发者预览阶段（本文按 0.2.1-rc.2 写成并实测），Cohive 暂不正式支持**：`cohive init` 不会为它生成配置，下面的步骤要每个人在自己电脑上手工做一次，dsh 升级后可能需要调整。

为什么不能像 Claude Code、Codex、OpenCode 那样由 `cohive init` 写进仓库：dsh 的 MCP 和插件配置只在家目录（`~/.dsh/`）里，不读仓库里的配置。所以**一个 dsh 档案（profile）只能连一个 Cohive 工作空间**。在多个工作空间的项目之间切换时，要为每个工作空间建一个档案，或者改配置里的工作空间名。

## 动手之前

- **Node 22.19 以上的 22 版，或 Node 24 以上**。Node 23 不在支持范围内：`dsh` 会直接退出，不输出任何内容。
- 本机有 `pnpm`（`dsh plugin` 用它装插件）。
- 已经装好 `cohive` 并配对过这台电脑，见 [安装 cohive 命令行](install-cli.md) 和 [新成员接入](onboarding.md)。再执行一次，为 dsh 签发令牌：

  ```bash
  cohive login --tools dsh
  ```

下文用网页档案 `web`（`dsh web` 启动的那个）举例。示例里的 `https://cohive.example.com` 和 `pay` 换成你们的服务地址和工作空间名，`/Users/you` 换成你的家目录。

## 第 1 步：接入 Cohive 的 MCP

把下面这段加进 `~/.dsh/profiles/web/cordis.patch.yml`。这个文件在你第一次用这个档案时生成，开头几行注释，正文只有一行 `[]`（空列表）：**把 `[]` 这一行换成下面的内容**，否则文件就不是合法的 YAML，dsh 会启动失败。文件里已经有别的条目时，把这段追加到末尾，原有内容保留；还没用过这个档案、文件不存在时，新建它：

```yaml
- insert:
    - id: mcp-cohive
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: cohive
        transport: stdio
        command: cohive
        args: [mcp-proxy, --server, https://cohive.example.com, --workspace, pay, --tool, dsh]
```

- dsh 里的工具名是 `mcp__cohive__cohive_inbox` 这样的写法。
- dsh 找不到 `cohive` 时，把 `command` 改成 `cohive` 的完整路径（`which cohive` 可查）。
- 配置里不含令牌：令牌由 `cohive mcp-proxy` 从本机凭据文件读取。

## 第 2 步：确认与会话内提醒

**不做这一步，dsh 会不经询问直接执行 Cohive 的全部工具，包括发出、接单、关闭这类会改变别人工作的操作。** dsh 没有“按工具名设成每次询问”的配置，要借官方的“Claude Code 钩子兼容”插件来做确认；会话内提醒也靠这个插件。

1. 安装插件。版本号写成与 `dsh --version` 相同的版本，这个包的 latest 标签指向的是旧版：

   ```bash
   dsh plugin --profile web add -w @deepseek-ai/dsh-hooks-claude-code@0.2.1-rc.2
   ```

   安装时会提示它“declares no dsh.bundle”，可以忽略。

2. 新建钩子文件 `~/.dsh/cohive-hooks.json`：

   ```json
   {
     "hooks": {
       "SessionStart": [{"hooks": [{"type": "command", "command": "cohive hook --server https://cohive.example.com --workspace pay --tool dsh", "timeout": 5}]}],
       "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "cohive hook --server https://cohive.example.com --workspace pay --tool dsh", "timeout": 5}]}],
       "PreToolUse": [{
         "matcher": "mcp__cohive__cohive_(submit_issue|accept_issue|close_issue|cancel_issue|delete_draft)",
         "hooks": [{"type": "command", "command": "echo '{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"ask\",\"permissionDecisionReason\":\"Cohive：影响他人工作，需要确认\"}}'"}]
       }]
     }
   }
   ```

3. 在第 1 步那段 `insert` 的列表里再加一项。路径要写绝对路径：相对路径是按启动 dsh 时所在的目录算的。

   ```yaml
       - id: cohive-hooks
         name: '@deepseek-ai/dsh-hooks-claude-code'
         config:
           configPath: /Users/you/.dsh/cohive-hooks.json
   ```

4. 重启 `dsh web`。改了钩子文件也要重启，它只在启动时读一次。

### 哪些工具需要确认

| 每次确认（`PreToolUse` 返回 ask） | 直接执行 |
|---|---|
| `cohive_submit_issue` 发出 | `cohive_inbox`、`cohive_get_issue`、`cohive_search` |
| `cohive_accept_issue` 接单、退回、转交 | `cohive_who_owns`、`cohive_find_member` |
| `cohive_close_issue` 验证后关闭或重开 | `cohive_draft_issue`、`cohive_update_issue` |
| `cohive_cancel_issue` 作废 | `cohive_request_verification` |
| `cohive_delete_draft` 删除草稿 | `cohive_watch_issue` |

这份清单与 `cohive init` 给 Claude Code、Codex、OpenCode 配的一致。

### 实际表现（0.2.1-rc.2 实测）

- **网页版**：输入框上方出现“等待审批”，只有“拒绝 / 允许一次”两个选项，没有“总是允许”。
- **全权限模式**（`danger-full-access`）：需要确认的工具会被直接拒绝，不会被放行。
- **无界面模式**（`dsh headless`）：没有确认渠道，同样拒绝。
- **会话内提醒**：会进入模型的上下文，但网页对话里不单独显示这一行。智能体会按 `/cohive` 技能的要求，在最终答复末尾另起一行告诉你，例如“Cohive 提醒：#12 tester-a 交给你处理，可用 /cohive 12 查看”。

## 第 3 步：`/cohive` 技能

dsh 读仓库里的 `.agents/skills/` 和 `.dsh/skills/`，**不读** `.claude/skills/`。

- 项目里执行过 `cohive init --tools codex` 或 `--tools opencode` 的，已经有 `.agents/skills/cohive/SKILL.md`。
- 其他项目可以从 [examples/opencode/.agents/skills/cohive](../../examples/opencode/.agents/skills/cohive) 复制一份。

新建的技能目录要重启 dsh 才会出现在输入 `/` 后的列表里。之后输入 `/cohive` 或 `/cohive 12` 即可。

## 隐私：建议关闭会话日志上传

dsh 在使用 DeepSeek 官方模型接口时（包括经你配置的网关转发），**默认会把会话日志随后续请求一起上传给 DeepSeek**。Cohive 的问题单内容会通过工具结果进入会话，包括标题、复现步骤、日志、证据。我们建议关闭，两种方法任选：

- 网页 **设置 → 通用**，关掉“在使用官方模型 API 时上传 Session Log”。
- 在 `~/.dsh/cordis.patch.yml`（对全部档案生效）里加下面这段（同样，文件里只有 `[]` 时替换掉它）：

  ```yaml
  - id: session-log-deepseek
    config:
      enabled: false
  ```

另外，dsh 只在你提交反馈时才上报遥测；不想上报可以设环境变量 `DSH_TELEMETRY_DISABLED=1`。

## 排查

- **会话里没有提醒**：执行 `cohive hook --check --server https://cohive.example.com --workspace pay --tool dsh`。
- **工具列表里没有 `mcp__cohive__*`**：dsh 首次连 MCP 失败时照常启动，只是不显示这些工具。先在终端里直接执行第 1 步 `args` 里的那条 `cohive mcp-proxy …` 命令，看能否启动。
- 依据和更多细节见调研报告 [2026-10-19-coding-tools-opencode-deepseek.md](../research/2026-10-19-coding-tools-opencode-deepseek.md)。
