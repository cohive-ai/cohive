# 多语言抽象与开源社区规范方案

日期：2026-10-18　状态：第 0 步方案，待主脑与 lee 认可后实施

## 1. 目标与边界

- 让界面、命令行、MCP 工具说明与报错、会话钩子、群通知都能按语言输出；先内置简体中文、英文两种，其他语言交给社区贡献者维护。
- 补齐主流开源项目的社区规范：贡献指南、行为准则、安全策略、议题与合并请求模板、标签、鸣谢、持续集成。
- 不在本任务内：改仓库设置、安装 GitHub 应用（只列清单给 lee）；第三种语言的翻译本身。

## 2. 现状盘点（按非测试代码中含中文的行数，排除注释）

| 区域 | 行数 | 所在段 |
|---|---|---|
| 网页页面（不含 Admin） | 约 376 | 第一段 |
| 网页 Admin 页 | 162 | 第二段 |
| 网页公共模块（format、components、rules、ui、api 等） | 约 75 | 第一段 |
| internal/core（含 audit.go 138、admin.go 45） | 296 | 第二段 |
| internal/notify | 157 | 第二段 |
| internal/store | 9 | 第二段 |
| cmd/cohive-server（运维命令行） | 216 | 第一段 |
| cmd/cohive（成员命令行、会话钩子） | 165 | 第一段 |
| internal/mcpserver、view | 57 | 第一段 |
| internal/secretscan（规则中文名） | 44 | 第一段 |
| internal/httpapi | 34 | 第一段 |
| internal/client、clidist（含安装脚本模板） | 52 | 第一段 |
| internal/issue（状态名、状态机报错） | 18 | 第一段 |
| scripts/*.sh（单机运维脚本） | 约 60 | 第一段 |
| internal/skill/SKILL.md（技能文件，62 行） | 整篇 | 第一段 |

另有约 2300 行测试代码断言中文文案，处理方式见第 7 节。

几个影响设计的事实：

1. **业务层的报错是写死的中文字符串**：`core.inputErr(format, ...)` 共约 70 处，`ConflictError`、`RetryError`、`notFoundIssue` 等直接拼中文；httpapi 的 `writeError` 与 mcpserver 把 `err.Error()` 原样返回。因此报错的多语言要在 core 里改造，属于第二段；第一段只能先改 httpapi、mcpserver 自己产生的文案。
2. **数据库只存结构化数据**：管理操作记录、问题单动态、通知记录都存编号与类别，展示时才拼文字，历史数据不需要迁移。
3. **没有任何地方按中文报错文本做逻辑判断**（已检索 `includes`、`strings.Contains`、`HasPrefix`），换语言不会破坏流程。
4. **MCP 服务是单例**（`NewServer` 一次、无状态处理），工具说明来自结构体标签，要按语言输出须预建每种语言一份。
5. **会话钩子文案在本机命令行里拼**（cmd/cohive/hook.go），技能文件里引用了钩子的固定开头“Cohive 提醒”，两者的语言必须对得上。
6. **账号表没有语言字段**，存账号语言需要新迁移和 internal/store 改动，只能放第二段。

## 3. 语言如何决定

### 3.1 语言代码与回退

- 内置两种：`en`、`zh-CN`。社区语言用 BCP 47 代码，例如 `ja`、`zh-TW`、`pt-BR`。
- 匹配：用 `golang.org/x/text/language` 的匹配器（网页侧写同样规则的小函数）。`zh`、`zh-Hans`、`zh-SG` 都归到 `zh-CN`；`zh-TW`、`zh-HK`、`zh-Hant` 在社区提供繁体前也先归到 `zh-CN`（对繁体用户比英文友好）。
- 缺词回退：当前语言缺某个键时回退英文；英文也缺（只可能是代码错误）时显示键名并记日志，完整性测试会提前拦住。

### 3.2 每个出口的语言来源

| 出口 | 第一段 | 第二段（最终） |
|---|---|---|
| 网页 | 本机设置（localStorage）> 浏览器语言 > 英文 | 账号语言 > 本机设置 > 浏览器语言；登录页可切换语言 |
| 网页调用的接口报错 | 网页把当前界面语言放进 `Accept-Language` 请求头 | 同左 |
| 成员命令行 `cohive` | `COHIVE_LANG` > 系统语言（`LC_ALL`/`LC_MESSAGES`/`LANG`；macOS 再看系统首选语言；Windows 看界面语言）> 英文 | `COHIVE_LANG` > 账号语言（服务器在响应头 `Content-Language` 回告，命令行缓存在本机登录信息里）> 系统语言 |
| 会话钩子 | 同命令行 | 同命令行（即账号语言） |
| MCP 工具说明、报错 | 服务器缺省语言 | 账号语言 > 服务器缺省语言；忽略编程工具发来的 `Accept-Language`（各家客户端不可控） |
| 运维命令行 `cohive-server` | `COHIVE_LANG` > 系统语言 > 英文 | 同左 |
| 群通知、群里机器人回复 | 服务器缺省语言（不变） | 工作空间语言（工作空间管理员在 Admin 页设置）> 服务器缺省语言 |
| 发给个人的私聊消息（如设备审批） | 服务器缺省语言 | 收件人账号语言 |
| 服务器日志 | 见待决定 D7（建议一律英文、不翻译） | 同左 |

### 3.3 一人一份语言设置（对主脑初步建议的补充）

主脑初步建议里“命令行跟随系统语言”有一个实际问题：很多中文开发者的终端是 `LANG=en_US.UTF-8`，按系统语言会把 lee 团队的命令行全部切成英文；同时 MCP 没有客户端信号，只能看账号。所以建议**账号语言是唯一的个人设置**，网页、命令行、MCP、会话钩子都以它为准；系统语言、浏览器语言只在“还不知道是谁”时使用（安装、首次登录前、未登录页面）。

- 注册或首次登录时，把当时的网页界面语言写进账号（显式值），之后在个人中心改。这样 MCP 一开始就有正确语言，不会出现“网页是英文、编程工具里是中文”。
- 命令行在没有 `COHIVE_LANG` 时不主动发语言头，由服务器按账号决定并回告；有 `COHIVE_LANG` 时以它为准（临时覆盖、写脚本时有用）。第一段还没有账号语言，过渡期命令行总是发送自己判断出的语言。

### 3.4 服务器缺省语言

- 环境变量 `COHIVE_DEFAULT_LANGUAGE`（`en` 或 `zh-CN`，也接受任何已有语言包的代码），不设时为英文。
- Docker 的 `.env.example`、单机的 `server.env.example` 都写上这一项并注释；单机安装脚本按安装机器的系统语言给出初值。
- **测试服必须显式设 `COHIVE_DEFAULT_LANGUAGE=zh-CN`**，否则合入后群通知和 MCP 会变英文。这一条会写进交回，由主脑在更新测试服时处理。
- 第二段可再加到“服务器管理 → 设置”里网页可改（环境变量优先并锁定，与对外地址的做法一致）。

## 4. Go 端设计

### 4.1 选库：nicksnyder/go-i18n v2（v2.6.1，2026-01 发布，仍在维护）

| 方案 | 优点 | 缺点 |
|---|---|---|
| **go-i18n v2（建议）** | Go 最通行的消息库；内置 CLDR 复数规则；Weblate 原生支持其 JSON 格式（`go-i18n-json-v2`）；Phrase 等平台也支持 | 插值语法是 `{{.Count}}`，与网页的 `{{count}}` 不同；复数写法与 i18next 不同 |
| 自写加载器，读 i18next 格式 | 前后端一种格式、一种插值，翻译者只学一套 | 要自己维护复数规则与解析，约 200 行加测试；不算“通行库” |
| golang.org/x/text 的 gotext | 官方仓库 | 工具链多年处于实验状态，翻译文件对社区不友好，平台支持少 |

两种插值语法对翻译者的负担可以接受（Weblate 会检查占位符），选 go-i18n。

### 4.2 文件格式与目录

```
internal/i18n/
  i18n.go          # 内嵌语言包、支持的语言、匹配、缺省语言
  localizer.go     # L.T(key, data)、复数
  errors.go        # 可本地化的错误类型
  http.go          # 解析请求语言、写 Content-Language
  i18n_test.go     # 完整性、占位符一致、代码里用到的键都存在
  locales/
    en/     cli.json  server.json  mcp.json  issue.json  secretscan.json  notify.json(第二段) ...
    zh-CN/  同上
```

- 格式：go-i18n v2 JSON，**扁平点分键**（`"cli.login.done": "..."`），复数写成 `{"one": "...", "other": "..."}`。不用多层嵌套：Weblate 对 go-i18n 嵌套键的支持没有文档保证。
- 按区域分文件：减少并行分支之间的合并冲突，也方便子代理各管一个文件。
- 键名用语义命名（`issue.status.open`），不用中文原文作键，改文案不改键。
- 每个键可加 `description` 给翻译者说明上下文（go-i18n 与 Weblate 都支持）。

### 4.3 可本地化的错误

```go
// i18n.Error 携带键和参数；Error() 用英文渲染（日志、未知调用方）。
type Error struct {
    Key  string
    Data map[string]any
    Err  error // 可选：被包装的哨兵错误，保证 errors.Is 照旧可用
}
func Errorf(key string, data map[string]any) error
func Wrap(sentinel error, key string, data map[string]any) error
// Message 沿错误链找第一个 *i18n.Error 按语言渲染；找不到时退回 err.Error()。
func Message(l L, err error) string
```

- httpapi 的 `writeError` 与 mcpserver 统一调用 `i18n.Message(lang, err)`。第一段先把这一出口接好，core 仍是中文字符串，会原样透出；第二段把 core 的 `inputErr` 等改为键，自动生效。
- 接口错误响应增加稳定的 `code` 字段（`{"error": "<本地化文字>", "code": "issue.not_found"}`），供网页和命令行做判断，避免将来有人按文字判断。见 D21。

### 4.4 MCP

- 启动时为每种语言建一份 `mcp.Server`；`NewStreamableHTTPHandler` 的回调按请求选择（第一段按服务器缺省语言，第二段读鉴权后的账号语言）。
- 工具说明、`trustNote`、`secretNote` 进 `mcp.json`；参数说明从结构体 `jsonschema` 标签移出：先用 `jsonschema.For` 推出结构，再按键填各字段的 `Description`，作为 `InputSchema` / `OutputSchema` 传入。`internal/view` 的输出结构同样处理。
- 工具名、参数名、动作名（`accept`、`decline` 等）不翻译，它们是接口。

### 4.5 成员命令行、会话钩子、技能文件

- `cohive` 的输出全部走键；`internal/client` 发请求时带语言头（规则见 3.2），并读回 `Content-Language`。
- 会话钩子：开头固定为 `Cohive`，后接本地化文字（英文 `Cohive reminder: ...`，中文 `Cohive 提醒：...`）。技能文件只认“以 Cohive 开头的提醒”，不依赖具体语言。
- 技能文件 SKILL.md 出中英两版，见 D8。

### 4.6 安装脚本与运维脚本

- 成员安装脚本 `/install.sh`、`/install.ps1` 由服务器渲染：语言取 `?lang=` > 服务器缺省语言（`curl` 不带语言头）；网页“绑定新电脑”里复制的命令在界面语言与服务器缺省不同时自动带上 `?lang=`。脚本文案进 `cli.json`，社区翻译自动覆盖。
- 单机运维脚本 `scripts/*.sh`：脚本里内置中英两套文案，按 `COHIVE_LANG` > `LANG` 选择，不进社区翻译（运维脚本面向部署者，量小且难以用 JSON 驱动）。见 D9。

### 4.7 issue、secretscan 两个底层包

- `internal/issue` 的状态名与状态机说明、`internal/secretscan` 的规则中文名（38 条）改为键，提供带语言参数的新函数；旧的无语言函数暂时保留（固定中文），因为调用方在 core，第二段改完调用方后删除。

## 5. 网页设计

- 依赖：`i18next` 26.x、`react-i18next` 17.x（支持 React 19）。不引入浏览器语言检测插件，自写十几行检测，规则见 3.2。
- 目录：`web/src/locales/<lang>/<命名空间>.json`，命名空间按区域：`common`（导航、按钮、状态名、严重程度、动作名、通用报错）、`issues`（看板、列表、详情、动态）、`auth`（登录、注册、改密码、设备授权）、`me`（个人中心、钉钉连接）、`server`（服务器管理）、`admin`（第二段）。格式 i18next JSON v4（嵌套键，复数 `_one`/`_other`），Weblate 与 Crowdin 都能直接导入。
- `en`、`zh-CN` 打包进主程序；社区语言用 `import.meta.glob` 按需加载，不增加首屏体积。
- 类型检查：用 i18next 的 `CustomTypeOptions` 以英文语言包为类型来源，`t("拼错的键")` 在 `pnpm typecheck` 时报错。
- 日期、相对时间、数字用 `Intl.DateTimeFormat` / `Intl.RelativeTimeFormat`，跟随当前语言。
- 语言切换：个人中心“语言”一项（选项用各语言自己的名称：English、简体中文；第二段加“跟随浏览器”并存到账号）；登录页、注册页底部放语言切换。
- `api.ts` 每个请求带 `Accept-Language: <当前语言>`；`index.html` 的 `lang` 属性随语言更新。

## 6. 翻译工作流与社区语言

- **源语言用英文**（翻译平台的基准文件是 `en`），社区翻译者从英文译出；维护者新增文案时必须同时写 `en` 与 `zh-CN`，由完整性测试保证。见 D4。
- 新增一种语言的步骤（写进 CONTRIBUTING）：复制 `en` 目录为新代码 → 翻译 → 在语言清单里登记 → 跑测试 → 提合并请求。网页与服务端各一处语言清单，由测试校验二者一致。
- **语言维护者登记**：`docs/i18n/LANGUAGES.md` 一张表：语言、代码、状态（内置 / 社区）、维护者 GitHub 账号。社区语言长期没人维护、完成度低于一定比例时，在发布说明里标注，必要时降级为“不完整”。
- 说明：CODEOWNERS 只对有写权限的人生效，社区维护者一般没有，所以先用登记表加合并请求里 @ 提醒；可信的维护者由 lee 授予 Triage 角色。
- 翻译平台：先只走合并请求；出现第一种社区语言或社区要求时，再申请 Hosted Weblate 的开源免费计划（格式已兼容，接入只需配置组件）。见 D17。

## 7. 测试

- **完整性测试**（Go 与网页各一）：`en` 与 `zh-CN` 键集合必须完全一致；其他语言允许缺词，但不能有英文里不存在的键；同一键在各语言的占位符必须一致（Go 比 `{{.X}}`，网页比 `{{x}}`）；JSON 必须合法；两边的语言清单一致。
- **键存在测试**：Go 用 `go/ast` 扫出代码里 `T("…")`、`Errorf("…")` 的字面量键，必须都在 `en` 中；网页靠类型检查。
- **防回潮守卫**：扫描非测试源码（Go、TS、TSX），注释之外出现中文字符即失败。第一段带白名单（internal/core、internal/notify、internal/store、web/src/pages/Admin.tsx），第二段清空白名单。这是主脑建议之外加的，用来防止以后又有人直接写中文。
- **现有断言中文的测试：固定用中文跑**（建议，见 D11）。Go 测试通过测试辅助函数把缺省语言设为 `zh-CN`；网页在 vitest 初始化文件里固定 `zh-CN`。理由：约 2300 行断言大部分在第二段的 core、notify 里，改成断言键工作量大，还会丢掉“文案确实正确”的检查。另外每个出口补少量英文用例，确认切换真的生效。
- 测试口径照旧：`go vet ./... && go test -count=1 ./... && make build`，网页测试通过；新增 CI（见 9.6）跑同一套。

## 8. 术语表（英文统一说法）

| 中文 | 英文 | 备注 |
|---|---|---|
| 问题单 | issue | |
| 工作空间 | workspace | |
| 看板 | board | |
| 起草 / 草稿 | draft | |
| 发出 | submit | 按钮可写 Send |
| 接单 | accept | |
| 退回 | decline | 状态“已退回”= Declined |
| 转交 | transfer | |
| 改派 | reassign | 管理员操作 |
| 开始修复 / 修复中 | start fixing / Fixing | |
| 请求验证 / 待验证 | request verification / Ready for verification | |
| 验证 | verify | |
| 关闭 / 已关闭 | close / Closed | |
| 重开 / 已重开 | reopen / Reopened | |
| 待接单 | Open | |
| 已接单 | Accepted | |
| 评论 | comment | |
| 动态 | activity | 问题单下的事件列表 |
| 发起人 | reporter | |
| 负责人 | owner | 问题单与服务的负责人统一用 owner |
| 服务 | service | |
| 严重程度：低 / 普通 / 高 / 紧急 | Severity: Low / Normal / High / Critical | |
| 简述（群通知简述） | brief | |
| 复现脚本 / 复现环境 | repro script / repro environment | |
| 证据 | evidence | |
| 成员 | member | |
| 服务器管理员 / 工作空间管理员 | server admin / workspace admin | |
| 邀请、邀请链接 | invite, invite link | |
| 个人中心 | Profile | |
| 登录名 / 显示名 | username / display name | |
| 绑定新电脑、设备配对 | pair a new computer, device pairing | |
| 授权 | Authorize | |
| 令牌 / 凭据 | token / credentials | |
| 编程工具 | coding tool | |
| 智能体 | agent | |
| 会话钩子 | session hook | |
| Cohive 提醒 | Cohive reminder | |
| 群、群通知 | chat group, group notification | |
| 停用（账号）/ 移出（工作空间） | deactivate / remove | |
| 管理操作记录 | admin audit log | |
| 对外地址 | external URL | |
| 密钥扫描 | secret scanning | |
| 命令行 | CLI | |

英文风格：句子式大小写（Sentence case）、简短直接、用主动语态，参照 GitHub、Linear 等开发者工具的常见说法，不逐字硬译中文。

## 9. 社区规范

### 9.1 文件清单（第一段提交）

| 文件 | 内容与取舍 |
|---|---|
| `CONTRIBUTING.md`（英文）+ `CONTRIBUTING.zh-CN.md` | 开发环境（Go 1.25、Node 20+、pnpm、Docker 起测试 Postgres）、测试口径、提交信息用约定式提交（中英文皆可）、合并请求规范（小而聚焦、关联议题、勾选清单、外部贡献默认压缩合并）、新增或维护一种语言、语言维护者登记、首次贡献者从 `good first issue` 入手 |
| `CODE_OF_CONDUCT.md` | Contributor Covenant 2.1 英文原文，附官方中文译本链接（官网有 2.1 中文版时链接该版，否则链接最近版本）；需要一个处理违规举报的联系邮箱，见 D18 |
| `SECURITY.md` | 只支持最新发布版；通过 GitHub 私密漏洞报告提交，不要开公开议题；7 天内确认收到；列出重点范围（鉴权与权限、令牌、密钥扫描绕过、群通知泄漏敏感内容） |
| `.github/ISSUE_TEMPLATE/` | `bug_report.yml`、`feature_request.yml`、`translation.yml`（新语言或译文错误）、`config.yml`（关闭空白议题，提问引到讨论区，安全问题引到私密报告） |
| `.github/pull_request_template.md` | 改了什么、为什么、怎么测的；勾选：测试通过、新增文案已写 en 与 zh-CN、文档已更新、没有密钥 |
| `.github/release.yml` | 发布说明按标签分类（新功能、修复、翻译、文档），GitHub 自动附“New Contributors”一节 |
| `.github/workflows/ci.yml` | 持续集成：Postgres 服务容器 + `go vet`、`go test`、网页类型检查与测试、`make build`；对来自分叉的合并请求只读权限 |
| `.github/dependabot.yml` | 每周检查 Go、npm、GitHub Actions 依赖更新（可选，见清单） |
| `docs/i18n/LANGUAGES.md` | 语言与维护者登记表 |
| `docs/maintainers.zh-CN.md` | 给 lee 的维护者手册：议题分诊流程、标签用法、怎样回复首次贡献者、合并外部贡献前查什么、怎样发版本 |

### 9.2 CLA / DCO

建议都不要（同主脑意见）。Apache-2.0 第 5 条已规定：贡献者有意提交的贡献按 Apache-2.0 授权，除非另有声明，足以覆盖开源使用。代价只有一个：将来若想改许可证或做商业双授权，需要逐个联系贡献者。lee 如果有这类打算，应现在就决定（越晚越难补），见 D15。

### 9.3 鸣谢

- README 贡献者头像墙：用 contrib.rocks 的图片链接，不需要装应用。它按提交作者统计，翻译者通过合并请求或 Weblate 提交（Weblate 保留作者）同样会出现。
- 发布说明：`.github/release.yml` + GitHub“Generate release notes”按钮，自动列出新贡献者。
- 翻译者：`docs/i18n/LANGUAGES.md` 里列维护者与主要译者；发布说明“翻译”分类单列。
- 暂不装 all-contributors 机器人：它能记录非代码贡献，但要装应用、会频繁开合并请求，项目初期收益不大，见 D16。

### 9.4 标签建议

`bug`、`enhancement`、`documentation`、`question`、`good first issue`、`help wanted`、`translation`、`lang:<代码>`（每种社区语言一个）、`needs-triage`（模板默认加上）、`security`、`breaking-change`、`dependencies`、区域标签 `area:web`、`area:cli`、`area:server`、`area:mcp`、`area:notify`。建标签的命令写进给 lee 的清单。

### 9.5 文档英文版

- 新增英文版：`docs/guides/deploy-docker`、`onboarding`、`install-cli`、`single-host`、`group-notify`（Telegram 群通知）。README 已链接前两篇，目前标着“(Chinese)”。
- 只保留中文：`dingtalk.md`（面向国内用户）、`m1-walkthrough.md`（里程碑一的历史走查）、`docs/plan.md`、`docs/brain/`、`docs/superpowers/`（内部工作文档）。
- 命名与 README 默认语言见 D13。
- README 里“界面目前只有中文”的说明，第一段改为“中英两种，英文正在完善”，第二段完成后去掉；截图在第二段换成英文界面另出一套。

### 9.6 持续集成

仓库目前没有持续集成，外部合并请求无法自动验证。第一段加 `.github/workflows/ci.yml`（文件可以直接提交；Actions 的权限设置留给 lee）。

## 10. 分段实施

### 第一段（方案认可后即做；不碰 internal/notify、internal/core、internal/store、web/src/pages/Admin.tsx）

1. 我：`internal/i18n` 包、网页 i18n 框架、完整性与守卫测试、示范改造一个文件（成员命令行的 login），定下写法。
2. 子代理（`model: sonnet`，按区域分文件，互不重叠，可并行；要求中文回报）：
   - A：网页 Board、Issues、IssueDetail、EventLog、WorkspaceShell 与公共模块 → `common`、`issues`
   - B：网页 Login、Register、Password、DeviceAuth、Me、DingTalk、Server → `auth`、`me`、`server`
   - C：cmd/cohive、internal/client、会话钩子、安装脚本模板 → `cli.json`
   - D：cmd/cohive-server、scripts/*.sh → `server.json`
   - E：internal/httpapi、internal/mcpserver、internal/view、internal/issue、internal/secretscan → `mcp.json`、`issue.json`、`secretscan.json`
   - F：英文文档（5 篇指南）与 SKILL.md 英文版
3. 我：审核全部英文译文与键名，跑完整测试，起草社区规范文件（中文版由子代理翻译、我审核）。
4. 交回 `docs/brain/inbox/I18N-1.md`。

### 第二段（主脑通知 N-1、N-2 合入后）

- 迁移：账号语言、工作空间语言两个字段；store 读写。
- core：`inputErr` 等改为 `i18n.Errorf`，audit 展示文字改为键；删除 issue、secretscan 的旧中文函数。
- notify：群通知、机器人回复按工作空间语言；私聊按收件人账号语言。
- 网页 Admin 页文案与“工作空间语言”设置；个人中心语言存到账号；注册时记录界面语言。
- MCP 按账号语言选服务；命令行改为读 `Content-Language`。
- 守卫测试清空白名单；英文截图。
- 交回 `docs/brain/inbox/I18N-2.md`。

## 11. 待决定项

| 编号 | 问题 | 选项 | 建议 |
|---|---|---|---|
| D1 | Go 端用什么库 | A go-i18n v2；B 自写 i18next 格式加载器；C x/text gotext | A |
| D2 | 语言文件格式 | Go：go-i18n v2 JSON 扁平点分键；网页：i18next v4 JSON 嵌套、按命名空间分文件；均为 `locales/<语言>/` | 按左 |
| D3 | 简体中文的语言代码 | A `zh-CN`；B `zh`；C `zh-Hans` | A：浏览器最常发、贡献者最熟悉，Crowdin 默认也用它 |
| D4 | 翻译基准语言 | A 英文；B 中文 | A：社区翻译者大多能读英文；内置两种由维护者同时写 |
| D5 | 服务器缺省语言 | A 内置英文，`COHIVE_DEFAULT_LANGUAGE` 可改，测试服显式设中文；B 内置中文 | A：面向外部用户；需在测试服加一行配置 |
| D6 | 个人语言设置 | A 账号语言统一管网页、命令行、MCP、钩子，注册时记录界面语言；B 各端各自跟随本地 | A（理由见 3.3） |
| D7 | 服务器日志语言 | A 一律英文、不翻译；B 跟随缺省语言；C 保持中文 | A：日志用于排错和提交议题，固定语言便于搜索；会改变 lee 团队看日志的体验 |
| D8 | 技能文件 SKILL.md | A 中英两版，`cohive init` 按命令行语言选，项目里已有则沿用原语言（避免同一仓库被不同语言的成员来回改写）；B 只出英文版，要求智能体用用户的语言回复 | A |
| D9 | 单机运维脚本 | A 脚本内置中英两套，按 `COHIVE_LANG`/`LANG` 选，不进社区翻译；B 只英文 | A |
| D10 | 成员安装脚本的语言 | A 服务器按 `?lang=` > 缺省语言渲染，网页复制的命令自动带参数；B 脚本只英文 | A |
| D11 | 现有断言中文的测试 | A 固定中文跑，另补英文用例；B 改为断言键 | A |
| D12 | 防回潮守卫测试 | A 加，第一段带白名单；B 不加 | A |
| D13 | README 与文档的默认语言 | A README.md 改英文、中文移到 README.zh-CN.md；指南保持现有中文文件名，英文版加 `.en.md`；B 维持 README.md 中文、英文 README.en.md；C 全部改为英文无后缀、中文 `.zh-CN.md` | A：GitHub 首页默认展示 README.md，外部用户先看到英文；指南不改名，避免与在飞分支冲突、不打断团队已有链接 |
| D14 | 代码注释、提交信息的语言 | A 不批量翻译现有中文注释，新贡献中英皆可；B 逐步翻译成英文 | A：批量翻译收益低、冲突大；有外部贡献者后再看 |
| D15 | CLA / DCO | A 都不要；B DCO；C CLA | A（若 lee 有改许可证或商业双授权的打算，现在改选 C） |
| D16 | 鸣谢方式 | A contrib.rocks + 自动发布说明 + 语言维护者表；B 另装 all-contributors | A |
| D17 | 翻译平台 | A 先走合并请求，有社区语言后申请 Hosted Weblate；B 现在就接 Crowdin 或 Weblate | A |
| D18 | 行为准则的举报联系方式 | 需要 lee 提供一个邮箱，建议专用别名（例如 conduct@ 加 lee 的个人域名），不用个人主邮箱 | lee 定 |
| D19 | 持续集成 | A 第一段加 GitHub Actions；B 不加 | A |
| D20 | 接口错误响应加 `code` 字段 | A 加；B 不加 | A |

## 12. 留给 lee 的仓库设置清单（不由会话操作）

1. Settings → General → Features：开启 Discussions（建议分类：Q&A、Ideas、Show and tell、Announcements）。
2. Settings → Code security：开启 Private vulnerability reporting；开启 Dependabot alerts（Dependabot 更新的配置文件由第一段提交）。
3. Settings → Actions → General：保持“首次贡献者的合并请求需批准后才运行工作流”；Workflow permissions 选只读。
4. Settings → Rules（或 Branches）给 `main` 加规则：禁止强推、禁止删除；要求 CI 通过时给管理员留绕过，以免挡住主脑的正常合入。
5. 建标签：第一段交回里给出一组 `gh label create` 命令，lee 在本机执行。
6. 仓库首页的简介、主题标签（建议 `mcp`、`claude-code`、`codex`、`ai-agents`、`collaboration`、`self-hosted`）和社交预览图。
7. 给行为准则准备举报邮箱（D18）。
8. 以后有社区语言时：申请 Hosted Weblate 开源免费计划；可信的语言维护者授予 Triage 角色。
9. 发版本时在 Releases 页面用“Generate release notes”。

## 13. 风险

- **测试服变英文**：合入后若没设 `COHIVE_DEFAULT_LANGUAGE=zh-CN`，群通知与 MCP 会变英文。交回与更新测试服的步骤里都会写明。
- **第一段是过渡态**：英文界面下，Admin 页、业务层报错、管理操作记录、群通知仍是中文，要到第二段才完整。第一段合入后不宜对外宣传英文支持。
- **与在飞分支冲突**：第一段严格避开四个目录与 Admin 页；语言文件按区域分开；docs 指南只新增 `.en.md`，不改原文件。
- **英文质量**：子代理译文由我逐条审核，遵守术语表；第二段完成后可请一位母语使用者通读界面。
