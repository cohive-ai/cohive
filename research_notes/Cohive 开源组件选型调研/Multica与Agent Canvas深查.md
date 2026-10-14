# Multica 与 OpenHands Agent Canvas 深查：Cohive 应基于它们构建、在其上做一层，还是独立自建

> 信息日期：2026-10-14。Multica 的结论来自三类一手材料：用 `gh api` 实查的元数据；许可证原文；2026-10-14 浅克隆的 main 分支只读源码，克隆在 `/private/tmp/multica-ro.KQjiIn/`，未执行其中任何脚本或构建命令。文档引用的是仓库内 `apps/docs/content/docs/*.mdx` 源文件，与 multica.ai/docs 同源。OpenHands 的结论来自 `gh api` 读取的 README、SELF_HOSTING.md、LICENSE，以及官方文档页。未能核实的内容一律放进「缺口」。
> 与已有笔记的关系：《智能体间协作先行方案.md》已记录 Multica 的基本形态与许可证概要，《智能体接入与执行隔离.md》已记录 Agent Canvas 概要。本篇只补新信息，并给出判断。

---

## 问题一：Multica 许可证逐字核对——(a) 公司内部自托管是否付费；(b) 二次开发后以开源形式发布是否允许；(c) 不改代码、只写插件或外部集成是否受限

### Takeaway
(a) 单一组织内部自托管**免费**，多个工作空间也免费。(b) **允许**公开发布派生项目的源码，但派生物整体仍受「Multica 许可证」约束：要保留品牌，或注明「基于 Multica 构建」；任何人用它向组织外提供服务，都要各自向 Multica 购买商业许可。因此它算不上 Cohive 意义上的「开源项目」。(c) 只通过接口调用、与 Multica 可分离的独立程序，按 Apache-2.0 的定义**不构成派生作品**，本身不受附加条件约束。但只要 Cohive 的用户运行了 Multica，就要遵守 Multica 的条件，尤其是「组织外的人通过任何入口驱动实例，就需要商业许可」。

### Cited Findings
**许可证结构（逐字要点）**
- 许可证名为「Multica License」，由第一部分「附加条件」和第二部分「完整、未修改的 Apache-2.0 原文」组成，两者合起来才构成许可，“Neither part grants any rights on its own”。出品方是 **Index Labs (Hong Kong) Limited** — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 条件 1(a)，托管或嵌入：未取得商业许可时，“you may not use the Multica source code to provide a hosted service to third parties, or embed Multica as a component of a product or service that is sold, licensed, or otherwise commercially distributed to third parties”。其中：
  - 以 SaaS、托管服务、其他商业产品的集成组件形式提供，均受限；
  - “Providing a hosted service to third parties is restricted whether or not you charge for it”。面向组织外用户的公开实例，即使免费、无广告、无付费档，也需要商业许可；
  - “Internal use within a single organization (including multiple workspaces) does not require a commercial license.”；
  - “Making the source code available, including publishing the source code of a fork in a public repository, is not itself a hosted service and does not require a commercial license.”，但这种分发仍须遵守 (b)、(c) 和第 3 条；且 “Source distribution does not grant recipients the right to operate a hosted service; each operator must obtain its own commercial license.” — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 条件 1(b)，品牌：未获书面「品牌豁免」，不得移除或修改 Multica 界面上显示的标志、产品名、版权与署名信息。「Multica 界面」指全部或大部分派生自本仓库界面代码的界面，明确列出 `apps/web/`、`apps/desktop/`、`apps/mobile/`、`packages/views/`、`packages/ui/` 和 `ghcr.io/multica-ai/multica-web` 镜像；并强调界面代码被修改、移动、改名或抽到其他仓库后仍受此约束。只运行 `server/` 后端、守护进程或命令行、不使用 Multica 界面时，此条不适用 — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 条件 1(c)，非界面使用的署名：分发或运营基于 Multica 后端、守护进程或命令行的产品时，须保留源码和 NOTICE 中的全部版权、专利、商标与署名声明，并在面向用户的文档中写明产品基于 Multica 构建，附 `https://github.com/multica-ai/multica` 链接 — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 条件 1(d)：品牌豁免不等于商业许可，商业许可也不自动豁免 (b)、(c)，两者须分别、明示取得 — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 条件 2，贡献：贡献者同意 “The producer can adjust this Multica License to be more strict or relaxed as deemed necessary”，并同意贡献代码可用于出品方的商业云业务 — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 条件 3：Apache 原文里的「this License」一律指整个 Multica 许可证；第一、二部分冲突时以第一部分为准；再分发 Multica 或其派生作品时，必须交付完整的许可证文件，只交付 Apache 原文不算满足 — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)
