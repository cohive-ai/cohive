# DM-1 交回：关注者与留言 @ 改为机器人私聊

- 分支：`feature/dm-1-direct-notify`（从 origin/main `e194b1e` 开），已推送，未合入 main，未动测试服。
- 提交：私聊接口骨架、`a73008d`（实现、测试、部署指南）、本交回（含一处测试补强：不重试错误后会话提醒仍在）。
- 分工：Opus 编排设计与逐个审核；三个 Sonnet 子代理分别做三个通道的私聊、核心分流与投递、部署指南（文件互不重叠）。审核中主脑自己改了三处，见第 3 节。

## 1. 动作 × 收件人 × 群 / 私聊 / 会话提醒

通用规则：
- 操作者本人永远不 @、不私聊、不写会话提醒（N-3）。
- 一人一事件只收一条，优先级：原收件人 > 被 @ > 关注者。原收件人只在群里 @，不私聊；关注者、被 @ 的人只私聊，群消息里不出现。
- 会话提醒（member_alerts）在状态变更的同一事务里写好，与有没有绑群、私聊成败都无关。
- 私聊与有没有绑群无关；没连接通讯账号（或账号只有用户名没有编号）的人投递时跳过，只剩会话提醒。

| 动作 | 原收件人：群里 @（绑群时）+ 会话提醒 kind=事件名 | 关注者：私聊 + 提醒 kind=watching | 留言 @：私聊 + 提醒 kind=mentioned |
|---|---|---|---|
| 发出（含发单抄送） | 负责人 | 全部关注者（含刚抄送的） | — |
| 接单 / 退回 / 开始修复 / 请验证 | 发起人 | 是 | — |
| 转交 | 新负责人（新单）；发起人（已转交） | 是，事件按“新单” | — |
| 关单 / 重开 | 负责人；重开满 3 次另有“需要人介入”给双方 | 是 | — |
| 作废 | 发起人作废：负责人；管理员作废：双方 | 是 | — |
| 留言 | 发起人、负责人（去掉操作者） | 是 | 被 @ 的人，只就这一条 |
| 草稿上留言 | 不通知 | 不通知 | 不解析 |
| 改派（移出成员时） | 新接手人“新单 / 请验证”；其余相关人“已改派”（被换下的人只在群里 @，不写提醒） | 是，事件按第一条 | — |
| 催办第 1 档 | 当事人 | 不带 | — |
| 催办第 2 档 | 当事人 + 工作空间管理员 | 是（系统事件，原因行不写操作者） | — |

没有任何原收件人、只有关注者或被 @ 的人时：不写群通知行，只有私聊与提醒（原来是单独一条“#N 事件，由某某操作；关注（仅告知）…”的群消息，已删）。

### 群消息变化
首行去掉“；留言提到 …”“；关注（仅告知）…”两段（中英文案键 `notify.cc.*`、`notify.head.watchers_only` 已删）。其余不变。

### 私聊样例（Telegram，中文；钉钉为 Markdown，Slack 为 mrkdwn，内容相同）
```
你关注的问题单 #12 有新动态：已接单（bob 操作）
服务 order-api，发起人 alice，负责人 bob
标题：下单接口返回 500            ← 照工作空间“显示标题”开关
简述：空购物车下单返回 500         ← 照“显示简述”开关
在编程工具里用 /cohive 12（Codex 用 $cohive 12）查看
https://cohive.example.com/w/payments/issues/12
```
被 @：首行为 `bob 在问题单 #12 的留言里提到了你`；催办第 2 档：`你关注的问题单 #12 有新动态：催办（已请管理员协调）`。
英文：`Update on issue #12 you're watching: Accepted (by bob)`、`bob mentioned you in a comment on issue #12`、`Reporter alice, owner bob`。
私聊里谁都不 @（只写名字），留言正文、作废与退回理由不发；动作行一律“查看”（关注者不用动手）。语言用收件人账号语言，没设时用服务器缺省语言。

