# 钉钉接入（含第 0 步：通道抽象与拆表）实现计划

> **给执行者：** 本会话自己实施（调研也是本会话做的），最后整条分支交一次独立审查。步骤用 `- [ ]` 勾选。第 0 步（T1–T3）做完先回报主脑，算一个阶段。

**目标：** 在不改变 Telegram 现有行为的前提下，把通知通道抽象成可容纳多种通讯工具的接口。成员的通讯账号、工作空间的群拆成独立表。然后接入钉钉国内版，做到以下几件事：

- 群里发问题单通知并 @ 到人；
- 群里点审批按钮完成注册、加入、新设备审批，原消息改成结果；
- 在群里一步绑定工作空间；
- 成员在单聊里发一次性码，再到网页确认，完成账号连接。

服务端只需出站访问。网页审批始终是权威入口，钉钉不可用时自动降级为网页审批。

**依据：**
- `docs/brain/decisions.md`“第二种通讯工具”（2026-10-17 lee 定，2026-10-18 改为现在就做）。
- 调研 `docs/research/2026-10-17-dingtalk-feishu.md`（第 1–5 节钉钉部分，第 7 节改造点）。
- 主脑派活（2026-10-18）：lee 的朋友在自己的钉钉企业里实测，问题报到 lee 新建群对应的 Cohive 工作空间。

**前提（main `d1267ec`）：**
- 通知相关代码在 `internal/notify/`：
  - `telegram.go`：Telegram 接口客户端，错误分四类——限流、通道整体不可用、群已升级、不值得重试。
  - `bot.go`：长轮询；命令 `/chatid`、`/whoami`、`/start ln_…`；审批按钮；把各群里的审批消息改成结果。
  - `dispatcher.go`：问题单通知的投递、重试、过期。
  - `render.go`：渲染出 Telegram HTML，@ 用 `@用户名`。
- Telegram 连接存在账号表上：`accounts.telegram_user_id`、`accounts.telegram_username`（第 7 号迁移），全服务器唯一。命令行 `account set --telegram/--telegram-id` 可以只设其中一个。
- 群存在工作空间表上：`workspaces.notify_channel/notify_target`，`(channel, target)` 唯一。审批消息记在 `bot_posts` 表里，`chat_id` 和 `message_id` 都是 `bigint`。连接请求在 `link_requests` 表里（有 `provider` 列）。
- 管理操作记录的 `via` 有检查约束，取值里有 `telegram`；动作 `account.telegram_link/unlink`。
- 机器人令牌走环境变量 `COHIVE_TELEGRAM_BOT_TOKEN`。单机运行时由 `scripts/set-telegram-token.sh` 写入 `~/.config/cohive/telegram.env`，`scripts/cohive-server.sh` 只在服务进程里加载，不打印、不进 `server.env`（测试服由 launchd 常驻，见 `docs/guides/single-host.md`）。Docker 部署时写在 `deploy/.env` 里，见 `docs/guides/deploy-docker.md`。

**不在本次：**
- 钉钉国际版：先按国内版写，接口基础地址可配；国际版能否使用等实测。
- 飞书 / Lark。
- 网页授权登录（OAuth）方式的绑定。
- 一个工作空间同时绑多个群：表结构允许，界面和业务层第一期只允许一个。
- 私聊通知。
- Telegram 改用绑定码绑群（见待拍板 5）。

## 全局约束

- 只新增迁移 `0011_chat_channels.sql`，不改写 0001–0010。Telegram 现有数据**无损**迁移：包括只有用户名或只有编号的半连接状态，以及群绑定、审批消息记录。
- 第 0 步结束时，Telegram 的行为与现在一致，唯一的有意变化是：@ 人改为按用户编号（`<a href="tg://user?id=…">`），没设用户名的人也能 @ 到（decisions：“Telegram 改为按用户编号 @”）。
- 钉钉应用凭据（Client ID、Client Secret）只来自环境变量，不进数据库，不进日志、错误信息和管理操作记录。访问令牌放在请求头里，不进地址；请求体里含密钥的“取令牌”请求，失败时也不打印请求体。
- **钉钉卡片回调必须在 2 秒内返回**：在回调响应里直接改卡，官方要求 2 秒内，整个回调是 3 秒。审批业务事务完成后马上返回结果卡片；其他群里的同一条审批消息异步回写。
- **身份只取平台填写的字段**：钉钉回调里的 `userId` 加 `corpId`、`senderStaffId` 加 `senderCorpId`。按钮的回传参数只用来区分“批准/拒绝”，审批对象由卡片编号（`outTrackId`）在 `bot_posts` 里反查。
- **只向钉钉的域名发请求**：回复命令用回调里带的 `sessionWebhook` 时，先校验它的主机名属于 `*.dingtalk.com`（或配置的基础地址）。
- 测试口径：
  - 执行 `go vet ./... && go test -count=1 ./... && make build`；
  - 用真实 Postgres（`localhost:55432`，用户和密码都是 cohive），禁止模拟数据库；
  - 钉钉的开放接口和 Stream 网关都用本地测试服务器模拟（同 Telegram 的做法），`go test` 不访问外网；
  - 关键判定做变异验证。
- 本机走查用 `127.0.0.1:8792`，新建库 `cohive_dingtalk_try`。不碰测试服 `127.0.0.1:8787`、库 `cohive_test` 和 Telegram 机器人。
- 仓库已公开：提交和文档里不写本机路径、令牌、公司事项；示例一律用 `example.com`、`dingxxxx` 这类占位值。

