# TOOLS-A 交回：OpenCode 成为第三种工具、工具上限放宽到 8、DeepSeek Harness 手工接入说明

## 结论

- 分支：`feature/tools-a-opencode`（从 `origin/main` 1707b5b 开出），已推送，未合入、未动 main，没碰测试服。
- 没碰 `internal/notify`（另一个会话在改）。所以群通知里的工具显示名 `toolLabel` 还没加 OpenCode，群消息里会显示小写的 `opencode`；等 IM2 合入后补一行即可，见“遗留”。
- 检查结果：
  - `go vet ./...` 通过。
  - `go test ./...` 全部通过（真实 Postgres，localhost:55432）。
  - `make build` 成功，看板 18 个测试文件、83 个用例通过。
  - 变异验证 10 个探针全部被测试发现，见文末。
- 本机实测：用 `cohive init --tools opencode` 生成的配置，在 OpenCode 1.19.3（最新）和 1.18.29（本机 brew 版）上走通全流程。DeepSeek Harness 按新写的手工说明原样配置也走通了，并据此改了说明里的一处错误（见“走查”）。

## 做了什么

1. **`cohive init --tools opencode`**（新文件 `cmd/cohive/init_opencode.go`）
   - **`opencode.json`**（新建或合并）：
     - `mcp.cohive` 写成 `{"type": "local", "command": ["cohive", "mcp-proxy", "--server", …, "--tool", "opencode"]}`。
     - `permission` 按工具名写规则：8 个只读或只影响自己的工具 `allow`，5 个会改变别人工作的工具 `ask`。清单直接用 `confirmTools`、`allowTools`，与 Claude Code、Codex 同一份，包括 `cohive_find_member`、`cohive_cancel_issue`、`cohive_delete_draft`。
     - **按顺序读写，不用 map**：OpenCode 的权限规则按顺序匹配、最后命中的生效，重排用户的键可能改变含义。用户的键、嵌套内容、数字写法都原样保留；cohive 的 13 条规则先删再追加到最后，压过用户写在前面的通配。重复执行，文件逐字节不变。
     - `permission` 整体写成字符串（`"ask"` 等）时，等价展开成 `{"*": 值}` 再加规则。
     - 拒绝处理、什么都不写的情形：
       - 仓库里有带注释的 `opencode.jsonc`：改写会丢注释，提示挪到 `opencode.json` 或参照 `examples/opencode`；
       - `opencode.json` 不是合法 JSON，或顶层、`mcp`、`permission` 写法不对；
       - 路径上有符号链接。
   - **`.opencode/plugins/cohive.ts`**：会话内提醒插件。模板是 `cmd/cohive/opencode_plugin.ts.tmpl`，用 go:embed 嵌入，只替换 `cohive hook` 的参数列表。插件逻辑：
     - 收到 `session.created` 时记下这是新会话，它的第一句按 `SessionStart`，其余按 `UserPromptSubmit`。
     - 每次提交都调用 `cohive hook --tool opencode`。有 `additionalContext` 时作为隐藏文本附到这条用户消息上，有 `systemMessage` 时弹一行提示条。
     - 5 秒超时；任何失败都只是“没有提醒”。
     - 文件第一行是标记。用户自己的同名文件不覆盖（报错），`--no-hooks` 只删 cohive 写的那份，不删用户的、也不因它报错。
   - **技能**：写到 `.agents/skills/cohive/SKILL.md`，与 Codex 共用同一份。OpenCode 也读这个目录，不另写一份（另写会出现重名技能，OpenCode 只记警告）。实测 `/cohive 1` 会把“1”传给模型。
   - `knownTools` 加 `opencode`，所以 `cohive login` 默认也会为它申请令牌。已配对的电脑执行 `cohive init --tools opencode` 时，会用设备密钥自动补签。