## 2. 实现要点

- **接口**：`notify.DirectSender{ SendDirect(ctx, to store.ChatIdentity, m Rendered) error }`，三个通道都实现。
  - Telegram：`sendMessage`，`chat_id` = 用户编号。屏蔽机器人 403 → 不重试。
  - 钉钉：`POST /v1.0/robot/oToMessages/batchSend`（`msgKey=sampleMarkdown`）。响应 `invalidStaffIdList` 含此人 → 不重试；`flowControlledStaffIdList` → 60 秒后再试。
  - Slack：`conversations.open`（需 `im:write`）拿私聊频道（按用户缓存）再 `chat.postMessage`。`missing_scope` 归为“不重试”并提示补 im:write；`user_not_found`、`user_disabled`、`cannot_dm_bot` 等同样不重试。
- **存储**：迁移 `0017_direct_notifications.sql` 给 `notifications` 加可空列 `direct_member_id`（外键 members）。私聊一人一行，`recipients` 为空，原因记在 `watchers=[此人]` 或 `mentions=[此人]`。
  测试服是 postgres:17，加可空无默认值的列只改目录不重写表，**可以直接在测试服库上执行**；旧通知 `direct_member_id` 为空，照旧当群通知投递（旧行里的关注者名单不再渲染，只少了首行末尾那段）。
- **投递**（`Dispatcher.deliverDirect`）：归档、过期（24 小时）、过时（单子已往前走）一律跳过，与群通知同；收件人已移出或账号停用跳过。通道选择：先用工作空间绑定的通讯工具，此人在那边没连接再依次试 Telegram、钉钉、Slack。
- **失败策略（防重试风暴）**：限流按平台给的秒数延后、不计次；通道整体不可用（令牌失效、钉钉缺权限或额度用尽）**计入次数**；不值得重试的错误直接放弃；其余最多 3 次（间隔 10 秒、1 分钟）后放弃。私聊不返回“暂停本轮”，不卡别的群通知。
  私聊失败不进 `cohive-server notify requeue` 的重排，也不计入 `workspace list` 的待发 / 失败条数（这两处只算群通知）。日志英文，不含消息正文。
- **会话提醒**：写法完全没变，仍在原事务里写，私聊成败与它无关。

## 3. 主脑替你定的几处（需要你确认或改口）

1. **私聊不重复群消息首行**：群消息首行写的是“谁该动手”（如“新单交给 @bob，请接单、退回或转交”），对关注者来说是噪音，还会出现“告知发起人 ”这种半句。私聊改成：原因行（哪张单、什么事、谁做的）+ 一行“发起人 X，负责人 Y” + 标题简述 + 查看指引 + 链接。
2. **通道整体不可用时私聊也计次数**（与群通知不同）。群通知一条一事件，延后等恢复值得；私聊一人一条，若照群通知每分钟延后不计次，钉钉缺单聊权限或额度用尽时，积压的私聊会每分钟重试一遍直到 24 小时过期。代价：令牌短暂失效超过约 1 分钟，期间的私聊会丢（会话提醒仍在）。
3. **一个人连了多种通讯工具**：只发一处，优先工作空间绑定的那种；没绑群的工作空间按 Telegram → 钉钉 → Slack 的顺序取第一个。没有让成员自己选私聊渠道。
4. **私聊行在投递时才判断有没有连接**：没连接的人也写一行，投递时标“跳过（收件人没有连接可私聊的通讯账号）”。好处是事件发生后才连接的人，在 24 小时内的待发私聊也能收到；代价是表里多些跳过行。

## 4. 验证