## 审查重点（规格没明说、最容易咬到人的情况）

1. **迁移无损**：
   - 只有用户名没有编号、只有编号没有用户名、两者都有的账号，迁移后 @ 和审批识别都与原来一致；
   - 关闭了群通知（`--off`）的工作空间，迁移后仍然关闭；
   - 旧的 `bot_posts` 回写仍然有效。
   → T1 迁移用例。
2. **按编号 @ 的转义**：Telegram 显示名里有 `<`、`&`、`"`；钉钉显示名里有 Markdown 特殊字符 `*`、`[`、`]`、`@`。各通道各自转义，不串用。→ T2、T6 用例。
3. **钉钉审批的时限与重复点击**：
   - 两位管理员几乎同时点：第二个人得到“已由 X 处理”的提示，不报错；
   - 数据库慢到快超时：返回“处理中，请稍后在网页查看”，业务事务照常提交，结果卡片随后异步回写；
   - 网页上已经处理过的申请，群里再点得到“已处理”。
   → T6 用例。
4. **绑定码泄露**：
   - 账号绑定码只在机器人单聊里有效。出现在群里时立即作废，并在群里提醒（回复里不复述码）。
   - 钉钉用户已连接别的 Cohive 账号时拒绝绑定。
   - 网页确认前，绑定不生效。
   → T5 用例。
5. **伪造与越权**：
   - 不在 `bot_posts` 里的 `outTrackId` 一律拒绝；
   - 卡片所在群与反查到的工作空间不符时拒绝；
   - 点击者没连接、已停用或不是管理员时拒绝，并留下被拒记录（沿用 `RecordDenied`）；
   - `sessionWebhook` 指向非钉钉域名时不发送。
   → T5、T6 用例。
6. **降级**：
   - 没配卡片模板时，通知改发不带 @ 的 Markdown，审批改发文字加网页链接；
   - 钉钉报“月度额度用尽”时暂停投递，通知保留、不计失败次数，服务器管理页显示原因；
   - Stream 断开超过 5 分钟后，新的审批消息不带按钮。
   → T6 用例。
7. **一个应用只能接一台 Cohive**：钉钉 Stream 在同一应用有多条连接时随机投给其中一条，不广播。两台 Cohive 共用一个应用，会导致按钮回调随机丢到另一台。部署文档要写清楚；启动日志打印 Client ID 的尾号，方便核对。→ T7 文档。

---

## 1. 第 0 步：通道抽象与拆表

### 1.1 数据模型（迁移 `0011_chat_channels.sql`）

```sql
-- 成员的通讯账号：一个 Cohive 账号在每种通讯工具上最多连一个；一个通讯账号全服务器只能连一个 Cohive 账号
CREATE TABLE chat_identities (
    id            bigserial PRIMARY KEY,
    account_id    bigint NOT NULL REFERENCES accounts(id),
    provider      text   NOT NULL CHECK (provider IN ('telegram', 'dingtalk')),
    tenant        text   NOT NULL DEFAULT '',   -- Telegram 为空；钉钉为 corpId
    user_id       text   NOT NULL DEFAULT '',   -- Telegram 用户编号；钉钉 userId（staffId）。空表示只登记了用户名
    handle        text   NOT NULL DEFAULT '',   -- Telegram 用户名（只用于显示与旧式 @）
    display_name  text   NOT NULL DEFAULT '',   -- 钉钉昵称（绑定时记下，网页确认与 @ 时显示）
    linked_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, provider)
);
CREATE UNIQUE INDEX chat_identities_user ON chat_identities (provider, tenant, user_id) WHERE user_id <> '';

INSERT INTO chat_identities (account_id, provider, user_id, handle)
SELECT id, 'telegram', coalesce(telegram_user_id::text, ''), telegram_username
FROM accounts WHERE telegram_user_id IS NOT NULL OR telegram_username <> '';

-- 工作空间绑定的群（取代 workspaces.notify_channel / notify_target）
CREATE TABLE workspace_chats (
    id            bigserial PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    provider      text   NOT NULL CHECK (provider IN ('telegram', 'dingtalk')),
    tenant        text   NOT NULL DEFAULT '',
    chat_id       text   NOT NULL,              -- Telegram 群编号；钉钉 openConversationId（cid…）
    title         text   NOT NULL DEFAULT '',   -- 绑定时的群名，网页显示用
    bound_by      bigint REFERENCES accounts(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, tenant, chat_id),         -- 一个群只绑一个工作空间（审批按钮靠群反查工作空间）
    UNIQUE (workspace_id, provider)             -- 每个工作空间每种通讯工具最多一个群；第一期业务层再限制为总共一个
);
INSERT INTO workspace_chats (workspace_id, provider, chat_id)
SELECT id, notify_channel, notify_target FROM workspaces WHERE notify_channel <> '' AND notify_target <> '';
-- “显示标题 / 显示简述”两个开关留在 workspaces 上：它们决定发什么，与发到哪无关

-- 群里的审批消息：编号改成文本（钉钉的是 outTrackId）
ALTER TABLE bot_posts ADD COLUMN provider text NOT NULL DEFAULT 'telegram';
ALTER TABLE bot_posts ALTER COLUMN chat_id TYPE text USING chat_id::text,
                      ALTER COLUMN message_id TYPE text USING message_id::text;
ALTER TABLE bot_posts ALTER COLUMN provider DROP DEFAULT;

-- 通讯账号连接请求：钉钉要先记下“谁发来了码”，网页确认后才生效
ALTER TABLE link_requests
    ADD COLUMN candidate_tenant  text NOT NULL DEFAULT '',
    ADD COLUMN candidate_user_id text NOT NULL DEFAULT '',
    ADD COLUMN candidate_name    text NOT NULL DEFAULT '';
-- status 新增取值 awaiting_confirm（码已收到，等网页确认）

-- 群绑定码：管理员在网页生成，在群里 @机器人 发出后完成绑定
CREATE TABLE chat_bind_requests (
    id            bigserial PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    provider      text   NOT NULL,
    created_by    bigint NOT NULL REFERENCES accounts(id),
    code          text   NOT NULL UNIQUE,
    status        text   NOT NULL DEFAULT 'pending',   -- pending、bound、failed
    detail        text   NOT NULL DEFAULT '',
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE admin_events DROP CONSTRAINT <via 的检查约束>, ADD CONSTRAINT … CHECK (via IN (…, 'dingtalk'));
ALTER TABLE accounts DROP COLUMN telegram_user_id, DROP COLUMN telegram_username;   -- 见待拍板 7
ALTER TABLE workspaces DROP COLUMN notify_channel, DROP COLUMN notify_target;
```

