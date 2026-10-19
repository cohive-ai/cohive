# 飞书（含 Lark）、Slack、Discord 接入调研

日期：2026-10-19 · 性质：调研，只读官方文档和依赖包源码，未注册账号、未建应用、未调用任何需要凭据的接口 · 前置阅读：[钉钉、飞书接入调研](2026-10-17-dingtalk-feishu.md)、[钉钉部署指南](../guides/dingtalk.md)

标注约定：“**原文**”表示已在官方文档或依赖包源码里找到原话；“**推断**”表示根据原文推出、没有逐字依据；“**未核实**”表示官方没写，或者只能实测才知道。本次**全部未实测**，需要实测的地方都写了“需测试应用”。

资料来源：飞书 `open.feishu.cn`、Lark `open.larksuite.com` 与 `larksuite.com`、Slack `docs.slack.dev` 与 `slack.com`、Discord `docs.discord.com`（原 `discord.com/developers/docs` 已整体跳转到这里）。依赖包信息来自 GitHub 与 Go 模块代理，查询日期 2026-10-19。

---

## 一页结论

**三个平台都能做，而且都能做到“服务端只需出站”**：飞书和 Lark 用长连接，Slack 用 Socket 模式，Discord 用网关长连接。审批按钮的点击都能从这条出站连接收到，点击者的平台用户编号由平台填写。现有的通道接口（`Channel` 加 `Hub.Click`）和“私聊发码连接账号、群里发码绑群”这套流程，三个平台都能直接套用，不需要再改表结构。

| 平台 | 值不值得做 | 主要理由 | 主要代价或风险 | 工作量（含用例和部署指南） |
|---|---|---|---|---|
| **Slack** | **值得，建议第一个做** | 开源项目面向海外团队的头号通讯工具；应用清单可以一键建好应用，部署步骤是四个平台里最少的；接口公开稳定，免费、没有月度额度 | 国内要代理（不归 Cohive 管）；免费版每个工作区最多装 10 个应用；**2025-10 新条款：Cohive 将来如果出付费版或托管版，Slack 接入要走应用市场、改成公网回调**（见 D5） | 2.5–3.5 人日 |
| **飞书 / Lark** | **值得，第二个做**；如果近期有具体中文团队要用，提到第一 | 一套代码覆盖国内（飞书）和东南亚（Lark）；卡片由代码直接生成，不用像钉钉那样手工建模板；能主动私聊 | 每个团队要自建应用、管理员审核发版（管理员可设免审）；飞书免费版的“限时 100 万次/月”额度可能本月底结束；Lark 免费版限 20 人；长连接是私有协议，只能用官方依赖包 | 3–4 人日（含 Lark 0.5 人日） |
| **Discord** | **按需做，暂不排期** | 一个机器人可以同时服务任意多个服务器，形态最像 Telegram；免费、无额度；Go 依赖包成熟 | 定位是社区和游戏，公司团队很少用，每个成员都要有 Discord 账号；国内要代理；主动私聊受用户隐私设置限制，官方不鼓励 | 3–4 人日 |

**建议顺序**：先做一个 1 人日的**共用前置**（把钉钉通道里的群命令、私聊发码、`我是谁` 抽成与平台无关的收件处理，见 5.1），然后 Slack → 飞书 / Lark → Discord（按需）。理由：

1. 现有两个通道（Telegram、钉钉）都偏中文用户；仓库是公开的英文开源项目，Slack 是海外小团队的默认选项，补上它对推广最有用。
2. Slack 用应用清单建应用，部署指南可以压到“粘贴一段配置 → 安装 → 复制两个令牌”，比钉钉的十几步少一半以上，对“自托管、每个团队自己建应用”这个前提最友好。
3. 飞书 / Lark 的技术难度不高，但飞书的接口额度政策正在变（见 2.7），先观察一个月再做，可以少走弯路。如果在此之前出现了要用飞书的真实团队，就把它提到第一。
4. Discord 没有已知的需求方，技术上随时能接，等有团队提出再做。

**和旧结论相比有变化的地方**（详见第 4 节）：飞书“每次改动都要管理员审核”不绝对，管理员可以开免审；飞书免费版的 100 万次限时额度按版本对比页写法到 2026 年 10 月底截止；Lark 的卡片回调里**没有** `union_id`，旧方案“重建应用后用 union_id 自动对回”在 Lark 上用不了；Lark 支持长连接、飞书与 Lark 账号不互通，现在都有官方原文；飞书能主动私聊（旧调研没写）。

---

## 1. 对照总表

Telegram、钉钉两列是现状，作对照；三个新平台的结论都**未实测**。

### 1.1 机器人形态与接入门槛

