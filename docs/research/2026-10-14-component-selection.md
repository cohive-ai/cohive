# Cohive 选型：自建协作语义，其余各层借力

> 这是方案早期（V0.1）的选型调研，部分前提已被后续决定取代（例如不做自动执行、不做本机后台进程），现行方案以 [docs/plan.md](../plan.md) 为准。
>
> 信息日期：2026-10-14。依据为 7 篇调研笔记（智能体间协作先行方案、任务与问题单系统、记忆与共享知识、编排与持久化执行、智能体接入与执行隔离、可观测性与审计、Multica 与 Agent Canvas 深查；原稿可在提交 `def43c0` 中查看），以及当时的方案 V0.1。星数、许可证、最近推送时间都是当天用 GitHub 接口实查的结果。文中所有许可证判断都只是工程视角的阅读，**不是法律意见**；标有「需法务确认」的条目，正式决策前必须请法务过目。

没有任何现成项目覆盖 Cohive 的定位。最接近的 Multica 已经做好了“看板 + 问题单 + 本机守护进程 + 自建 GitLab 联动 + 后台无界面执行”，但它**不对外提供 MCP 服务**，官方文档原话是“智能体没有收件箱”。它也没有确定性的归属路由、接口变更广播、代码占用，没有复现脚本驱动的验证闭环。OpenHands Agent Canvas 是单人或小团队用的执行控制台，开源版没有成员模型，也没有问题单模型。所以 Cohive 真正必须自建的只有一层：**交互会话之间的协作语义**，包括远程 MCP、按“人 + 工具”签发的令牌、收件箱与推送、归属路由、建议性代码占用、验证闭环、超时与轮次升级、审计。这一层恰好是 Multica 架构里最缺的。其余各层尽量借用现成的：队列用 river，第三阶段的长链路用 DBOS Go，智能体驱动用 ACP（Go 端用 coder/acp-go-sdk），隔离用工作树加工具自带沙箱，归属与契约用 Backstage 描述格式加 oasdiff、buf，团队知识放 git 里的 Markdown，观测用 OpenTelemetry 并且只作可选外接。路线上分两种目标：单一组织内部自用可以在 Multica **旁边叠加**一层（不派生改它的代码）；开源目标必须**独立自建**，Multica 和 Agent Canvas 只做可选适配器。两种目标共用同一套核心代码。plan-v0.1 的大方向（Go + Postgres + river、远程 MCP、本机小助手、自动执行放到第三阶段）都得到了调研支持，但有十余处需要修正。最紧迫的四处：Codex 现在也有会话钩子；自动执行必须用 API 密钥；“升级”应当和状态正交，不该是一个状态；对外命名只用 Cohive，避免与智能体客户端协议（ACP）混淆。动手前最值得先做两件事：查清要对接的 GitLab 的版本与授权档位；花半天自托管试用一次 Multica。

## 一页结论：十一个组成部分里只有协作语义必须从零写

下表的“阶段”对应 plan-v0.1 第 9 节。“自有”指 Cohive 自己的代码，许可证由作者决定。

| 层 | 推荐 | 理由 | 许可证 | 阶段 |
|---|---|---|---|---|
| 智能体间消息与收件箱 | **自建**：协作服务直接提供远程 MCP；本机小助手维持 WebSocket 长连接，配一张未确认消息表，断线重连后补发；会话内待办提示用两家工具的钩子注入 | 现有项目要么只能在本机用（claude-peers、Concord、Claude Code 跨会话消息），要么没有成员模型（MCP Agent Mail）；Multica 不对外提供 MCP | 自有 | 一 |
| 代码占用 | **自建建议性租约**：带过期时间，分独占与共享，冲突立即报告，可选提交前钩子拦截，占用原因填问题单号 | 设计直接借鉴 MCP Agent Mail 和 Concord，代码量很小；跨机器、跨人的版本没人做过 | 自有 | 一末（原方案为二） |
| 问题单与任务 | **自建 Postgres 薄状态机作为真源**；GitLab/GitHub 议题只做镜像，用官方 Go 客户端写入；组织内部自用时可选接 Multica 适配器 | 问题单跨仓库、跨人，本身就是产品；通用项目管理系统意味着多部署一套系统，而且多为 AGPL；免费版 GitLab 无法表达互斥状态 | 自有；Multica License（仅内部可选） | 一 |
| 归属与接口契约 | 服务登记文件采用 Backstage `catalog-info.yaml` 的子集，**不部署 Backstage**；用 `oasdiff`、`buf breaking` 检测破坏性变更 | 事实标准格式；两个检测工具都用 Go 写，可以编进同一个可执行文件 | Apache-2.0 | 一（归属）／二（广播） |
| 记忆与共享知识 | 团队约定写成 git 里的 Markdown 决策记录，经合并请求由人审批后合入；指令文件里只放索引，内容通过 MCP 只读查询；graphify 作为可选证据；**不引入任何记忆系统** | 主流记忆系统都是 Python 加大模型抽取，团队功能都在付费墙后面；人工审批能封堵“借数据下指令”这条路 | graphify 为 Apache-2.0 | 三（第一阶段只做归属表） |
| 编排与持久化执行 | 第一阶段用 river 加自写状态机；第三阶段的自动执行链路用 **DBOS Go**；不用 Temporal，也不用任何智能体编排框架 | 两者都是库形态，能编进同一个可执行文件，只依赖已有的 Postgres | river 为 MPL-2.0，DBOS Go 为 MIT | 一／三 |
| 智能体接入 | 第一阶段：远程 MCP + 指令文件 + 两家工具的钩子；第三阶段：通过 **ACP** 驱动智能体（coder/acp-go-sdk 拉起 claude-agent-acp、codex-acp），降级路径为 `claude --bare -p` 和 `codex exec --json` | ACP 注册表已收录 40 多个智能体，自带标准的权限请求，可以直接映射成人工审批 | Apache-2.0 | 一／三 |
| 执行隔离 | 本机：工作树加工具自带沙箱；服务器：复用团队已有的 GitLab Runner；可选 E2B 自托管、Kubernetes agent-sandbox、OpenHands Agent Canvas 作为执行后端 | 起步零新增依赖，后端可以插拔替换 | Apache-2.0／MIT | 三 |
| 可观测性 | Postgres 里存业务事件和“会话 ↔ 问题单”关联表；可选实现一个精简的 OpenTelemetry 日志接收接口；后期加团队级 OTel Collector 做脱敏，Grafana、Langfuse 只作外接 | 两家智能体的原生遥测以指标和日志为主，大模型专用观测平台接不顺，而且大多依赖 ClickHouse | Collector 为 Apache-2.0 | 一（关联表）／二 |
| 审计 | Postgres 追加写表，按工作空间做哈希链，应用账号只授予插入和查询权限；写入前用 gitleaks 规则扫描密钥 | Go 生态没有成熟的审计库，自己写代码量很小 | gitleaks 为 MIT | 一 |
| 看板 | 维持 7.1 节：自建 React + Vite；组织内部自用时如果试用 Multica 满意，可以先借用它的界面 | Multica 前端是 Next.js 16，并受品牌条款约束 | 自有 | 一 |