- **“关闭群通知”的语义保持不变**：现在 `--off` 是把 `notify_channel` 清空，迁移后表现为该工作空间没有 `workspace_chats` 行。投递时“没有群”就标记失败，原因“工作空间已关闭群通知”，与现在一致。
- 管理操作记录的旧附加信息（`telegram_username` 等）照常显示，人话映射保留。

### 1.2 通道接口（`internal/notify/channel.go`）

```go
// Channel 是一种通讯工具。服务器为每种启用的通讯工具建一个实例，可以同时启用多种。
type Channel interface {
	Provider() string                                                      // telegram、dingtalk
	Format() Formatter                                                     // 本通道的文本格式与 @ 写法
	Send(ctx context.Context, chat string, m Rendered) error               // 问题单通知、加入通知
	SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (msgRef string, err error)
	FinishApproval(ctx context.Context, chat, msgRef string, m Rendered) error // 改成结果、去掉按钮
	Run(ctx context.Context, in Inbound)                                   // 收件循环（长轮询 / Stream），断线自动重连
	Status() ChannelStatus                                                 // 是否在线、最近错误，供服务器管理页显示
}

type Formatter interface {
	Esc(s string) string
	Bold(s string) string
	Code(s string) string
	Link(text, url string) string
	Mention(p Person) string // 有通讯账号时按编号 @，否则只写名字
}
type Person struct{ Name, Display string; ID *ChatIdentity } // ID 为空表示没连接这个通讯工具
type Rendered struct {
	Title   string   // 钉钉卡片标题、会话列表摘要；Telegram 不用
	Text    string
	Mention []ChatIdentity // 正文里 @ 到的人（钉钉要单独传 atUserIds）
}
type Approval struct{ Kind string; WorkspaceID, RefID int64 } // su 注册与加入、da 设备

// Inbound 由与平台无关的 Hub 实现，各通道把收到的事件翻译成这两种调用。
type Inbound interface {
	Command(ctx context.Context, c Command) (reply Rendered, ok bool)
	Click(ctx context.Context, c Click) ClickResult // 钉钉要求 2 秒内返回
}
type Command struct {
	Provider, Tenant, ChatID, ChatTitle string
	Private bool
	From    ChatUser
	Name    string   // chatid、whoami、bind（绑定群）、link（账号绑定码）、start（Telegram 带参数链接）
	Args    []string
}
type Click struct {
	Provider, Tenant, ChatID, MsgRef string
	From    ChatUser
	Kind    string; WorkspaceID, RefID int64 // Telegram 来自按钮数据；钉钉由 MsgRef 在 bot_posts 里反查
	Approve bool
}
type ClickResult struct{ Toast string; Result *Rendered } // Result 不为空时，通道直接用它更新被点的那条消息
type ChatUser struct{ Provider, Tenant, UserID, Handle, Name string }
```

- **`Hub`**（由现在的 `Bot` 拆出与平台无关的部分）实现 `Inbound` 和 `httpapi.Announcer`：
  - 发审批：按工作空间找到绑定的群，交给对应的 `Channel`；
  - 记 `bot_posts`；
  - 处理点击：审批对象和工作空间来自 `bot_posts`，按钮数据只作核对，点击者按 `chat_identities` 找到账号；
  - 回写其余群。
- **`Telegram`** 实现 `Channel`：
  - 现有客户端方法不动；
  - 长轮询循环移到 `telegramChannel.Run`；
  - 把 `/start ln_…`、`/chatid`、`/whoami` 翻译成 `Command`；
  - `answerCallbackQuery` 用 `ClickResult.Toast`；
  - `Format().Mention`：有编号时写 `<a href="tg://user?id=编号">@用户名或名字</a>`，只有用户名时写 `@用户名`，都没有时只写名字。
- **`Render`** 改为 `Render(f Formatter, base string, ws, is, n, people map[int64]Person) Rendered`。Telegram 渲染出的文字与现在逐字一致（@ 写法除外），用现有的 `render_test` 断言。
- **`Dispatcher`**：
  - `Senders map[string]Sender` 改为 `Channels map[string]Channel`；
  - 投递时按工作空间的群找通道；
  - 错误分类沿用四类，钉钉的错误在钉钉客户端里映射成这四类（见 2.6）。