| 项 | Telegram（现状） | 钉钉（现状） | 飞书 / Lark | Slack | Discord |
|---|---|---|---|---|---|
| 团队要建什么 | 找 BotFather 建一个机器人，拿令牌 | 企业内部应用，加机器人能力，再在卡片平台建两个卡片模板 | 企业自建应用，加机器人能力 | 在 `api.slack.com/apps` 用应用清单建一个只装在本工作区的应用 | 在开发者门户建应用并开启机器人，用邀请链接加进自己的服务器 |
| 是否要审核或上架 | 不要 | 自己发布，不需要平台审核 | 每次发版要**企业管理员**审核；管理员可以给应用开免审（**原文**）。不需要平台审核 | 不上架、不需要审核（**原文**）。付费工作区的管理员可能开了“装应用需审批”（**推断**） | 进入 100 个以下服务器不需要验证（**原文**） |
| 免费额度与收费 | 免费 | 标准版免费；接口 1 万次/月，Webhook 与 Stream 合计 3000 次/月 | 飞书免费版 100 人、单群 500 人；Lark Starter 免费版 20 人、单群 20 人；接口额度见 1.6 | 免费版每个工作区最多 10 个应用，只能看最近 90 天消息（**原文**）；接口不收费 | 免费，无额度 |
| 最小权限 | 无需配置 | 机器人发消息、互动卡片实例写入 | `im:message:send_as_bot`（发消息、改消息）、`im:message.group_at_msg:readonly`（收群里 @ 机器人的消息）、`im:message.p2p_msg:readonly`（收单聊消息）；可选 `im:chat:readonly`（读群名）。权限标识以后台实际显示为准（**未实测**） | 机器人令牌：`chat:write`、`app_mentions:read`、`im:history`、`im:write`；可选 `commands`（斜杠命令）、`channels:read`（读频道名）、`users:read`（读昵称）。应用级令牌：`connections:write`（**原文**） | 邀请链接 `scope=bot`（已含斜杠命令）；权限位：查看频道、发送消息、嵌入链接、读消息历史。网关意图：服务器消息、私聊消息，**不需要任何特权意图**（**原文**） |
| 一个应用服务的范围 | 任意多个群 | 一个钉钉组织（机器人只能进本组织内部群） | 一个飞书企业（自建应用不能跨企业，**原文**） | 一个 Slack 工作区（跨工作区要公开分发加授权安装流程，会触发条款问题） | 任意多个服务器（100 个以内免验证） |
| 外部成员 | 无限制 | 只能进内部群 | 外部群要开“对外共享”，且企业须已认证 | 跨组织共享频道里的外部用户能点按钮，点击者带的是**对方**工作区编号（**原文**） | 无此概念，进了服务器就是成员 |

