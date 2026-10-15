# 第一阶段·里程碑三：多工作空间、Telegram 群通知与 /cohive 技能 实现计划

**目标：** 问题单轮到某人时，工作空间对应的 Telegram 群里出现一条只含信号的通知并 @ 到人；被 @ 的人在编程工具里用 `/cohive`（Claude Code）或 `$cohive`（Codex）拉单、初筛、拍板。支持多个工作空间各自配置群。

**依据：** [docs/plan.md](../../plan.md) V0.4 第 5.3、5.4、9 节

## 全局约束

- 群消息从不包含复现脚本、证据、日志、期望与实际结果、评论与退回理由正文。
- 标题与群通知简述默认显示，工作空间级可关闭。
- 群通知简述是独立字段，不超过 80 个字符，由人在发出时一并确认。
- 机器人令牌只从环境变量 `COHIVE_TELEGRAM_BOT_TOKEN` 读取，不入库、不进日志。
- 待发通知与状态变更在同一事务写入；投递失败自动重试，不阻塞业务操作。
- 网络与代理不在范围内。
- `go test ./...` 不访问外部网络：Telegram 接口地址可替换为测试服务器。

## 设计

**数据（迁移 0003）：**
- `workspaces` 增加 `notify_channel`（空或 `telegram`）、`notify_target`（群聊编号）、`notify_show_title`、`notify_show_brief`（默认 true）、`public_url`（看板外部地址，可空）。
- `members` 增加 `telegram_username`。
- `issues` 增加 `brief`。
- 新表 `notifications`：`id, workspace_id, issue_id, event, recipients(jsonb 成员编号数组), status(pending/sent/failed), attempts, next_attempt_at, last_error, created_at, sent_at`。

**事件与 @ 对象：** 发出、转交 → 负责人；退回、请求验证 → 发起人；重开 → 负责人；重开触发升级 → 额外一条“需要人介入”，@ 发起人与负责人。评论、开始修复不通知。工作空间未配置群时不写待发记录。

**投递：** 服务端后台循环每 2 秒取一批到期的待发记录（`FOR UPDATE SKIP LOCKED`），渲染消息时读取当时的问题单与工作空间设置，调用通道发送。失败按 10 秒、1 分钟、5 分钟、30 分钟退避，第 5 次失败标记 failed 并记录错误；Telegram 返回 429 时按其 `retry_after` 等待。

**消息格式（HTML，全部转义）：**

```
#12 新单交给 @lee_codex，请接单、退回或转交
服务 order-api，由 lee-claude 用 Claude Code 发出
标题：下单接口返回 500
简述：下单接口对空购物车返回 500，前端联调卡住
在编程工具里用 /cohive 12（Codex 用 $cohive 12）处理
https://cohive.example.com/issues/12
```

**管理命令：**

```
cohive-server workspace add --name payments
cohive-server workspace list
cohive-server workspace notify --workspace payments --telegram-chat -100123 [--show-title=false] [--show-brief=false] [--public-url https://…]
cohive-server member set --workspace payments --name lee-codex --telegram lee_codex
cohive-server notify test --workspace payments
```
原有 member、token、service 命令增加 `--workspace`，缺省为 `default`。

**机器人命令（2026-10-15 调整，代替原计划的 `telegram chats` 命令）：** 服务端用长轮询接收机器人消息（只需出站访问）。群里发 `/chatid`，机器人回复群聊编号和一条可复制的 `workspace notify` 命令；发 `/whoami`，回复发送者的 Telegram 用户名和一条 `member set` 命令。同一个机器人同时只能有一个程序接收消息，所以不再提供命令行查询。

**投递循环（调整）：** 用服务端自己的后台循环加 `FOR UPDATE SKIP LOCKED`，暂不引入 river；等需要定时任务（超时催办）时再引入。

**技能：** `examples/skills/cohive/SKILL.md` 一份内容，示例里分别放到 `.claude/skills/cohive/` 与 `.agents/skills/cohive/`。

## 任务

1. 迁移 0003、多工作空间管理命令、成员 Telegram 用户名。
2. 群通知简述字段：起草参数、校验、输出。
3. 事件写入待发表：业务层在同一事务内写入；测试覆盖各事件、评论不写、幂等重放不重复、未配置群不写、升级额外一条。
4. 消息渲染：标题与简述开关、@ 映射、从不包含正文、HTML 转义。
5. Telegram 通道与投递循环：成功、失败退避、429、超过次数标记失败；机器人 `/chatid`、`/whoami` 命令；`notify test` 命令。
6. `/cohive` 技能与示例配置、操作指南；真实 Telegram 群实测。