### 1.3 业务层、接口与网页（只做 Telegram，形状为多通道准备）

| 地方 | 改为 |
|---|---|
| `core.StartLink(provider)` | Telegram 照旧；`dingtalk` 在第 2 节实现 |
| `core.CompleteTelegramLink`、`UnlinkTelegram` | 改用 `chat_identities`；`Unlink(act, accountID, provider)` |
| `core.SetWorkspaceSettings` | 群绑定改写 `workspace_chats`；第一期一个工作空间只允许一个群，绑另一种通讯工具的群要先解绑 |
| `/api/v1/me` | 保留 `telegram: {username, linked}`（兼容），新增 `chat: [{provider, linked, handle, display_name}]`，以及服务器启用了哪些通讯工具 |
| `DELETE /api/v1/me/links/telegram` | 保留；新增 `DELETE /api/v1/me/links/{provider}` |
| `DELETE /api/v1/server/accounts/{name}/telegram` | 保留；新增 `…/links/{provider}` |
| `GET/POST /api/v1/w/{ws}/admin/workspace` | 保留 `telegram_chat`；新增 `chat: {provider, chat_id, title}` |
| `GET /api/v1/server/channels` | 新增：启用的通讯工具及状态（在线、最近错误、Client ID 尾号），只给服务器管理员 |
| 命令行 | `account set --telegram/--telegram-id` 改写 `chat_identities`；`workspace notify --telegram-chat` 改写 `workspace_chats`；`workspace list` 显示通讯工具与群名 |
| 网页 | 个人中心“通讯工具”一栏，按服务器启用的通讯工具列出（第 0 步只有 Telegram，界面不变）；管理 → 工作空间的群设置与服务器管理的账号列表改读新字段 |

## 2. 钉钉实现

### 2.1 配置

| 环境变量 | 必填 | 说明 |
|---|---|---|
| `COHIVE_DINGTALK_CLIENT_ID` | 是 | 应用的 Client ID（原 AppKey） |
| `COHIVE_DINGTALK_CLIENT_SECRET` | 是 | 应用的 Client Secret（原 AppSecret） |
| `COHIVE_DINGTALK_ROBOT_CODE` | 否 | 机器人编码；企业内部应用一般等于 Client ID，留空即用 Client ID |
| `COHIVE_DINGTALK_NOTICE_TEMPLATE` | 否 | 通知卡片模板编号；不填时通知降级为不带 @ 的 Markdown |
| `COHIVE_DINGTALK_APPROVAL_TEMPLATE` | 否 | 审批卡片模板编号；不填时审批消息不带按钮 |
| `COHIVE_DINGTALK_API_BASE` | 否 | 接口基础地址，默认 `https://api.dingtalk.com`；测试和以后的国际版用 |

- 单机运行：
  - `scripts/set-dingtalk-credentials.sh`：交互输入，密钥不回显，写 `~/.config/cohive/dingtalk.env`，权限 600；
  - `scripts/cohive-server.sh` 像处理 `telegram.env` 一样，只在服务进程里加载 `dingtalk.env`（可用 `COHIVE_DINGTALK_ENV` 指定别的文件），不打印、不进 `server.env`；
  - `scripts/run-server.sh` 同步加载；`server.env.example` 注明密钥不要放在这里。
- Docker：`deploy/docker-compose.yml` 透传这六个变量，`deploy/.env.example` 加注释，`docs/guides/deploy-docker.md` 的变量表补上。
- 启动日志：“钉钉：已启用（Client ID 尾号 …1a2b），Stream 连接中”；连上后打印“已连接”。
- Client ID 和 Secret 只配了一个时，服务拒绝启动，并说明缺哪个。

### 2.2 开放接口客户端（`internal/notify/dingtalk.go`）

自己写一个轻量 HTTP 客户端，风格同 `telegram.go`，**不引入**阿里云那套接口依赖包：

| 方法 | 接口 | 说明 |
|---|---|---|
| `token` | `POST /v1.0/oauth2/accessToken` | 有效期 2 小时，缓存，到期前 5 分钟刷新；并发只取一次 |
| `deliverCard` | `POST /v1.0/card/instances/createAndDeliver` | `openSpaceId = dtv1.card//IM_GROUP.<cid>`；`imGroupOpenDeliverModel{robotCode, atUserIds}`；`imGroupOpenSpaceModel{supportForward:false, notification{alertContent}}`；`callbackType=STREAM` |
| `updateCard` | `PUT /v1.0/card/instances` | 按 `outTrackId` 改 `cardParamMap`，`updateCardDataByKey=true` |
| `groupSend` | `POST /v1.0/robot/groupMessages/send` | 降级用：`sampleMarkdown`，不能 @ |
| `reply` | `sessionWebhook` | 回复命令；先校验主机名 |

- **卡片编号（`outTrackId`）**：
  - 问题单通知用 `cohive-n-<通知编号>`：服务恰好在发出后、记录前崩溃时，重发用同一个编号，**可能**不会出现第二张卡（未核实，列入实测清单）；
  - 审批用 `cohive-<种类>-<编号>-<工作空间>-<6 位随机>`，记进 `bot_posts`。

### 2.3 Stream 收件（`internal/notify/dingtalk_stream.go`）