**“基于 / 叠加 / 自建”路线结论**

| 目标 | 基于 Multica 派生改代码 | 在 Multica / Agent Canvas 之上叠加 | 独立自建 |
|---|---|---|---|
| 组织内部自用（自建 GitLab） | **不推荐**：上游仍是 0.x，迁移文件已编到 369 号以上，许可证 9 个月改了 6 次，派生后合并上游的成本极高 | **可行，以试用结果为前提**：Cohive 自建协作语义核心，问题单状态的真源仍在 Cohive；Multica 只当看板和第三阶段后台执行的可选适配器 | 同样可行，也是叠加方案的底座 |
| Cohive 开源 | **否决**：派生物不能成为 OSI 认可的开源项目，下游运营者各自都要买商业许可 | 只作可选适配器：Multica 是问题单与看板后端之一，Agent Canvas 是执行后端之一 | **推荐** |

两种目标落到同一个架构上：协作语义核心独立可用，问题单存储与执行后端都做成适配器接口（与方案“代码平台用适配器”同一思路），组织内部自用时再打开 Multica 适配器。

## Multica 补齐了底座，唯独缺 Cohive 第一阶段要的东西

Multica 是目前与 Cohive 最像的项目：Go 后端（Chi、sqlc、gorilla/websocket），PostgreSQL 17，本机守护进程经 WebSocket 领取任务后拉起本机智能体命令行，支持 26 种智能体，可接自建 GitLab ([Multica README](https://github.com/multica-ai/multica/blob/main/README.md))。它的热度也是 Cohive 追不上的：2026-01-13 创建，到 10 月 14 日已有 **52,790 星、339 位贡献者**，发版约每 2 到 4 天一次 ([Multica 仓库](https://github.com/multica-ai/multica))。但深查源码后得出的关键事实是：**它不对外提供 MCP 服务**。代码里的“工作空间 MCP”是一个库，管理员把外部 MCP 服务登记进去，再在后台运行时注入给智能体，源码注释原话是“creating one gives it to nobody”。社区在 2026-04-19 就提了“为 Multica 提供 MCP 服务”的需求，到调研当天仍未关闭，也没有维护者回复 ([#1351](https://github.com/multica-ai/multica/issues/1351))。开发者自己的交互式 Claude Code / Codex 会话要读写 Multica 问题单，官方路径只有“本机 `multica` 命令行 + 技能包”，用的是个人令牌，身份只记到“人”，记不到“人的哪个工具” ([multica-cli](https://github.com/multica-ai/multica-cli))。

协作语义上的缺口更系统。Multica 内置 7 种状态，文档写明“There is no fixed flow between statuses”，状态由智能体在运行中通过命令行自行写入 ([Issues 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/issues.mdx))。回复超时不会转交；智能体之间互相 @ 不限轮次，文档要求用户“把停止条件写进智能体指令” ([Mentioning agents 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/mentioning-agents.mdx))。路由靠小队领队智能体按自然语言说明去 @ 人，结果由模型判断，没有确定性的“接口 → 负责人”表 ([Squads 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/squads.mdx))。安全方面，后台运行默认拥有守护进程所在系统用户的全部权限：Claude Code 以 `bypassPermissions` 运行，Codex 以 `danger-full-access` 运行，“Multica makes no filesystem-sandbox guarantee” ([Security model 文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/security-model.mdx))。对照 Cohive 的七个差异点，Multica **一个也没有完整具备**：身份追溯、拉闸、交互会话交接、归属路由四项是部分具备，接口变更广播、代码占用、复现脚本验证闭环三项完全没有。反过来，它把 Cohive 第一阶段的“看板、问题单、指派”和第三阶段的“自动执行、GitLab 合并请求与流水线联动”做得很好：自建 GitLab 集成只需要 `read_api` 令牌，按分支名或 `Closes MUL-123` 自动关联合并请求，所有关联合并请求合并后自动改状态 ([自建代码托管文档](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/vcs-integration.mdx))。

所以重叠在**平台底座**，差异在**协作语义**。差异点几乎都能用“外部服务 + Multica 命令行或接口”实现，薄层路线在技术上成立。但薄层能挂靠的接口面并不理想：有版本承诺的“公开接口 v1”只有 5 条路径，且只对插件开放 ([publicapi v1](https://github.com/multica-ai/multica/blob/main/server/pkg/publicapi/v1/README.md))；插件系统由特性开关控制，默认关闭；网页用的应用接口约有 457 处路由注册，没有版本承诺。最稳的耦合点反而是命令行。项目健康度也有隐忧：未关闭 issue 1,018 个，未合并的合并请求 846 个，9 月底以后 main 分支提交量明显下降，原因未知 ([Releases](https://github.com/multica-ai/multica/releases))。想把“对外 MCP 服务”贡献回上游，不确定性很高。况且按许可证第 2 条，贡献的代码会被出品方用于其商业云业务。

### Agent Canvas 能当执行后端，当不了协作层

OpenHands 已经转型为 Agent Canvas，自述是“面向编程智能体与自动化的自托管开发者控制中心”，可以运行 OpenHands、Claude Code、Codex、Gemini、OpenCode 和任意 ACP 智能体，自动化可以定时触发或由 Webhook 事件触发。主仓库是 **MIT**，状态为 beta ([OpenHands README](https://github.com/OpenHands/OpenHands))。它的开源版鉴权只有“每个后端一把共享接口密钥”，官方给的机器规格写的是“2 vCPU / 4 GB RAM is plenty for a single user” ([SELF_HOSTING](https://github.com/OpenHands/OpenHands/blob/main/docs/SELF_HOSTING.md))。多用户鉴权在 OpenHands Enterprise 里，后者用的是 PolyForm 试用许可，每个日历年只能免费用 30 天 ([enterprise README](https://github.com/OpenHands/enterprise))。结论很清楚：它解决的是“一个人如何在多个后端上运行和自动化智能体”，Cohive 解决的是“多个人的交互会话之间如何交接、归属、验证”，两者不是竞争关系。它与 Cohive 第一、二阶段几乎不重叠，与第三阶段高度重叠。因为 MIT 许可、提供 REST 接口、已支持 ACP 和按会话的 Docker 隔离，它适合做**可插拔执行后端**。代价是 Python 加 Node 技术栈、beta 状态，审计粒度也只到后端级别。不要依赖它的 Enterprise 版。

### 其他先行者都停在单人、单机或单个仓库

“消息加占用”这一类设计最完整的是 MCP Agent Mail。它有收件箱、线程号、带过期时间的建议性文件占用租约，提交前钩子可以拦截冲突提交，Rust 重写版已有 45 个 MCP 工具；但它是单实例、SQLite 加 git 存储，没有成员模型，共享同一个令牌的客户端彼此可读 ([mcp_agent_mail_rust](https://github.com/Dicklesworthstone/mcp_agent_mail_rust))。它的许可证是 MIT 加一段附加条款：不向 OpenAI、Anthropic 及“代表其行事的任何人”授予任何权利 ([LICENSE](https://github.com/Dicklesworthstone/mcp_agent_mail/blob/main/LICENSE))。Cohive 本身就是给 Claude Code、Codex 用的，这段条款的边界很模糊，所以只借设计，不依赖它。Concord MCP 采用 MIT 许可，只有 5 个粗粒度工具，生命周期操作带 `expected_version` 做乐观并发，消息带幂等键，还能标出“陈旧认领”；但它的范围限定在“同一份本地检出” ([concord-mcp](https://github.com/Get-Concord-AI/concord-mcp))。Claude Code 自带的跨会话消息只覆盖“你自己的会话”；跨机器必须经远程控制，并要求用 claude.ai 账号登录 ([跨会话消息文档](https://code.claude.com/docs/en/cross-session-messaging))。平台方的路线（GitHub Agent HQ、GitLab Duo、Linear、OpenAI Symphony）都是“把问题单派给云端智能体”，协作中心是托管平台上的问题单，不是开发者本机正在用的会话。Symphony 的规范更是明确把“多租户控制面”列为非目标 ([Symphony SPEC](https://github.com/openai/symphony/blob/main/SPEC.md))。“多人 + 跨机器 + Claude Code 与 Codex 混用 + 人在交互会话中交接并闭环验证”这个组合，目前是空白。

## 各层的边界：哪些整合，哪些借鉴，哪些自建

### 消息、收件箱与代码占用：借设计不借代码

这一层没有可以直接依赖的组件，但可借鉴的设计很成熟，应当原样吸收。工具设计学 Concord 的“少而粗”，plan-v0.1 第 4.1 节的 8 个工具规模合适，但每个改状态的工具都应加上 `expected_version` 和幂等键，因为智能体会重试工具调用。代码占用学 MCP Agent Mail：用建议性租约，不用硬锁，因为租约对崩溃的智能体更稳健，到期自动失效；冲突时立即报告，不阻塞等待；提交前钩子在提交时拦截冲突；占用原因填问题单号 ([mcp_agent_mail_rust](https://github.com/Dicklesworthstone/mcp_agent_mail_rust))。这些做起来成本低，可以从第二阶段提前到第一阶段末。信任模型学 Claude Code 跨会话消息：另一个会话发来的消息标注为非用户输入，不能批准权限，消息里的命令不执行；同时按发送方限流，并对相同消息去重，防止消息死循环 ([跨会话消息文档](https://code.claude.com/docs/en/cross-session-messaging))。Cohive 第 8 节第 1 条红线方向正确，应把这套信任说明直接写进 `cohive init` 生成的指令文件。还要补上 Multica 明确交给用户自己处理的那块：**智能体之间往返的轮次上限**，以及超时升级。

真正的工程难点在“最后一公里投递”：把待办可靠地送到开发者电脑和正在运行的会话里。任何队列或引擎都不负责这一段。往运行中的会话推消息，目前三条路都不稳。Claude Code 频道仍是研究预览，只接受 Anthropic 白名单插件或组织管理员允许的插件，Team 和 Enterprise 组织需要 Owner 显式开启 ([Channels 文档](https://code.claude.com/docs/en/channels))。远程控制不支持 API 密钥 ([Remote Control 文档](https://code.claude.com/docs/en/remote-control))。Codex 的 `turn/steer` 依赖实验性的应用服务器，官方声明它“不支持生产工作负载” ([Codex App Server 文档](https://learn.chatgpt.com/docs/app-server))。所以基础方案应当是“系统通知 + 钩子在下一次提交提示词时注入待办”，推进会话只作可选增强。新情况是 **Codex 现在也有 SessionStart、UserPromptSubmit 钩子**，可以通过 `additionalContext` 注入上下文，默认上限约 2,500 个令牌。但项目级钩子要用户逐一信任，信任绑定在钩子内容的哈希上，内容一改就要重新信任 ([Codex Hooks 文档](https://learn.chatgpt.com/docs/hooks))。Claude Code 的钩子支持 `http` 类型，可以把事件直接 POST 到本机小助手的端口 ([Claude Code Hooks](https://code.claude.com/docs/en/hooks))。

### 问题单：真源自建，代码平台议题只做镜像

问题单表不是“轮子”，而是 Cohive 的产品本体：路由、闭环、升级、审计都围绕它展开。自建只需要一张主表、一张只追加的状态变更事件表、一张评论与证据表，状态迁移在单个事务里校验。其他三条路都走不通。智能体专用工具里最成熟的是 Beads（Go、MIT、约 2.79 万星），它有原子认领、状态分类、`ready` 队列，还有 GitLab/GitHub 同步；但它的存储是“每个仓库一个 Dolt 库，靠 git 远端推拉”，嵌入模式只允许单写入者 ([Beads](https://github.com/gastownhall/beads))。它与跨仓库的工作空间级问题单相冲突，还会引入第二种数据库。通用项目管理系统同样不合适：Plane 社区版是 AGPL-3.0，工作项类型和自定义属性要 Pro 档才有 ([Plane 定价](https://plane.so/pricing.md))；Vikunja 也是 AGPL，而且只有通用任务模型。用 GitLab 议题作真源的障碍在授权档位：作用域标签、可配置工作项状态、自定义字段全部是 **Premium / Ultimate** 功能 ([GitLab 标签文档](https://docs.gitlab.com/user/project/labels/)、[工作项状态](https://docs.gitlab.com/user/work_items/status/))，自定义字段即使付费也只有四种类型，文本最长 1,024 字符，放不下复现脚本 ([自定义字段文档](https://docs.gitlab.com/user/work_items/custom_fields/))。

推荐的组合是：Postgres 状态机作为唯一真源，GitLab/GitHub 议题作为镜像与通知面。镜像议题上用普通标签（例如 `cohive:fixing`）展示状态，这样免费版 GitLab 也能用；Cohive 是唯一写入者，平台上有人手改标签，就当作一次“请求”，由 Cohive 校验后回写或纠正。智能体写问题单只能走 Cohive 自己的 MCP，否则就绕过了状态机。平台官方的 MCP 服务只用来让智能体读合并请求和流水线的细节。注意 GitLab 官方 MCP 服务 19.2 起才下放到免费版，19.5 才正式可用，而且**不接受传统个人令牌**，只认带 `mcp` 范围的 OAuth 令牌或细粒度令牌 ([GitLab MCP 文档](https://docs.gitlab.com/user/model_context_protocol/mcp_server/))。从 Beads 和 GitLab 借两点设计：一是状态分两层，“具体状态 + 状态类别”，收件箱、路由按类别工作，以后加状态不影响它们；二是原子认领，用 `UPDATE ... WHERE` 加版本号实现。

### 归属与契约：用业界格式，不部署门户

`cohive_who_owns` 是第一阶段路由的前提，但 plan-v0.1 没说归属表从哪来。负责人是组织层面的事实，代码图谱推导不出来。Backstage 的描述格式已经覆盖全部所需：Component 和 API 都必须填 `spec.owner`，Component 可以声明 `providesApis`、`consumesApis`、`dependsOn`，API 用 `spec.definition` 引用 OpenAPI、AsyncAPI、gRPC 定义，文档原话是“there will always be one ultimate owner” ([Backstage 描述格式](https://backstage.io/docs/features/software-catalog/descriptor-format))。Cohive 应采用 `catalog-info.yaml` 的子集作为服务登记文件，从各仓库读取后写进 Postgres；公司如果已有 Backstage，再写一个适配器导入。不要部署 Backstage 本身，它是 Node 门户，会打破“单个可执行文件”的目标。查询链路是：接口路径 → API 实体 → `spec.owner` → Cohive 成员；查不到就退回发起人确认。第二阶段的接口变更广播，在合并请求流水线里对契约文件跑 `oasdiff breaking` 或 `buf breaking --against '.git#branch=main'`（后者不依赖 Buf 的托管仓库服务），再按 `consumesApis` 反查下游并通知。两个工具都是 Go 实现、Apache-2.0 ([buf](https://github.com/bufbuild/buf)、[oasdiff](https://github.com/oasdiff/oasdiff))。调研没有找到任何项目实现过“契约变更 → 通知下游智能体”，这是 Cohive 的又一个空白机会。归属表真正的难点不是存储，而是**过期**：每条记录应带负责人和“最后确认时间”，超期未确认的在看板上标黄；首次路由失败时，反向把对应的归属条目标为可疑。

### 记忆：git 里的 Markdown 胜过任何记忆系统

2026 年的主流记忆系统都不适合当团队事实来源。mem0 开源版 v3 改为“单次抽取、只追加”，图谱记忆移到了云平台 ([mem0 迁移文档](https://github.com/mem0ai/mem0/blob/main/docs/migration/oss-v2-to-v3.mdx))。Cognee 的“用 Postgres 当图存储”在 README 里被标为演示功能，生产版要另买许可 ([Cognee README](https://github.com/topoteretes/cognee))。Graphiti 必须配图数据库。连推销时序图谱的 Zep 也承认，“Markdown memory is a legitimate pattern” ([Zep 博客](https://www.getzep.com/blog/markdown-is-not-agent-memory))，不过那篇文章本身是厂商观点。两家编程工具的官方立场一致：团队规则放进纳入版本管理的指令文件，自动记忆仅限本机，“Files are not shared across machines” ([Claude Code 记忆文档](https://code.claude.com/docs/en/memory))。Codex 文档也要求团队必须遵守的规则写进 `AGENTS.md` 或检入仓库的文档 ([Codex Memories](https://learn.chatgpt.com/docs/customization/memories?surface=app))。因此共享约定库应当是 git 里带结构化头部（`status`、`superseded_by`、`owner`）的 Markdown 决策记录：智能体只能“提议”，经合并请求由人审批后合入；Cohive 在 Postgres 里建全文索引，通过 MCP 只读查询；指令文件里只放一行索引指引。人工审批这一步还有安全价值：它封住了“一个智能体写的记忆被其他人的智能体当成指令”的通道。有一个兼容性细节：仓库里同时存在两份文件时，Claude Code 默认不读 `AGENTS.md`，所以 `cohive init` 应在 `CLAUDE.md` 里写 `@AGENTS.md` 导入，只维护一份。代码图谱方面，graphify 是 Apache-2.0、基于抽象语法树、结果确定，但跨仓库能力在付费云版 ([graphify](https://github.com/Graphify-Labs/graphify))；GitNexus 是 PolyForm Noncommercial ([LICENSE](https://github.com/abhigyanpatwari/GitNexus/blob/main/LICENSE))，与方案第 7.2 节的判断一致。

### 编排：等待是状态，不是挂起的工作流

“超时升级、等待审批”常被误认为需要工作流引擎，其实它们是**实体状态 + 定时检查**。river 的定位是“事务提交则任务一定入队，回滚则任务被移除” ([river README](https://github.com/riverqueue/river))。进入 `ROUTED` 状态时，在同一个事务里入队一个定时任务，到期检查状态是否推进，没推进就升级；请求验证时，在同一个事务里写状态并入队“重跑复现脚本”的任务，这天然等价于发件箱模式。需要注意，river 的“工作流”功能属于付费 Pro 版，每月 125 美元 ([River Pro](https://riverqueue.com/pro))，第一阶段用不到。第三阶段的自动修复链路（申请短期凭证 → 建工作树 → 跑无界面命令 → 推送 → 建合并请求 → 等流水线 → 请求验证 → 吊销凭证）才真正需要步骤级检查点。这里推荐 DBOS Go：MIT 许可，库形态，只依赖 Postgres；持久睡眠可以长达数天，`Recv` 可以带 48 小时级超时等待人工信号 ([DBOS Go](https://github.com/dbos-inc/dbos-transact-golang)、[工作流通信](https://docs.dbos.dev/golang/tutorials/workflow-communication))。它的管理面板 Conductor 自托管版是专有许可，所以执行状态要在 Cohive 自己的看板里展示 ([Conductor 自托管](https://docs.dbos.dev/production/hosting-conductor))。不选其他方案的理由各不相同：Temporal 生产自托管需要数据库加 Elasticsearch 和多个服务 ([Temporal 部署文档](https://docs.temporal.io/self-hosted-guide/deployment))；Restate 是 BSL ([LICENSE](https://github.com/restatedev/restate/blob/main/LICENSE))；Inngest 服务端是 SSPL ([LICENSE](https://github.com/inngest/inngest/blob/main/LICENSE.md))；Hatchet 是 MIT，但要多跑一个引擎服务，可以作备选 ([Hatchet 自托管](https://docs.hatchet.run/self-hosting))。LangGraph、CrewAI、Microsoft Agent Framework 这类智能体编排框架解决的是“一个程序里编排多次模型调用”，而 Cohive 第一阶段根本不调用模型，可以全部排除。A2A 已有 1.0 稳定规范和 Apache-2.0 的 Go SDK ([A2A](https://github.com/a2aproject/A2A)、[a2a-go](https://github.com/a2aproject/a2a-go))，但它假设每个智能体都是可以被网络访问的服务端，而开发者电脑在内网，所以第一阶段只借它的 Task / Message / Artifact 命名，第三阶段再考虑开一个 A2A 入口。

### 接入与隔离：驱动层交给 ACP，隔离从零依赖起步

第三阶段不要逐个适配 stream-json 和 Codex 应用服务器这两套私有协议。ACP 注册表已收录 40 多个智能体，Claude Code、Codex、Gemini CLI、OpenCode、Goose 都在其中 ([ACP 注册表](https://github.com/agentclientprotocol/registry))。协议自带 `session/request_permission`，选项有允许一次、总是允许、拒绝一次、总是拒绝，可以直接映射成看板上的“待审批”卡片 ([ACP 工具调用规范](https://agentclientprotocol.com/protocol/tool-calls))。ACP 组织下没有官方 Go SDK，可以用 Apache-2.0 的 coder/acp-go-sdk ([acp-go-sdk](https://github.com/coder/acp-go-sdk))。两个适配器都是 Apache-2.0：claude-agent-acp 基于 Claude Agent SDK 实现 ([claude-agent-acp](https://github.com/agentclientprotocol/claude-agent-acp))，codex-acp 新版建在 Codex 应用服务器之上 ([codex-acp](https://github.com/agentclientprotocol/codex-acp))。风险有两条：codex-acp 依赖的应用服务器是实验性的；ACP 的远程传输还停留在草案阶段 ([RFD 草案](https://agentclientprotocol.com/rfds/streamable-http-websocket-transport.md))。所以执行节点应在本地拉起 ACP 子进程，节点与协作服务之间仍走 Cohive 自己的 WebSocket，并保留 `codex exec --json` 作为降级路径。直接调命令行时，Claude Code 应使用 `--bare` 加 API 密钥。官方说明 `--bare` 将来会成为 `-p` 的默认值，而且不加 `--bare` 时，`-p` 会运行项目里的钩子和 MCP 配置，即使是从未信任过的文件夹 ([headless 文档](https://code.claude.com/docs/en/headless))。隔离先用“工作树 + 工具自带沙箱”：Claude Code 的沙箱基于 Apache-2.0 的 sandbox-runtime，但文件工具、MCP 服务和钩子都在沙箱外运行 ([Sandboxing 文档](https://code.claude.com/docs/en/sandboxing))。服务器端第一选择是复用公司 GitLab Runner；规模化后再接 E2B，其运行时与控制平面都是 Apache-2.0，可用 Terraform 自托管 ([E2B open source](https://e2b.dev/open-source.md))。Daytona 的开源仓库 2026 年 6 月起已停更，且为 AGPL，不要依赖 ([Daytona](https://github.com/daytonaio/daytona))。

额度问题已经有明确答案。Anthropic 规定 OAuth 订阅登录仅用于“Claude Code 及其他原生 Anthropic 应用的普通使用”；第三方开发者不得“代表其用户通过 Free、Pro、Max 方案凭据转发请求”；Pro 和 Max 的额度“假定为普通的个人使用” ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance))。消费者条款还禁止在没有 API 密钥或明确许可的情况下以脚本等自动化方式访问服务 ([Consumer Terms](https://www.anthropic.com/legal/consumer-terms))。OpenAI 也把 API 密钥作为自动化场景的推荐默认 ([Codex 鉴权文档](https://learn.chatgpt.com/docs/auth))。因此第一阶段不受影响：开发者在自己的会话里、用自己的订阅、由自己发起处理。但**第三阶段自动执行必须用团队 API 密钥**。开源发行版不得内置 claude.ai 登录流程，也不得打包 Claude Code 二进制。

### 观测与审计：Cohive 只存真相，明细交给外部

两家工具都能导出 OpenTelemetry，但形态各异。Claude Code 导出指标和事件（链路仍在测试）：`api_request` 带令牌数和费用，费用被标为估算值；可以用 `OTEL_RESOURCE_ATTRIBUTES` 给所有数据加自定义属性。在 `-p` 模式下它会继承外部传入的 `TRACEPARENT`，交互式会话则会忽略 ([监控文档](https://code.claude.com/docs/en/monitoring-usage))。Codex 能导出日志、链路和指标，但**没有费用字段** ([Codex 高级配置](https://learn.chatgpt.com/docs/config-file/config-advanced))。生成式 AI 语义约定仍是“开发中”，两家都没有采用 ([semantic-conventions-genai](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/README.md))。Langfuse 自部署需要 Postgres、ClickHouse、Redis 和对象存储 ([Langfuse 自部署](https://langfuse.com/self-hosting))；Phoenix 是 Elastic License 2.0，禁止作为托管服务对外提供 ([Phoenix LICENSE](https://github.com/Arize-ai/phoenix/blob/main/LICENSE))。它们都不应进入 Cohive 的部署包。第一阶段只用 Postgres：问题单事件表兼作审计主体；审计事件表做追加写和哈希链，并发追加用 advisory lock 串行化，定期把链头锚定到库外；“会话 ↔ 问题单”按时间区间关联，由钩子和认领、更新工具上报。交互式会话一个会话可能先后处理多张单，用量只能按会话编号加时间窗口近似归属，展示时必须标注“估算”。第三阶段自动执行时注入 `TRACEPARENT` 和问题单编号，就能精确归属。防泄露分三层：源头不开工具参数导出；经 Collector 时用脱敏处理器 ([redaction processor](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/redactionprocessor/README.md))；写入前用 gitleaks 规则扫描 ([gitleaks](https://github.com/gitleaks/gitleaks))。trufflehog 是 AGPL，不适合嵌入。

## 许可证把候选项切成两张清单

同一个组件，组织内部自用和被开源的 Cohive 依赖，结论可能完全相反。下表把差异摊开，“需法务确认”一列标出不能只靠工程判断的条目。

| 组件 | 许可证 | 组织内部自用 | 开源 Cohive 依赖或随附 | 需法务确认 |
|---|---|---|---|---|
| Multica | Multica License（Apache-2.0 加附加条件） | 单一组织内部免费，包括多个工作空间 ([LICENSE](https://github.com/multica-ai/multica/blob/main/LICENSE)) | 不可派生；只能作为可分离的可选集成，并在文档中注明“基于 Multica” | **是**：“单一组织”是否包含关联公司、子公司；外包或合作方登录、经接口驱动实例是否算“托管服务”（官方常见问题称算，[源文案](https://github.com/multica-ai/multica/blob/main/apps/web/features/landing/i18n/en.ts)）；出品方可单方修改许可证，历史上已改 6 次 ([提交历史](https://github.com/multica-ai/multica/commits/main/LICENSE)) |
| MCP Agent Mail | MIT 加针对 OpenAI/Anthropic 的附加条款 | 大概率允许 | 不依赖，只借设计 | **是**：附加条款对“使用 Claude Code、Codex 的普通公司”是否适用 |
| OpenHands 主仓、SDK、自动化服务 | MIT | 可以 | 可以 | 否；Enterprise 版是 PolyForm 试用许可，不要用 |
| ACP 规范、acp-go-sdk、claude-agent-acp、codex-acp | Apache-2.0 | 可以 | 可以 | 否 |
| Claude Agent SDK（TypeScript 版）、Claude Code 本体 | 受 Anthropic 商业条款约束 ([Agent SDK 概览](https://code.claude.com/docs/en/agent-sdk/overview)) | 可以作为外部工具使用 | 只能由用户自行安装，**不可打包分发** | **是**：用开发者个人订阅自动处理同事派来的任务，官方没有逐字表述，结论是推断 |
| river | MPL-2.0 | 可以 | 可以，只要不修改 river 自身的文件 | 否；river Pro 的条款未核实，不要依赖 |
| DBOS Go | MIT | 可以 | 可以 | 否；Conductor 面板为专有许可 |
| Restate／Inngest | BSL 1.1／SSPL | 可以 | 不适合作必需依赖 | 是（如果坚持要用） |
| Plane、Vikunja、Grafana、Daytona、coder | AGPL-3.0 | 作为独立服务通过网络调用没有问题 | 不嵌入、不修改、不随附 | 是（如果要打进同一镜像） |
| Phoenix | Elastic License 2.0 | 内部可以 | 不可作为依赖 | 否 |
| GitNexus | PolyForm Noncommercial | **商用不允许** | 不可 | 否，结论明确 |
| Serena | GPL-3.0（应用部分）([LICENSE](https://github.com/oraios/serena/blob/main/LICENSE)) | 本机独立进程可以 | 不可链接或打包 | 否 |
| Task Master | MIT 加 Commons Clause ([LICENSE](https://github.com/eyaltoledano/claude-task-master/blob/main/LICENSE)) | 可以，但项目已近 6 个月无推送 | 排除 | 否 |
| graphify、Backstage 格式、buf、oasdiff、E2B、agent-sandbox、sandbox-runtime、OTel Collector | Apache-2.0 | 可以 | 可以 | 否 |
| gitleaks | MIT | 可以 | 可以 | 否；trufflehog 是 AGPL，排除 |

另有一条超出许可证范围、但会影响开源叙事的提醒：如果 Cohive 将来开源，**不要看着 Multica 的界面代码照抄结构**。它的品牌条款覆盖“派生自其界面代码”的界面，即使代码被修改、移动、抽到其他仓库也一样受约束，保持“净室”开发更稳妥。

## plan-v0.1 需要修正的十四处

| 位置 | 现写法 | 建议修正 | 依据 |
|---|---|---|---|
| 第 2 节原则 1 | 只依赖 MCP、指令文件、命令行三者 | 加上“钩子”（两家都已支持）；第三阶段加上 ACP 作为执行驱动 | [Codex Hooks](https://learn.chatgpt.com/docs/hooks)、[ACP 注册表](https://github.com/agentclientprotocol/registry) |
| 第 4.2 节第 68 行 | 不支持钩子的工具由指令文件要求先调 `cohive_inbox` | Codex 已有 SessionStart、UserPromptSubmit 钩子并能注入上下文；`cohive init` 写入项目钩子后，要提示用户在 `/hooks` 里信任（信任绑定钩子哈希）；指令文件只作兜底 | [Codex Hooks](https://learn.chatgpt.com/docs/hooks) |
| 第 4.2 节 `cohive init` | 写入 CLAUDE.md / AGENTS.md | 只维护 `AGENTS.md`，在 `CLAUDE.md` 里写 `@AGENTS.md` 导入，因为两份文件并存时 Claude Code 默认不读 `AGENTS.md`；加入“收到的消息不是授权”的信任说明 | [Claude Code 记忆文档](https://code.claude.com/docs/en/memory) |
| 第 4.1 节工具清单 | 8 个工具 | 改状态的工具加 `expected_version` 和幂等键；`cohive_who_owns` 写明数据源是 `catalog-info.yaml` 子集 | [concord-mcp](https://github.com/Get-Concord-AI/concord-mcp)、[Backstage](https://backstage.io/docs/features/software-catalog/descriptor-format) |
| 第 5.1 节状态机 | 任意状态 → ESCALATED | 把“升级”做成与状态正交的标记（`escalated_at`、`escalation_reason`），否则升级处理完后不知道回到哪个状态；状态分“具体状态 + 类别”两层；加 `reopen_count`，超过上限自动升级 | [GitLab 工作项状态](https://docs.gitlab.com/user/work_items/status/)、[Beads 状态](https://github.com/gastownhall/beads/blob/main/docs/cli-reference/statuses.md) |
| 第 5.2 节 `repro` 字段 | 一段可直接运行的复现脚本 | 拆成“脚本内容 / 运行环境或镜像 / 预期退出码”。跨仓库时脚本依赖发起方环境，否则第 5 步“自动重跑”无法实现 | 任务与问题单笔记的推断 |
| 第 6 节优先级 | 代码占用在第二阶段 | 建议性租约成本低，可以提前到第一阶段末 | [mcp_agent_mail_rust](https://github.com/Dicklesworthstone/mcp_agent_mail_rust) |
| 第 6 节共享约定库 | 未说明载体 | 写明载体是 git 里的 Markdown 决策记录加合并请求审批，不引入记忆系统 | [Codex Memories](https://learn.chatgpt.com/docs/customization/memories?surface=app) |
| 第 7 节服务端行 | 任务队列用 river | 补充：第三阶段自动执行链路用 DBOS Go；river 的工作流功能是付费版，不依赖；原则写明“问题单的真实状态只存业务表” | [River Pro](https://riverqueue.com/pro)、[DBOS Go](https://github.com/dbos-inc/dbos-transact-golang) |
| 第 7 节自动执行行 | `claude -p --output-format stream-json`、`codex exec` | 首选 ACP（coder/acp-go-sdk），保留上面两者作降级；Claude 必须加 `--bare` 和 API 密钥；codex-acp 依赖实验性的应用服务器 | [headless 文档](https://code.claude.com/docs/en/headless)、[Codex App Server](https://learn.chatgpt.com/docs/app-server) |
| 第 7 节代码平台行 | 先接 GitLab | 补充前置检查：版本是否 ≥ 19.5（官方 MCP 服务正式可用）、授权档位（免费版没有作用域标签、状态、自定义字段）；议题只做镜像 | [GitLab MCP 文档](https://docs.gitlab.com/user/model_context_protocol/mcp_server/)、[GitLab 标签文档](https://docs.gitlab.com/user/project/labels/) |
| 第 7.2 节频道 | 研究预览、要求 claude.ai 登录 | 补充两条限制：只接受白名单插件或组织允许的插件；Team / Enterprise 组织需要 Owner 开启 | [Channels 文档](https://code.claude.com/docs/en/channels) |
| 第 7.2 节不纳入清单 | 4 项 | 增加：派生 Multica、依赖 MCP Agent Mail、Temporal / Restate / Inngest、智能体编排框架、通用项目管理系统、记忆系统、OpenHands Enterprise、Daytona | 见上文各节 |
| 第 8、10、11 节 | 未涉及审计与观测；第 11 节第 4、5 项待定 | 第 8 节补审计表（追加写加哈希链）和密钥扫描，补“若用 Multica 守护进程执行，必须放进专用用户或容器”；第 10 节的指标依赖“会话 ↔ 问题单”关联表，要在第一阶段建好；第 11 节第 4 项建议定为团队 API 密钥；第 5 项命名只用 Cohive，Cohive 第三阶段恰恰要用智能体客户端协议（ACP），避免混淆 | [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)、[Multica Security model](https://github.com/multica-ai/multica/blob/main/apps/docs/content/docs/security-model.mdx) |

## 需要作者拍板的八件事

| 决策点 | 选项 | 建议 |
|---|---|---|
| 1. 要对接的 GitLab 的版本与授权档位 | 免费版 / Premium / Ultimate；是否 ≥ 19.5；管理员是否愿意开放 MCP 访问和细粒度令牌 | 先实查。它决定镜像议题能否用原生状态、智能体能否直接读合并请求；无论结果如何，问题单真源都留在 Cohive |
| 2. 第一阶段开工前是否先花半天试用 Multica | 试 / 不试 | **试**。按官方 compose 自托管，设 `DO_NOT_TRACK=1`，守护进程放进专用用户或容器，在两个交互会话里装技能包，手工走一遍“发现 → 交接 → 修复 → 验证”。重点看三件事：命令行读写顺不顺手、指派后对方能否及时知道、自定义属性能否装下复现脚本和提交号 |
| 3. 组织内部自用时是否叠加 Multica | 用它的看板和后台执行 / 完全自建 | 取决于第 2 项的结果。若叠加，要接受三点：它的 Next.js 界面与 7.1 节的决定相反、界面品牌不可去除、许可证可能被单方收紧 |
| 4. 自动执行用什么额度 | 团队 API 密钥 / 开发者个人订阅 | **团队 API 密钥**，每个执行节点或工作空间一把，可随时吊销；个人订阅用于无人值守执行，合规风险很高 |
| 5. Cohive 是否开源、何时开源、用什么许可证 | MIT / Apache-2.0；先内部后开源 / 一开始就开源 | 建议从第一天就按开源标准选依赖（本报告的绿色清单），许可证倾向 Apache-2.0（带专利授权）。具体选哪个需要作者定 |
| 6. 代码占用是否提前到第一阶段末 | 是 / 否 | 是，成本低、差异化强 |
| 7. 第三阶段执行后端的优先顺序 | 本机执行器 / GitLab Runner / Agent Canvas / Multica 守护进程 | 本机执行器（ACP）加 GitLab Runner 优先；Agent Canvas 作为开源用户的可选后端，等它脱离 beta 后再评估 |
| 8. 是否收集开发者的遥测 | 默认开 / 默认关 | `cohive init` 默认关，事先说明收集范围（事件里默认带 `user.email`）；第一阶段只靠钩子和工具调用做会话关联 |

## 仍未核实、需要实测的事项

以下内容笔记里明确标为缺口，本报告的相关结论都以它们为前提，属于待验证假设。Multica 方面：没有实际部署，“指派给成员后对方在命令行或会话里的完整体验”“是否存在工作空间一键停止派发”“插件系统能否在自托管实例上开启”“应用接口是否都接受个人令牌”都未验证；商业许可与 Cloud 价格未公开；9 月底后提交量下降的原因不明。Agent Canvas 方面：Agent Server 的 REST 接口有没有版本承诺、能否对单个会话下发“工作目录 + 智能体 + 提示词”并流式取回事件，都未逐条核实。接入层：coder/acp-go-sdk 加两个适配器的端到端链路没有跑通过；`codex mcp-server` 子命令在本机 0.154.0 上不见了，是移除还是改名未查到说明；Codex SDK 的语言支持未核实（文档页 404）。编排层：DBOS Go 在 Cohive 负载下的实测数据缺失；代码升级后正在运行的工作流如何恢复，未核实；river 能否按工作空间暂停队列，未核实。观测层：Codex 是否认 `OTEL_RESOURCE_ATTRIBUTES` 是读源码得出的推断；两家钩子能否稳定拿到会话编号，需要实测。平台层：GitLab 外部智能体在自管实例上的可用性，各版本文档说法冲突；Plane 社区版是否含 Webhook，资料相互矛盾；A2A 移交 Agentic AI Foundation 的确切日期，各来源不一致。工作量方面：“独立自建比叠加 Multica 多出数周”是粗估，没有详细拆分。

## 结论

调研改变的不是方向，而是重心。原方案把看板、服务端、本机小助手、自动执行看作同等分量的组成部分。实际上，前三者的大部分能力 Multica 已经做到了 5 万星的规模，自动执行则有 ACP、Agent Canvas、GitLab Runner 可以借用。Cohive 唯一没有替代品的，是“开发者手里正在用的交互会话作为一等参与者”，以及围绕它的归属、占用、验证、升级语义。这意味着第一阶段的成败取决于远程 MCP 和投递可靠性，而不是看板做得多完整。Multica 社区要求对外 MCP 服务已近半年，Cohive 的差异化窗口可能只有几个月，应当先做出能跨人、跨工具走通一次闭环的 MCP 原型，再补界面。

另一个容易被低估的判断是：多数“需要引擎、需要平台、需要记忆系统”的直觉，在 Cohive 的规模下都能还原成 Postgres 里的几张表加定时任务。真正的难点在系统边界上：待办能否送达离线的开发者，归属表会不会悄悄过期，一个智能体写下的文字会不会变成另一个智能体的指令。把这三件事做扎实，比接入任何一个热门组件都更能决定 Cohive 能否被团队真正用起来。
