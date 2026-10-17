# 钉钉、飞书（含国际版）接入调研

日期：2026-10-17 · 性质：调研，只读官方文档，未注册账号、未建应用、未调用任何真实接口 · 适用：接第二种通讯工具之前

资料以官方开放平台为准：钉钉国内 `open.dingtalk.com`、钉钉国际版帮助中心 `help.dingtalk.io`、飞书 `open.feishu.cn`、Lark `open.larksuite.com`（三家文档地址末尾加 `.md` 都能取到原文）。Go 依赖信息来自 GitHub 与 Go 模块代理（2026-10-17 查询）。标“**未核实**”的是官方没写或只能推断的；标“（推断）”的是根据官方原文推出来的。

---

## 一页结论

**能不能做：都能做，而且都满足“服务端只需出站访问”。**

- **飞书 / Lark**：企业自建应用 + 官方“长连接”模式。服务端主动连出一条 WebSocket，事件订阅和卡片按钮回调（`card.action.trigger`）都从这条连接收。官方原话是“无需提供公网 IP 或域名、无需使用内网穿透工具”。点击者身份在回调的 `operator.open_id` 里，点击后可以把原卡片改成结果。[长连接接收回调](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/callback-subscription/configure-callback-request-address)
- **钉钉国内版**：企业内部应用 + 机器人 + 互动卡片（投放时设 `callbackType=STREAM`）+ Stream 模式长连接。官方称“零公网 IP、零域名”，回调里带点击者 `userId` 和卡片编号 `outTrackId`，可以改卡。[卡片事件回调](https://open.dingtalk.com/document/development/event-callback-card.md)
- **钉钉国际版**：通知和网页登录能做。审批按钮**不能承诺**：国际版服务端接口目录里没有互动卡片章节，Stream 网关能否连通也未核实。

**先做哪个：先飞书（同一套代码顺带支持 Lark），钉钉第二。** 理由：

1. 飞书卡片 JSON 可以由代码直接生成，不需要在后台预先搭模板。钉钉要求每个自部署方先在“卡片平台”手工建审批卡片模板，模板能否导入导出未核实。
2. 飞书官方 Go 依赖包维护活跃，MIT 许可证，长连接和卡片回调都有现成封装。
3. 飞书换一个基础域名就是 Lark，可以覆盖东南亚团队。
4. 飞书免费版支持 100 人，个人可以自建企业，自己当管理员。

**关键限制（和 Telegram 的根本差别）：**

| # | 限制 | 影响 |
|---|---|---|
| 1 | **成员必须都在同一个企业 / 组织里**。飞书自建应用只能在本企业用；钉钉企业机器人只能进本组织的“内部群” | 不像 Telegram 那样谁都能把机器人拉进任意群。外部协作者要额外开“外部群”能力（飞书要求企业已认证） |
| 2 | **每个自部署团队都要在自己的企业里建一个应用**（飞书企业自建应用 / 钉钉企业内部应用），把应用编号和密钥配给 Cohive，相当于 Telegram 的机器人令牌 | 长连接只支持自建应用。如果 Cohive 做成商店应用，就得维护一个公网回调地址，违背自部署初衷。部署文档要逐步写清楚 |
| 3 | **没有 Telegram 那种 `t.me/机器人?start=参数` 的带参数机器人链接**。飞书“打开机器人会话”的链接只接受 `appId` 一个参数；钉钉没找到同类链接 | 账号连接流程要改：在单聊里发一次性绑定码，或者走网页授权登录。见第 3 节 |
| 4 | **@ 人要靠平台用户编号**（飞书 `open_id`，钉钉 `userId`），不能用用户名 | 现在 `Render` 输出的是 Telegram HTML 加 `@用户名`，要改成结构化消息，由各通道自己渲染 |
| 5 | **按钮回调要在 3 秒内响应**（钉钉在回调响应里改卡要求 2 秒内） | 审批要快速落库后立即响应；改卡可以异步做 |
| 6 | **免费档有接口调用的月度额度** | 钉钉标准版：接口 1 万次/月，Webhook 与 Stream 合计 3000 次/月，超额后 Stream 停推。Lark 免费版：1 万次/月。飞书免费版：目前限时 100 万次/月，之后可能回到 1 万次。额度用尽要当成正式故障处理，降级为网页审批 |

**对已定口径的一点修正**：decisions.md 写的是“以后换 Slack 只换连接这一步”。调研结果是，换通讯工具要换的不只是连接这一步，还有四件事：发消息的格式与 @ 方式、按钮回调的收法、改原消息的方式、群的绑定方式。所以要先把通道接口抽象好（见第 7 节），之后再接新平台才真的是“只加一个实现”。

---

## 1. 群里发消息并 @ 到人

### 1.1 机器人形态

| 平台 | 形态 | 能进什么群 | 主动发群消息 | @ 人 | 按钮能否回调 | 出处 |
|---|---|---|---|---|---|---|
| 钉钉 | 群自定义机器人（Webhook + 加签 / 关键词 / IP） | 任何群（内部群、普通群、外部群） | 能 | `atUserIds`（userId）/ `atMobiles`（手机号），一次最多 50 人 | **不能**，actionCard 只能跳链接 | [自定义机器人发送群消息](https://open.dingtalk.com/document/development/custom-robots-send-group-messages.md) |
| 钉钉 | **企业内部应用机器人** | **只能进本组织的内部群**（钉钉的“普通群”专指不属于组织的群，进不去） | 能，`/v1.0/robot/groupMessages/send`，需要 `openConversationId` + `robotCode` | **这个接口“暂不支持 @”**。要 @ 只能走群内机器人 Webhook、回调里的 sessionWebhook 或互动卡片 | 能（互动卡片） | [添加机器人入群](https://open.dingtalk.com/document/dingstart/add-robot-to-group.md)、[机器人回复/发送消息](https://open.dingtalk.com/document/dingstart/robot-reply-and-send-messages.md) |
| 钉钉 | 场景群模板机器人、群聊酷应用 | 场景群 / 装了酷应用的群 | 能 | — | 能 | [机器人概述](https://open.dingtalk.com/document/development/development-robot-overview.md)，Cohive 用不上 |
| 飞书 | 群自定义机器人（Webhook + 签名 / 关键词 / IP 白名单） | 只能在添加它的那一个群 | 能 | `<at user_id="open_id 或 user_id">`；不支持 email、union_id | **不能**，按钮只能跳链接，也不能改消息 | [自定义机器人使用指南](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot) |
| 飞书 | **企业自建应用机器人** | 本企业的群（外部群要开“对外共享”，且企业须已认证） | 能，`POST /open-apis/im/v1/messages` | 卡片里写 `<at id=open_id>`，被 @ 的人会收到提醒 | 能（卡片回调） | [机器人概述](https://open.feishu.cn/document/client-docs/bot-v3/bot-overview)、[发送消息](https://open.feishu.cn/document/server-docs/im-v1/message/create)、[卡片富文本](https://open.feishu.cn/document/uAjLw4CM/ukzMukzMukzM/feishu-cards/card-json-v2-components/content-components/rich-text) |
| 飞书 | 商店应用 | 安装了它的企业 | 能 | 同上 | 能，但**不支持长连接**，要用公网回调地址 | [长连接接收事件](https://open.feishu.cn/document/server-docs/event-subscription-guide/event-subscription-configure-/request-url-configuration-case) |

**建议**：钉钉和飞书都用“企业内部 / 自建应用机器人”。钉钉的通知和审批统一用互动卡片发，原因是 orgGroupSend 接口不支持 @。

### 1.2 @ 人用什么编号、怎么拿到

| 平台 | 编号 | 有效范围 | 怎么拿到 | 出处 |
|---|---|---|---|---|
| 钉钉 | `userId`（也叫 staffId） | 同一企业内唯一，**不可更改** | 消息回调里的 `senderStaffId`；网页登录拿到 unionId 后换 userId；管理员也可以按手机号查 | [常见问题·通讯录](https://open.dingtalk.com/document/development/common-errors.md)、[接收消息](https://open.dingtalk.com/document/development/receive-message.md) |
| 钉钉 | `unionId` | 同一个“开发者企业账号”内不变，换开发者账号就不同 | 网页登录 `/v1.0/contact/users/me` | [根据 unionid 获取 userid](https://open.dingtalk.com/document/development/query-a-user-by-the-union-id.md) |
| 飞书 | `open_id`（ou_） | **一个用户在一个应用里**唯一；重建应用后全部变化 | 消息事件、卡片回调、进群事件里的 operator 字段都带；网页授权登录也能拿，不需要额外权限 | [用户身份概述](https://open.feishu.cn/document/home/user-identity-introduction/introduction)、[如何选择 ID](https://open.feishu.cn/document/home/user-identity-introduction/user-id) |
| 飞书 | `union_id`（on_） | 同一开发商（按组织算）名下的所有应用都相同 | 同上 | 同上 |
| 飞书 | `user_id` | 同一企业内所有应用都相同 | 要单独申请 `contact:user.employee_id:readonly` 权限，只有自建应用能申请 | 同上 |
| Telegram（对照） | 用户编号 | 全局 | 现在已经存了 | 现状是用 `@用户名` 提及，没设用户名的人 @ 不到 |

**跳出原框架的一点**：Telegram 其实也能按编号 @ 人。官方支持 `<a href="tg://user?id=…">名字</a>` 这种写法。对象是群成员，或者私聊过机器人的人，都能保证生效；已经连接过的成员正好私聊过机器人。这样可以修掉“没设用户名就 @ 不到”的问题，建议随第 0 步的拆表一起做。[Bot API · formatting options](https://core.telegram.org/bots/api#formatting-options)

### 1.3 消息格式

| 平台 | 格式 | 说明 | 出处 |
|---|---|---|---|
| 钉钉 | text、markdown、actionCard、feedCard（Webhook）；orgGroupSend 用 `sampleText`、`sampleMarkdown`、`sampleActionCard` 等 msgKey | actionCard 的按钮只能跳链接 | [消息类型](https://open.dingtalk.com/document/development/robot-message-type.md) |
| 钉钉 | **互动卡片（新版）** `POST /v1.0/card/instances/createAndDeliver` | 要先在卡片平台建模板，拿到 `cardTemplateId`；能投到群里；支持 `atUserIds`；按钮可以设成“回传请求” | [创建并投放卡片](https://open.dingtalk.com/document/development/create-and-deliver-cards.md) |
| 钉钉 | 普通版互动卡片 / JSON 构建卡片 | 普通版“不再新增能力”，新应用基本用不了；JSON 卡片的服务端回调“未开放” | [普通版](https://open.dingtalk.com/document/development/robots-send-interactive-cards.md)、[JSON 构建卡片](https://open.dingtalk.com/document/development/json-build-card.md) |
| 飞书 | text、post（富文本）、interactive（卡片） | **卡片 JSON 2.0 可以由代码直接生成，不需要模板**；2.0 只支持共享卡片（更新后所有人看到同一份），正适合审批 | [卡片 JSON 2.0 结构](https://open.feishu.cn/document/uAjLw4CM/ukzMukzMukzM/feishu-cards/card-json-v2-structure) |

### 1.4 频率限制

| 对象 | 限制 | 出处 |
|---|---|---|
| 钉钉自定义机器人 | 每个机器人**每分钟 20 条，超出后限流 10 分钟**（国际版相同） | [自定义机器人发送群消息](https://open.dingtalk.com/document/development/custom-robots-send-group-messages.md) |
| 钉钉企业应用接口 | 每个应用每个接口 20 次/秒（标准版）；单个出口 IP 20 秒内超过 1 万次封 5 分钟 | [调用频次与限流](https://open.dingtalk.com/document/development/how-to-process-api-throttling-on-the-dingtalk-server.md) |
| 飞书自定义机器人 | 100 次/分钟、5 次/秒；请求体不超过 20 KB；建议避开整点和半点 | [自定义机器人使用指南](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot) |
| 飞书应用发消息 | 1000 次/分钟、50 次/秒；同一用户 5 次/秒；**同一群里所有机器人合计 5 次/秒**；可以带 `uuid` 去重（1 小时内同一个 uuid 只发一次） | [发送消息](https://open.feishu.cn/document/server-docs/im-v1/message/create) |
| 飞书超限 | HTTP 429，错误码 99991400，响应头 `x-ogw-ratelimit-reset` 给出建议等待秒数 | [频控策略](https://open.feishu.cn/document/server-docs/api-call-guide/frequency-control) |

3–15 人的团队，通知量离这些上限很远。飞书的 `uuid` 去重可以直接用来消除现在“发出后、记录前崩溃会重复发一条”的问题。

---

## 2. 群里的交互按钮与回调

| 问题 | 钉钉国内 | 钉钉国际 | 飞书 / Lark | Telegram（对照） |
|---|---|---|---|---|
| 点击事件怎么回到服务端 | 卡片按钮设成“回传请求”；投放时选 `callbackType=STREAM` 或 `HTTP` | 未核实（国际版文档里没有卡片接口） | `card.action.trigger` 回调 | callback_query |
| **必须有公网回调地址吗** | **不必**。走 Stream 模式：服务端出站建 WebSocket，“零公网 IP、零域名” | Stream 有文档，但 SDK 默认连国内域名，要改成 `api.dingtalk.io`；能否连通**未核实** | **不必**。走长连接：“无需提供公网 IP 或域名” | 不必（长轮询） |
| 长连接的前提 | 仅企业内部应用（和第三方企业应用）；每个应用最多 50 条连接 | 同左（文档层面） | **仅企业自建应用**；每个应用最多 50 条连接；多条连接时随机投给其中一条，不广播 | — |
| 同一条连接能否既收消息又收按钮 | 能：机器人消息、卡片回调、事件订阅都走 Stream | — | 能：事件和回调注册到同一个分发器 | 能 |
| 响应时限 | 3 秒；在回调响应里改卡要 2 秒内 | — | **3 秒**，超时用户端报错 200341 | 宽松 |
| 点击者身份 | 回调里的 `userId`，由平台填写 | — | `operator.open_id` / `union_id` / `tenant_key`；**不要用按钮里带的自定义值当身份** | `from.id` |
| 身份可靠吗 | 由平台在鉴权后的通道推送，可信度高（推断，官方没有逐字承诺不可伪造） | — | 同左（推断）。HTTP 模式要自己验签解密，长连接由 SDK 处理 | 可信 |
| 点击后能否改原消息 | 能：① 在回调响应里返回新的 `cardData`；② 之后异步调 `PUT /v1.0/card/instances`，按 `outTrackId` 更新（不要在回调过程中调） | — | 能：① 3 秒内在回调响应里返回新卡片；② 凭回调里的令牌延时更新，30 分钟内有效、最多用 2 次（Lark 文档写 3 次，两处不一致）；③ `PATCH /im/v1/messages/:id`，只对 14 天内的消息有效 | editMessageText |
| 别处处理后回写群里的卡片 | 按 `outTrackId` 更新，有效期**未核实** | — | 用 PATCH，只限 14 天内的消息；卡片发出 30 天内还能点 | 可以 |
| Webhook 机器人的按钮 | 只能跳链接 | 只能跳链接 | 只能跳链接 | — |

出处：[钉钉卡片事件回调](https://open.dingtalk.com/document/development/event-callback-card.md)、[钉钉更新卡片](https://open.dingtalk.com/document/development/interactive-card-update-interface.md)、[钉钉 Stream 模式（国际）](https://help.dingtalk.io/zh/open/development/introduction-to-stream-mode.md)、[钉钉国际版服务端接口索引](https://help.dingtalk.io/_llms/zh/product-6fef8881/api.txt)、[飞书长连接接收回调](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/callback-subscription/configure-callback-request-address)、[飞书卡片回传交互回调](https://open.feishu.cn/document/feishu-cards/card-callback-communication)、[飞书处理卡片回调](https://open.feishu.cn/document/uAjLw4CM/ukzMukzMukzM/feishu-cards/handle-card-callbacks)、[飞书延时更新](https://open.feishu.cn/document/ukTMukTMukTM/uMDO1YjLzgTN24yM4UjN)、[飞书更新消息卡片](https://open.feishu.cn/document/server-docs/im-v1/message-card/patch)、[Lark 长连接接收回调](https://open.larksuite.com/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/callback-subscription/configure-callback-request-address)

**需要注意的坑：**

- 飞书旧版“消息卡片回传交互”回调**不支持长连接**，必须用新版 `card.action.trigger`。
- 卡片 JSON 1.0 默认是“只有点击者看到更新”。审批卡片要直接用 2.0。
- 两家都是 3 秒时限。Cohive 现在 `handleCallback` 的写法是：先在事务里审批，再回应点击，再改各群的消息。迁移过来要改成：审批落库后马上在响应里返回结果卡片，**其他群里的同一条审批消息异步回写**。设备申请会发到多个群，现有的 `bot_posts` 就是为这件事建的。

---

## 3. 账号绑定（对应 Telegram 的“打开带参数的机器人链接”）

| 方案 | 钉钉 | 飞书 / Lark | 只需出站吗 | 评价 |
|---|---|---|---|---|
| 带参数的机器人链接（现在 Telegram 的做法） | **没找到同类链接**（未核实）。官方跳转协议只有打开网页、打开应用、打开小程序 | **不支持**。`applink.feishu.cn/client/bot/open` 只有 `appId` 一个参数；“用户进入单聊”事件也不带自定义参数 | — | 不能照搬 |
| A. 机器人单聊发一次性绑定码 | 用户在机器人单聊里发码（或在内部群里 @ 机器人发码），回调里带 `senderStaffId` | 网页显示码，并给一个打开机器人单聊的链接；用户发码后，`im.message.receive_v1` 里带发送者 `open_id` | **是**，全程走长连接 | 最接近现在的体验，也不用在后台登记回调地址 |
| B. 网页授权登录 | `login.dingtalk.com/oauth2/auth`（国际版是 `login.dingtalk.io`）→ 用授权码换用户令牌 → 拿 unionId → 换 userId | `accounts.feishu.cn/open-apis/authen/v1/authorize` → 用授权码换令牌 → `user_info` 拿 open_id 和 union_id（不需要额外权限） | 回跳在**用户浏览器**里完成，换令牌是 Cohive 出站请求（推断） | 一步到位；但要在应用后台登记回跳地址，内网地址能否登记**未核实**；相关接口两家都不计入额度 |
| C. 按邮箱或手机号自动对号 | 有通讯录权限时可以按手机号查 | `batch_get_id` 接口 | 是 | 飞书官方说邮箱、手机号是管理员导入的，“未经本人验证”，只能作为建议，必须本人确认 |

出处：[钉钉单聊跳转协议](https://open.dingtalk.com/document/dingstart/private-chat-related-jump-protocol.md)、[钉钉获取登录用户凭证](https://open.dingtalk.com/document/development/obtain-identity-credentials.md)、[飞书打开机器人会话](https://open.feishu.cn/document/uAjLw4CM/uYjL24iN/applink-protocol/supported-protocol/open-a-bot)、[飞书用户进入单聊事件](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/chat-access_event/events/bot_p2p_chat_entered)、[飞书获取授权码](https://open.feishu.cn/document/authentication-management/access-token/obtain-oauth-code)、[飞书获取用户信息](https://open.feishu.cn/document/server-docs/authentication-management/login-state-management/get)、[飞书按邮箱查 ID](https://open.feishu.cn/document/server-docs/contact-v3/user/batch_get_id)

**安全提醒（现有 Telegram 流程没有这个问题）**：现在的连接参数藏在私人链接里，别人看不到。改成“发码”后，码一旦泄露（比如有人误把码发到群里），别人可以抢先把**他自己的**通讯账号绑到你的 Cohive 账号上，之后他点审批按钮，系统会把他当成你。缓解办法有两个：码只在机器人单聊里认，群里发的码一律作废；绑定前在网页上弹一次确认，例如“将连接到飞书用户 张三，确认？”，本人点了才生效。

**编号稳定性 → 存什么：**

- 钉钉：存 `corpId + userId`。userId 不可改；unionId 换开发者账号会变。
- 飞书：存 `tenant_key + open_id`，同时存 `union_id`。团队重建应用后 open_id 全部失效，这时可以用 union_id 自动重新对上（union_id 跨开发商是否稳定官方没说，自建场景下等价于每个企业一套，推断）。
- 飞书和 Lark 是两套独立的账号体系，同一个人在两边是两个账号。

---

## 4. 拿到群编号（对应 Telegram 的 /chatid）

| 途径 | 钉钉 | 飞书 / Lark |
|---|---|---|
| 群里 @ 机器人发命令 | 回调里的 `conversationId` 就是发群消息要用的群编号，同时带群名、`senderStaffId`、`isAdmin` | `im.message.receive_v1` 里带 `chat_id`（oc_…）；只需“接收群里 @ 机器人的消息”这一项权限，不用申请读全部群消息 |
| 机器人进群事件 | 未细查 | `im.chat.member.bot.added_v1`：带群编号、群名、谁拉的机器人、是否外部群 |
| 客户端里直接看 | — | 飞书 7.60 以上，群设置页能看到群 ID |
| 机器人所在群列表 | — | `GET /open-apis/im/v1/chats` |

出处：[钉钉企业机器人发群消息（国际）](https://help.dingtalk.io/zh/open/development/the-application-robot-in-the-enterprise-sends-group-chat-messages.md)、[钉钉机器人接收消息](https://open.dingtalk.com/document/dingstart/robot-receive-message.md)、[飞书机器人进群事件](https://open.feishu.cn/document/server-docs/group/chat-member/event/added-2)、[飞书接收消息](https://open.feishu.cn/document/server-docs/im-v1/message/events/receive)、[飞书群 ID 说明](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/chat-id-description)

**建议比 /chatid 更顺的流程**：工作空间管理员在网页“管理 → 工作空间 → 连接群”拿到一个一次性码。在群里 @ 机器人发 `/bind 码`，回调里同时拿到群编号和发令人，服务端校验发令人已连接、并且是这个工作空间的管理员后，直接绑定。管理员不用再复制粘贴群编号。飞书还可以加一种办法：根据进群事件，在网页上列出“待认领的群”。`/chatid` 保留作兜底；Telegram 也可以顺手改成同样的流程。

---

## 5. 接入门槛

| 项 | 钉钉国内 | 钉钉国际 | 飞书 | Lark |
|---|---|---|---|---|
| 必须有企业 / 组织吗 | 必须 | 必须 | 必须；个人可以免费自建企业，自己当管理员 | 必须 |
| 免费档 | 标准版免费（建团队本身收费与否**未逐字核实**） | Free / Basic / Standard / Premium 四档，付费档 30 天试用 | 免费版 0 元，最多 100 人 | Starter 免费，**最多 20 人、单群最多 20 人**，聊天记录保留 18 个月 |
| 谁能建应用 | 企业管理员，或有开发者权限的子管理员 | 同左 | 企业成员在开发者后台建；管理员能否限制谁可以建，**未核实** | 同飞书 |
| 审核 | 企业内部应用自己发布，不用平台审核；发布文档的准备工作里写了“企业已完成认证”，是否强制**未核实** | 同左（推断） | **每次改权限、事件、回调都要发新版本，并经企业管理员审核**（自己是管理员就是自审） | 同飞书 |
| 一次性手工准备 | 建应用 → 开机器人、选 Stream → 申请权限 → **在卡片平台建审批卡片模板** → 发布 → 把机器人加进内部群 | 同左，但卡片能力未核实 | 建应用 → 开机器人 → 事件和回调都选长连接（保存时要求 Cohive 已经在线）→ 申请权限 → 发版本 → 拉机器人进群 | 同飞书 |
| 一键建应用 | 无 | 无 | 官方 Go 依赖包新增了“扫码一键创建应用”（`registration.RegisterApp`）：扫码确认后返回应用编号和密钥，还能预填权限和回调。建出来是什么类型的应用、是否免审，**未核实** | 同左（自动识别是飞书还是 Lark） |
| 接口月度额度 | 标准版 **1 万次/月**（2026-10-21 起）、20 次/秒；拿令牌、通讯录、登录类接口不计。**Webhook 与 Stream 合计 3000 次/月**，超额后 Stream 报 20001 并停推；卡片回调算不算在内，**未核实** | 限流页写 1 万次/月；Webhook/Stream 额度**未核实** | 所有自建应用合计：原 1 万次/月，超额返回 99991403；现在**限时 100 万次/月**（调整说明页写“2026 年 6 月份限时”，版本对比页标“26 年 10 月限时”），之后是否回到 1 万次**未核实**。发消息、改卡片计入；授权登录、事件订阅、通讯录、群自定义机器人 Webhook 都不计入 | **1 万次/月**；付费 Basic 每人每月 6 美元起，不限次数 |
| 权限（最小集） | `qyapi_robot_sendmsg`、`Card.Instance.Write`、通讯录读取（换 userId 用） | 同左 | 以机器人身份发消息、接收群里 @ 机器人的消息、接收单聊消息（绑定码方案用）、读取群信息；可选：改消息、user_id 字段 | 同飞书 |

出处：[钉钉开发者权限](https://open.dingtalk.com/document/dingstart/get-developer-permissions.md)、[钉钉发布应用](https://open.dingtalk.com/document/dingstart/publish-dingtalk-application.md)、[钉钉计费模型](https://open.dingtalk.com/document/development/dingtalk-application-development-platform-billing-model.md)、[钉钉权益调整公告](https://open.dingtalk.com/document/connection/notice-announcement.md)、[钉钉国际版开通指南](https://help.dingtalk.io/zh/oa/poc-onboarding-guide.md)、[钉钉国际版限流](https://help.dingtalk.io/zh/open/development/api-throttling.md)、[飞书版本对比](https://www.feishu.cn/service)、[飞书自建应用开发流程](https://open.feishu.cn/document/home/introduction-to-custom-app-development/self-built-application-development-process)、[飞书发布与审核](https://open.feishu.cn/document/home/intro-to-custom-app-review)、[飞书调用量上限说明](https://open.feishu.cn/document/uAjLw4CM/ugTN1YjL4UTN24CO1UjN/platform-updates-/custom-app-api-call-limit)、[Lark 定价](https://www.larksuite.com/en_us/plans)、[飞书 Go 依赖包说明](https://github.com/larksuite/oapi-sdk-go/blob/v3_main/README.md)

**额度粗算**：15 人团队，每天 30 条通知加上审批与改卡，约 1000–2000 次/月，1 万次/月的额度够用。钉钉的 Webhook/Stream 3000 次/月要盯住：组织里别人自建的机器人也在消耗这个额度，可能把 Stream 耗停，审批按钮会跟着失效。

---

## 6. Go 生态

| 项 | 钉钉接口 | 钉钉 Stream | 飞书 / Lark |
|---|---|---|---|
| 依赖包 | [`github.com/alibabacloud-go/dingtalk`](https://github.com/alibabacloud-go/dingtalk) | [`github.com/open-dingtalk/dingtalk-stream-sdk-go`](https://github.com/open-dingtalk/dingtalk-stream-sdk-go) | [`github.com/larksuite/oapi-sdk-go/v3`](https://github.com/larksuite/oapi-sdk-go) |
| 许可证 | Apache-2.0 | MIT | MIT |
| 维护 | 代码由工具生成，约每月发布一次，最新 v1.7.46（2026-09-11）。仓库里另有一个 2024 年的 v2.0.83 标签，比 v1 线还旧，**别选错** | 最近提交 2026-07-30；仍是 0.x 版本，最新正式版 v0.9.1 发布于 2025-04；48 星 | 活跃，最新 v3.12.2（2026-10-15）；约 620 星 |
| 体量 | 重：依赖阿里云的 tea / darabonba 运行时，一个仓库里有上百个模块 | 小 | 大，但可以只引入 `core`、`service/im/v1`、`ws`、`event/dispatcher` 几个子包（推断，未实测） |
| 换域名、走代理 | 端点可配置（国际版能否用，**未核实**） | `WithOpenApiHost`、`WithProxy` | `WithOpenBaseUrl(lark.LarkBaseUrl)`；长连接支持 `WithDomain` 和自定义连接器（可以走代理） |

**直接调 HTTP 接口的复杂度：**

- 普通接口（拿令牌、发消息、改卡片、网页登录换令牌）两家都很简单。令牌有效期 2 小时，按应用缓存、提前刷新即可，和现在的 `Telegram` 客户端差不多大。
  - 钉钉：`/v1.0/oauth2/accessToken`，请求头带 `x-acs-dingtalk-access-token`。新旧两套接口风格并存，`getbyunionid` 还在旧版 `oapi` 上。[钉钉 accessToken](https://open.dingtalk.com/document/development/obtain-the-access-token-of-an-internal-app.md)
  - 飞书：`tenant_access_token`，剩余不到 30 分钟时再取会拿到新令牌。[飞书 tenant_access_token](https://open.feishu.cn/document/server-docs/authentication-management/access-token/tenant_access_token_internal)
- **长连接不要自己写**。飞书是私有的二进制分帧协议（protobuf 帧、分片、心跳、ACK）；钉钉要先换连接地址和票据，再处理心跳、重连、逐条确认。

**建议**：普通接口自己写一个轻量 HTTP 客户端，保持和 `internal/notify/telegram.go` 一样的风格与错误分类，不引入阿里云那套运行时。长连接直接用官方依赖包，两个都是 MIT 许可证，与 Apache-2.0 兼容，没有 AGPL 问题。

---

## 7. 对 Cohive 的改造点

### 7.1 现状里绑死 Telegram 的地方

| 位置 | 现状 | 要改成 |
|---|---|---|
| `notify.Sender` | `Send(ctx, target, text string)`，文本是 Telegram HTML | 发送结构化的通知，由各通道自己渲染 |
| `notify.Render` | 输出 HTML，@ 用 `m.TelegramUsername` | 输出“行 + 提及列表 + 链接”这样的结构；@ 用通讯账号表里的编号 |
| `notify.Bot` | Telegram 长轮询；命令、按钮、绑定混在一起 | 每个通道一个收件循环（长轮询 / 长连接），统一把事件交给与平台无关的处理器 |
| `chatAPI`（`SendButtons`、`EditMessage`、`AnswerCallback`） | 群编号和消息编号都是 `int64` | 群和消息的引用改成字符串（飞书 `om_…`、钉钉 `outTrackId`），按钮回应改成“处理函数直接返回结果” |
| 错误类型 | `RetryAfterError`、`ChannelDownError`、`ChatMigratedError`、`PermanentError` | 保留，作为通道的公共错误。新增映射：飞书 99991400 → 限流；99991403 或钉钉 `ApiCountLimitForOrg` / 20001 → 通道暂停 |
| `accounts.telegram_user_id`、`telegram_username` | 存在账号表上 | 移到通讯账号表（7.3） |
| `workspaces.notify_channel`、`notify_target` | 一个工作空间一个群 | 移到工作空间群表（7.3） |
| `bot_posts.chat_id`、`message_id` | `bigint` | 加 `provider` 字段，两列改成 `text` |
| `core.StartLink`、`CompleteTelegramLink` | 只认 telegram | 按通道分两种绑定方式：带参数链接（Telegram）、绑定码加网页确认（飞书 / 钉钉） |
| `/chatid` 加手工填写 | — | 一次性码 + `/bind`（7.4） |

### 7.2 通道接口草案

```go
// Channel 是一种通讯工具。服务器为每种通讯工具配一个实例（Telegram 机器人 / 飞书自建应用 / 钉钉内部应用）。
type Channel interface {
	Provider() string      // telegram、feishu、lark、dingtalk
	Caps() Caps            // 这个通道能做什么，界面和降级逻辑据此判断
	SendNotice(ctx context.Context, chat string, n Notice) (MsgRef, error)
	SendApproval(ctx context.Context, chat string, a Approval) (MsgRef, error) // 无按钮能力时发带网页链接的文字
	UpdateApproval(ctx context.Context, ref MsgRef, result Notice) error      // 别处处理后回写
	Run(ctx context.Context, h Inbound) error                                 // 长轮询或长连接，断线自动重连
}

type Caps struct {
	Buttons    bool // 群里按钮能回调到服务端
	EditAfter  bool // 能在事后改原消息
	MentionID  bool // 能按编号 @ 人
	LinkByCode bool // 账号绑定用“单聊发码”（否则用带参数的机器人链接）
}

// Inbound 由 notify 包里与平台无关的处理器实现，各通道把收到的事件翻译成这几种调用。
type Inbound interface {
	Command(ctx context.Context, c Command) (reply string)       // /bind、/whoami、绑定码……
	Click(ctx context.Context, c Click) (toast string, card *Notice) // 必须在 2 秒内返回，结果卡片直接用于回调响应
}

type Who struct{ Provider, Tenant, UserID, Handle string } // 点击者 / 发令人，来自平台填写的字段
type Mention struct{ Name string; UserID, Handle string }  // 没连接的成员 UserID 为空，只显示名字
```

要点：

- `Click` 的返回值直接做回调响应，解决 3 秒时限；其他群里的同一条审批消息，由 `UpdateApproval` 异步回写。
- 按钮里带的数据只放 `种类:工作空间:编号:是否批准`，身份一律取 `Who`。这和现在 Telegram 的做法一致。
- 一台服务器配一个飞书应用，就只服务**一个**飞书企业：自建应用只能在本企业用。要服务多个企业就要配多个应用，第一期不做。

### 7.3 建议的表结构

```sql
-- 成员的通讯账号：一个账号在每种通讯工具上最多连一个，一个通讯账号全服务器只能连一个 Cohive 账号
CREATE TABLE chat_identities (
    id            bigserial PRIMARY KEY,
    account_id    bigint NOT NULL REFERENCES accounts(id),
    provider      text   NOT NULL,              -- telegram、feishu、lark、dingtalk
    tenant        text   NOT NULL DEFAULT '',   -- Telegram 为空；飞书 tenant_key；钉钉 corpId
    user_id       text   NOT NULL,              -- Telegram 用户编号；飞书 open_id；钉钉 userId（@ 和审批都认它）
    union_id      text   NOT NULL DEFAULT '',   -- 飞书 union_id、钉钉 unionId：重建应用后用于自动对回
    handle        text   NOT NULL DEFAULT '',   -- Telegram 用户名等，只用于显示
    display_name  text   NOT NULL DEFAULT '',
    linked_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, tenant, user_id),
    UNIQUE (account_id, provider)
);

-- 工作空间绑定的群（取代 workspaces.notify_channel / notify_target）
CREATE TABLE workspace_chats (
    id            bigserial PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    provider      text   NOT NULL,
    tenant        text   NOT NULL DEFAULT '',
    chat_id       text   NOT NULL,              -- Telegram 群编号；飞书 oc_…；钉钉 openConversationId
    title         text   NOT NULL DEFAULT '',   -- 绑定时的群名，网页显示用
    bound_by      bigint REFERENCES accounts(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, tenant, chat_id),         -- 一个群只能绑一个工作空间（审批按钮靠群反查工作空间）
    UNIQUE (workspace_id, provider)             -- 每个工作空间每种通讯工具最多一个群
);
-- 显示标题、显示简述两个开关留在 workspaces 上：它们决定“发什么”，与“发到哪”无关。
```

迁移要点：

- `accounts.telegram_*` 迁到 `chat_identities`，`workspaces.notify_*` 迁到 `workspace_chats`。
- `bot_posts` 加 `provider` 列，`chat_id` 和 `message_id` 改成 `text`。
- 待发通知表（`notifications`）要记录发往哪个群（`workspace_chat_id`），这样一个群失败只重试这一个群。
- 审计记录的 `via` 约束要加 `feishu`、`lark`、`dingtalk` 三个取值。
- 应用密钥和 Telegram 令牌一样，放在服务器的环境变量 / 凭据文件里，不进数据库。

### 7.4 一个工作空间能不能绑多个通讯工具

- 表结构上**允许**：每种通讯工具各一个群。有的团队正在从 Telegram 迁到飞书，有的团队国内、东南亚成员混合，过渡期会用到。
- 行为：通知发到所有绑定的群；审批消息也发到所有群，在任一处处理后，其余群里的消息都回写成结果（现有 `bot_posts` 的机制直接沿用）。
- 每个成员只在自己连接过的那个通讯工具上被 @；在一个群里，没连接这个通讯工具的成员只显示名字。

是否在产品里开放多绑，交给 lee 决定（见 D3）。

---

## 8. 建议：先接哪个、分几步

| 步骤 | 内容 | 工作量（粗估，含用例） | 前置 |
|---|---|---|---|
| **第 0 步：通道抽象与拆表（只有 Telegram）** | 7.2 的接口和 7.3 的两张表；`Render` 改成结构化输出；Telegram 改为按编号 @（`tg://user?id=`）；群绑定改成“一次性码 + `/bind`”，`/chatid` 保留。行为上除了“没设用户名也能被 @”，别的不变 | 2–3 人日 | 无。就是台账队列里的 P4，建议提前做 |
| **第 1 步：飞书 / Lark 通知 + 绑定** | 自写 HTTP 客户端（令牌、发卡片、改卡片）；接入官方长连接；绑定码加网页确认；`/bind` 绑群；配置项“平台 = 飞书或 Lark”；部署文档（逐步截图） | 3–4 人日 | 第 0 步；一个飞书测试企业（D2） |
| **第 2 步：飞书 / Lark 审批按钮** | `card.action.trigger`，2 秒内返回结果卡片；别处处理后用 PATCH 回写；额度和限流错误的降级 | 2 人日 | 第 1 步 |
| **第 3 步：钉钉国内版** | Stream 依赖包；互动卡片（附可复制的模板定义和建模板步骤）；@ 走卡片的 `atUserIds`；绑定码；审批回调与按 `outTrackId` 改卡 | 4–5 人日 | 第 0 步；钉钉测试组织；按需求再排 |
| 第 4 步：钉钉国际版 | 先只做通知加网页审批；按钮等实测通过后再开 | 1 人日 + 实测 | 第 3 步；国际版测试组织 |

**做不到时的降级（统一规则：网页审批永远是权威入口，群里的按钮只是快捷方式）：**

| 情况 | 降级 |
|---|---|
| 通道不能回调按钮（Webhook 机器人、钉钉国际版卡片未开通） | 审批消息只发文字，加一个“去网页审批”的链接（要求设了对外地址）；处理后如果能改消息就回写，不能就不回写 |
| 额度用尽、长连接断开 | 通知保留不计失败（沿用现在的通道暂停逻辑）；在网页“服务器管理”里显示通道状态；审批照常在网页做 |
| 通讯账号没连接、通道不能按编号 @ | 只显示成员名（与现在一致） |
| 3 秒内没处理完 | 先回“处理中”，处理完后用 `UpdateApproval` 改卡 |
| 飞书消息超过 14 天不能再改 | 不回写，只记日志（审批申请本身就有更短的过期时间） |

**本次没覆盖**：企业微信（plan.md 第 5.3 节列在“按需接入”里）、Slack。

---

## 需要 lee 决定的事

| 编号 | 问题 | 选项 | 建议 |
|---|---|---|---|
| D1 | 第二种通讯工具先接哪个 | A. 飞书 / Lark；B. 钉钉国内版；C. 先不接，等有真实团队提需求 | **A**。卡片不用模板，依赖包活跃，一套代码同时覆盖国内（飞书）和东南亚（Lark）。如果近期没有具体团队要用，选 C 也合理，但**第 0 步建议无论如何先做** |
| D2 | 是否授权建测试企业 / 组织做实测（免费，不涉及真钱） | A. 建飞书测试企业，外加一个 Lark Starter 测试租户；B. 再加钉钉国内 + 国际测试组织；C. 不建，只按文档写 | **A**。钉钉等真要接时再建。实测要回答的头几个问题：内网地址能否登记为回跳地址、“一键建应用”建出来是什么类型、机器人占不占 Lark 群的 20 人名额 |
| D3 | 一个工作空间能否同时绑多种通讯工具的群 | A. 表结构允许，产品第一期只开放一个；B. 直接开放多个，通知和审批发到所有群 | **A**。表按 7.3 建，第一期界面只让选一个，等有迁移需求再放开 |
| D4 | 飞书 / 钉钉的账号绑定方式 | A. 机器人单聊发一次性码 + 网页确认；B. 网页授权登录；C. 两种都做 | **A**。纯出站，不用在应用后台登记回跳地址，也不依赖对外地址；B 等实测确认内网回跳可行后作为增强 |
| D5 | 是否做“只用群 Webhook 机器人”的最低档（不用建应用，只发通知） | A. 不做；B. 做，作为“只要通知、不要按钮”的快速接入方式 | **A**。Webhook 机器人拿不到用户编号，@ 不到人，审批又要靠网页，价值有限；钉钉还限每分钟 20 条 |
| D6 | Telegram 改为按编号 @（修掉“没设用户名就 @ 不到”） | A. 随第 0 步一起做；B. 单独排一个小改动；C. 不改 | **A** |

## 未核实（建议实测时逐条确认）

1. 钉钉国际版有没有互动卡片和卡片回调；Stream 网关指向 `api.dingtalk.io` 能否连通；Webhook/Stream 额度是多少。
2. 钉钉卡片回调算不算 Webhook/Stream 每月 3000 次的额度；2026-10-21 的调整有没有动这项额度。
3. 钉钉企业内部应用能否导入导出卡片模板（决定每个部署方要不要手工建模板）；`outTrackId` 改卡的有效期。
4. 钉钉发布企业内部应用是否强制要求企业已认证；国内建团队是否完全免费。
5. 钉钉有没有能带参数打开机器人单聊的链接（官方跳转协议里没找到）。
6. 飞书免费版 100 万次/月的限时何时结束、结束后是否回到 1 万次（两处官方页面写法不一致）。
7. 飞书 / 钉钉的网页登录回跳地址能否登记内网地址或 http 地址。
8. 飞书“一键创建应用”（`registration.RegisterApp`）建出的应用类型、是否免管理员审核、能否用长连接。
9. 飞书管理员能否限制谁可以创建自建应用；Lark 延时更新令牌能用 2 次还是 3 次（两处文档不一致）。
10. Lark Starter 里机器人占不占群成员名额；东南亚直连 Lark、钉钉国际版的网络质量；Lark 与飞书“数据完全不互通”的官方原文（只看到依赖包按品牌区分租户，以及二手来源的说法）。
11. 回调里点击者身份“不可伪造”，两家官方都没有逐字承诺（按通道鉴权推断为可靠）。