- **用官方 Stream 依赖包** `github.com/open-dingtalk/dingtalk-stream-sdk-go`（MIT 许可证，见待拍板 1）：
  - `RegisterChatBotCallbackRouter` 收机器人消息（群里 @ 机器人、单聊）；
  - `RegisterCardCallbackRouter` 收卡片回调；
  - `WithOpenApiHost` 指向配置的基础地址，开启自动重连；
  - 依赖包自带日志：替换成我们的日志函数，过滤掉带 `ticket=` 的连接地址。
- **翻译规则**，与依赖包类型解耦，便于单测：
  - 群消息（`conversationType = "2"`）：去掉 @ 机器人的部分，识别 `群编号`、`我是谁`、`绑定群 <码>`，其余忽略；
  - 单聊（`"1"`）：识别 `绑定 <码>` 或直接发来的码、`我是谁`，其余回复用法说明；
  - 卡片回调：`outTrackId` → `MsgRef`，`params.decision` → `Approve`，`userId` / `corpId` → `From`。
- **连接状态**：断开超过 5 分钟，或连接时报 20001（消息服务已暂停，通常是额度用完），就把 `Status().Online` 置为假，并记下原因。审批据此降级，见 2.6。

### 2.4 群消息与 @

- **问题单通知、加入通知**：
  - 用通知卡片模板，变量 `title`、`content`（Markdown），`atUserIds` 填已连接钉钉的被 @ 成员；
  - 正文写成“@昵称（成员名）”，没连接的人只写名字；
  - 正文内容与 Telegram 相同，即只发信号，不发正文、证据、日志。
- **没配通知模板**：用 `groupSend` 的 `sampleMarkdown`，正文相同，但只显示名字、不 @。服务器管理页提示“未配通知卡片模板，群通知不会 @ 到人”。

### 2.5 群里审批按钮

- **审批卡片模板变量**：
  - `content`：申请内容（Markdown）；
  - `status`：结果文字，例如“已批准（审批人 lee）”；
  - `pending`：`"true"` 或 `"false"`，用来控制按钮显示。能否按变量隐藏按钮未核实；做不到时按钮保留，再点会得到“已处理”提示。
- **按钮**：两个按钮，回传参数分别是 `{"decision":"approve"}` 和 `{"decision":"reject"}`。
- **点击处理**（`Hub.Click`），整体限时 1.8 秒：
  1. 按 `MsgRef` 查 `bot_posts`，得到种类、申请编号、工作空间；查不到就拒绝。
  2. 卡片所在的群必须是该工作空间绑定的群。
  3. 按 `(dingtalk, corpId, userId)` 查 `chat_identities` 找到账号；账号必须存在且未停用。
  4. 调 `DecideSignup` 或 `DecideDeviceAuth`。业务层负责权限校验、并发控制和被拒记录。
  5. 返回结果卡片数据（`pending=false`，`status=结果`），由依赖包作为回调响应写回被点的那张卡。
  6. 异步对同一申请的其他 `bot_posts` 调 `updateCard`。
- **超时**：超过 1.8 秒先返回提示“处理中，稍后结果会更新到卡片上”，事务照常完成，之后异步改卡。
- **网页上处理后**：沿用 `AnnounceSignupDecided` / `AnnounceDeviceDecided`，对全部 `bot_posts` 调 `updateCard`。

### 2.6 降级与错误映射

| 情况 | 映射 / 行为 |
|---|---|
| 取令牌返回凭据无效 | 通道整体不可用：通知保留，不计失败次数，服务器管理页显示“钉钉应用凭据无效” |
| `Forbidden.AccessDenied.QpsLimitForAppkeyAndApi` | 限流，等 2 秒 |
| `Forbidden.AccessDenied.ApiCountLimitForOrg` | 通道整体不可用，原因“钉钉接口月度额度已用完”；每 10 分钟重试一次 |
| 机器人不在群里、群编号无效 | 不值得重试，原因说清“请把机器人重新加进群，或在网页上重新绑定群” |
| 没配审批模板，或 Stream 不在线 | 审批消息改发文字：“请到网页 管理 → 待审 处理”，设了对外地址时附链接；不记 `bot_posts` 的按钮 |
| 卡片已发出后 Stream 掉线 | 群里点按钮没反应（钉钉客户端会提示失败）；网页照常可审批，审批后照常改卡（改卡走接口，不依赖 Stream） |

### 2.7 拿群编号与绑群

- **主流程**：
  1. 工作空间管理员在网页“管理 → 工作空间 → 群通知”选“钉钉”，点“生成绑定码”。码 8 位，10 分钟有效，一次性。
  2. 在钉钉群里 @机器人 发 `绑定群 码`。
  3. 回调里带群编号 `conversationId` 和群名 `conversationTitle`，服务端写 `workspace_chats`，记 `bound_by = 生成码的人`，并在群里回复“已绑定到工作空间 X”。
  4. 网页轮询，自动显示“已绑定：群名”。
- **兜底**：在群里 @机器人 发 `群编号`，机器人回复 `cid…`；管理员可以在网页或命令行 `workspace notify --dingtalk-chat` 手工填。
- **约束**：一个群已绑别的工作空间时拒绝，提示是哪个工作空间。写管理操作记录 `workspace.chat_bind`，途径 `dingtalk`。

### 2.8 账号绑定（单聊发一次性码 + 网页确认）

