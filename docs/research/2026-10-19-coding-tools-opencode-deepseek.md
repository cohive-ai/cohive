# opencode 与 DeepSeek Harness 适配调研

日期：2026-10-19 · 性质：调研加本机实测，未写实现代码 · 适用：决定 `cohive init --tools` 是否新增第三、第四种编程工具之前

**实测环境**：macOS arm64；本机自建的 Cohive 服务和单独的走查库；两个走查成员，每人一个隔离的家目录（不读写真实的编程工具配置和 Cohive 凭据）。模型用本机写的假模型服务：它兼容 OpenAI 接口，按提示词里的指令发起指定的工具调用，并记录编程工具发给模型的每一次请求。所以“模型实际收到什么”可以直接看到，全程不需要任何模型密钥。没有注册账号，没有填 DeepSeek 密钥，没有连测试服。

**标注约定**：

- “实测”：在上述环境里跑通并看到了结果。
- “源码”：读了对应版本的源码确认，没有跑。
- “文档”：只有官方文档的说法。
- “推断”：根据前三者推出来的。
- “未核实”：没找到依据。

---

## 一页结论

**两个对象是什么**

- **opencode**：就是原来的 `sst/opencode`，仓库已改名为 [anomalyco/opencode](https://github.com/anomalyco/opencode)，旧地址会自动跳转。MIT 许可证，约 21.7 万星，发版很频繁。最新版 1.19.3（2026-10-16，npm 包名 `opencode-ai`）。本机 Homebrew 装的是 1.18.29；关键项在 1.19.3 上又复测了一遍，结论一致。
- **“DeepSeek harness”**：最可能指 DeepSeek 官方 2026-08 开源的 **DeepSeek Harness**：仓库 [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)，命令名 `dsh`，npm 包 `@deepseek-ai/dsh`，MIT 许可证，约 25.4 万星。当前版本 0.2.1-rc.2，官方标注为“开发者预览、会有破坏兼容的变更”。另外两种理解见第 1 节，**需要 lee 确认**（第 5 节第 1 问）。

**opencode：建议做，而且是目前四家里最容易接的。** Cohive 需要的四项能力它都有，并已在本机全部跑通：

1. **MCP**：支持本机命令方式，配置写在仓库根目录的 `opencode.json`。`cohive mcp-proxy --tool opencode` 能直接连上，服务端也接受 `opencode` 这个工具名签发令牌。
2. **按工具确认**：`opencode.json` 的 `permission` 可以按工具名设 `ask`、`allow`。终端界面弹出的确认框有“仅本次 / 总是允许 / 拒绝”三个选项。“总是允许”只存在内存里，进程重启就失效，不像 Claude Code 那样永久写入本地。`--auto` 全自动模式**会绕过**确认（实测“发出”被直接执行），与 Claude Code、Codex 相同，按决策 D13 属于尽力而为。
3. **`/cohive`**：opencode 会自动读 `.claude/skills`、`.agents/skills`、`.opencode/skills` 下的 SKILL.md，每个技能自动变成斜杠命令，`/cohive 2` 会把“2”传进去。也就是说，Claude Code、Codex 已有的技能文件它直接就能用。
4. **会话内提醒**：opencode 没有 Claude Code 格式的钩子，但有插件（仓库里 `.opencode/plugins/` 下的 TS/JS 文件，启动时自动加载）。原型插件做到了：
   - 用户每次提交时调用 `cohive hook`，把提醒作为隐藏的附加文本插进这条用户消息。模型看得到，用户的消息气泡里不显示。
   - 同时在终端界面右上角弹出一行提示条。
   - 新会话摘要和会话中途来新单都实测通过。

**DeepSeek Harness：能接，但和 Cohive“按项目配置”的模式不契合，建议暂缓，先只写一页手工接入说明。**

- **能做到的**（都实测过）：
  - 本机命令方式的 MCP，工具名 `mcp__cohive__*`，和 Claude Code 一样。
  - 用官方的“Claude Code 钩子兼容”插件，现有的 `cohive hook` 原样可用：新会话提醒能注入，也能按工具名要求确认。网页版弹出“等待审批 · 拒绝 / 允许一次”。
  - 全权限模式下需确认的工具会被**拒绝**，而不是放行。四家里只有它这样，最符合 Cohive 的本意。
  - 读 `.agents/skills`（和 Codex 共用），`/cohive` 能用。
  - 读 AGENTS.md。
- **主要障碍**：
  - MCP 和钩子配置**只有用户级**（家目录下的 `~/.dsh/`），没有仓库内自动发现。一个进程只能接一个 Cohive 工作空间、一份钩子配置，而网页版一个进程会同时开多个项目。
  - 钩子兼容插件不随 dsh 安装，要用户自己 `dsh plugin add`，而且这个插件属于实验组。
  - 网页对话里看不到注入的提醒那一行，只能靠模型在答复末尾写出来（和 Codex 桌面版一样）。
  - 要求 Node 22.19 以上或 24 以上。Node 23 下会静默退出、没有任何输出（实测）。
  - 用 DeepSeek 官方接口时，默认会把会话日志上传给 DeepSeek，其中可能包含问题单内容（文档）。
  - 整体是开发者预览，插件接口不承诺稳定。

**如果 lee 说的“DeepSeek harness”其实是“用 DeepSeek 模型驱动 Claude Code 或 opencode”**：那适配对象还是 Claude Code 或 opencode 本身，Cohive 不用做任何事。换模型不影响 MCP、确认、技能、钩子。

---

## 1. 先弄清对象：“DeepSeek harness”可能指什么

| 候选 | 是什么 | 依据 | 对 Cohive 意味着什么 |
|---|---|---|---|
| A. **DeepSeek Harness（`dsh`）** | DeepSeek 官方开源的智能体运行框架，口号“一切皆插件”。形态有本地网页（`dsh web`，主形态）、无界面单次任务（`dsh headless`）、ACP（给编辑器接入用）、SDK、Electron 桌面版；终端界面不在主仓库里 | GitHub 仓库与 npm 包（2026-10-19 查询）；本机实测 | 一个新的编程工具，要单独适配，见第 2、4 节 |
| B. 用 DeepSeek 模型驱动现成工具 | DeepSeek 提供 Anthropic 兼容接口（dsh 内置的 DeepSeek 适配器默认就用 `api.deepseek.com/anthropic`），Claude Code 可以改接口地址后使用；opencode 内置 DeepSeek 模型提供方 | dsh 源码 `packages/llm/llm-deepseek`；opencode 模型目录 | 适配对象就是 Claude Code 或 opencode，不需要新工作 |
| C. 社区工具 | **Codewhale**（原名 DeepSeek-TUI，[codewhale-hq/Codewhale](https://github.com/codewhale-hq/Codewhale)，Rust 编写，MIT，约 4.1 万星）；“Deep Code CLI”等 | GitHub 搜索；只看了仓库简介，**能力未核实** | 名字不叫“harness”，可能性较低；需要的话另做调研 |

判断：名字与 A 完全吻合，而且 A 是 DeepSeek 官方 2026-08 发布、热度很高的项目，所以本报告按 A 展开。**请 lee 确认**（第 5 节第 1 问）。

---

## 2. 对照表

Claude Code、Codex 两列来自 Cohive 已有的适配（`cmd/cohive/init.go`、`docs/brain/decisions.md` 的“人工确认”“客户端实时提醒”两节），这次没有重测。

| 项 | Claude Code | Codex | opencode 1.19.3 | DeepSeek Harness 0.2.1-rc.2 |
|---|---|---|---|---|
| **1. MCP 方式** | 本机命令（`cohive mcp-proxy`） | 本机命令 | 本机命令（`type: "local"`）或远程 HTTP（可带请求头），**实测本机命令** | 本机命令（`transport: stdio`）或远程 streamable-http（可带请求头），**实测本机命令** |
| MCP 配置位置 | 仓库 `.mcp.json`（项目级） | 仓库 `.codex/config.toml`（项目级） | 仓库根 `opencode.json` 或 `.opencode/opencode.json`（项目级，向上找到 git 根为止），另有用户级 | **只有用户级**：`~/.dsh/cordis.patch.yml` 或 `~/.dsh/profiles/<档案>/cordis.patch.yml`；也可以每次启动用 `--patch <文件>` 手动指定 |
| 工具名 | `mcp__cohive__cohive_inbox` | `cohive_inbox` | `cohive_cohive_inbox`（服务名加下划线加工具名） | `mcp__cohive__cohive_inbox` |
| **2. 按工具确认** | `permissions.ask` / `allow` | `approval_mode = "prompt"` / `"approve"` | `permission` 里按工具名写 `"ask"` / `"allow"`，**实测** | 没有内置的按工具规则表；用钩子兼容插件的 `PreToolUse` 返回 `ask`，或写原生插件，**实测** |
| 没写规则的工具 | 询问 | 询问 | **直接放行**（大多数权限默认放行） | **直接放行**，所有模式下都不询问（实测“退回”被直接执行） |
| “总是允许” | 永久写入本地，之后不再问 | 类似 | 只到进程退出为止，**源码** | 没有这个选项，只有“允许一次” |
| 全自动模式 | auto 模式绕过 | “完全访问”绕过 | `--auto`、`--yolo` 绕过，只有显式 `deny` 仍然生效，**实测** | 全权限模式下，按工具配的询问一律**拒绝**，**实测** |
| **3. `/cohive`** | `.claude/skills/cohive/SKILL.md` → `/cohive` | `.agents/skills/cohive/SKILL.md` → `$cohive` | 读 `.opencode/skills`、`.claude/skills`、`.agents/skills`，技能自动成为 `/cohive`，参数追加在末尾，**实测** | 读 `.dsh/skills`、`.agents/skills`（**不读** `.claude/skills`），技能自动成为 `/cohive`；网页斜杠菜单里能看到，**实测**（新建的技能目录要重启 dsh 才出现） |
| **4. 会话开始、提交时注入** | 钩子 `SessionStart`、`UserPromptSubmit` | 同名钩子，需要在 `/hooks` 信任一次 | 没有命令钩子。插件的 `chat.message` 可以往用户消息里追加隐藏文本，`event` 能收到 `session.created`，**实测** | 钩子兼容插件直接跑 Claude Code 格式的钩子，**实测**；或写原生插件（`agent/created`、`agent/pre-step`） |
| 给人看的那一行 | `systemMessage` 显示 | 命令行版显示，桌面版不显示 | 插件调用 `client.tui.showToast`，终端界面右上角弹提示条，**实测**；桌面版、网页版**未核实** | 钩子兼容插件不显示 `systemMessage`（文档）；网页对话里没看到注入那一行，**实测** |
| 需要信任才运行 | Claude 桌面版要信任文件夹 | 每个克隆目录要在 `/hooks` 信任一次 | **不需要**：仓库里的 `opencode.json`、插件、MCP 命令打开即运行（源码，没有信任机制） | 不涉及（配置在用户目录） |
| **5. 指令文件** | CLAUDE.md | AGENTS.md | AGENTS.md；没有时退而读 CLAUDE.md，**实测** | AGENTS.md、CLAUDE.md，从 git 根一路读到当前目录，**实测** |
| **6. 形态** | 终端、桌面、编辑器插件 | 命令行、桌面 | 终端界面（主形态）、桌面版（Electron，测试版）、VS Code 插件（只是在内置终端里开终端界面）、ACP（Zed 等编辑器） | 本地网页（主形态）、无界面单次任务、ACP、SDK、Electron 桌面版；终端界面是仓库外的项目 |
| **7. 许可证与活跃度** | 商业 | Apache-2.0 | MIT，约 21.7 万星，几乎每天发版 | MIT，约 25.4 万星，开发者预览，接口不承诺稳定 |
| 网络 | — | — | 首次在含 `.opencode/` 的仓库启动时，会用 npm 在 `.opencode/` 下装插件依赖，并自己写一个 `.gitignore`（实测）；另外会拉模型目录、检查自动更新 | 安装和加插件走 npm；模型用 `api.deepseek.com`；默认上传会话日志给 DeepSeek（可关）；反馈时才上报遥测 |

东南亚团队访问 npm、GitHub、DeepSeek 接口都是直连，没有已知障碍。opencode 支持标准的 `HTTPS_PROXY`（[网络文档](https://opencode.ai/docs/network/)）。

---

## 3. 逐项依据

### 3.1 opencode

| 项 | 支持 | 怎么配、在哪 | 依据 | 是否实测 |
|---|---|---|---|---|
| MCP | 支持 | `opencode.json` → `"mcp": {"cohive": {"type": "local", "command": ["cohive", "mcp-proxy", ...]}}` | [MCP 文档](https://opencode.ai/docs/mcp-servers/)；工具名规则见源码 `packages/opencode/src/mcp/catalog.ts`（`sanitize(服务名) + "_" + sanitize(工具名)`） | 实测：`opencode mcp list` 显示 connected；只读工具执行成功；服务端记录的来源工具是 `opencode` |
| 按工具确认 | 支持 | `"permission": {"cohive_cohive_submit_issue": "ask", ...}`，规则按顺序匹配、最后一条命中的生效 | [权限文档](https://opencode.ai/docs/permissions/)；MCP 工具每次调用都会按工具名走一次权限判断（源码 `src/session/tools.ts`） | 实测：普通模式下“发出”“接单”触发询问；`opencode run` 非交互运行时自动拒绝；终端界面弹出三选项确认框 |
| 全自动绕过 | 会 | `--auto` / `--yolo` / `--dangerously-skip-permissions`，或在界面命令面板里开 | 权限文档“Auto mode”；源码 `src/cli/cmd/run.ts` | 实测：`--auto` 下“发出”被直接执行 |
| 插件能否强制确认 | 不能 | 插件接口里定义了 `permission.ask`，但 1.19.3 源码里**没有任何地方触发它**；`tool.execute.before` 可以抛错拦截，但没法改成“询问” | 源码搜索 | 源码 |
| 技能、命令 | 支持 | 技能：`.opencode/skills/<名>/SKILL.md`，兼容 `.claude/skills`、`.agents/skills`，同名时 `.opencode` 下的覆盖其他；命令：`.opencode/commands/*.md` | [技能文档](https://opencode.ai/docs/skills/)、[命令文档](https://opencode.ai/docs/commands/)；技能转命令见 `src/command/index.ts` | 实测：`opencode run --command cohive 2`，模型收到技能全文，末尾附“2” |
| 插件与注入 | 支持 | `.opencode/plugins/` 下的 TS 或 JS 文件启动时自动加载；`chat.message` 钩子拿得到这条用户消息的分片，可以往里追加 `synthetic: true` 的文本 | [插件文档](https://opencode.ai/docs/plugins/)；钩子签名见 npm 包 `@opencode-ai/plugin@1.19.3`；触发点在 `src/session/prompt.ts` | 实测：新会话摘要与中途新单的提醒都进了模型请求；终端界面用户气泡里不显示注入文本，右上角弹出“Cohive 提醒：你有 2 张待处理的单……” |
| 指令文件 | 支持 | 仓库 AGENTS.md，没有时用 CLAUDE.md；可用 `instructions` 字段追加文件 | [规则文档](https://opencode.ai/docs/rules/) | 实测：AGENTS.md 内容出现在系统提示里 |
| 桌面版、编辑器、ACP | — | 桌面版和终端界面共用同一套核心服务，配置和插件应当一样生效；VS Code 插件只是在内置终端里开终端界面；ACP 文档写的是“MCP、权限、AGENTS.md 都支持” | [IDE 文档](https://opencode.ai/docs/ide/)、[ACP 文档](https://opencode.ai/docs/acp/) | **未实测**：桌面版和 ACP 下提示条能否显示未核实 |

### 3.2 DeepSeek Harness

源码依据都在 [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)（提交 `687f282e`，2026-10-17）。

| 项 | 支持 | 怎么配、在哪 | 依据 | 是否实测 |
|---|---|---|---|---|
| MCP | 支持 | 在用户叠加层里 `insert` 一条 `@deepseek-ai/dsh-mcp-client`，写 `serverName`、`transport: stdio`、`command`、`args`。注意：子进程环境里名字含 KEY、PASSWORD、SECRET、TOKEN 的变量会被清掉，`cohive mcp-proxy` 读的是凭据文件，不受影响 | `packages/mcp/mcp-client/README.zh.md`；配置位置见 `docs/user/guide/mcp-memory.zh.md` | 实测：工具列表出现 `mcp__cohive__*`，只读工具执行成功 |
| 项目级配置 | **不支持** | 没有扫描仓库内 `.dsh/` 配置的代码，只能 `--patch` 手动指定 | `apps/cli/reference/README.zh.md`；子任务查了 `apps/cli/src`、`packages/boot` | 源码 |
| 按工具确认 | 间接支持 | 方法一：钩子兼容插件，`PreToolUse` 的匹配写 五个改变状态工具的完整名字（正则，例如 `mcp__cohive__cohive_close_issue`），命令输出 `permissionDecision: "ask"`。方法二：原生插件监听 `tools/pre-execute` 返回 `{kind: 'ask'}` | `docs/subsystems/approval.zh.md`；`packages/experimental/hooks-claude-code/README.zh.md` | 实测：网页版弹出“等待审批 · Cohive：影响他人工作，需要确认 · 拒绝 / 允许一次”；无界面模式没有确认渠道，直接拒绝 |
| 全权限模式 | 不绕过 | `danger-full-access` 的审批策略是 `never`，意思是“所有询问一律拒绝” | `docs/subsystems/approval.zh.md`；`packages/bundle/base/cordis.patch.yml` | 实测：`DSH_PERMISSION_MODE=danger-full-access` 下“关闭”被拒绝 |
| 技能 | 支持 | `.dsh/skills` 或 `.agents/skills` 下的 `<名>/SKILL.md`；不默认读 `.claude/skills` | `docs/subsystems/skills.zh.md`、`packages/skill/skill-filesystem/README.zh.md` | 实测：重启后网页斜杠菜单出现 cohive |
| 自定义命令文件 | 不支持 | 只能由插件代码注册命令，没有 markdown 命令文件 | `docs/subsystems/commands.zh.md` | 源码 |
| 钩子注入 | 支持（实验插件） | 1. `dsh plugin --profile <档案> add -w @deepseek-ai/dsh-hooks-claude-code`（需要 pnpm；不带 `-w` 会报错；装上时提示它没有组合包声明，只按普通依赖安装）。2. 在叠加层里 `insert` 这个插件，`configPath` 指向钩子文件。整个进程只读一份、启动时读一次，相对路径按启动目录算 | `packages/experimental/hooks-claude-code/README.zh.md`、`src/index.ts` | 实测：`cohive hook` 原样可用，新会话摘要进了模型请求（排在用户消息之前） |
| 指令文件 | 支持 | AGENTS.md、CLAUDE.md 及 `.local.md` 变体 | `packages/context/agent-instructions/README.zh.md` | 实测 |
| 形态差别 | — | 审批面板只有网页和桌面版有；无界面模式下询问一律变拒绝；桌面版和命令行共用设置与会话，但“插件激活彼此隔离”，所以桌面版能否装钩子兼容插件**未核实** | `apps/desktop/README.zh.md` 已知限制 | 网页与无界面实测；桌面版未测 |
| 运行环境 | — | Node 22.19 以上的 22 版，或 24 以上 | 根目录 `package.json` 的 engines | 实测：Node 23.7 下 `dsh --version` 无任何输出、退出码 0；Node 24 正常 |
| 隐私 | — | 默认把会话日志随后续 DeepSeek 请求一起上传，可在网页“设置 → 通用”关闭 | `apps/cli/reference/README.zh.md` | 文档 |

---

## 4. 建议的适配方案

### 4.1 opencode：`cohive init --tools opencode`

**新增取值**：`opencode`。令牌按“人 + opencode + 工作空间”签发，服务端不用改（工具名只校验格式，实测已能签发）。

**生成的文件**（都在仓库里，和另外两家一样随仓库提交）：

1. **`opencode.json`**（新建或合并，只动 `mcp.cohive` 和 `permission` 里 `cohive_cohive_*` 这几项）：
   ```json
   {
     "$schema": "https://opencode.ai/config.json",
     "mcp": {
       "cohive": {
         "type": "local",
         "command": ["cohive", "mcp-proxy", "--server", "<服务地址>", "--workspace", "<工作空间>", "--tool", "opencode"]
       }
     },
     "permission": {
       "cohive_cohive_inbox": "allow",
       "cohive_cohive_get_issue": "allow",
       "cohive_cohive_search": "allow",
       "cohive_cohive_who_owns": "allow",
       "cohive_cohive_find_member": "allow",
       "cohive_cohive_draft_issue": "allow",
       "cohive_cohive_update_issue": "allow",
       "cohive_cohive_request_verification": "allow",
       "cohive_cohive_submit_issue": "ask",
       "cohive_cohive_accept_issue": "ask",
       "cohive_cohive_close_issue": "ask",
       "cohive_cohive_cancel_issue": "ask",
       "cohive_cohive_delete_draft": "ask"
     }
   }
   ```
   - 只读工具要显式写 `allow`：用户如果在全局写了 `"*": "ask"`，没写规则的只读工具也会每次询问。
   - 仓库里如果只有带注释的 `opencode.jsonc`，按原样改写会丢掉注释，`init` 应该停下来，打印要手工合并的片段。
2. **`.opencode/plugins/cohive.ts`**：会话内提醒插件，约 40 行，由 `cohive` 程序内嵌并写出，和技能文件同样处理。逻辑和原型一致：
   - 收到 `session.created` 时记下这是新会话。
   - `chat.message` 时调用 `cohive hook --server … --workspace … --tool opencode`，标准输入沿用现有格式（`session_id`、`hook_event_name`）；新会话按 `SessionStart`，其余按 `UserPromptSubmit`。
   - 有 `additionalContext` 就追加为隐藏文本；有 `systemMessage` 就用 `client.tui.showToast` 弹提示条。
   - 5 秒兜底超时，任何失败都静默。
3. **`.opencode/skills/cohive/SKILL.md`**：沿用 `internal/skill` 的中英文版本。仓库里已经有 `.claude/skills/cohive` 或 `.agents/skills/cohive` 时，opencode 会读到重复的同名技能，只记一条警告，`.opencode` 下的那份生效（源码）。所以照写不会出问题。

**要改的代码**：

- `cmd/cohive/init.go`：`knownTools` 加 `opencode`，取值校验，新增 `initOpencode`（合并 JSON，写插件与技能）。
- `cmd/cohive/hook.go`：`--tool` 接受 `opencode`；提醒里的调用写法用 `/cohive N`（现在只有 codex 用 `$cohive`）。
- `cmd/cohive/login.go`：`readRepoTarget` 增加从 `opencode.json` 读服务地址和工作空间；`knownToolsFirst` 的固定顺序也要加上 opencode。
- `internal/notify/render.go`：`toolLabel` 加“opencode”。
- 中英文案（`cli.init.*`、`cli.login.repo_ready`、群通知里“Codex 用 $cohive”的说明）、`examples/opencode/`、接入文档。
- 测试：沿用 `init_test.go`、`init_hook_test.go` 的写法；再加一个插件冒烟测试（可选，需要本机有 opencode）。

**做不到的和降级办法**：

| 做不到 | 降级 |
|---|---|
| `--auto` 全自动模式会绕过确认 | 与另外两家相同，按 D13 不处理，在接入文档里说明 |
| 桌面版、网页版、ACP 下的提示条未核实 | 提醒的主渠道本来就是“模型在最终答复末尾写出来”，提示条只是加分项。上线前请 lee 在桌面版看一眼 |
| 插件接口 1.x 仍在改：主分支正在重构 v2，`permission.ask` 定义了却从不触发 | 插件只用 `chat.message`、`event`、`client.tui.showToast` 三个接口，并且全部包在 try 里，失败只是“没有提醒”。每次 opencode 大版本更新后跑一遍冒烟测试 |
| 仓库里的插件和 MCP 命令打开即运行，没有信任提示 | 对 Cohive 是好事，少一步。但要在文档里写明：这个仓库会在 opencode 启动时运行 `cohive hook` |
| 首次启动要从 npm 往 `.opencode/` 装插件依赖 | 东南亚直连没问题；内网环境需要 npm 镜像，写进文档 |

**工作量**：编码加测试约 1.5 天；本机走查（含真实模型、桌面版各走一遍）半天。合计约 **2 天**。

### 4.2 DeepSeek Harness：建议暂缓，先出一页手工接入说明

**为什么不建议现在就加 `--tools dsh`**：

1. **配置只在用户级**。`cohive init` 的前提是“仓库里的配置决定连哪个工作空间”，dsh 没有项目级配置。如果 `init` 往家目录写，会影响这台电脑上所有项目；如果写进仓库、靠 `--patch` 启动，就要求用户每次都带参数启动。
2. **一个进程一套配置**。网页版一个进程会同时开多个项目，但 MCP 连接（锁定一个工作空间）和钩子文件都是整个进程只有一份。要接两个工作空间，只能为每个工作空间建一个 dsh 档案（`dsh --profile cohive-xxx web`）。
3. **关键环节依赖实验插件**。钩子兼容插件不随 dsh 安装，要用户自己 `dsh plugin add -w`；而且它在实验组，整个项目也处于“会有破坏兼容变更”的预览期。
4. **网页对话里看不到提醒那一行**，只能靠模型在答复末尾写出（这点 Codex 桌面版已经是同样处理，可以接受）。

**手工接入说明的内容**（约半天）：

- Node 24 的要求。
- 在 `~/.dsh/profiles/web/cordis.patch.yml` 里 insert 一条 `dsh-mcp-client`（stdio，`cohive mcp-proxy --tool dsh`）。
- `dsh plugin --profile web add -w @deepseek-ai/dsh-hooks-claude-code`，再 insert 这个插件，指向一份钩子文件。钩子文件写 `SessionStart`、`UserPromptSubmit` 两个提醒钩子，加一个对五个改变状态的工具返回 `ask` 的 `PreToolUse`。
- 技能复用 Codex 的 `.agents/skills/cohive`。
- 建议关闭会话日志上传。

**正式支持的做法**（等 dsh 出正式版、或者团队真有人在用时再做）：

- 写一个 Cohive 原生 dsh 插件组合包，三件事放在一个插件里，不依赖实验插件：
  - `tools/pre-execute`：五个改变状态的工具返回 `ask`。
  - `agent/created`：会话开始时调用 Cohive 并注入提醒。
  - `agent/pre-step`：每次提交时追加上下文。
- 插件运行时按会话所在目录找仓库里的 Cohive 配置，确定工作空间，从而绕开“一进程一个工作空间”。
- `cohive init --tools dsh` 只写仓库里的工作空间标记和技能，插件由用户装一次。
- 工作量估计 3 到 4 天，另加跟随 dsh 版本变化的维护成本。

### 4.3 两家都要注意的一点：每台设备最多 4 种工具

服务端 `internal/core/pairing.go` 的 `cleanTools` 限制一次配对最多 4 个工具。加上 opencode 是 3 个；以后再加 dsh 正好 4 个，再多就会配对失败。`cohive login` 默认按 `knownTools` 全部申请令牌，所以这个上限迟早会碰到。

---

## 5. 需要 lee 决定的事

1. **“DeepSeek harness”指什么？**
   - A. DeepSeek 官方的 DeepSeek Harness（`dsh`）
   - B. 用 DeepSeek 模型驱动 Claude Code 或 opencode
   - C. 社区的 Codewhale（原 DeepSeek-TUI）等
   - **建议按 A 理解**。如果其实是 B，Cohive 不用做任何事，接入文档里加一句“换模型不影响 Cohive”即可。
2. **opencode 做不做、什么时候做？**
   - A. 现在做，作为第三种工具，约 2 天
   - B. 等有成员真在用再做
   - **建议 A**：四项能力齐全，而且已有的技能文件它直接能读，成本低。
3. **DeepSeek Harness 怎么处理？**
   - A. 暂缓，只写一页手工接入说明（半天）
   - B. 现在做实验性的 `--tools dsh`，往家目录写配置
   - C. 写原生插件、正式支持（3 到 4 天）
   - **建议 A**，理由见 4.2。
4. **opencode 的提醒插件放哪里？**
   - A. 放仓库里（`.opencode/plugins/cohive.ts`，随仓库提交，团队成员打开即生效）
   - B. 放每个人的家目录（`~/.config/opencode/plugins/`，`init` 时写，换电脑要重来）
   - **建议 A**，与 Claude Code、Codex 的钩子随仓库走一致。
5. **是否放宽“每台设备最多 4 种工具”的上限（比如放到 8）？**
   - **建议放宽**：改动只是一个常数加测试，可以和 opencode 一起做。
6. **dsh 的会话日志默认上传给 DeepSeek，接入说明里怎么写？**
   - **建议写明默认行为，并建议关闭**：问题单内容会进入会话。
7. **本机走查库 `cohive_walk_opencode` 删不删？**
   - 里面只有这次调研造的两张走查单和三个走查令牌。**建议删**。

---

## 附：实测记录摘要

| # | 操作 | 结果 |
|---|---|---|
| 1 | 服务端为走查成员签发 `opencode`、`dsh` 工具令牌 | 成功，服务端不用改 |
| 2 | opencode 1.18.29、1.19.3：`mcp list`；调用 `cohive_inbox`、`cohive_get_issue` | 已连接；只读工具直接执行 |
| 3 | opencode 起草问题单（没写规则） | 直接执行，来源工具记为 opencode |
| 4 | opencode 普通模式下“发出”“接单” | 询问；非交互运行时自动拒绝；终端界面弹出三选项确认框（未点允许） |
| 5 | opencode `--auto` 下“发出” | 直接执行，绕过确认 |
| 6 | 成员 B 接单、请求验证后，成员 A 开新会话 | 插件注入“有 1 张单等用户处理：#1 待你验证……”，模型请求里可见 |
| 7 | 成员 B 新发一张单给 A，A 在原会话继续说话（间隔超过 20 秒节流） | 插件注入“#2 walk-b 交给你处理” |
| 8 | opencode `/cohive 2` | 技能全文加参数“2”发给模型 |
| 9 | dsh（Node 24）无界面模式调用只读工具与“退回” | 都直接执行，没有询问 |
| 10 | dsh 装钩子兼容插件后，“关闭” | 无界面模式：没有确认渠道，拒绝；全权限模式：拒绝；网页：弹出“拒绝 / 允许一次”（点了拒绝） |
| 11 | dsh 新会话 | `cohive hook` 的提醒排在用户消息之前进了模型请求；网页对话里没看到这一行 |
| 12 | dsh 网页输入 `/` | 重启后列出 cohive 技能（来自 `.agents/skills`） |

原型里调用 `cohive hook` 用的是 `--tool codex`，因为现在的 `hook.go` 只接受 claude-code 和 codex。所以注入文本里的调用写法是 `$cohive N`；正式实现改成接受 opencode、dsh 后，写法应为 `/cohive N`。