2. **`cohive hook`**：接受 `opencode` 和 `dsh`。提醒里的调用写法照旧：只有 Codex 是 `$cohive N`，其余都是 `/cohive N`（`slashCommand` 本来就是这样，加了注释）。
3. **`cohive login`**：`readRepoTarget` 也从 `opencode.json` 读服务地址和工作空间。与 `.mcp.json`、`.codex/config.toml` 不一致时报错，提示文案里也带上了 `opencode.json`。`knownToolsFirst` 改为按 `knownTools` 判断。
4. **服务端**：复核过，工具名只校验格式（`^[a-z][a-z0-9-]{1,31}$`），`opencode`、`dsh` 都能签发，不用改；数据库也没有工具数量约束。**每台设备最多工具数从 4 放宽到 8**（`internal/core/pairing.go` 的 `maxTools`，中英文案同步改）。
5. **文案**（全部走 `internal/i18n`，中英成对）：
   - 新增 7 个键：`written_opencode`、`written_opencode_plugin`、`hook_opencode`、`opencode_jsonc`、`opencode_plugin_foreign`、`at.opencode_mcp`、`at.opencode_permission`。
   - 改了用法、`--tools` 说明、`tools_unsupported`（改为列出 `knownTools`）、`repo_ready`、`next_1`、`next_2`、`hook --check` 用法、`hooks_note`。`hooks_note` 原来写“已写入两个钩子”，对 OpenCode 不准确，改为分别说明。
   - 产品名写 OpenCode，工具取值写 `opencode`。
   - 中英技能文件里“用户调用时可能带编号”一句加上 OpenCode。
6. **看板**：`web/src/format.ts` 的工具显示名加了 OpenCode。改用 Map 查表，避免服务端给出 `constructor` 这类工具名时取到对象原型上的属性。
7. **示例**：新增 `examples/opencode/`：
   - `opencode.json`：与另外两家示例一样，用手工令牌的远程写法，`{env:COHIVE_TOKEN}`。
   - 插件：按示例参数生成，有测试保证与模板同步。
   - 技能链接。
   - 顺手修了 **`examples/skills/cohive/SKILL.md` 这条既有的坏链接**：它指向的 `internal/skill/SKILL.md` 在技能拆成中英两个目录时（7b922dc）已经不存在，Codex 示例和新的 OpenCode 示例都经它取技能。现在指向英文版。
8. **文档**：
   - README 中英：开头工具列表、确认一节、成员三步、状态一节写明支持的工具和 DeepSeek Harness 说明的链接；中文“文档”列表加了这份说明。
   - `docs/guides/onboarding` 中英：第 3 步加 OpenCode（不用信任、首次启动用 npm 装插件依赖）；会话内提醒一节加 OpenCode 的表现与未验证范围；`init` 写入的文件表加一行；三家“全自动”模式都会跳过确认；OpenCode 的“总是允许”只到退出为止；`opencode.jsonc` 的处理。
   - `install-cli` 里没有工具清单，没改。
9. **DeepSeek Harness 手工接入说明**：`docs/guides/deepseek-harness.md` 与 `.en.md`。内容：
   - 开发者预览、暂不正式支持、一个档案只连一个工作空间；Node 版本要求（Node 23 会静默退出）。
   - `cohive login --tools dsh`；在用户级配置里加 MCP（stdio，`cohive mcp-proxy --tool dsh`）。
   - 装“Claude Code 钩子兼容”插件。版本要写成与 dsh 相同，它的 latest 标签指向旧版。
   - 钩子文件：两条提醒钩子，加一条 `PreToolUse` 对 5 个工具返回 ask。
   - 哪些工具需要确认，以及网页、全权限、无界面三种情况的实际表现。
   - 技能读 `.agents/skills`，不读 `.claude/skills`。
   - **默认上传会话日志给 DeepSeek，问题单内容会进入会话**，建议关闭，给了网页开关和配置两种关法；遥测开关；排查。

## 走查（本机自建实例、走查库 `cohive_walk_tools_a`、本机假模型、隔离家目录，不连测试服、不用真实令牌）