1. 个人中心点“连接钉钉”。网页显示码（8 位，10 分钟有效）和说明：在钉钉里打开与机器人“Cohive”的单聊（群里点机器人头像 → 发消息，或搜索机器人名），发送这个码。
2. 机器人在单聊里收到码，把请求改为 `awaiting_confirm`，记下 `senderCorpId`、`senderStaffId`、`senderNick`，回复“已收到，请回到 Cohive 网页确认”。
3. 网页显示“将连接到钉钉用户 张三，确认？”。本人点确认后才写 `chat_identities`，并写记录 `account.dingtalk_link`；点取消则作废。
4. 拒绝的情况：
   - 这个钉钉用户已连接别的 Cohive 账号；
   - 码过期、已用过；
   - 码出现在群里：立即作废，在群里回复“绑定码要私聊发给机器人，这个码已作废，请回网页重新生成”，回复中不复述码。
5. 解除：本人或服务器管理员，写记录 `account.dingtalk_unlink`。

### 2.9 网页与命令行（钉钉部分）

- **个人中心**：“连接钉钉”卡片，依次是码与说明 → 等待中 → 待确认（显示钉钉昵称）→ 已连接，可以解除。
- **管理 → 工作空间**：群通知区域按服务器启用的通讯工具给出选项；钉钉选项包括生成绑定码、显示已绑定的群名、解绑。
- **服务器管理**：通讯工具状态一栏（在线 / 离线原因、Client ID 尾号、模板是否配置）；账号列表显示钉钉连接状态。
- **命令行**：
  - `workspace notify --dingtalk-chat <cid>`；
  - `account unlink --name x --provider dingtalk`；
  - `notify test --workspace x` 对任一通道可用。

## 3. 部署者文档

写成独立的 `docs/guides/dingtalk.md`，**草稿随本计划一起提交**。这样朋友现在就能做钉钉侧的准备：建应用、开权限、开 Stream、建两个卡片模板，这些不依赖 Cohive 代码。T7 再按实现定稿。草稿结构：

1. 准备：组织管理员或开发者权限；Cohive 跑在哪（见待拍板 2）；一个应用只能接一台 Cohive。
2. 建企业内部应用，记下 Client ID 和 Client Secret。
3. 加机器人能力，消息接收模式选 Stream。
4. 开权限（只要两项）：企业内机器人发送消息、互动卡片实例写。
5. 卡片平台建两个模板（通知、审批），写明组件、变量名、按钮回传参数；发布，记下模板编号。导出模板 JSON 发回给我们，以后附在仓库里供别人导入。
6. 发布应用，可见范围选参与试用的人。
7. 把凭据和模板编号配进 Cohive，启动并确认 Stream 已连接。
8. 建内部群，把机器人加进群。
9. 在 Cohive 网页生成群绑定码，在群里 @机器人 完成绑定。
10. 每位成员连接钉钉。
11. 验收清单：通知 @ 到人、群里批准与拒绝、网页审批后卡片同步、绑定码发到群里会作废。
12. 请实测并反馈清单（调研里未核实的项）。
13. 常见问题。

## 4. 任务

每项完成即 commit + push `feature/im-dingtalk`。

### 第 0 步

#### T1：迁移与存储
- [x] 迁移测试 `TestMigrate0011`：在 0010 结构上造数据，迁移后逐项断言 `chat_identities`、`workspace_chats`、`bot_posts`。造的数据：
  - 账号：有编号和用户名 / 只有用户名 / 只有编号 / 都没有；
  - 工作空间：已绑群 / 已关闭；
  - `bot_posts` 两条。
- [x] 存储层：两张新表的增删查；`AccountByChatIdentity(provider, tenant, userID)`；`WorkspaceByChat(provider, tenant, chatID)`；成员列表带上各通讯工具的连接信息（替换 `TelegramUsername/TelegramUserID` 字段）。
- [x] commit：`feat(im-T1): 通讯账号表与工作空间群表，Telegram 数据无损迁移`

#### T2：通道接口与 Telegram 适配
- [x] 按 1.2 拆出 `Channel`、`Formatter`、`Hub`；`Dispatcher` 改用 `Channels`。
- [x] 现有 `internal/notify` 用例全部照常通过，只做适配接口的必要改动。
- [x] 新用例：
  - 按编号 @ 的写法与转义（显示名带 `<&"`）；
  - 只有用户名的老数据仍按 `@用户名` 提及；
  - 两种通道同时启用时，按工作空间的群分发到对应通道（用假通道）；
  - 点击时审批对象以 `bot_posts` 为准，按钮数据与之不符时拒绝。
- [x] commit：`refactor(im-T2): 通知通道抽象，Telegram 改为按用户编号 @`

#### T3：业务层、接口、网页、命令行（多通道形状）
- [x] 用例：
  - 第一期“一个工作空间只允许一个群”；
  - 一个群不能绑两个工作空间；
  - 解除连接与记录；
  - `/api/v1/me` 同时有新旧字段；
  - `GET /api/v1/server/channels` 的权限；
  - 命令行表驱动更新。
- [x] vitest：个人中心与工作空间设置页在只有 Telegram 时表现不变。
- [x] 全量验证，**回报主脑：第 0 步完成**。
- [x] commit：`feat(im-T3): 接口与网页改为多通讯工具形状（行为不变）`

### 钉钉