- Apache-2.0 第 1 条对「派生作品」的定义（已并入 Multica 许可证）：“Derivative Works shall not include works that remain separable from, or merely link (or bind by name) to the interfaces of, the Work” — [LICENSE 第二部分](https://github.com/multica-ai/multica/blob/main/LICENSE)
- NOTICE：版权归 Index Labs (Hong Kong) Limited，并重申许可证包含托管、品牌、署名三类附加条件 — [NOTICE](https://github.com/multica-ai/multica/blob/main/NOTICE)

**许可证的变更历史（证明「单方修改」已实际发生过）**
- `git log -- LICENSE`（`gh api repos/multica-ai/multica/commits?path=LICENSE`，2026-10-14 实查）：
  - 2026-04-01，改为纯 Apache 2.0（c52c6e69）；
  - 2026-04-08，改为「带商业限制的修改版 Apache 2.0」（f4ba27f2），同日收窄为只针对 SaaS 与转售（4f8969ef）；
  - 2026-07-30，把品牌条件扩大到全部界面代码，并新增 NOTICE（0314df3b）；
  - 2026-07-31，正式命名为「Multica License」，恢复逐字的 Apache 原文（00e20609）；
  - 2026-08-03，写明“免费的公开托管也需要商业许可”（10746ad3）；
  - 2026-09-24，官网增加许可证常见问题，措辞改为「源码可见」（9c69661f）。 — [LICENSE 提交历史](https://github.com/multica-ai/multica/commits/main/LICENSE)

**官方许可证常见问题（官网 /licensing，源文件位于仓库 `apps/web/features/landing/i18n/en.ts`）**
- 判断口诀：“Is anyone outside your organization driving the instance — creating issues, talking to agents, or triggering work? If so, through any interface (web, Slack, or API), that is a hosted service.”。如果组织外的人只是收到你们用 Multica 做出的结果，算内部使用 — [Multica Licensing 源文案](https://github.com/multica-ai/multica/blob/main/apps/web/features/landing/i18n/en.ts)
- 不需要商业许可的情形：组织内部使用（任意数量工作空间）；替客户部署、由客户自己内部使用；用 Multica 为客户干活、客户只拿交付物；智能体只向客户的 Slack 推送报告。需要商业许可的情形：替客户运营托管实例（收费与否都算）；组织外的人登录你的实例；组织外的人经网站、Slack 集成或接口驱动你的实例（免费也算）；把 Multica 嵌入你销售或分发的产品 — 同上
- 商业许可经「联系销售」表单申请，承诺三个工作日内回复。页面未给出价格 — 同上；[multica.ai](https://multica.ai)

### Inferences
- **(a) 公司内部使用**：免费。前提是只有本公司员工登录或驱动实例。如果外包方、合作伙伴、客户要登录或通过飞书、钉钉、接口触发智能体，按常见问题的口诀就落入「托管服务」，需要商业许可。「单一组织」是否包含母子公司、关联公司，许可证没有定义（见缺口）。
- **(b) 派生后开源**：可以公开源码，但有三个后果。
  1. 派生部分仍是 Multica 许可证，不是 OSI 认可的开源许可，无法把整个 Cohive 改发为 MIT 或 Apache-2.0。Apache 第 4 条允许你为自己新增的修改另定条款，但来自 Multica 的部分不变。
  2. 用了界面代码，就不能把品牌换成 Cohive；只用后端，就要在文档中声明「基于 Multica」。
  3. Cohive 的每个下游用户，只要对组织外提供服务，都要单独向 Multica 购买商业许可。对一个想做社区开源的项目，这几乎是否决性的。
- **单方修改的实际影响**：Apache 第 2 条的授权是“perpetual … irrevocable”，所以已经拿到的版本，按当时的许可证条款可以继续使用（推断，非法律意见）。但后续版本可能更严，历史上 4 月到 9 月已经收紧过四次。派生项目要么永远停在某个版本，要么接受每次升级时重新审视条款。
- **(c) 插件与外部集成**：Cohive 作为一个独立进程，通过 Multica 的命令行、HTTP 接口或插件机制交互、不复制其代码，按「可分离 / 仅链接接口」的定义**不是派生作品**，Cohive 自身代码可以用任意许可证。但有两点要注意：
  1. 条件 1(c) 的措辞是“a product or service built on Multica's backend”，如果 Cohive 必须依赖 Multica 才能工作，保守做法是在文档里注明「可选集成 Multica（基于 Multica 构建）」并附链接；
  2. 更关键的是**用户侧传导**：Cohive 用户运行的 Multica 实例，如果被组织外的人经 Cohive 入口驱动，按常见问题“through any interface … API”的说法需要商业许可。所以「Cohive 必须搭配 Multica」这种设计，会把 Multica 的商业限制传导给 Cohive 的所有用户。

### Gaps
- 「single organization」是否包括关联公司、集团子公司，许可证与常见问题都没有定义。
- 商业许可和品牌豁免的价格与条款不公开，未取得。
- 以上是对许可证文本的阅读，不是法律意见。正式决策前应由法务确认。

---

## 问题二：Multica 的架构与扩展点（MCP、接口、Webhook、插件、数据模型、代码托管集成、多用户、部署）

### Takeaway
Multica 是「问题单驱动、由守护进程在后台无界面运行智能体」的平台。截至 2026-10-14，**它不对外提供 MCP 服务**：代码里的 MCP 功能，都是把别人的 MCP 服务注入后台运行。开发者交互式的 Claude Code / Codex 会话，要读写 Multica 问题单，官方路径是「本机 `multica` 命令行 + 官方技能包」，身份是用户本人的个人访问令牌。对外扩展点有四类：
- 约 450 余条路由的内部应用接口（无版本承诺）；
- 只有 5 条路径的「公开接口 v1」（目前只服务插件）；
- 插件系统（事件钩子、界面面板，默认被特性开关关闭）；
- 仅用于触发自动任务的入站 Webhook。

数据模型是通用问题单，带自定义属性。状态没有固定流转，防循环只防「自己触发自己」和重复排队，不限制智能体之间来回的轮数。

### Cited Findings
**部署形态与技术栈**
- 架构：网页（Next.js 16）、桌面（Electron）、移动端（Expo）→ Go 后端（Chi、sqlc、gorilla/websocket）→ PostgreSQL 17（pgcrypto + pg_trgm）；本机守护进程通过 WebSocket 领取任务，再拉起本机智能体命令行 — [README](https://github.com/multica-ai/multica/blob/main/README.md)
- 自托管编排 `docker-compose.selfhost.yml` 只有三个服务：`postgres`（镜像 `pgvector/pgvector:pg17`）、`backend`（`ghcr.io/multica-ai/multica-backend`）、`frontend`（`ghcr.io/multica-ai/multica-web`）；另提供 Helm 包。登录靠邮件验证码，建议配置 `RESEND_API_KEY`，也支持 Google 登录；附件可放 S3 或本地卷 — [docker-compose.selfhost.yml](https://github.com/multica-ai/multica/blob/main/docker-compose.selfhost.yml)；[SELF_HOSTING.md](https://github.com/multica-ai/multica/blob/main/SELF_HOSTING.md)
- 自托管服务端默认每天发送一次匿名部署快照（版本号与分桶计数），设 `DO_NOT_TRACK=1` 关闭 — [README](https://github.com/multica-ai/multica/blob/main/README.md)；[SELF_HOSTING.md](https://github.com/multica-ai/multica/blob/main/SELF_HOSTING.md)
- 服务端代码中有 `entitlement`（「工作空间级的强制策略」，零配置即关闭，配置 BaseURL 后连接内部云策略端点）和 `seatcapacity`（「Multica Cloud 预购工作空间席位协议的执行端」）两个包。说明云版有席位与配额控制，自托管默认不启用 — 源码 `server/internal/entitlement/`、`server/internal/seatcapacity/`（[仓库](https://github.com/multica-ai/multica/tree/main/server/internal)）
- 数据库迁移文件已编号到 369 以上，例如 `369_plugin_mcp_approvals.up.sql` — 源码 `server/migrations/`

**MCP：不对外提供 MCP 服务（关键结论）**
- 「工作空间 MCP」是一个**MCP 服务器库**：管理员登记外部 MCP 服务，再「绑定」给某个智能体，领取任务时合并进该智能体的 `mcpServers` 配置。注释原文：“A workspace MCP server is a LIBRARY entry, exactly like a workspace skill: creating one gives it to nobody.” — 源码 `server/internal/handler/workspace_mcp.go`；命令行 `multica agent mcp` 的说明是“Manage which workspace MCP servers an agent uses”（`server/cmd/multica/cmd_agent_mcp.go`）
- `remote_mcp_broker.go` 是守护进程侧的远程 MCP 代理，把远程 MCP 服务转给后台运行使用；`plugin_hook_mcp.go` 注释为“A local MCP server that presents this workspace's plugin hooks as tools”，即只在守护进程本机、针对单次后台运行合成 MCP 工具 — 源码 `server/internal/daemon/`
- 全仓搜索处理 `"tools/list"` 的代码，只出现在上述守护进程代理、插件钩子、`pkg/remotemcp` 客户端和测试中，服务端没有对外的 MCP 端点 — 本地 `grep`（2026-10-14 克隆）
- 社区早在 2026-04-19 就提出「为 Multica 提供 MCP 服务」（#1351，4 条评论，均来自非成员，其中有人称赞其“CLI-first philosophy”）；2026-07-08 又有「官方 MCP 服务」提案（#5125，0 评论）。两者截至 2026-10-14 均未关闭，未见维护者回复 — [#1351](https://github.com/multica-ai/multica/issues/1351)；[#5125](https://github.com/multica-ai/multica/issues/5125)
- 交互会话的官方接入方式是 **multica-cli 技能包**（MIT 许可，2026-06-23 创建，最近推送 2026-08-14）。它教 Claude Code、Codex、Cursor 通过已登录的本机 `multica` 命令行读写问题单、评论、状态、@ 提及，并且“does **not** grant Multica access by itself”；写操作前应先征得用户同意 — [multica-cli README](https://github.com/multica-ai/multica-cli)

**接口与令牌**
- 个人访问令牌（`mul_` 前缀）“represents your account”，可访问你有权限的全部工作空间和接口；调用时放在 `Authorization: Bearer` 头里，工作空间级接口另带 `X-Workspace-ID` — [Auth tokens 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/auth-tokens.mdx)
- 后台运行时，服务端为每次运行签发临时令牌（`mat_` 前缀），“bound to the current user, workspace, agent, and run”，最长 24 小时，运行结束即清理；因此“Requests made by the agent are therefore recorded as agent actions” — 同上
- 命令行在守护进程管理的任务中，用注入的任务级凭证鉴权，不读写用户本人的配置文件 — [CLI 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/cli.mdx)
- **有版本承诺的「公开接口 v1」**（`server/pkg/publicapi/v1/openapi.yaml`）目前只有 5 条路径：`/context`、`/issues/{issue_ref}`、`/issues/{issue_ref}/comments`、`/storage/{scope}`、`/storage/{scope}/{key}`。服务地址是 `plugin-api.multica.ai/v1`，鉴权方式是插件令牌。说明文档写明“The Plugin API is the first deployed surface”；增量字段保持在 v1 内，破坏性变更需要新主版本和迁移窗口 — [publicapi/v1/README.md](https://github.com/multica-ai/multica/blob/main/server/pkg/publicapi/v1/README.md)
- 网页和命令行使用的应用接口规模较大（`server/cmd/server/router.go` 中约 457 处路由注册），没有版本化说明 — 本地 `grep -c`

**插件机制（特性开关 `plugins_v1`，默认关闭）**
- `PluginsV1Enabled` 调用 `flags.IsEnabled(ctx, PluginsV1, false)`，默认值为 false — 源码 `server/internal/featureflags/keys.go`
- 插件与 Multica 的关系分三种：Action（插件调 Multica）、Hook（Multica 调插件）、Resource（静态贡献，如技能文本）。界面挂载位置有 `issue_panel` 和 `modal`（`sidebar_panel` 尚未开放）。钩子触发方式有 `ui`、`manual`、`agent`、`event`、`schedule`（最小间隔 5 分钟），传输方式有 `http`、`mcp` — 源码 `server/pkg/plugincontract/manifest.go`、`capabilities.go`
- 插件能订阅的事件只有 7 种：`issue created/updated/status_changed`、`comment created`、`task started/completed/failed`。权限范围是封闭列表：`issues:read/write`、`comments:read/write`、`tasks:read/write`、`agents:read`、`members:read`、`storage:user/workspace`，以及 `net:<域名>` — 同上
- 插件调用外部服务时由服务端统一签名，不把签名密钥放到守护进程所在机器上 — 源码 `server/internal/daemon/plugin_hook_mcp.go` 注释

**Webhook**
- 入站：自动任务（Autopilot）可以由 Webhook 触发，有投递记录、重放与去重；自动任务被暂停、归档或事件被过滤时返回 200 并标记 ignored。近 7 天至少 50 次运行且失败率达 90% 时，自动暂停该任务 — [Autopilots 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/autopilots.mdx)；源码 `server/internal/handler/webhook_delivery.go`
- 代码托管侧：接收 GitLab、Gitea、Forgejo 的合并请求与流水线 Webhook — 见下文
- 未发现「工作空间事件 → 任意外部地址」的通用出站 Webhook。出站事件只能经插件的 `event` 钩子发出 — 本地源码检索

**数据模型与状态机**
- `Issue` 表的字段：`id`、`workspace_id`、`title`、`description`、`status`、`priority`、`assignee_type/assignee_id`、`creator_type/creator_id`、`parent_issue_id`、`acceptance_criteria`、`context_refs`、`due_date`、`number`、`project_id`、`origin_type/origin_id`、`first_executed_at`、`start_date`、`metadata`、`stage`、`properties`（自定义属性）、`revision`、`last_activity_at`、`triage_state`、`duplicate_of_issue_id` — 源码 `server/pkg/db/generated/models.go`
- 负责人可以是成员、智能体或小队。指派给成员时“no run is created”；指派给智能体时创建一次运行；指派给小队时唤醒领队 — [Issues 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/issues.mdx)
- 内置 7 种状态：`backlog`、`todo`、`in_progress`、`in_review`、`blocked`、`done`、`cancelled`，归入 4 个生命周期类别。“There is no fixed flow between statuses — members and agents can change them directly.”。状态由智能体在运行中通过命令行显式写入。系统只在两种情况下自动改状态：运行失败且无其他运行时回退到 `todo`；全部关联合并请求合并后改为工作空间设定的状态。管理员可以自定义状态，例如 `Code Review`、`QA` — 同上
- @ 提及：回复智能体的评论即交给该智能体；同一智能体连续收到的评论会合并成一次运行；`@all` 不包含智能体；“Agents have no inbox”。超时后不会自动转交负责人 — [Mentioning agents 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/mentioning-agents.mdx)
- 智能体之间互相 @：“the system merges duplicate runs at the same moment, but it does not decide when the collaboration should end — write the stop condition into the agent instructions” — 同上
- 小队：领队必须是智能体，成员可以是智能体或人；领队按系统提示中的「运行协议、成员名册、小队说明」用 @ 分派。防循环规则有四条：领队自己的评论不重新触发自己；显式 @ 他人的评论不唤醒领队；领队已有排队中的运行时去重；纯文本 `@name` 不触发 — [Squads 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/squads.mdx)
- 代码层面的防循环是 `IsSelfLoop`（检查当前请求是否来自正在该问题单上运行的智能体）和 `SuppressActiveSelfAssignment`（抑制重复的自我指派入队）— 源码 `server/internal/handler/issue_trigger.go`；`issueguard` 包只做重复问题单判定 — `server/internal/issueguard/duplicate.go`

**代码托管集成**
- GitHub 通过 GitHub App 接入，自托管需要自建 App；可以暂停 GitHub 功能而不断开连接 — [GitHub integration 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/github-integration.mdx)
- 自建 GitLab、Gitea、Forgejo 的集成**仅限自托管 Multica**，需要设置 `MULTICA_VCS_INTEGRATION_ENABLED=true` 和 `MULTICA_VCS_SECRET_KEY`。GitLab 令牌只需要 `read_api` 权限，Webhook 要勾选 Merge request events 和 Pipeline events。分支名或标题中含问题编号、或正文 `Closes MUL-123` 时，自动关联到该问题单；全部关联合并请求合并后改状态；流水线结果显示为检查条；支持私有证书颁发机构 — [Self-hosted Git 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/vcs-integration.mdx)
- 智能体创建合并请求不经过 Multica 配置，而是“using the runtime host's own Git credentials”，例如运行机上的 SSH 部署密钥或凭证助手 — 同上

**多用户、权限、隔离**
- 角色分 `owner`、`admin`、`member` 三级 — [Members & roles 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/members-roles.mdx)
- 每个智能体的访问范围有三档：仅自己、整个工作空间、指定的人；新建智能体默认「仅自己」 — [Agents 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/agents.mdx)
- 工作空间是顶层边界，成员、问题单、智能体、运行记录完全隔离 — [Workspaces 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/workspaces.mdx)
- 安全模型：运行默认拥有守护进程所在系统用户的全部权限；“Multica makes no filesystem-sandbox guarantee”。Codex 以 `danger-full-access` 运行，Claude Code 以 `--permission-mode bypassPermissions` 运行（Windows 上显式开启 Codex 沙箱时例外）。隔离边界要靠容器、专用用户或虚拟机自己划定 — [Security model 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/security-model.mdx)
- 频道集成有 Slack、飞书/Lark、钉钉、企业微信、Telegram（后三者为社区维护）— [README](https://github.com/multica-ai/multica/blob/main/README.md)

### Inferences
- 对 Cohive 来说，最重要的事实是：**Multica 的核心交互模式是「人在看板派活 → 后台无界面运行」**。人在自己的交互会话里与 Multica 协作，只能靠「命令行 + 技能包」。这条路径能用，但有两个限制：
  - 身份上只是「某个人」，不是「某人的某个工具」；
  - 没有任何「把待办推进正在运行的交互会话」的机制：智能体没有收件箱，也没有会话钩子注入。

  这正是 Cohive 方案第 4.2 节设计的核心（远程 MCP、按「人 + 工具」发令牌、会话钩子注入待办）。
- 社区要求 MCP 服务已近半年、未获回应，再加上团队公开的「CLI-first」倾向，说明 Multica 短期内未必会补上这块。但这是推断，不能排除他们随时加入（风险见问题六）。
- 用来做外部集成的接口面不理想：
  - 唯一有版本承诺的接口只覆盖问题单读写与评论，且只对插件开放；
  - 插件系统默认关闭，仍在演进；
  - 应用接口规模大、无版本承诺，迁移文件已达三百多号。

  在其上做一层，最稳的耦合点是**命令行**（官方技能包每天在持续集成里与真实命令行对齐校验），其次是应用接口。
- 数据模型足以「装下」Cohive 的问题单：`acceptance_criteria`、`properties` 和 `metadata` 可以存期望结果、复现脚本、提交号，自定义状态可以表示「待验证 / 已验证 / 重开」。但状态流转不受约束、无超时升级，这些语义要由上层自己保证。

### Gaps
- 未逐条核实应用接口路由的鉴权差异，例如某些路由是否只认浏览器会话、不认个人访问令牌。
- 未核实插件系统在自托管上能否通过环境变量开启（特性开关的配置来源未追查）。
- 未找到代码占用（文件锁、租约）相关实现；全文检索 issue 标题也没有相关提案。只能说「未发现」，不能断言绝对不存在。

---

## 问题三：对照 Cohive 方案的差异点，Multica 是否已具备

### Takeaway
在 Cohive 的七个差异点中，Multica **完整具备的一个也没有**，有三四个是「部分具备」（身份追溯、拉闸、交互会话交接、归属路由）。完全不具备的是接口变更广播、代码占用声明、复现脚本驱动的自动验证闭环。另外，Multica 已经很好地覆盖了 Cohive 第一阶段的「看板 / 问题单 / 指派 / 多工具运行 / GitLab 合并请求与流水线联动」，以及第三阶段的「自动执行」。

### Cited Findings
| Cohive 差异点 | Multica 现状 | 依据 |
|---|---|---|
| 交互会话之间的交接（MCP 直入会话、会话内待办提示） | **部分**：交互会话可借命令行和技能包读写问题单；指派给成员时不创建运行，成员可在 Web、桌面、移动端的收件箱收到通知。但没有对外 MCP 服务、没有会话钩子注入，“Agents have no inbox” | [multica-cli](https://github.com/multica-ai/multica-cli)；[Issues 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/issues.mdx)；[Mentioning 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/mentioning-agents.mdx) |
| 接口归属路由（谁负责某接口） | **部分**：没有确定性的「接口或服务 → 负责人」归属表；只有小队领队智能体按「小队说明」里的自然语言规则用 @ 分派，由模型判断 | [Squads 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/squads.mdx) |
| 接口变更广播 | **无**：插件事件只有问题单、评论、运行三类；代码托管 Webhook 只处理合并请求和流水线，没有推送事件或文件变更的订阅 | `plugincontract/manifest.go`；[Self-hosted Git 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/vcs-integration.mdx) |
| 代码占用声明 | **未发现** | 本地源码与 issue 检索 |
| 复现脚本驱动的验证闭环 | **无原生支持**：有验收标准字段和 `in_review` 评审关口；`done` 通常由人确认，或由合并请求合并触发，没有「重跑复现脚本 → 通过则关闭 / 失败则重开」 | [Issues 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/issues.mdx) |
| 按「人 + 工具」的身份追溯 | **部分**：后台运行的 `mat_` 令牌绑定「用户 + 工作空间 + 智能体 + 运行」，智能体本身记录了服务商（工具）。交互会话走个人访问令牌，只记到人 | [Auth tokens 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/auth-tokens.mdx) |
| 拉闸与凭证吊销 | **部分**：可吊销个人访问令牌；归档智能体会取消其全部未完成运行；自动任务可暂停；可暂停 GitHub 功能。未发现「工作空间一键停止派发」。推送代码用的是运行机自身的 git 凭据，不是平台签发的短期、单仓库凭证；运行默认无沙箱 | [Agents 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/agents.mdx)；[Security model 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/security-model.mdx)；[Self-hosted Git 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/vcs-integration.mdx) |
| （附）超时升级、超轮次升级 | **无**：回复超时不会转交；智能体之间来回不限轮次，要求在指令中自写停止条件 | [Mentioning 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/mentioning-agents.mdx) |
| （附）问题单只引用明确提交号 | **无专门字段**，可用自定义属性或元数据承载 | `models.go` 中的 `Issue` 结构 |

### Inferences
- Multica 与 Cohive 的**重叠在「平台底座」**：看板、问题单、成员、工作空间、GitLab 联动、后台执行。**差异在「协作语义」**：确定性归属、跨会话交接、广播、占用、自动验证、升级规则。
- 差异点几乎都能用「外部服务 + Multica 命令行或接口」实现，不必改 Multica 代码：
  - 归属表、广播、占用租约放在 Cohive 自己的库里；
  - 验证闭环由 Cohive 小助手在本机重跑复现脚本，再改 Multica 状态。

  这说明「薄层」路线在技术上可行。
- 反过来，Cohive 第一阶段最核心的东西（远程 MCP 直入交互会话、「人 + 工具」令牌），恰好是 Multica 架构里最缺的。如果 Cohive 自建，这块是真正的差异化；如果做薄层，这块也必须由 Cohive 自建，Multica 帮不上。

### Gaps
- 未实际部署 Multica 验证「指派给成员」后，成员在命令行或交互会话中的完整体验（收件箱能否经命令行拉取、是否有系统通知）。
- 「工作空间级一键停止」可能存在于设置界面而未写进文档，未验证。

---

## 问题四：Multica 项目健康度与商业模式

### Takeaway
热度与投入都非常高：
- 2026-01-13 创建，9 个月 5.28 万星、6,895 个复刻、339 位贡献者，发版约每 2 到 4 天一次；
- 背后是香港公司 Index Labs，团队此前做过 devv.ai；
- 商业模式是「免费源码可见 + Multica Cloud 订阅（有免费与 Pro 档）+ 商业许可 + 品牌豁免」。

风险信号有三个：
- 核心开发高度集中在 5 个账号；
- 1,018 个未关闭 issue、846 个未合并合并请求，近 30 天新 issue 关闭率约 38%；
- 9 月底以后 main 分支提交量明显下降，原因未知。

### Cited Findings
- 仓库元数据（2026-10-14）：星数 52,790，复刻 6,895，创建于 2026-01-13，最近推送 2026-10-14，GitHub 识别的许可证为 NOASSERTION — `gh api repos/multica-ai/multica`；[仓库](https://github.com/multica-ai/multica)
- 贡献者 339 个（贡献者接口分页计数）。前五名提交数：Bohan-J 1535、forrestchang 1111、NevilleQingNY 1096、ldnvnbl 500、multica-eve 485；第六名起都在 61 次以下 — `gh api repos/multica-ai/multica/contributors`
- 发版：v0.4.37（2026-08-31）到 v0.6.2（2026-10-13），共 15 个版本，仍处于 0.x — `gh api repos/multica-ai/multica/releases`；[Releases](https://github.com/multica-ai/multica/releases)
- 提交量：周提交统计显示 7 月底到 9 月中每周 96 到 183 次；按提交接口实数，2026-09-14 以来 309 次、09-28 以来 64 次、10-07 以来 49 次。2026-09-14 以来合并 310 个合并请求，10-01 以来 58 个 — `gh api .../stats/commit_activity`、`.../commits?since=`、`search/issues`
- issue 与合并请求：已关闭 issue 1,798，未关闭 1,018；未合并的合并请求 846；2026-09-14 以来新开 issue 281 个，其中已关闭 107 个 — `gh api search/issues`
- 公司与团队：许可证与 NOTICE 写明出品方为 Index Labs (Hong Kong) Limited。官网「关于」页称团队自 2021 年起合作，此前做过面向开发者的 AI 搜索 devv.ai，2025 年转做 Multica — [LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)；[关于页源文案](https://github.com/multica-ai/multica/blob/main/apps/web/features/landing/i18n/en.ts)
- 商业模式：
  - 官网首屏有「Start free trial」和「Talk to sales」；
  - 隐私政策提到 Multica Cloud 订阅通过 Stripe 付款；
  - 前端代码中有 `plan: "free"` 和 `plan: "pro"` 两种计划，以及席位购买组件；
  - 服务端有云版席位与授权策略模块，以及 Cloud Fleet 节点凭证（`mcn_`）。

  官网 `/pricing` 返回 404，首页未列价格 — [multica.ai](https://multica.ai)；[隐私政策源文案](https://github.com/multica-ai/multica/blob/main/apps/web/features/landing/i18n/en.ts)；源码 `packages/views/settings/components/billing-state.test.ts`；[Auth tokens 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/auth-tokens.mdx)
- 同一组织下还有 `andrej-karpathy-skills` 仓库（约 21.97 万星），说明团队擅长流量运营 — `gh api orgs/multica-ai/repos`

### Inferences
- 团队规模小而产出高，属于「核心小团队 + 大量外部贡献」的模式。846 个积压的合并请求说明外部贡献吸收能力有限。对 Cohive 来说，**想把自己需要的功能（例如对外 MCP 服务）贡献给上游并被合并，不确定性较高**。何况按许可证第 2 条，贡献代码将被出品方用于商业云业务。
- 9 月底以后提交量下降可能有多种原因（发版节奏调整、合并策略变化、团队转向云端或私有仓库），**无法确认**，只能作为需要观察的信号。
- 0.x 版本、迁移频繁、许可证 9 个月改了 6 次。无论派生还是薄层，都要预期较高的跟进成本。

### Gaps
- Multica Cloud 的具体价格、席位限制、商业许可价格：官网未公开，未取得。
- 是否有融资、团队人数：未找到可靠来源。
- issue 首次响应时间的统计：只抽样看了最新几条（当天新开的 issue 多数为 0 评论），未做系统统计。

---

## 问题五：OpenHands Agent Canvas 评估（许可证、架构、多人协作、交互会话交接、与 Cohive 第三阶段的重叠）

### Takeaway
Agent Canvas 的主仓采用 **MIT** 许可（企业版另有不开源的 PolyForm 试用许可），定位是「**个人或小团队**自托管的编程智能体控制中心 + 自动化」，状态为 beta。开源版本的鉴权只是「每个后端一把共享接口密钥」，没有成员、角色、问题单路由；多用户鉴权在 OpenHands Enterprise 里（商业许可，每年只能免费试用 30 天）。它不解决「人与人之间的交互会话交接」，与 Cohive 第一、二阶段几乎不重叠；与第三阶段（自动执行、Webhook 与定时触发、多后端执行、按会话的 Docker 沙箱）高度重叠，适合作为**可插拔执行后端**。

### Cited Findings
- 仓库 OpenHands/OpenHands：MIT，90,873 星，最近推送 2026-10-14；README 标题为 “Agent Canvas”，自述“The self-hosted developer control center for coding agents and automations”；状态徽章为 beta — `gh api repos/OpenHands/OpenHands`；[README](https://github.com/OpenHands/OpenHands)
- 能运行 OpenHands、Claude Code、Codex、Gemini、Pi、OpenCode 及任意 ACP 智能体，可在本机、Docker、虚拟机、公司基础设施或 OpenHands Cloud 上运行；自动化可按计划或 Webhook 事件触发，集成 Slack、GitHub、Linear 等 — 同上
- 架构：前端连接多个 Agent Server（Python 编写，由 software-agent-sdk 实现，提供 REST 接口，“a REST API for running multiple agents on a single machine”），常与 Automation Server（调度、Webhook、运行历史）配合。仓库职责划分：OpenHands/OpenHands 是前端与本地编排，software-agent-sdk 是 Agent Server 与规范接口，typescript-client 是 TS 客户端，automation 是自动化服务 — 同上
- 许可证：software-agent-sdk 为 MIT（1,254 星）；automation 为 MIT；OpenHands/enterprise 为 “PolyForm Free Trial License 1.0.0”，README 警告“This is **not** an open source license. Usage is limited to 30 days per calendar year without a commercial license”，自述包含“authentication, billing, and integrations”等企业功能 — `gh api repos/OpenHands/{automation,software-agent-sdk,enterprise}`；[enterprise README](https://github.com/OpenHands/enterprise)
- 安装需要 Node.js 24 以上和 `uv`；“无沙箱”模式下智能体对文件系统有完全访问权限；可选 `OH_CONVERSATION_RUNTIME=docker`，让每个会话在独立容器中运行。但使用同一宿主目录的会话仍共享文件，需要自行使用不同目录或工作树 — [README](https://github.com/OpenHands/OpenHands)
- 自托管鉴权：设置 `LOCAL_BACKEND_API_KEY` 并以 `--public` 启动，所有 `/api/*` 请求须带 `X-Session-API-Key`，界面也要先输入该密钥；文档建议的机器规格写的是“2 vCPU / 4 GB RAM is plenty for a single user” — [SELF_HOSTING.md](https://github.com/OpenHands/OpenHands/blob/main/docs/SELF_HOSTING.md)
- 官方后端文档只描述后端的连接信息（显示名、主机地址、接口密钥）与按后端管理的设置、模型、MCP、自动化，未提及账号、团队、权限或会话共享与交接 — [Agent Canvas Backends 文档](https://docs.openhands.dev/openhands/usage/agent-canvas/backends)
- 发版节奏：v1.23.0（2026-09-23）到 v1.27.1（2026-10-12）；2026-08-06 有提交把桌面应用更名为「OpenHands Agent Canvas」 — `gh api repos/OpenHands/OpenHands/releases`、`search/commits`

### Inferences
- **与 Cohive 的分工清晰**：Agent Canvas 解决的是「一个人如何在多处后端运行和自动化智能体」，Cohive 解决的是「多个人的交互会话之间如何交接、归属、验证」。两者不是竞争关系。
- **作为 Cohive 第三阶段的执行后端很合适**：主仓、SDK、自动化服务都是 MIT，可以被开源的 Cohive 依赖或对接；Agent Server 有 REST 接口，可被 Go 服务调用；已支持 ACP 智能体和按会话的 Docker 隔离。代价是：
  - 技术栈为 Python 加 Node，状态为 beta；
  - 开源版没有多用户，团队共享一个 Agent Server 时要共用一把密钥，审计粒度只到后端；
  - 「人 + 工具」的身份追溯仍需由 Cohive 在派发时记录。
- 不要依赖 OpenHands Enterprise：它的 PolyForm 试用许可每年只有 30 天，与 Cohive 的开源目标冲突。

### Gaps
- Agent Server 的 REST 接口是否稳定（有无版本承诺）、能否对单个会话下发「工作目录 + 智能体 + 提示词」并流式取回事件，未读 software-agent-sdk 的接口规范逐条核实。
- Agent Canvas 是否对外提供 MCP 服务、是否支持把一个会话转交给另一个人，官方文档未提及，未核实。
- OpenHands Enterprise 的价格未查。

---

## 问题六：三条路线的利弊与推荐（分「公司内部使用」与「Cohive 开源」两种目标）

### Takeaway
- **公司内部使用**：推荐**路线二（在 Multica 之上做薄协作层）**，并且先花半天自托管 Multica 验证。由 Multica 承担看板、问题单、GitLab 联动和第三阶段的后台执行；Cohive 只做 Multica 缺的部分：远程 MCP 服务、「人 + 工具」令牌、归属表路由、广播、占用、复现脚本验证、超时升级。**不要派生改代码**，上游变化太快。
- **Cohive 开源**：推荐**路线三（独立自建，借鉴 Multica 设计）**，并把 Multica 和 Agent Canvas 都做成**可选适配器**：Multica 作为「问题单与看板后端」之一，Agent Canvas 作为第三阶段的执行后端之一。**不派生 Multica 代码**。

两种目标可以用同一套代码满足：Cohive 核心自建且独立可用，公司内部部署时再打开 Multica 适配器。

### Cited Findings
（以下判断基于前五个问题的引用，这里只列关键依据）
- 内部使用免费、多工作空间免费 — [LICENSE 条件 1(a)](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 派生物须交付完整的 Multica 许可证，界面品牌不可去除，非界面使用须声明「基于 Multica」，下游运营者各自需要商业许可，出品方可修改许可证 — [LICENSE 条件 1(b)(c)、2、3](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 「可分离、仅链接接口」的作品不是派生作品 — [LICENSE 第二部分第 1 条](https://github.com/multica-ai/multica/blob/main/LICENSE)
- 组织外的人经接口驱动实例，即属托管服务 — [Licensing 源文案](https://github.com/multica-ai/multica/blob/main/apps/web/features/landing/i18n/en.ts)
- Multica 不对外提供 MCP 服务，相关 issue 自 2026-04 起未处理 — [#1351](https://github.com/multica-ai/multica/issues/1351)
- 有版本承诺的公开接口只有 5 条路径，且只对插件开放；插件系统默认关闭 — [publicapi/v1](https://github.com/multica-ai/multica/blob/main/server/pkg/publicapi/v1/README.md)；`featureflags/keys.go`
- Agent Canvas 主仓、SDK、自动化服务均为 MIT — `gh api`；[README](https://github.com/OpenHands/OpenHands)

### Inferences

**三条路线对比**

| 路线 | 公司内部使用 | Cohive 开源 |
|---|---|---|
| **一、基于 Multica 派生二次开发** | 利：一天内拥有完整的看板、问题单、26 种智能体运行、GitLab 联动、飞书/钉钉/企业微信频道；同为 Go + Postgres，与方案技术栈一致。弊：上游 0.x 版本、日均十余次提交、三百多个迁移文件，派生后合并上游的成本极高；前端是 Next.js 16，与方案 7.1 节「看板用 React + Vite、不用 Next.js」的决策相反；界面品牌必须保留；运行默认无沙箱。**结论：不推荐**，除非打算永久停在某个版本。 | 弊：派生物无法成为 OSI 开源项目；Cohive 的每个下游用户若向组织外提供服务，都要买 Multica 商业许可；出品方可收紧许可证；品牌与署名要求和独立品牌冲突。**结论：否决**。 |
| **二、在 Multica 或 Agent Canvas 之上做薄协作层** | 利：工作量最小，Cohive 第一阶段只需做远程 MCP 服务、本机小助手、归属表与验证逻辑，问题单和看板直接复用 Multica；第三阶段的后台执行也直接复用 Multica 的守护进程。弊：耦合点要么是无版本承诺的应用接口，要么是命令行，升级时可能被破坏；数据模型受制于 Multica（状态不能强制流转、无超时升级，只能靠上层兜底）；交互会话以 MCP 写入时，要用某种身份代理调用 Multica——要么为每人保存一个个人访问令牌，要么用一个服务账号再在评论或属性里标注「人 + 工具」，Multica 侧的审计会失真。**结论：内部推荐**。 | 利：Cohive 自身代码可以用 MIT 或 Apache 许可。弊：产品形态变成「Multica 的配件」，用户必须先部署 Multica 并接受其许可证（向组织外提供服务即需购买），开源吸引力和独立性很弱；Multica 一旦自己加上 MCP 服务，Cohive 的价值就被吃掉一大半。Agent Canvas 之上的薄层则不成立，因为它没有多人与问题单模型，只适合当执行后端。**结论：不作为主路线，只作为可选适配器**。 |
| **三、独立自建，借鉴设计** | 利：数据模型、状态机、身份模型完全贴合 Cohive；没有许可证风险。弊：要自己做看板与问题单 CRUD、GitLab 联动（工作量约多出数周），并与 Multica 正面重叠。**结论：可行但不必要**；如果公司本来就要开源 Cohive，则直接走这条，内部部署时接 Multica 适配器即可。 | 利：许可证干净，可以开源；差异化清晰（交互会话之间的 MCP 协作层），不与 Multica 的「后台派活看板」正面竞争。弊：工作量最大；要证明「交互会话交接」值得单独做成产品。**结论：开源推荐**。 |

**具体落地建议**
1. **先验证再决策（半天到一天）**：在本机按官方 compose 自托管 Multica，设 `DO_NOT_TRACK=1`，并让守护进程运行在专用系统用户或容器内。在两个交互会话里装上 multica-cli 技能包，手工模拟一次方案第 5 节的「发现 → 交接 → 修复 → 验证」。观察三点：交互会话经命令行读写是否顺手；指派给成员后对方能否及时知道；复现脚本和提交号放在自定义属性里是否够用。
2. **把 Cohive 设计成「协作语义层」，问题单存储做成适配器接口**：自建实现（开源默认）和 Multica 适配器（公司内部可选）并列，与方案「代码平台用适配器模式」同一个思路。这样内部可以先用 Multica 快速上线，开源版本也不被许可证绑定。
3. **身份设计提前定好**：Multica 的个人访问令牌只代表「人」。如果内部走路线二，Cohive 必须自己签发「人 + 工具」令牌并在自己的库里记账，写入 Multica 时把工具标识写进评论或属性，否则追溯会丢失。
4. **第三阶段执行后端分两档**：内部可以直接用 Multica 的守护进程（已支持 26 种命令行和运行日志回放）。开源版优先对接 MIT 许可的 Agent Canvas Agent Server 或 GitLab Runner，不依赖 Multica 执行层，也不依赖 OpenHands Enterprise。
5. **借鉴清单（只借设计，不复制代码）**：
   - Multica：`mat_` 运行级临时令牌（绑定人、空间、智能体、运行，最长 24 小时）；小队防循环四条规则与同一智能体的触发去重；连续评论合并成一次运行；自动任务失败率熔断；插件权限的封闭列表与 `net:` 出站白名单；外部 MCP 工具按结构摘要固定、需要审批后才生效；状态分「类别 + 具体状态」两层。
   - Cohive 应补上而 Multica 没有的：智能体之间往返的**轮次上限**（Multica 明确把这件事交给指令自行约定）；超时升级。
6. **超出原问题框架的提醒**：
   - Multica 的社区已有大量「对外 MCP 服务」需求。Cohive 的第一阶段差异化窗口可能只有几个月，应尽快做出可用原型，而不是先做看板。
   - 如果 Cohive 将来要开源，**不要阅读后照抄 Multica 的界面代码结构**。其品牌条款覆盖「派生自其界面代码」的界面，即使经过修改、移动或抽取，保持「净室」更稳妥。
   - 公司内部使用 Multica 时，如果需要让外包或合作方登录，事先确认是否落入「托管服务」，必要时联系对方销售取得书面答复。

### Gaps
- 未实际部署、运行 Multica 与 Agent Canvas，以上「薄层可行性」都基于文档与源码阅读，需要按建议 1 实测确认。
- Multica 是否会在近期推出官方 MCP 服务，没有公开路线图可查。
- 路线三相对路线二多出的工作量（「约多出数周」）是粗略估计，没有依据详细拆分。