两名走查成员用 `cohive login --token-stdin` 登录，各自在空仓库执行 `cohive init`（没有加 `--tools`，按已登录的工具自动选中 opencode），只在家目录的 OpenCode 配置里接假模型，仓库里的配置全部是 `init` 生成的。

| # | 操作 | 1.19.3 | 1.18.29 |
|---|---|---|---|
| 1 | `opencode mcp list` | cohive 已连接 | 已连接 |
| 2 | B 起草 → 普通模式发出 | 起草直接执行；发出触发询问，非交互运行时被拒绝 | — |
| 3 | B `--auto` 发出给 A | 执行（全自动模式绕过确认，与 D13 一致） | — |
| 4 | A 开新会话 | 第一句附上“有 1 张单等用户处理：#1 待你接单、退回或转交”，调用写法 `/cohive 1` | 附上“有 2 张单……” |
| 5 | B 再发一张，A 在原会话继续（间隔超过节流） | 附上“#2 walk-b 交给你处理”，`/cohive 2` | — |
| 6 | A 只读 `get_issue` / 接单 | 只读直接执行；接单触发询问 | 关闭触发询问 |
| 7 | A `/cohive 1` | 技能全文加参数“1”发给模型 | — |
| 8 | 终端界面打开，初始提示词调用接单 | 右上角弹出“Cohive 提醒：你有 2 张待处理的单……”，确认框“Allow once / Allow always / Reject”（没点允许） | — |
| 9 | 服务端 | 两张单的 `source_tool` 都是 opencode | — |

**DeepSeek Harness**（0.2.1-rc.2，Node 24）按说明原样配置：

- 新会话提醒注入，调用写法 `/cohive 2`；只读工具直接执行；接单被要求确认（无界面模式没有确认渠道，所以拒绝）。
- 关闭会话日志上传的配置能被正常加载。
- **据实测改了说明**：档案的 `cordis.patch.yml` 初次生成时只有一行 `[]`，直接“追加到末尾”会变成非法 YAML，dsh 启动失败。说明改为“把 `[]` 换成这段”。

## 与派活的出入

- 群通知里的工具显示名没改（`internal/notify/render.go` 的 `toolLabel`），原因是派活要求不碰 `internal/notify`。
- 技能没有写到 `.opencode/skills`，复用 Codex 的 `.agents/skills`，理由见上文第 1 条。
- 额外改了三处：
  - 看板显示名；
  - 修了 `examples/skills` 的坏链接；
  - `hooks_note` 的文案。

## 遗留

1. IM2 合入后，在 `internal/notify/render.go` 的 `toolLabel` 加 `case "opencode": return "OpenCode"`（一行，加一个用例）。
2. OpenCode 桌面版、经 ACP 接入的编辑器里，提示条能否显示还没验证（文档已如实写明）。智能体在回复末尾写出的提醒不受影响。
3. 走查库 `cohive_walk_tools_a` 还在本机，按惯例合入后删。上一轮调研的走查库 `cohive_walk_opencode` 我去删时发现已经不在了。

## 变异验证

每个探针改一处源码，跑对应测试，确认失败后还原；10 个全部被发现。

| 探针 | 发现它的测试 |
|---|---|
| 需确认的工具写成 allow | TestInitOpencodeWritesConfig |
| 放行规则原地改写、不挪到最后 | TestInitOpencodeMergeKeepsOrder（第一次跑时这个探针存活，补了“用户先写了一条只读工具规则”的场景和完整顺序断言后被发现） |
| 字符串 permission 不展开 | TestInitOpencodePermissionString |
| 不拦用户自己的同名插件 | TestInitOpencodeRefuses |
| `--no-hooks` 不移除插件 | TestInitOpencodeNoHooks |
| hook 不接受 dsh | TestHookOpencodeAndDsh |
| login 不读 opencode.json | TestInitAllThreeAndRepoTarget |
| 工具上限仍为 4 | TestDeviceToolLimit |
| 插件模板改了、示例没同步 | TestOpencodeExampleInSync |
| 不拦 opencode.jsonc | TestInitOpencodeRefuses、TestInitOpencodeEnglish |