**第 0 步实施记录（2026-10-18，T1 `f2c37b8`、T2 `2263f71`、T3 `ec5608a`）：** 与上文不同的地方：
- Telegram 按用户编号 @ 在本分支开始前已由 main 做完（`tgMention`），本步只把它收进 `TelegramFormat`。
- 账号、成员、工作空间结构体上的 `TelegramUsername/TelegramUserID`、`Notify.Channel/Target` 保留，改为从新表读（取第一个群），调用方基本不动；新代码用 `ChatIdentitiesOf`、`WorkspaceChats`。
- `Dispatcher` 字段仍叫 `Senders`（只要 `Format` 与 `Send`）；`Bot` 作为 Telegram 通道实现 `Channel`，没配 `Hub` 时自建一个只含自己的，原有用例几乎不用改。
- 按消息编号反查审批记录要带群编号：Telegram 的消息编号只在群内唯一；钉钉卡片编号全局唯一，传空群编号，核对工作空间时用记录里的群。
- 已绑钉钉群的工作空间，在管理页保存 Telegram 群编号为空时不解除钉钉群；填了 Telegram 群编号则要求先解绑。
- 网页这一步只补类型，界面不变；通讯工具状态面板随钉钉部分做。
- 变异 7 项全部变红：迁移丢只有用户名的连接、迁移把关闭群通知的也迁过去、点击不核对按钮数据、回写时只按消息编号跳过、Mentions 带上未连接的人、改开关解除钉钉群、按 Telegram 编号找账号不限通讯工具。

#### T4：钉钉客户端与配置
- [x] 假钉钉服务器（`httptest`）：取令牌、投放卡片、更新卡片、群消息、`sessionWebhook`。
- [x] 用例：
  - 令牌缓存与并发只取一次；
  - 过期前刷新；
  - 请求头带令牌，地址里不带；
  - 错误按 2.6 映射（表驱动）；
  - `sessionWebhook` 主机名校验；
  - 配置只给一半时启动报错；
  - 日志和错误里不出现 Secret 与令牌（断言输出里不含假密钥字符串）。
- [x] `scripts/set-dingtalk-credentials.sh`，`cohive-server.sh` 与 `run-server.sh` 加载，Docker 编排透传；`scripts_test` 补用例。
- [x] commit：`feat(im-T4): 钉钉开放接口客户端与凭据配置`

#### T5：Stream 收件、绑群、账号绑定
- [x] 翻译层单测：喂依赖包的消息结构，断言产出的 `Command` / `Click`，包括 @ 机器人前缀、全角空格、码大小写。
- [x] 端到端单测：假 Stream 网关，包括 `connections/open` 和本地 WebSocket，推一帧机器人消息、一帧卡片回调，断言我们的应答帧。确认依赖包在 `WithOpenApiHost` 下完全不连外网。
- [x] 业务用例（真实 Postgres）：
  - 绑群：码过期、码复用、群已绑别处、成功后网页状态；
  - 账号绑定：单聊发码 → 待确认 → 确认才生效；取消；码出现在群里立即作废；钉钉用户已连别人；停用账号。
- [x] 网页：个人中心“连接钉钉”、工作空间“生成绑定码”（vitest）。
- [x] commit：`feat(im-T5): 钉钉 Stream 收件，群里绑定工作空间，单聊绑定码加网页确认`

#### T6：通知卡片、审批按钮、降级
- [x] 用例：
  - 通知卡片的 `atUserIds` 只含已连接的人；
  - Markdown 转义；
  - 没配模板时降级为 `groupSend`；
  - 审批点击的全部拒绝路径（审查重点 5）；
  - 两人同时点；
  - 超过 1.8 秒先回“处理中”、之后改卡；
  - 网页审批后改所有卡；
  - 额度用尽时暂停、不计失败；
  - Stream 离线时审批消息不带按钮。
- [x] commit：`feat(im-T6): 钉钉群通知卡片与 @，群里审批按钮，额度与离线降级`

#### T7：部署者文档定稿与走查
- [x] `docs/guides/dingtalk.md` 按实现定稿；`group-notify.md`、`onboarding.md`、`test-server.md` 补钉钉入口。
- [x] 本机走查（8792 端口，库 `cohive_dingtalk_try`，用假钉钉服务）：绑群、连接、发单、审批全流程，截图存证。
- [x] commit：`docs(im-T7): 钉钉部署指南定稿`

#### T8：变异与回报
- [x] 变异：
  - M1 点击时信按钮数据不查 `bot_posts`；
  - M2 不核对卡片所在群；
  - M3 账号绑定不等网页确认就生效；
  - M4 群里出现的码不作废；
  - M5 `atUserIds` 带上未连接的人或漏掉已连接的人；
  - M6 额度用尽计入失败次数；
  - M7 `sessionWebhook` 不校验主机名；
  - M8 迁移丢掉只有用户名的连接；
  - M9 Telegram 按编号 @ 不转义；
  - M10 回调超时不先返回。
- [x] 全量验证，回报主脑（阶段完成，请检查分支）。

