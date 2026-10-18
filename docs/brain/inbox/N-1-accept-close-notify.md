# N-1 交回：接单、关单发群通知

- 分支：`feature/notify-accept-close`（基于 origin/main `91b2878`）
- 代码提交：`e001b45`；本交回单独一个提交，在它之后
- 未合入 main，未推送到 main，测试服没有动

## 做了什么

1. **新增两种群通知事件**（`internal/core/notify.go`）
   - `accepted`：负责人接单时写入，@ 发起人。
   - `closed`：发起人验证**通过**并关闭时写入，@ 负责人。
   - 验证不通过时只走原有的 `reopened`，不发关单。
2. **渲染**（`internal/notify/render.go`）：Telegram 和钉钉共用同一套渲染，各用各的格式。
   - 接单：“#N 已被 X 接单，告知发起人 @Y”，下一行“服务 …，由 X 用 Codex 接单”。
   - 关单：“#N 已由 X 验证通过并关闭，告知负责人 @Y”，下一行“…由 X 用 Claude Code 关闭”。
   - 指引改为“在编程工具里用 /cohive N（Codex 用 $cohive N）**查看**”：对方不需要动手。
   - 钉钉卡片标题、降级 Markdown 的标题：“#N 已接单”“#N 已关闭”（`eventTitle`）。
   - 内容规则不变：只有单号、服务、交接关系、动作、指引、链接；标题、简述按工作空间开关；问题单正文不发；接单、关单时附带的说明文字不发（渲染本来就不读说明）。
3. **投递**（`internal/notify/dispatcher.go`）：接单、关单是“告知型”事件，**不按“过时”跳过**。
   - 原有规则：投递前问题单的状态或负责人变了，就不再发。这对“轮到你做事”的通知是对的，对告知型通知不对。
   - 例如接单后马上开始修复，按原规则“已接单”会被跳过，信息链又断了。
   - 告知型通知仍受 24 小时有效期限制。
4. **会话钩子提醒**：没有改。
5. **文档**
   - `docs/plan.md` 第 5.3 节事件表：加上“已接单 → 发起人”“验证通过并关闭 → 负责人”，并说明告知型的规则。
   - `docs/guides/group-notify.md` 第 3 节同步。
   - `docs/guides/dingtalk.md` 原来没有事件列表，在开头的能力说明里补了一句，并链到群通知指南。

## 改了哪些文件

- `internal/core/notify.go`：事件常量、`InfoEvent`、入队规则
- `internal/core/notify_test.go`：完整流程的期望改为含接单、关单；新增两个用例
- `internal/notify/render.go`：两种事件的文案、“查看”指引、钉钉标题
- `internal/notify/render_test.go`：两种事件的 Telegram、钉钉渲染；不泄露正文与按编号 @ 的用例覆盖到新事件
- `internal/notify/dispatcher.go`：告知型事件不按过时跳过
- `internal/notify/dispatcher_test.go`：接单后马上开始修复，“已接单”照样发出
- `docs/plan.md`、`docs/guides/group-notify.md`、`docs/guides/dingtalk.md`

## 测试结果

- 测试口径全部通过：`go vet ./...`、`go test -count=1 ./...`（真实 Postgres `localhost:55432`）、`make build`。
- 派活要求的四类用例都有：
  - 两种事件各自入队、收件人正确：`TestNotificationsAlongTheLoop`；
  - 验证不通过不产生关单：`TestReopenIsNotClose`；
  - Telegram 渲染：`TestAcceptedAndClosedMessages`；
  - 钉钉渲染：`TestAcceptedAndClosedDingTalk`。
- 另外补了两个：
  - 发起人与负责人是同一人：`TestAcceptCloseSamePerson`；
  - 告知型不算过时：`TestDispatchAcceptedNotStale`。
- 变异验证 10 项全部变红：
  - 接单不入队；
  - 关单不入队；
  - 接单告知错了人；
  - 关单告知错了人；
  - 验证不通过也发关单；
  - 告知型也按过时跳过；
  - 接单消息不 @ 发起人；
  - 钉钉标题缺“已接单”；
  - 告知型指引仍写“处理”；
  - 所有事件都不算过时。
- 本机走查：新建库 **`cohive_n1_try`**（用完请删），服务端口 8794，假 Telegram 端口 8795。
  - 过程：用 MCP 走完“起草 → 发出 → 接单（带说明）→ 开始修复 → 请求验证 → 验证通过关闭（带说明）”。
  - 群里收到“已接单”“已验证通过并关闭”两条，@ 都正确，两段说明文字都没有出现在群里，服务日志里没有机器人令牌。
  - 这次走得很快，“新单交给你”“请验证”两条在投递前就已过时，按原有规则没有发出；这正好说明告知型通知不跳过是必要的。

## 与派活要求的出入

1. **对自己不 @**：现有群通知代码没有“对自己不 @”的做法（问题单通知、加入通知都照常提及）。按派活的说法照常 @，用例 `TestAcceptCloseSamePerson` 固定了这个行为。发起人和负责人是同一人时，他会收到自己操作的提醒。如果要改成“不 @ 自己、只写名字”，四行左右的改动，等拍板。
2. **新增的“告知型不算过时”规则**：派活里没写，是我加的，理由见上文第 3 点。如果更希望和其他事件一样过时就不发，删掉 `stale` 里的一个条件即可，但信息链会在“接单后马上开始修复”这类常见情况下断掉。
3. **文案**：用了“告知发起人 / 告知负责人”的写法，好让被 @ 的人知道这条只是告知、不用动手。派活里的示例“#N 已被 X 接单”是这句话的前半句。
4. **管理员改派**（`queueReassign`）：不涉及接单、关单，没有改。