依据：[飞书应用类型](https://open.feishu.cn/document/home/app-types-introduction/overview)、[飞书发布与审核](https://open.feishu.cn/document/home/intro-to-custom-app-review)、[Lark 定价](https://www.larksuite.com/en_us/plans)、[飞书版本对比](https://www.feishu.cn/service)、[Slack 应用清单](https://docs.slack.dev/app-manifests/configuring-apps-with-app-manifests)、[Slack 清单字段](https://docs.slack.dev/reference/app-manifest)、[Slack 免费版限制](https://slack.com/help/articles/115002422943)、[Slack 跨组织频道](https://docs.slack.dev/apis/slack-connect/)、[Discord 授权与邀请](https://docs.discord.com/developers/topics/oauth2)、[Discord 权限](https://docs.discord.com/developers/topics/permissions)、[Discord 网关与意图](https://docs.discord.com/developers/events/gateway)

### 1.2 收消息与回调：能否不开公网入站

| 项 | Telegram | 钉钉 | 飞书 / Lark | Slack | Discord |
|---|---|---|---|---|---|
| 出站方式 | 长轮询 | Stream 长连接（自己实现） | 长连接：私有二进制分帧（protobuf），“无需公网 IP 或域名”（**原文**）；Lark 同样支持（**原文**，用 `WithDomain` 切域名） | Socket 模式：先用应用级令牌调 `apps.connections.open` 拿 WebSocket 地址，每条推送回传 `envelope_id` 确认（**原文**） | 网关长连接：握手、心跳、序列号、断线恢复；交互**默认就走网关**，只要不填“交互回调地址”（**原文**） |
| 同一条连接收什么 | 消息、按钮 | 机器人消息、卡片回调 | 消息事件、进群事件、卡片回调（`card.action.trigger`） | 事件、斜杠命令、按钮点击（**原文**） | 消息、斜杠命令、按钮点击 |
| 回应点击的方式 | 出站 HTTP | 在连接上回包 | 在连接上回包（3 秒内可直接返回新卡片） | 在连接上回确认；改消息另走出站 HTTP | **必须走出站 HTTP 回应**（**原文**），仍然只需出站 |
| 前提与限制 | 一个令牌只能一个长轮询 | 仅企业内部应用；每应用 50 条连接 | 仅企业自建应用；每应用 50 条连接，多条时随机投给一条（**原文**） | 每应用最多 10 条连接；连接每隔几小时会被刷新，要处理重连；官方“生产环境推荐 HTTP”，但 Socket 模式“完全支持”并点名适用于“防火墙后、不能暴露 HTTP 端点”的场景；**上应用市场必须用 HTTP**（**原文**） | 建连认证每 24 小时 1000 次；连接每 60 秒最多发 120 个事件（**原文**） |
| 是否要验签 | 不要 | 不要 | 长连接不要：“无需额外解密、验签逻辑”（**原文**）。HTTP 模式才要 Encrypt Key 签名 | Socket 模式不要（连接预先鉴权，**原文**）。HTTP 模式才要 v0 签名 | 网关不要。HTTP 模式才要 Ed25519 验签 |
| 多台 Cohive 共用一个应用 | 不行 | 不行（点击随机投递） | 不行（同上） | 不行（同上，**推断**） | 不行（交互只投给一条连接，**推断**） |

依据：[飞书长连接接收回调](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/callback-subscription/configure-callback-request-address)、[Lark 长连接](https://open.larksuite.com/document/ukTMukTMukTM/uYDNxYjL2QTM24iN0EjN/event-subscription-configure-/use-websocket)、[Slack Socket 模式](https://docs.slack.dev/apis/events-api/using-socket-mode)、[Slack HTTP 与 Socket 模式对比](https://docs.slack.dev/apis/events-api/comparing-http-socket-mode)、[Discord 交互概述](https://docs.discord.com/developers/interactions/overview)、[Discord 接收与回应交互](https://docs.discord.com/developers/interactions/receiving-and-responding)

**结论**：三个平台都不需要公网入站，内网部署和钉钉一样省事。

### 1.3 群通知：发消息、@ 人、消息格式、事后修改

| 项 | Telegram | 钉钉 | 飞书 / Lark | Slack | Discord |
|---|---|---|---|---|---|
| 发群消息 | `sendMessage` | 互动卡片 `createAndDeliver` | `POST /open-apis/im/v1/messages`，可带 `uuid` 一小时内去重 | `chat.postMessage`；机器人要先被 `/invite` 进频道（**原文**） | `POST /channels/{id}/messages` |
| 按编号 @ | 现状按用户名（旧调研建议改 `tg://user?id=`） | 卡片的 `atUserIds` | 卡片里 `<at id=open_id></at>` | `<@U012AB3CD>`；只有频道成员会收到提醒（**原文**） | `<@用户编号>`，并用 `allowed_mentions.users` 精确指定提醒谁（**原文**） |
| 消息格式 | HTML | 卡片模板变量 + Markdown | 卡片 JSON 2.0，**代码直接生成，不要模板** | 消息块（Block Kit），另给一段纯文本作通知摘要（**原文**） | 正文 Markdown（2000 字）+ 按钮行；新版组件消息不能带正文，且标志不可撤销（**原文**） |
| 事后修改 | `editMessageText` | 按卡片编号更新 | 卡片 `PATCH`：只限 14 天内的消息、单条 5 次/秒；文本消息 `PUT`：最多改 20 次，时间窗口由企业管理员设定（**原文**） | `chat.update`：只能改自己发的；可能遇到 `edit_window_closed`，对机器人是否生效**未核实** | `PATCH`：未写时间上限；对一小时前的旧消息编辑次数有上限（错误码 30046，**原文**） |
| 防注入要点 | 现有 HTML 转义 | 现有全角替换 | 卡片 Markdown 转义（**未实测**） | 只转义 `&`、`<`、`>` 三个字符，否则用户输入可能拼出 `<!channel>` 提醒全员（**原文**） | 转义 `*_~|>` 和反引号；`allowed_mentions` 只列要 @ 的人，杜绝 `@everyone` 注入 |

依据：[飞书发送消息](https://open.feishu.cn/document/server-docs/im-v1/message/create)、[飞书编辑消息](https://open.feishu.cn/document/server-docs/im-v1/message/update)、[飞书更新卡片](https://open.feishu.cn/document/server-docs/im-v1/message-card/patch)、[Slack chat.postMessage](https://docs.slack.dev/reference/methods/chat.postMessage)、[Slack 文本格式](https://docs.slack.dev/messaging/formatting-message-text)、[Slack chat.update](https://docs.slack.dev/reference/methods/chat.update)、[Discord 消息](https://docs.discord.com/developers/resources/message)、[Discord 组件](https://docs.discord.com/developers/components/reference)、[Discord 状态码](https://docs.discord.com/developers/topics/opcodes-and-status-codes)

**就地更新状态（以后可能做）**：三个平台都能改原消息。最紧的是飞书卡片 14 天上限，问题单通知如果要“随状态就地更新”，飞书上超过 14 天的旧卡片只能改发新消息。

### 1.4 审批按钮

| 项 | Telegram | 钉钉 | 飞书 / Lark | Slack | Discord |
|---|---|---|---|---|---|
| 点击事件 | `callback_query` | 卡片回传请求 | `card.action.trigger`（旧版“消息卡片回传交互”不支持长连接） | `block_actions` | `INTERACTION_CREATE`（组件类型） |
| 按钮数据上限 | 64 字节 | 模板里写死 | 按钮 `value` 是 JSON 对象 | `value` 2000 字、`action_id` 255 字（**原文**） | `custom_id` 100 字（**原文**） |
| 点击者身份 | `from.id` | `userId` + `corpId` | `operator.open_id` + `tenant_key`；飞书另有 `union_id`，**Lark 没有**（**原文**） | `user.id` + `user.team_id`（**原文**） | `member.user.id`（服务器里）或 `user.id`（私聊里）（**原文**） |
| 回应时限 | 宽松 | 3 秒，回调里改卡 2 秒 | 3 秒 | 3 秒确认（**原文**，Socket 模式文档没单写，按 3 秒设计） | 3 秒内首次回应，交互令牌 15 分钟有效（**原文**） |
| 点后改成结果 | `editMessageText` | 回调响应里返回卡片变量 | 回调响应里直接返回新卡片；或延时更新令牌（30 分钟内，按 2 次设计） | 先确认，再用 `response_url`（30 分钟内最多 5 次）或 `chat.update` 改原消息（**原文**） | 回应类型 7 直接改原消息；或类型 6 先确认、15 分钟内再改（**原文**） |
| 给点击者的私下提示（“你没有权限”） | `answerCallbackQuery` 弹提示 | 卡片私有变量 | 回调响应里的提示（toast） | 没有弹提示，用“仅自己可见”的临时消息（`response_url` 发 `ephemeral`） | 没有弹提示，用“仅自己可见”的回应（标志 64） |
| 别处处理后回写 | 按消息编号改 | 按卡片编号改 | `PATCH`（14 天内） | `chat.update`（频道编号 + 消息时间戳） | `PATCH`（频道编号 + 消息编号） |

依据：[飞书卡片回传交互](https://open.feishu.cn/document/feishu-cards/card-callback-communication)、[Slack 处理用户交互](https://docs.slack.dev/interactivity/handling-user-interaction)、[Slack block_actions 负载](https://docs.slack.dev/reference/interaction-payloads/block_actions-payload)、[Slack 按钮元素](https://docs.slack.dev/reference/block-kit/block-elements/button-element)、[Discord 接收与回应交互](https://docs.discord.com/developers/interactions/receiving-and-responding)

**结论**：三家都能拿到可信的点击者编号，和 `Hub.Click` 的“身份只取平台填写字段、审批对象按消息记录反查”完全对得上。`ClickResult.Toast` 在 Slack 和 Discord 上没有弹提示，要改发“仅自己可见”的消息，这在各自通道里处理即可，不影响接口。

### 1.5 群绑定与账号连接

| 项 | Telegram | 钉钉 | 飞书 / Lark | Slack | Discord |
|---|---|---|---|---|---|
| 群里发绑定码 | `/bind` | @机器人 `绑定群 码` | @机器人 发码：消息事件带 `chat_id` | @机器人 发码（`app_mention` 事件带 `channel`）；或斜杠命令 `/cohive bind 码`，负载带 `channel_id`、`team_id`（**原文**） | @机器人 发码（被 @ 的消息不开特权意图也能读正文，**原文**）；或斜杠命令，回复可以“仅自己可见” |
| 带参数的机器人深链（Telegram `?start=`） | 有 | 没有 | 没有（旧结论不变） | **没有**：`slack://app` 只接受 team、id、tab 三个参数（**原文**） | 没有（**推断**） |
| 私聊发连接码 | — | 有（现行做法） | 单聊消息事件带发送者 `open_id` | 应用主页打开“消息”标签，`message.im` 事件带 `user`（**原文**） | 私聊消息不开特权意图也能读正文（**原文**）；或全局斜杠命令（按服务器注册的命令不能在私聊里用，**原文**） |
| 网页授权登录（可选增强） | — | 有 | 有 | “用 Slack 登录”（OpenID Connect），令牌里直接带用户编号和工作区编号（**原文**）；回跳地址是否必须 https **未核实** | `identify` 授权，`GET /oauth2/@me` 拿用户编号（**原文**）；回跳地址能否是内网 **未核实** |
| 用户编号稳定性 | 全局 | 组织内不变 | `open_id` 按应用；重建应用后全变 | 工作区内稳定；企业版里可能是 `U` 或 `W` 开头，不要假设前缀（**原文**） | 全 Discord 唯一（**原文**） |
| 建议的 `tenant` 取值 | 空 | `corpId` | `tenant_key` | 工作区编号 `team_id` | 空（用户编号全局唯一） |

依据：[飞书接收消息](https://open.feishu.cn/document/server-docs/im-v1/message/events/receive)、[Slack app_mention](https://docs.slack.dev/reference/events/app_mention)、[Slack 斜杠命令](https://docs.slack.dev/interactivity/implementing-slash-commands)、[Slack message.im](https://docs.slack.dev/reference/events/message.im)、[Slack 深链](https://docs.slack.dev/interactivity/deep-linking)、[用 Slack 登录](https://docs.slack.dev/authentication/sign-in-with-slack)、[Slack 企业版](https://docs.slack.dev/enterprise)、[Discord 斜杠命令](https://docs.discord.com/developers/interactions/application-commands)

**结论**：三家都没有带参数深链，**钉钉那套“私聊发码 + 网页确认”和“群里发码绑群”原样适用**，`SubmitLinkCode`、`CompleteChatBind` 本来就带 `provider` 参数，不用改。安全规则也照搬：连接码只在私聊里认，发到群里立即作废。

**Slack 和 Discord 多一种更顺的做法**：斜杠命令的回复可以“仅自己可见”，命令本身也不会出现在频道里，所以群绑定可以改成 `/cohive bind 码`，不会把码暴露给群里其他人。

### 1.6 主动私聊、限流与额度

| 项 | Telegram | 钉钉 | 飞书 / Lark | Slack | Discord |
|---|---|---|---|---|---|
| 主动私聊（新登录通知等） | 只能私聊启动过机器人的人（已连接的成员都满足） | 能（批量单聊接口，现未使用） | **能**：`receive_id_type=open_id`，用户不必先和机器人说话；前提是用户在应用可用范围内，用户屏蔽机器人时报 230053（**原文**） | **能**：`chat.postMessage` 直接填用户编号，消息进应用主页的消息标签；或先 `conversations.open`（需 `im:write`）（**原文**） | **有条件**：先建私聊频道再发；用户关了“允许服务器成员私信”时报 50007；官方原话私聊“通常应由用户操作发起”，开得太多会被限流（**原文**） |
| 发送频率 | 群内约 20 条/分钟 | 每接口 20 次/秒 | 应用 50 次/秒；同一群所有机器人合计 5 次/秒 | `chat.postMessage` 每频道约 1 条/秒；其他接口按 1–4 档，每分钟 1 到 100 次以上（**原文**） | 全局 50 次/秒，按路由分桶；交互回应不计入；10 分钟内 1 万次无效请求会临时封地址（**原文**） |
| 月度额度 | 无 | 接口 1 万次/月；Webhook 与 Stream 合计 3000 次/月 | 飞书：所有自建应用合计，原 1 万次/月，“限时 100 万次/月”（见下）；Lark Starter：1 万次/月；付费版不限。超额报 99991403，失败的调用不计（**原文**） | 无（**推断**） | 无（**推断**） |

依据：[飞书发送消息](https://open.feishu.cn/document/server-docs/im-v1/message/create)、[飞书调用量上限说明](https://open.feishu.cn/document/uAjLw4CM/ugTN1YjL4UTN24CO1UjN/platform-updates-/custom-app-api-call-limit)、[Slack 限流](https://docs.slack.dev/apis/web-api/rate-limits)、[Slack 2025-05 限流变化](https://docs.slack.dev/changelog/2025/05/29/rate-limit-changes-for-non-marketplace-apps)、[Discord 用户与私聊](https://docs.discord.com/developers/resources/user)、[Discord 限流](https://docs.discord.com/developers/topics/rate-limits)

- **飞书额度**：调整说明页仍写“2026 年 6 月份限时 100 万次”，版本对比页写“100 万次/月，26 年 10 月限时”。按后者，限时额度到本月底结束，11 月起是否回到 1 万次，官方没写（**未核实**）。即使回到 1 万次，15 人团队每月约 1000–2000 次也够用；但这是“企业内所有自建应用合计”，同企业里别的应用也在消耗。
- **Slack 2025-05 限流收紧**：只针对非应用市场的**商业分发**应用读历史消息；原文明确“内部自建应用不受影响”。Cohive 不读历史，零影响。

### 1.7 网络与地区

| 项 | 飞书 | Lark | Slack | Discord |
|---|---|---|---|---|
| 国内 | 直连 | 能连（**推断**） | 要代理 | 被封，要代理 |
| 东南亚 | 能连，质量**未核实** | 总部新加坡；组织的数据存放地注册时确定、之后不能改（**原文**） | 直连（**推断**） | 直连；但印尼主管部门 2025-11 公开表示平台不删违规内容可能整体封禁，点名了 Discord（媒体报道，非官方） |
| 服务地址 | `open.feishu.cn` | `open.larksuite.com` | 连接地址由接口动态下发，没有固定白名单（**原文**） | `gateway.discord.gg`，必须显式写接口版本 v10（**原文**） |

代理按决策记录不归 Cohive 管。只需保证各通道的 HTTP 客户端和 WebSocket 拨号器跟随环境变量里的代理设置（Go 标准库默认如此，钉钉通道已是这样），部署指南里一句话带过即可。

**飞书与 Lark 必须当成两个平台**：Lark 帮助中心原话是两者是“不同主体运营的独立产品”，账号只能在各自的应用里用（**原文**，[Lark 帮助中心](https://www.larksuite.com/hc/en-US/articles/499556070769)）。接口路径和字段基本相同，所以**代码一套、`provider` 取值分开**（`feishu`、`lark`），配置项选品牌。

### 1.8 Go 依赖包

| 项 | 飞书 / Lark | Slack | Discord |
|---|---|---|---|
| 官方 Go 包 | 有：`larksuite/oapi-sdk-go/v3`，MIT，v3.12.2（2026-10-15），约 620 星；直接依赖 `gorilla/websocket` 和 `gogo/protobuf` | **没有**（官方只有 JavaScript、Python、Java） | **没有** |
| 社区包 | — | `slack-go/slack`：BSD-2，v0.30.1（2026-10-04），约 5000 星，活跃；带 Socket 模式子包；未到 1.0，README 自述小版本之间可能不兼容，Socket 模式分发器标注“实验性” | `bwmarrin/discordgo`：BSD-3，v0.29.0（2025-05），约 6000 星，发版偏慢；只依赖 `gorilla/websocket` 和 `x/crypto`，**两者 Cohive 已经在用**。`disgoorg/disgo`：Apache-2.0，v0.19.6（2026-06），活跃；但依赖里有语音加密的 C 库绑定 `godave`，只用网关时会不会引入 C 编译**未核实** |
| 自己实现的难度 | 高：私有二进制分帧协议，官方没公开协议文档 | 低：协议公开，JSON over WebSocket，取地址、逐条确认、处理 hello 和 disconnect、定期刷新重连；和钉钉 Stream 同一量级，约 300 行 | 中高：心跳和心跳确认、序列号、断线恢复、失效会话、多种不可重连的关闭码，比钉钉 Stream 高一档，约 500 行 |
| 建议 | **用官方包**，只引入 `ws`、`core`、`event/dispatcher` 子包；发消息、改卡片自己写轻量 HTTP 客户端，风格同钉钉 | **自己实现 Socket 模式加几个接口**，和钉钉通道写法一致，不引入未到 1.0 的依赖；需要的接口只有 `apps.connections.open`、`chat.postMessage`、`chat.update`、`chat.postEphemeral` 几个 | **用 `discordgo`**（依赖已在树里、许可证兼容）；如果后续发现维护停滞，再换 `disgo` 或自写 |

许可证都与 Apache-2.0 兼容，没有 GPL 类问题。依据：Go 模块代理上的各包 `go.mod`、[oapi-sdk-go](https://github.com/larksuite/oapi-sdk-go)、[slack-go](https://github.com/slack-go/slack)、[discordgo](https://github.com/bwmarrin/discordgo)、[disgo](https://github.com/disgoorg/disgo)

---

## 2. 飞书 / Lark 补充

旧调研第 1–6 节的结论大部分仍然成立，这里只补新内容。

### 2.1 一键建应用（扫码）

官方依赖包的 `registration.RegisterApp` 基于设备授权流程：用户扫码确认后，返回应用编号、密钥和扫码人的 `open_id`；链接 10 分钟有效、只能一人用（**原文**）。

- 建出来的应用官方叫“智能体应用”，源码请求参数是 `archetype=PersonalAgent`（**原文**）。它和“企业自建应用”是不是一回事、需不需要管理员审核，官方没写（**未核实**）。
- 默认模板预置约 35 项权限，范围偏大（含文档评论读写、建群、批量发消息）；事件和回调默认就是长连接，并预置了消息、进群、卡片回调三个事件（**原文**）。
- 设 `Addons.Preset=false` 可以改用最小模板、只申请自己列的权限；扫码人属于 Lark 时自动切到 Lark 域名（**原文**）。

**评价**：如果实测确认它建出的应用能长期使用、权限可以收到最小，部署指南能从十来步压到“扫码 → 管理员批准 → 拉机器人进群”。第一期不依赖它，作为实测项（D7）。

依据：[一键创建应用（Go）](https://open.feishu.cn/document/mcp_open_tools/integrating-agents-with-feishu/create-an-app-in-one-click-go)

### 2.2 其他补充

- **主动私聊**：能，见 1.6。新登录通知等可以直接做。
- **编辑文本消息**：`PUT /im/v1/messages/:id`，最多 20 次，时间窗口由企业管理员设定（**原文**）。审批和通知都用卡片，用不上，备查。
- **长连接选项**：依赖包新增 `WithConnectionHost`、`WithConnectionHeaders`，可以改写连接地址和握手头（**原文**），走网关或代理时有用。
- **依赖包的一个小隐患**：长连接分帧依赖 `gogo/protobuf`，该项目已停止维护（**推断**，属社区常识）。目前不影响使用，记一笔。

---

## 3. Slack、Discord 补充

### 3.1 Slack 的条款风险（重要）

Slack 接口条款（2025-10-10 生效）原文：只要“用户可能为你的产品付费”就算**商业分发**，包括免费增值、连接付费产品的免费应用，并明确覆盖“给客户提供定制应用或应用模板”；商业分发要另签协议，对多数应用来说就是**上应用市场**。而应用市场要求用 HTTP 回调，**不能用 Socket 模式**。条款还禁止用接口数据训练大模型。

- 现在：Cohive 开源免费、每个团队在自己的工作区自建应用，不构成商业分发（**推断**），可以用 Socket 模式。
- 将来：一旦 Cohive 出付费版或托管版，仓库里提供的 Slack 应用清单就可能被算作“应用模板”，要重新评估：要么走应用市场加公网回调，要么只在自托管免费版里提供 Slack。

依据：[Slack 接口条款](https://slack.com/terms-of-service/api)、[HTTP 与 Socket 模式对比](https://docs.slack.dev/apis/events-api/comparing-http-socket-mode)

### 3.2 Slack 的其他要点

- **应用清单**：清单能写好 Socket 模式开关、交互开关、订阅事件、机器人权限、应用主页消息标签、斜杠命令；开了 Socket 模式就不用填回调地址（**原文**）。清单做不到的只有一步：应用级令牌（`xapp-` 开头，勾 `connections:write`）要用户在“基本信息”页手工生成（**原文**）。
- **加权限后必须重新安装应用**才生效；清单里保持关闭令牌轮换（一旦打开不能关）（**原文**）。
- **跨组织共享频道**：外部用户能点按钮。`Hub.Click` 按 `(provider, tenant, user_id)` 找 Cohive 账号，`tenant` 取点击者自己的 `team_id`，外部用户自然对不上已连接的账号，会被拒绝。实现时要确认 `tenant` 取的是点击者的工作区，而不是频道所属工作区。

### 3.3 Discord 的要点

- **斜杠命令要注册**：全局命令（所有服务器可用，可在私聊里用）或按服务器注册（立即生效，不能在私聊里用）。Cohive 启动时注册一条全局命令 `/cohive`（子命令 bind、link、whoami）即可；可以用 `default_member_permissions` 让 bind 只对服务器管理员显示（**原文**）。
- **审批消息用传统的“正文 + 按钮行”**，不要用新版组件消息：新版不能带正文、标志不可撤销，和其他平台的“正文 + 按钮”结构对不齐。
- **邀请链接可以带 `guild_id` 和 `state`**：将来可以做“网页上点一下，安装完成即绑定服务器”，第一期不做（需要客户端密钥换令牌）。
- **私聊受限**：连接码流程是用户主动私聊机器人，没问题；新登录通知这类机器人主动私聊可能被用户隐私设置挡住（50007），要能降级成“只在网页提示”。连续私聊失败返回的 403 是否计入“10 分钟 1 万次无效请求”的封禁阈值，**未核实**，实现时对 50007 要做退避、别反复重试。
- **定位**：Discord 面向社区和游戏，公司团队很少用。它对开源项目自己的社区更有用，比如 Cohive 自己的贡献者社区。

---

## 4. 与旧调研（2026-10-17）的差异

| # | 旧结论 | 现在 | 影响 |
|---|---|---|---|
| 1 | 飞书每次改权限、事件、回调都要发版本并经管理员审核 | 管理员可以给应用开**免审**，也能设免审策略（**原文**） | 部署指南写“自己是管理员可开免审” |
| 2 | 飞书免费版限时 100 万次/月，结束时间未核实 | 两处页面写法仍不一致；版本对比页写“26 年 10 月限时”，即**本月底结束**（**推断**） | 建议 11 月后再核一次，再决定飞书排期 |
| 3 | 重建应用后可用 `union_id` 自动对回 | **Lark 的卡片回调没有 `union_id`**，只有 `user_id`、`open_id`、`tenant_key`（**原文**） | 这个“自动对回”只对飞书有效；Lark 重建应用后成员要重新连接 |
| 4 | Lark 延时更新令牌能用 2 次还是 3 次不一致 | Lark 回调文档已改成 2 次，延时更新接口页仍写 3 次 | 按 2 次设计 |
| 5 | Lark 支持长连接是推断 | Lark 文档示例里直接写了 `WithDomain(lark.LarkBaseUrl)`，限制与飞书相同（**原文**） | 结论不变，有了依据 |
| 6 | 飞书与 Lark 数据不互通，原文未找到 | Lark 帮助中心原文确认两者是独立产品、账号不互通；跨品牌协作要买旗舰版并另购“跨境关联组织” | `provider` 分 `feishu`、`lark` 两个取值 |
| 7 | 没写主动私聊 | 飞书能主动私聊，不要求用户先说话 | 新登录通知可做 |
| 8 | 没写单群人数 | 飞书免费版单群 500 人 | 无影响 |
| 9 | 一键建应用：类型、是否免审未核实 | 建出的是“智能体应用”（`PersonalAgent`），默认权限偏大但可改最小模板，默认长连接；是否免审仍未核实 | 列为实测项 |
| 10 | 建议长连接用官方依赖包 | 不变。注意：钉钉最后是自己实现 Stream 的；飞书协议是私有二进制格式，仍建议用官方包 | — |

---

## 5. 接入方案要点

### 5.1 共用前置：把收件处理从钉钉通道里抽出来（约 1 人日）

现状：`Channel` 接口（`Provider`、`Format`、`Send`、`SendApproval`、`FinishApproval`）和 `Hub.Click` 已经与平台无关；但群命令解析、私聊发码、`绑定群`、`我是谁`、帮助文字、回复语言的判断，都写在 `dingtalk_channel.go` 的 `handleBot` 里。再接三个平台，这段逻辑会被复制三遍。

建议改成：

```go
// Inbound 是与平台无关的收件处理：各通道把收到的消息翻译成 Message 交给它，把返回的回复按自己的格式发回去。
type Message struct {
	Provider, ChatID, ChatTitle string
	From                        ChatUser
	Private                     bool   // 私聊
	Text                        string // 已去掉开头的 @机器人
}
func (h *Hub) Handle(ctx context.Context, m Message) (reply Rendered)
```

同时把钉钉已有的在线状态定为**可选接口**；主动私聊等到有功能用到时再加（见 D8），形状先记在这里：

```go
// StatusReporter：服务器管理页显示通道在线状态（钉钉已有 Status 方法）。
type StatusReporter interface{ Status() (online bool, reason string) }

// DirectSender（以后加）：主动私聊一个人（新登录通知等）。飞书、Slack 原生支持；Discord 可能被用户设置挡住；Telegram 只能私聊启动过机器人的人。
type DirectSender interface {
	SendDirect(ctx context.Context, to store.ChatIdentity, m Rendered) error
}
```

`Channel` 主接口不需要改。表结构（`chat_identities`、`workspace_chats`、`bot_posts`、绑定码表）都已带 `provider`，不用迁移；只需在 `store` 里加 `feishu`、`lark`、`slack`、`discord` 四个取值，并检查数据库约束和审计记录的 `via` 取值是否要放开。

### 5.2 飞书 / Lark（3–4 人日）

**代码**

- 配置：`COHIVE_FEISHU_APP_ID`、`COHIVE_FEISHU_APP_SECRET`、`COHIVE_FEISHU_BRAND`（`feishu` 或 `lark`，决定域名和 `provider` 取值）。
- HTTP 客户端：取 `tenant_access_token` 并缓存；发卡片（带 `uuid` 去重，正好对上 `Rendered.Key`）；`PATCH` 改卡片。
- 长连接：官方包 `ws` 子包，注册 `im.message.receive_v1` 和 `card.action.trigger`；连接状态接到 `StatusReporter`。
- 格式：`Formatter` 输出卡片 Markdown，@ 写 `<at id=open_id></at>`；`Rendered.Title` 做卡片标题。
- 审批：卡片 JSON 2.0 由代码生成，按钮 `value` 带 `种类:工作空间:编号:批准与否`；回调 3 秒内直接返回结果卡片，沿用钉钉的“等 1.8 秒，超时先回处理中”。
- 错误映射：99991400 → 限流；99991403 → 通道暂停（额度用尽）；230053 → 用户屏蔽机器人，私聊放弃。
- Lark：同一实现，换域名、`provider` 取 `lark`，不依赖 `union_id`。

**部署指南要教的步骤**

1. 在开发者后台（飞书 `open.feishu.cn/app`，Lark `open.larksuite.com/app`）建企业自建应用，记下应用编号和密钥。
2. 添加“机器人”能力。
3. 开通权限：以应用身份发消息、接收群里 @ 机器人的消息、读取用户发给机器人的单聊消息（可选：读群信息）。
4. **先把凭据配进 Cohive 并启动**（后台保存长连接方式时要求客户端已在线）。
5. 事件与回调都选“长连接”，添加消息接收事件和卡片回调。
6. 可用范围选参与的成员；创建版本并发布，管理员审核（自己是管理员可开免审）。
7. 把机器人拉进群，在群里 @机器人 发绑定码；成员在网页拿连接码、私聊机器人发码、网页确认。

**需测试应用**：飞书测试企业一个、Lark Starter 测试租户一个（都免费）。头几个要实测的问题：保存长连接时 Cohive 不在线会怎样；卡片里 @ 的人有没有真正收到提醒；Lark 群里机器人占不占 20 人名额；一键建应用是否免审。

### 5.3 Slack（2.5–3.5 人日）

**代码**

- 配置：`COHIVE_SLACK_BOT_TOKEN`（`xoxb-`）、`COHIVE_SLACK_APP_TOKEN`（`xapp-`）。
- 仓库里放一份应用清单（例如 `deploy/slack-app-manifest.yaml`）：Socket 模式开、交互开、订阅 `app_mention` 和 `message.im`、应用主页消息标签开、斜杠命令 `/cohive`、上面列的最小权限。
- Socket 模式自己实现（约 300 行，复用 `gorilla/websocket`，结构照钉钉 Stream）：取地址、确认、hello 和 disconnect 处理、刷新重连。
- 格式：`Formatter` 输出 mrkdwn，只转义 `&<>`，@ 写 `<@U…>`；消息用一个段落块加按钮块，`text` 字段放纯文本摘要。
- 审批：`block_actions` 先确认，再 `chat.update` 改原消息；无权限等提示用 `response_url` 发“仅自己可见”消息。消息引用存“频道编号 + 时间戳”。
- `tenant` 取点击者的 `team_id`。

**部署指南要教的步骤**

1. 打开 `api.slack.com/apps` → “从清单创建” → 选工作区 → 粘贴仓库里的清单 → 创建。
2. “安装到工作区”，复制机器人令牌。
3. “基本信息 → 应用级令牌”，生成一个带 `connections:write` 的令牌并复制。
4. 两个令牌配进 Cohive，启动。
5. 在频道里 `/invite @Cohive`，然后 `/cohive bind 码`。
6. 成员在网页拿连接码，私聊机器人（应用主页的“消息”标签）发码，网页确认。

**需测试应用**：一个免费 Slack 工作区。要实测：Socket 模式下确认时限是否也是 3 秒；机器人改 90 天前的消息会不会失败；`edit_window_closed` 对机器人是否生效。

### 5.4 Discord（3–4 人日）

**代码**

- 配置：`COHIVE_DISCORD_BOT_TOKEN`（应用编号可以用令牌调 `/applications/@me` 取到）。
- 依赖 `discordgo`；网关意图只要“服务器消息”和“私聊消息”。启动时注册全局斜杠命令 `/cohive`（bind、link、whoami）。
- 格式：`Formatter` 输出 Markdown，@ 写 `<@id>`；发送时 `allowed_mentions.users` 只填 `Rendered.Mentions`。
- 审批：正文加一行按钮，`custom_id` 带审批数据（100 字以内够用）；点击用回应类型 7 直接改成结果，无权限时回“仅自己可见”的提示。
- 私聊：50007 视为“用户不收私信”，不重试。

**部署指南要教的步骤**

1. 开发者门户 → 新建应用 → 机器人页“重置令牌”并复制（不需要打开任何特权意图）。
2. 授权链接生成器勾 `bot`，权限勾查看频道、发送消息、嵌入链接、读消息历史；打开链接，选自己的服务器。
3. 令牌配进 Cohive，启动（自动注册斜杠命令）。
4. 在频道里 `/cohive bind 码`；成员 `/cohive link 码`（或私聊机器人发码），网页确认。

**需测试应用**：一个 Discord 测试服务器。要实测：全局命令首次注册后多久可用；`disgo` 只用网关时是否引入 C 编译；私聊被拒时的错误码与封禁阈值。

### 5.5 降级规则（沿用现有口径）

网页审批永远是权威入口，群里的按钮只是快捷方式。长连接断开、额度用尽、私聊被拒时：通知保留不计失败，服务器管理页显示通道状态和原因，审批在网页做；能改消息就回写，不能就只记日志。三个新平台都不需要新的降级种类。

---

## 6. 需要 lee 决定的事

| 编号 | 问题 | 选项 | 建议 |
|---|---|---|---|
| D1 | 三个平台做不做、什么顺序 | A. 共用前置 → Slack → 飞书 / Lark → Discord 按需；B. 共用前置 → 飞书 / Lark → Slack → Discord 按需；C. 都先不做，等有真实团队提需求 | **A**。仓库面向海外开源用户，Slack 部署最省事、条款现阶段无碍。如果近期有具体中文团队要用飞书，换成 B。无论选哪个，共用前置（5.1）建议先做 |
| D2 | 授权建测试环境做实测（都免费、不涉及真钱） | A. 做到哪个平台就建哪个的测试环境（Slack 免费工作区、飞书测试企业加 Lark Starter 租户、Discord 测试服务器）；B. 现在一次建齐；C. 不建，只按文档写 | **A**。每个平台的部署指南都要实测一遍才能交给外部团队 |
| D3 | 飞书和 Lark 的 `provider` 取值 | A. 分开：`feishu`、`lark`，代码一套；B. 合并成一个 `feishu`，靠 `tenant` 区分 | **A**。官方明确两边账号不互通，Lark 回调还少 `union_id`，分开更不容易出错 |
| D4 | 长连接用依赖包还是自己写 | A. 飞书用官方包，Slack 自己写，Discord 用 `discordgo`；B. 三个都用依赖包（Slack 用 `slack-go`）；C. 三个都自己写 | **A**。飞书是私有协议，自己写风险大；Slack 协议公开、简单，自己写和钉钉一致，也避开未到 1.0 的依赖；Discord 断线恢复较复杂，`discordgo` 的依赖 Cohive 已经都有 |
| D5 | Slack 条款：将来出付费版或托管版时 Slack 怎么办 | A. 现在照常用 Socket 模式，在决策记录里写明“出付费或托管版前重评 Slack 接入”；B. 现在就按应用市场的要求做 HTTP 回调；C. 不做 Slack | **A**。现在是开源免费自托管，不构成商业分发；提前做公网回调违背内网部署的初衷 |
| D6 | 账号连接方式 | A. 三个平台统一用“私聊发码 + 网页确认”（Slack、Discord 另加斜杠命令发码）；B. 再加网页授权登录（用 Slack 登录、Discord 授权）作为增强 | **A**。直接复用 `SubmitLinkCode`，纯出站；B 要在平台后台登记回跳地址，内网地址能否登记未核实，等有人嫌麻烦再做 |
| D7 | 飞书是否采用“扫码一键建应用” | A. 第一期不用，部署指南按手工步骤写；实测时顺带验证；B. 第一期就用 | **A**。建出的是“智能体应用”，默认权限偏大、是否免审未核实，不宜作为主路径 |
| D8 | 主动私聊（`DirectSender`）什么时候进接口 | A. 随第一个新平台一起作为可选接口加进来，Telegram 和钉钉之后补；B. 等“新登录通知”需求排期时再加 | **B**。现在没有用到私聊的功能，先不加接口；本调研已确认三个平台都能做（Discord 有条件） |

## 7. 未核实（实测时逐条确认）

1. 飞书免费版 100 万次限时额度何时结束、之后是否回到 1 万次。
2. 飞书一键建应用（`PersonalAgent`）是否需要管理员审核、能否长期当普通自建应用用。
3. Lark Starter 群里的机器人占不占 20 人名额；飞书管理员能否限制谁建自建应用。
4. 东南亚直连飞书、Lark 的网络质量；印尼、越南对 Discord、Slack 的可用性。
5. Slack Socket 模式下的确认时限；`edit_window_closed` 对机器人是否生效；免费版下改 90 天前消息是否失败；“用 Slack 登录”的回跳地址是否必须 https。
6. Slack 付费工作区管理员开了“装应用需审批”时的流程。
7. Discord 私聊被拒的 403 是否计入无效请求封禁阈值；`disgo` 只用网关时是否引入 C 编译；Discord 授权回跳地址能否是内网地址。
8. 各平台权限标识以后台实际显示为准（本报告按文档整理，未在后台核对）。