**钉钉部分实施记录（2026-10-18，T4/T5 `29621a9`、`0133fb4`，网页 `a935fe4`，文档 `83062bc`，变异补用例见最后一个提交）：** 与上文不同的地方：
- **待拍板 1 改选 B：Stream 协议自己实现**（约 250 行，只依赖 `gorilla/websocket`，BSD-2 许可证）。原因：官方 SDK v0.9.1 在断线时，读消息的协程会向已关闭的通道发送；这个协程没有异常保护，会让整个服务进程崩溃。另外它的重连循环不随 context 结束，日志器是全局的，连接成功的日志里还带一次性票据。修好这些问题的 0.9.2 还是测试版。协议与 SDK 一致，用假网关做了端到端用例（换票据、ping、机器人消息、卡片回调、服务端要求重连）。
- **审批卡片模板加一个私有变量 `tip`**：给点击者看的提示，例如没有权限、处理中、已批准。钉钉卡片回调的响应里没有独立的提示字段，只能这样做。部署指南的模板说明已同步。
- **工作空间群表一律不记企业**：钉钉 openConversationId 全局唯一。通讯账号表照样记 corpId，同一个 userId 在别的企业不会串号。所以 `Click` 去掉了 Tenant 字段，点击者的企业在 `From.Tenant` 里。
- **`Formatter` 加了 `Link`，`Rendered` 加了 `Key`**：`Key` 是幂等键，钉钉卡片编号用 `cohive-n<通知编号>`，重发不会出现第二张卡，是否生效列入实测清单。用户输入里的 Markdown 记号换成全角字符，因为钉钉对反斜杠转义的支持没有核实。
- **群绑定**：网页群绑定码和命令行 `workspace notify --dingtalk-chat` 走同一条业务路径，都要求“一个群只绑一个工作空间、第一期一个工作空间只绑一个群”。`workspace notify --off` 会一并解绑钉钉群。
- **走查**：本机 8792 端口、库 `cohive_dingtalk_try`，钉钉侧用假服务（开放接口加 Stream 网关）。走通了以下流程：生成群绑定码并在群里绑定；单聊发码后在网页确认；账号绑定码误发到群里作废，回复不复述码；团队注册后群里出现审批卡片，点批准后在回调响应里改成结果并发加入通知；陌生人点按钮只看到提示；网页拒绝后卡片同步改成结果；管理操作记录正确；服务器日志里没有密钥、访问令牌或票据。在浏览器里看过个人中心、工作空间设置、服务器设置三个页面。问题单通知加 @ 的路径没有走查，由真实数据库用例覆盖。走查库合入后再删。
- **变异**：钉钉部分 16 项全部变红，依次是：
  - 点击时不查审批消息记录；
  - 不核对按钮所在群；
  - 不等网页确认就连接；
  - 群里出现的码不作废；
  - 卡片不带 atUserIds；
  - 额度用完当成不值得重试；
  - 会话 Webhook 不校验主机名；
  - 名字不转义；
  - 回调超时不先返回；
  - 钉钉格式不转义；
  - 别人能确认我的连接；
  - ping 不原样应答；
  - 群绑定码不看过期；
  - 账号绑定码不看过期；
  - Stream 不在线时照样发按钮；
  - 点击者身份不取平台字段。

  其中 4 项（会话 Webhook 主机名、两种码的过期、Stream 不在线）第一次没有变红，补了用例后变红。第 0 步的 17 项重跑仍全部变红。

## 5. 工作量

第 0 步约 2–3 人日；钉钉 T4–T7 约 4–5 人日；真钉钉实测由朋友做，时间不在内。

## 6. 待拍板

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| 1 | **Stream 长连接用不用官方依赖包** | A 用 `open-dingtalk/dingtalk-stream-sdk-go`（MIT，带进 `gorilla/websocket`、`google/uuid` 两个小依赖）；B 自己实现协议（还是要引一个 WebSocket 库） | **A**。协议细节（心跳、断线、应答帧）官方维护，代码量小；它还停在 0.x 版本，锁定版本号，写进依赖说明 |
| 2 | **朋友的这一轮实测，Cohive 跑在哪**（2026-10-18 主脑：由 lee 定） | A 朋友自己按 `docs/guides/deploy-docker.md` 用 Docker 部署一套，钉钉凭据写进 `deploy/.env`；B 钉钉应用凭据配到 lee 的测试服，朋友的钉钉群绑到测试服上的一个工作空间，问题直接报到同一台服务器上 lee 新建群对应的工作空间 | **B 先行**。朋友只需做钉钉侧准备，零部署；同一台服务器同时开 Telegram 和钉钉，本次设计支持。代价：朋友要把 Client Secret 交给 lee，要走私密渠道；测试服由 lee 或主脑配置，本会话不碰。A 作为第二轮，同时验证 Docker 部署文档 |
| 3 | **卡片模板编号放哪** | A 环境变量（与凭据同一个文件）；B 网页“服务器管理 → 设置” | **A**。第一期改动最小；模板编号不是秘密，以后要网页改再挪 |
| 4 | **没建模板时允不允许启用钉钉** | A 允许：通知不 @，审批只发文字加网页链接；B 必须两个模板都配好才启用 | **A**。朋友可以先只配凭据，看到最基本的通知，再逐步加模板，排查问题更容易 |
| 5 | **Telegram 也改成“绑定码绑群”吗** | A 这次不改，保留 `/chatid` 手填；B 一起改 | **A**。不在决策范围内，Telegram 现在能用；以后想统一再做 |
| 6 | **账号绑定码出现在群里怎么办** | A 立即作废并在群里提醒；B 忽略 | **A**。码可能被别人抢先使用；作废的代价只是本人重新生成一次 |
| 7 | **`accounts.telegram_*`、`workspaces.notify_*` 删列还是保留** | A 本迁移删；B 保留一个版本 | **A**。与 M6-d 删 `public_url` 一致；回退本来就要用迁移前的备份，留着反而容易被误读 |

**主脑批复（2026-10-18）**：1、3、4、5、6、7 按建议；2 由 lee 定，不影响第 0 步。先做第 0 步（T1–T3），完成后回报再进钉钉部分。