- 全量（自建走查库 `cohive_walk_dm1`，localhost:55432）：`go vet ./...` 通过；`go test -p 1 -count=1 ./...` 全部通过；`make build` 成功（网页 vitest 25 个文件 184 例全过）。
- 三个平台各有本地假服务用例（`internal/notify/direct_channel_test.go`）：成功路径校验请求参数，屏蔽 / 无效用户 / 缺权限 / 流控的错误分类。
- 投递用例（`internal/notify/direct_dispatch_test.go`，自写同时实现群发与私聊的假通道）：发到正确连接、收件人语言、缺省语言、没绑群照发、没连接跳过且提醒仍在、只有用户名跳过、通道不支持私聊跳过、已移出跳过、不重试错误只试一次且提醒仍在、普通错误 3 次放弃、限流不计次、通道不可用计次与 3 次放弃、优先工作空间通讯工具、被 @ 原因。
- 核心用例（`internal/core/watch_test.go`）：每个动作的群行不带关注者、私聊行的收件人与原因、操作者与原收件人没有私聊行、既关注又被 @ 只一条、没绑群也有私聊行、催办第 2 档。

变异验证 15 个探针（在临时副本里逐个改代码、跑对应包测试，库 `cohive_walk_dm1_probe`），全部被抓住：

| 探针 | 抓住它的用例 |
|---|---|
| P1 关注者 / 被 @ 的人仍在群通知里（仍被 @） | TestCCOnSubmit、TestWatchersAlongTheLoop、TestCommentMentions 等 6 条 |
| P2 关注者没收到私聊 | TestCCOnSubmit、TestWatchersOnlyNotice 等 5 条 |
| P3 原收件人也收到私聊（重复） | TestNotificationsAlongTheLoop、TestEscalationNotifiesBoth 等 6 条 |
| P4 操作者本人收到私聊 | TestNoticesSelfIssue 等 6 条 |
| P5 私聊用错语言（用了工作空间语言） | TestDirectUsesRecipientLanguage |
| P6 私聊失败后撤掉会话提醒 | TestDirectPermanentErrorFails |
| P7 没绑群时不发私聊 | TestDirectWithoutGroup |
| P8 既关注又被 @ 收两条 | TestMentionedWatcherAndDraft |
| P9 通道不可用时私聊无限延后（重试风暴） | TestDirectChannelDownAndRateLimitDelay、TestDirectChannelDownGivesUp |
| P10 不优先用工作空间绑定的通讯工具 | TestDirectSentToConnection 等 6 条 |
| P11 重排失败通知时连私聊一起重发 | TestDirectNotificationRoundTripAndRequeue |
| P12 Slack 缺 im:write 归为通道不可用 | TestDirectSlackErrors |
| P13 钉钉 invalidStaffIdList 被忽略 | TestDirectDingTalkInvalidAndFlowControlled |
| P14 被 @ 的人收到“关注”原因文案 | TestDirectMentionedReason、TestRenderDirectMentionedAndEnglish |
| P15 私聊里 @ 人（Telegram 链接） | TestRenderDirectWatching 等 3 条 |

走查库 `cohive_walk_dm1`、`cohive_walk_dm1_probe` 保留在本机 55432，合入后可删。

## 5. 未做 / 后续建议

- 没有在真实 Telegram、钉钉、Slack 上收过私聊。**钉钉单聊接口的响应字段名（`invalidStaffIdList`、`flowControlledStaffIdList`）与缺单聊权限时的错误码未实测**，是按文档记忆写的；建议上测试服后用钉钉实测一次（关注一张单，让别人接单）。
- Slack 老部署要补 `im:write` 并重新安装应用，否则私聊全部失败（日志有 missing_scope 提示）。三份指南已写明。
- Slack 错误码靠匹配错误文字判断，没有改通用的 `call`；以后若改 `call` 的错误文字格式，要连带看 `OpenDM`。私聊频道缓存不失效，被关闭的私聊会一直失败到服务重启（目前没见过这种情况）。
- 成员没有“关闭私聊通知”的开关；关注者觉得吵只能取消关注。若需要，可在个人中心加“私聊通知开 / 关”。
- 新登录通知等其他私聊场景可以直接复用 `DirectSender`。
