# 第一阶段·里程碑六 c：管理操作记录 实现计划

> **给执行者：** 按任务逐项实施（本会话自己实施，最后整条分支交一次独立审查）。步骤用 `- [ ]` 勾选。

**目标：** 所有管理操作写进一张只追加的记录表，与业务在同一事务里写入；工作空间管理页、服务器管理页各加“记录”一栏，服务器命令行加只读查看命令；迁移时把已有的审批补成记录。顺带修两处：设备授权页“可用于”重复出现、被拒绝的注册申请仍留着密码哈希。真人试用前完成。

**依据：**
- `docs/brain/decisions.md`“管理操作记录”（2026-10-17 lee 定，台账 D11 选 B：“虽然是小团队，但还是符合规范比较好”）、“管理权限”（管理操作只认密码登录的网页会话；服务器命令行兜底）、“账号模型与数据库”（迁移只新增）。
- `docs/superpowers/plans/2026-10-17-phase1-m5b-accounts.md` 第 1.3 节权限矩阵（谁能做什么、从哪个入口做）。

**前提（main `0034a6c`）：**
- 管理操作的业务入口全部在 `internal/core`：`admin.go`（角色、设置、成员、转交与移出、停用与启用、服务器管理员、工作空间建删）、`pairing.go`（邀请、注册与加入申请的审批）、`device.go`（设备审批与吊销）、`link.go`（断开 Telegram）、`account.go`（重置密码）。权限判定在 `perm.go`，操作者 `actor *store.Account` 为空表示服务器命令行。
- 三个入口调用它们：网页接口 `internal/httpapi`（`admin.go` 工作空间管理页、`server.go` 服务器管理页、`account.go` 个人中心），群里的审批按钮 `internal/notify/bot.go`（`handleCallback`），服务器命令行 `cmd/cohive-server`。
- 现状缺口：只有 `signups`、`device_auths` 留了 `decided_by`（`signups` 还有 `decided_at`，`device_auths` 没有决定时间），不记入口；`CreateInvite`、`RevokeInvite`、`ResetPassword` 的写库不在事务里；命令行的 `member add`、`account set`、`token issue|revoke`、`service add` 直接调 `store`，绕过了 `core`。

**不在本里程碑：** 防篡改哈希链与外部归档；记录的保留期与清理（待拍板 7）；越权被拒的尝试（待拍板 3）；登录、登出、网页会话；问题单操作（已有 `issue_events` 时间线）；记录导出。

## 全局约束

- 只新增迁移 `0008_admin_events.sql`，不改写 0001–0007。
- 记录与业务同一事务：写记录的函数只接受 `pgx.Tx`，不接受连接池（编译期保证）。业务任何一步失败，整个事务回滚，不留记录。
- 记录表没有修改、删除的代码路径；库里再加触发器拦截 `UPDATE`、`DELETE`、`TRUNCATE`。
- 附加信息里绝不出现密码、密码哈希、令牌、邀请码、设备密钥、轮询密钥、网页登录码。只记编号、名字、角色、类别这类非秘密的值。
- 没有实际变化的请求不留记录（例如授予已是服务器管理员的人、工作空间设置提交了原值）。
- 管理查看接口沿用“只认密码登录的网页会话”（`adm()`、`srv()` 包装，即 `aPassword`）。
- 文案一律简体中文，动作用人话；入口、类别都有中文名，代码标识符不出现在界面上。
- 测试口径：`go vet ./... && go test -count=1 ./... && make build`；真实 Postgres（`localhost:55432`，库 `cohive` 里建随机 schema）；关键判定做变异验证。
- 本机走查用 `127.0.0.1:8790` 与新建库 `cohive_m6c_try`；不碰测试服进程与 `cohive_test`、`cohive`、`cohive_m5`。

## 审查重点（规格没明说、最容易咬到人的情况）

1. **两名管理员同时审批同一条申请**（网页和群按钮同时点）：只有先到的那个留一条记录，后到的报“已处理”且不留记录。→ T2 并发用例。
2. **工作空间被删除后**：它以前的记录仍在服务器管理页可见，显示原名（记录表不设指向工作空间的外键，另存名字快照）。→ T1、T4 用例。
3. **操作人后来被停用或撤销管理员**：他留下的记录照常显示；他本人不能再查看记录（查看时按库里最新权限判定）。→ T4 用例。
4. **分页翻页不重不漏**：同一时刻写入多条、翻页期间又有新记录写入时，按编号倒序的游标分页既不重复也不跳过。→ T4 用例。
5. **用户可控文本进记录**：设备名、显示名会出现在人话里；网页按纯文本渲染（不用 `dangerouslySetInnerHTML`），命令行输出去掉控制字符。→ T4、T5 用例。

---

## 1. 数据模型（迁移 `0008_admin_events.sql`）

### 1.1 表 `admin_events`

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | bigserial PK | 也是分页游标；同一事务内多条按写入顺序 |
| `at` | timestamptz NOT NULL DEFAULT now() | 与业务行同一事务的时间 |
| `actor_account_id` | bigint REFERENCES accounts(id) | 操作人；服务器命令行、系统为空（账号从不删除，可以设外键） |
| `actor_name` | text NOT NULL DEFAULT '' | 操作人登录名快照 |
| `via` | text NOT NULL CHECK (via IN ('web','telegram','server_cli','member_cli','system','unknown')) | 入口：网页 / 群按钮 / 服务器命令行 / 成员本机命令行 / 服务器自动 / 未知（功能上线前） |
| `workspace_id` | bigint（**不设外键**） | 工作空间级操作的工作空间；服务器级操作为空。工作空间可删除，所以不设外键 |
| `workspace_name` | text NOT NULL DEFAULT '' | 工作空间名快照 |
| `action` | text NOT NULL | 动作，见第 2 节 |
| `target_kind` | text NOT NULL | 对象类别：`account`、`member`、`device`、`invite`、`workspace`、`signup`、`device_auth`、`token`、`service` |
| `target_id` | bigint | 对象编号 |
| `target_name` | text NOT NULL DEFAULT '' | 对象名快照（登录名、设备名、工作空间名、服务名；邀请为空） |
| `detail` | jsonb NOT NULL DEFAULT '{}' | 附加信息，键见第 2 节 |

索引：`(workspace_id, id DESC)`、`(actor_account_id, id DESC)`、`(action, id DESC)`。

**为什么没有单独的“结果”列：** 业务失败不留记录，所以每条都是成功完成的操作；审批类的结果直接体现在动作名里（批准 / 拒绝）。请求与结果不一致时（管理员点了批准，但申请人已被停用，设备申请被改为拒绝），记实际结果，并在附加信息写 `"requested": "approve", "reason": "applicant_disabled"`。

**只追加：** 触发器函数 `admin_events_append_only()` 抛出“管理操作记录只能追加，不能修改或删除”，挂在 `BEFORE UPDATE OR DELETE … FOR EACH ROW` 与 `BEFORE TRUNCATE … FOR EACH STATEMENT` 上。能直接改库的人仍可删触发器，这一层防的是程序缺陷与误操作，不防数据库管理员（见待拍板 5）。

### 1.2 补历史（同一迁移）

| 来源 | 条件 | 补成 |
|---|---|---|
| `signups` | `decided_by IS NOT NULL`（`decided_at` 必有） | `name IS NULL`（已有账号的加入申请）→ `join.approve` / `join.reject`，否则 `signup.approve` / `signup.reject`，按 `status`；`at = decided_at`；操作人取 `decided_by` 的账号；`via = 'unknown'`；工作空间取 `workspace_id` 与当时的名字；对象 `signup`，`target_name` 取申请的登录名（加入申请取账号名） |
| `device_auths` | `decided_by IS NOT NULL` | `device_id IS NOT NULL` → `device.approve`，否则 `device.deny`（批准后、领取前被吊销的授权状态是 `denied` 但 `device_id` 非空，仍算批准过）；`device_auths` 没有决定时间：有设备时 `at` 取 `devices.created_at`（设备在批准时建，等于批准时间），没有设备时取申请的 `created_at`，并在附加信息写 `"time_approx": true`；工作空间为空（设备申请不属于某个工作空间）；对象 `device_auth`，附加信息记设备名与申请人登录名 |

补出的每条附加信息都带 `"backfilled": true`。按 `at` 升序插入，使编号顺序与时间顺序一致。

**补不了的（写进迁移注释与指南）：**
- `decided_by` 为空的已决申请：`signups` 里是第七号迁移按撞名自动拒绝的（不是人做的决定），`device_auths` 里是本人在浏览器授权的首台设备与本人拒绝；不补。
- 其余历史管理操作（角色、邀请、吊销、加人移出、停用、服务器管理员、工作空间建删与设置、重置密码）库里没有操作人与时间，无从补。`invites.created_by`、`members.added_by`、`members.removed_by`、`accounts.disabled_by`、`workspaces.created_by` 只说明“谁”，有的还会被后来的操作覆盖，补出来会误导，**不补**。

### 1.3 密码哈希

- 迁移：`UPDATE signups SET password_hash = NULL WHERE status = 'rejected'`。
- 代码：`DecideSignup` 拒绝时把 `password_hash` 置空（`store.DecideSignup` 增加一个“清掉密码哈希”的分支）。
- 已批准的申请：哈希已复制进 `accounts`，申请行上的副本在本人改密码后就成了旧密码的哈希。建议批准时同样清掉（待拍板 6，按建议做）。

## 2. 动作与入口

### 2.1 动作表（`internal/core/audit.go` 的常量；每个动作有中文名、类别与人话模板）

| 类别 | 动作 | 对象 | 附加信息 |
|---|---|---|---|
| 审批 | `signup.approve` / `signup.reject` | signup | `applicant`（登录名） |
| 审批 | `join.approve` / `join.reject` | signup | `applicant` |
| 审批 | `device.approve` / `device.deny` | device_auth | `owner`（申请人登录名）、`device_name`、`tools`；批准变拒绝时 `requested`、`reason` |
| 角色 | `member.role` | member | `from`、`to`（`admin` / `member`） |
| 邀请 | `invite.create` | invite | `kind`、`role`、`max_uses`、`expires_at` |
| 邀请 | `invite.revoke` | invite | `kind` |
| 设备 | `device.revoke` | device | `owner`、`device_name`、`self`（本人吊销自己的为真） |
| 成员 | `member.add` | member | `role`、`account_created`（命令行新建了账号时为真） |
| 成员 | `member.handover` | member | `issues: [{id, as, to}]`、`services: [{name, to}]`（`to` 为接手人登录名） |
| 成员 | `member.remove` | member | 同上，外加 `tokens_revoked`（条数） |
| 账号 | `account.disable` / `account.enable` | account | 停用时 `devices_revoked`（条数） |
| 账号 | `account.server_admin_grant` / `account.server_admin_revoke` | account | — |
| 账号 | `account.reset_password` | account | —（新密码绝不记） |
| 账号 | `account.update` | account | 变化的字段 `display_name`、`telegram_username`、`telegram_user_id` 各 `{from, to}` |
| 账号 | `account.telegram_unlink` | account | `self` |
| 工作空间 | `workspace.create` | workspace | `admin`（首位管理员登录名） |
| 工作空间 | `workspace.delete` | workspace | — |
| 工作空间 | `workspace.settings` | workspace | 变化的字段 `public_url`、`telegram_chat`、`show_title`、`show_brief` 各 `{from, to}` |
| 令牌与服务 | `token.issue` / `token.revoke` | token | `member`（登录名）、`tool`（令牌本身绝不记） |
| 令牌与服务 | `service.owner` | service | `from`、`to`（登录名） |

待拍板 1 的本人操作若按建议做，另加：`device.self_approve`（首台设备本人授权）、`invite.use`（凭个人邀请注册或加入，对象 member，附加信息 `invite_id`、`role`）、`account.telegram_link`（本人连接 Telegram）、`account.password_change`（本人改密码）、`account.create`（初始管理员，`via = 'system'`）。

**人话**（`core.EventText(e store.AdminEvent) string`，网页与命令行共用，服务端生成）：主语 = 操作人 + 入口短语。网页“lee 在网页上”、群按钮“lee 在群里”、成员本机命令行“lee 在本机命令行”、服务器命令行“服务器命令行”、系统“服务器首次启动时”、未知“lee（功能上线前的记录，入口未知）”。例：
- “lee 在群里批准了 tester-d 的注册申请”
- “lee 在网页上把 tester-a 在 proj-b 的角色从成员改为管理员”
- “服务器命令行重置了 tester-a 的密码”
- “lee 在网页上把 tester-c 移出 proj-b，转交 3 张问题单、1 个服务”
- “lee 在本机命令行吊销了自己的设备 MacBook-Pro”

### 2.2 入口清单（逐一对应；表驱动测试的数据就来自这张表）

| # | 入口 | 调用 | 动作 | `via` | 工作空间 |
|---|---|---|---|---|---|
| 1 | 网页·工作空间管理页 `POST /api/v1/w/{ws}/admin/signups/{id}` | `DecideSignup` | `signup.*` / `join.*` | web | 本空间 |
| 2 | 群按钮·注册与加入申请 | `DecideSignup` | 同上 | telegram | 群所在空间 |
| 3 | 网页·工作空间管理页 `POST …/admin/device-auths/{id}` | `DecideDeviceAuth` | `device.approve` / `device.deny` | web | 本空间 |
| 4 | 网页·服务器管理页 `POST /api/v1/server/device-auths/{id}` | `DecideDeviceAuth` | 同上 | web | 空 |
| 5 | 群按钮·设备申请 | `DecideDeviceAuth` | 同上 | telegram | 群所在空间 |
| 6 | 网页·工作空间管理页 改角色 `POST …/admin/members/{id}` | `SetRole` | `member.role` | web | 本空间 |
| 7 | 命令行 `member set` | `SetRole` | 同上 | server_cli | 指定空间 |
| 8 | 网页·工作空间管理页 生成邀请 `POST …/admin/invites` | `CreateInvite`（改为事务） | `invite.create` | web | 本空间 |
| 9 | 网页·工作空间管理页 作废邀请 `POST …/admin/invites/{id}/revoke` | `RevokeInvite`（改为事务） | `invite.revoke` | web | 本空间 |
| 10 | 网页·工作空间管理页 吊销设备 `POST …/admin/devices/{id}/revoke` | `RevokeDevice` | `device.revoke` | web | 本空间 |
| 11 | 网页·个人中心 吊销自己的设备 `POST /api/v1/me/devices/{id}/revoke` | `RevokeDevice` | `device.revoke`（`self`） | web | 空 |
| 12 | 成员本机 `cohive login --force` 用新设备密钥吊销旧设备（同一接口，设备密钥认证） | `RevokeDevice` | `device.revoke`（`self`） | member_cli | 空 |
| 13 | 网页·工作空间管理页 直接加入 `POST …/admin/members` | `AddMember` | `member.add` | web | 本空间 |
| 14 | 命令行 `member add`（账号不存在时新建；改为走新的 `core.AddMemberCLI`） | 新 | `member.add` | server_cli | 指定空间 |
| 15 | 网页·工作空间管理页 只转交 `POST …/admin/members/{id}/handover` | `ApplyHandover(remove=false)` | `member.handover` | web | 本空间 |
| 16 | 网页·工作空间管理页 移出 `POST …/admin/members/{id}/remove` | `ApplyHandover(remove=true)` | `member.remove` | web | 本空间 |
| 17 | 命令行 `member remove` | `ApplyHandover(remove=true)` | 同上（附加信息另记 `as`） | server_cli | 指定空间 |
| 18 | 网页·服务器管理页 停用 / 启用 `POST /api/v1/server/accounts/{name}/disable`、`…/enable` | `DisableAccount` / `EnableAccount` | `account.disable` / `account.enable` | web | 空 |
| 19 | 命令行 `account disable` / `account enable` | 同上 | 同上 | server_cli | 空 |
| 20 | 网页·服务器管理页 授予 / 撤销服务器管理员 `POST /api/v1/server/accounts/{name}` | `SetServerAdmin` | `account.server_admin_*` | web | 空 |
| 21 | 命令行 `account server-admin` | 同上 | 同上 | server_cli | 空 |
| 22 | 网页·服务器管理页 建工作空间 `POST /api/v1/server/workspaces` | `CreateWorkspace` | `workspace.create` | web | 新空间 |
| 23 | 命令行 `workspace add` | 同上 | 同上 | server_cli | 新空间 |
| 24 | 网页·服务器管理页 删工作空间 `DELETE /api/v1/server/workspaces/{name}` | `DeleteWorkspace` | `workspace.delete` | web | 被删空间（编号与名字快照） |
| 25 | 命令行 `workspace remove` | 同上 | 同上 | server_cli | 同上 |
| 26 | 网页·工作空间管理页 改设置 `POST …/admin/workspace` | `UpdateWorkspace` | `workspace.settings` | web | 本空间 |
| 27 | 命令行 `workspace notify` | 同上 | 同上 | server_cli | 指定空间 |
| 28 | 命令行 `admin reset-password` | `ResetPassword` | `account.reset_password` | server_cli | 空 |
| 29 | 网页·服务器管理页 断开某人的 Telegram `DELETE /api/v1/server/accounts/{name}/telegram` | `UnlinkTelegram` | `account.telegram_unlink` | web | 空 |
| 30 | 网页·个人中心 断开自己的 Telegram `DELETE /api/v1/me/links/telegram` | `UnlinkTelegram` | 同上（`self`） | web | 空 |
| 31 | 命令行 `account set`（待拍板 2；改为走新的 `core.UpdateAccountCLI`） | 新 | `account.update` | server_cli | 空 |
| 32 | 命令行 `token issue` / `token revoke`（待拍板 2；新的 `core.IssueTokenCLI`、`core.RevokeTokenCLI`） | 新 | `token.issue` / `token.revoke` | server_cli | 指定空间 |
| 33 | 命令行 `service add`（待拍板 2；新的 `core.SetServiceOwnerCLI`） | 新 | `service.owner` | server_cli | 指定空间 |

待拍板 1 按建议做时另加：网页授权首台设备（`AuthorizeDevice`，`device.self_approve`，web）、凭个人邀请注册（`Register`）与已有账号接受个人邀请（`AcceptInvite`）（`invite.use`，web）、私聊机器人完成连接（`CompleteTelegramLink`，`account.telegram_link`，telegram）、本人改密码（`ChangePassword`，`account.password_change`，web）、首次启动建初始管理员（`EnsureInitialAdmin`，`account.create`，system）。

只读入口（列表、查看、查找账号、`notify test`、`notify retry`、`migrate`、`serve`）不记；`notify retry` 只是把发送失败的通知重新排队，不改变任何人的权限。

### 2.3 入口怎么传到业务层：`core.Actor`

把各管理方法的 `actor *store.Account`（与 `RevokeDevice`、`UnlinkTelegram` 的 `actor store.Account`）统一换成：

```go
// Actor 是管理操作的操作人与入口。Account 为空表示服务器命令行或系统。
type Actor struct {
	Account *store.Account
	Via     string // ViaWeb、ViaTelegram、ViaServerCLI、ViaMemberCLI、ViaSystem
}
func WebActor(a store.Account) Actor
func BotActor(a store.Account) Actor
func MemberCLIActor(a store.Account) Actor
func ServerCLI() Actor
```

- 权限函数（`canAdmin` 等）仍接收 `*store.Account`，调用处传 `act.Account`，判定逻辑不变。
- 选这个而不是把入口放进 `context`：签名一改，所有调用处都必须写明入口，编译期就不会漏；放进 `context` 时漏传只能在运行时发现。代价是约 140 处调用（多数在测试里）机械替换。
- `Via` 为空或不在取值表里时，写记录返回错误，事务回滚（防御，测试里有一条用例）。
- 网页个人中心的吊销接口同时接受网页会话与设备密钥：设备密钥认证时用 `MemberCLIActor`，其余用 `WebActor`。

### 2.4 写记录

- `store.InsertAdminEvent(ctx context.Context, tx pgx.Tx, e AdminEvent) error`：只接受 `pgx.Tx`。
- `core.record(ctx context.Context, tx pgx.Tx, act Actor, e store.AdminEvent) error`：填操作人编号与名字快照、入口；`e.WorkspaceID` 非空时在事务里读名字快照；校验 `Via` 与 `Action` 在取值表里。
- 每个管理方法在业务全部成功之后、事务提交之前调用 `record`；`record` 失败即整体失败。
- `detail` 只由每个动作的具名字段构造（`map[string]any` 的键限定在第 2.1 节的表里）。

## 3. 查看

### 3.1 接口

| 路径 | 权限 | 范围 |
|---|---|---|
| `GET /api/v1/w/{ws}/admin/events` | `adm()`：密码会话 + `canAdmin(ws)` | `workspace_id = 本空间` |
| `GET /api/v1/server/events` | `srv()`：密码会话 + 服务器管理员 | 全部；可加 `workspace=<名>` |

查询参数：`type`（类别：`approval`、`role`、`invite`、`device`、`member`、`account`、`workspace`、`token_service`，映射为动作列表）、`actor`（操作人登录名，精确匹配）、`via`（入口）、`before`（游标：上一页最后一条的编号）、`limit`（默认 50，最大 200）。按 `id` 倒序。

返回：
```json
{"items": [{"id": 12, "at": "…", "via": "telegram", "via_label": "群按钮", "actor": "lee", "workspace": "proj-b",
            "action": "signup.approve", "action_label": "批准注册申请", "category": "approval",
            "text": "lee 在群里批准了 tester-d 的注册申请", "detail": {…}}],
 "next_before": 11}
```
`next_before` 为空表示没有更多。非法的 `type`、`via`、`limit` 返回 400；`actor` 查无此人返回空列表（不泄露账号是否存在给工作空间管理员之外的人——这里本来就只有管理员能调）。

工作空间管理员只看本空间编号的记录：服务器级操作（停用本空间成员、在服务器管理页批准本空间成员的设备）不在这里显示（待拍板 4）。

### 3.2 服务器命令行

`cohive-server audit list [--workspace <名称>] [--type <类别>] [--actor <登录名>] [--via <入口>] [--limit 50] [--before <编号>]`

每行：`#编号  2026-10-17 14:03  群按钮  [proj-b]  lee 在群里批准了 tester-d 的注册申请`；末尾提示“更早的记录：--before 11”。只读；人话里的控制字符替换为空格。

### 3.3 网页

- 工作空间管理页新增标签“记录”（`adminTabs` 加 `{ id: "events", label: "记录" }`）；服务器管理页新增标签“记录”（`serverTabs` 加 `{ id: "events", label: "记录" }`，多一列工作空间、多一个工作空间筛选）。
- 共用组件 `web/src/pages/EventLog.tsx`：筛选条（类型下拉、操作人输入框、入口下拉），列表每行显示人话、入口标签、时间（绝对时间 + 相对时间），点开显示附加信息（键用中文名，值按纯文本）；底部“加载更早的记录”。空列表显示“还没有记录”。
- 记录列表只用文本节点渲染。

## 4. 设备授权页文案

`web/src/pages/DeviceAuth.tsx` 里信息表的“可用于”一行和按钮上方的 `deviceScopeText` 一句重复。保留信息表那一行，标签改为“批准后可用于”，空列表时显示 `deviceScopeText([])` 的提示；删掉按钮上方那一句。`rules.test.ts` 的用例保留（函数仍用于空列表提示），新增一个页面用例：授权页里“可用于”只出现一次。

## 5. 文件清单

| 位置 | 改动 |
|---|---|
| `internal/db/migrations/0008_admin_events.sql` | 表、索引、触发器、补历史、清密码哈希 |
| `internal/db/migrate0008_test.go` | 补历史与清哈希的迁移测试（在 0007 结构上造数据再迁移） |
| `internal/store/admin_event.go` | `AdminEvent`、`InsertAdminEvent`、`AdminEventFilter`、`ListAdminEvents` |
| `internal/store/pairing.go` | `DecideSignup` 清哈希；`RevokeAccountDevices` 与移出时吊销令牌的函数返回条数（附加信息用） |
| `internal/core/audit.go` | `Actor`、入口与动作常量、类别、`record`、`EventText`、`ListEvents` |
| `internal/core/admin.go`、`pairing.go`、`device.go`、`link.go`、`account.go` | 签名换 `Actor`，写记录；`CreateInvite`、`RevokeInvite`、`ResetPassword` 进事务；新增命令行用的四个方法 |
| `internal/httpapi/admin.go`、`server.go`、`account.go` | 传 `WebActor` / `MemberCLIActor`；两个查看接口 |
| `internal/httpapi/session.go` | `handle()` 登记路由与认证方式，导出 `Routes()` 供测试遍历 |
| `internal/notify/bot.go` | 接口签名换 `Actor`，传 `BotActor` |
| `cmd/cohive-server/` | 传 `ServerCLI()`；四条直接写库的命令改走 `core`；新增 `audit list` |
| `web/src/api.ts`、`pages/EventLog.tsx`、`pages/Admin.tsx`、`pages/Server.tsx`、`pages/DeviceAuth.tsx` | 记录标签与组件；授权页文案 |
| `docs/guides/` | 管理指南加“操作记录”一节（看哪里、记了什么、补不了什么、命令行查看） |

## 6. 任务

每项完成即 commit + push `feature/phase1-m6c-admin-audit`。

### T1：迁移与存储层

- [ ] 写失败的测试 `internal/store/admin_event_test.go`：`TestInsertAndListAdminEvents`（插入三条，按编号倒序，`before` 游标翻页不重不漏，按工作空间、操作人、动作列表、入口筛选）；`TestAdminEventsAppendOnly`（`UPDATE`、`DELETE`、`TRUNCATE` 都报“只能追加”，行数不变）；`TestAdminEventsSurviveWorkspaceDelete`（删工作空间后记录还在、名字快照还在）。
- [ ] 写失败的迁移测试 `internal/db/migrate0008_test.go`：迁到 0007，造数据——管理员批准的新人注册、拒绝的新人注册、批准的加入申请、0007 自动拒绝的（`decided_by` 为空）、管理员批准并已领取的设备、管理员批准后被吊销的设备、管理员拒绝的设备、本人授权的首台设备——再迁到 0008。断言：补出 6 条（动作、操作人、`via = 'unknown'`、`at`、`backfilled`、设备的 `time_approx`）；自动拒绝与首台设备不补；按 `at` 升序编号；被拒申请的 `password_hash` 为空、待审与批准的不受影响（批准的是否清见待拍板 6）。
- [ ] 跑测试确认失败；写迁移与 `store/admin_event.go`；跑通。
- [ ] commit：`feat(M6c-T1): 管理操作记录表（只追加、触发器拦截改删）与补历史审批、清除被拒申请的密码哈希`

### T2：业务层写记录

- [ ] 写失败的测试 `internal/core/audit_test.go`：
  - `TestEveryAdminMethodRecords`：表驱动，每行 = 一个 `core` 管理方法的一次成功调用（第 2.2 节去掉入口重复后的全部方法 × 批准 / 拒绝等分支），断言恰好新增一条记录，动作、对象、操作人、入口、工作空间、附加信息的关键键都对。
  - `TestFailedOperationLeavesNoRecord`：降级最后一名管理员、移出最后一名管理员、停用最后一名服务器管理员、删非空工作空间、重复审批、非管理员操作、转交清单过期——每种都报错且记录条数不变。
  - `TestNoopLeavesNoRecord`：授予已是服务器管理员的人、提交未改动的工作空间设置。
  - `TestConcurrentDecideRecordsOnce`：两个协程同时批准同一条注册申请、同一条设备申请，只有一个成功，只有一条记录。
  - `TestRecordsHoldNoSecrets`：跑完 `TestEveryAdminMethodRecords` 同样的操作后，扫全部记录的 `detail` 与 `target_name`：不含本次用到的邀请码、令牌明文、设备密钥、轮询密钥、密码、任何 `$2a$`/`$2b$` 开头的哈希；`detail` 的键里不含 `password`、`token`、`secret`、`code`、`hash`。
  - `TestRecordRejectsUnknownVia`：`Via` 为空时操作失败且业务没有生效。
  - `TestEventTextCoversEveryAction`：遍历动作表，每个动作 × 每种入口都生成非空人话、有中文名和类别。
  - `TestRejectedSignupDropsPasswordHash`。
- [ ] 跑测试确认失败；写 `core/audit.go`；把各管理方法换成 `Actor` 并写记录，三处非事务写库改进事务；更新 `httpapi`、`notify`、`cmd/cohive-server` 的调用处（各传对应入口）与既有测试的调用；跑通全量。
- [ ] commit：`feat(M6c-T2): 管理操作与业务同一事务写记录，入口随操作人传入`

### T3：入口层覆盖与防漏

- [ ] 写失败的测试：
  - `internal/server/audit_entry_test.go` `TestEveryWebAdminEndpointRecords`：表驱动，第 2.2 节网页入口每行一条真实 HTTP 请求（密码会话），断言新增记录的 `via = web`（第 12 行用设备密钥请求，断言 `member_cli`）。`TestNoUnlistedWriteEndpoint`：遍历 `httpapi.Routes()` 里全部非 `GET` 路由，每条要么在上表，要么在“非管理写操作”白名单（登录、登出、注册、设备发起与轮询、改密码等，各附一句理由），否则失败——以后新增接口不登记就红。
  - `internal/notify/audit_test.go` `TestGroupButtonsRecord`：注册、加入、设备 × 批准、拒绝，经 `handleCallback` 走一遍（假 Telegram 接口），断言 `via = telegram`、工作空间为群所在空间。
  - `cmd/cohive-server/audit_test.go` `TestEveryServerCommandRecords`：表驱动跑 `run()`，第 2.2 节命令行行各一条，断言 `via = server_cli`、操作人为空。`TestEveryCommandClassified`：解析 `usage` 里的全部命令，每条要么在写表里，要么在只读名单里，否则失败。
- [ ] 跑测试确认失败；把命令行四条直接写库的命令改走 `core`；跑通。
- [ ] commit：`test(M6c-T3): 每个管理入口都留记录的表驱动测试与新增入口防漏检查`

### T4：查看接口与命令行

- [ ] 写失败的测试：`internal/server/audit_view_test.go`——工作空间管理员只看到本空间的记录（别的空间、服务器级的都看不到）；成员 403；`cohive web` 免密会话与令牌 403；服务器管理员看全部并能按工作空间筛选；四种筛选与游标翻页（翻页期间插入新记录也不重不漏）；非法参数 400；被删工作空间的记录在服务器页显示原名；操作人停用后记录照常、他本人查看 403。`cmd/cohive-server` `TestAuditList`：输出含人话与翻页提示，设备名里的控制字符被替换。
- [ ] 跑测试确认失败；写 `core.ListEvents`、两个接口、`audit list`；跑通。
- [ ] commit：`feat(M6c-T4): 管理操作记录查看接口与服务器命令行 audit list`

### T5：网页

- [ ] 写失败的测试（vitest）：`EventLog.test.tsx`——渲染人话与入口标签；改筛选重新请求且带对应参数；“加载更早的记录”带 `before`；人话含 `<b>` 时按文本显示；空列表提示。`router.test.tsx` 加 `/admin?tab=events`、服务器页 `?tab=events`。`DeviceAuth` 页面用例：“可用于”只出现一次。
- [ ] 跑测试确认失败；写组件、两个标签、授权页文案；`pnpm test` 与 `make build` 通过。
- [ ] commit：`feat(M6c-T5): 工作空间与服务器管理页的“记录”一栏；设备授权页去掉重复的“可用于”`

### T6：文档、走查、变异

- [ ] 管理指南加“操作记录”一节。
- [ ] 本机走查：`127.0.0.1:8790` + 新库 `cohive_m6c_try`，网页走一遍审批、改角色、邀请、吊销、加人移出、停用启用、建删工作空间、改设置，两个“记录”页逐条核对人话；命令行 `audit list` 对照。群按钮不在本机实测（需要真实群，按“越界即停”交主脑在测试服做）。走查完列出建过的库，不删。
- [ ] 变异验证（改坏 → 跑 → 必须红 → 还原）：
  - M1 删掉 `DecideSignup` 里的 `record` → T2 表驱动审批行、T3 网页与群按钮行红。
  - M2 `record` 改用连接池在业务检查之前写 → `TestFailedOperationLeavesNoRecord` 红（编译期限制要先临时放开）。
  - M3 `invite.create` 的附加信息塞进邀请码 → `TestRecordsHoldNoSecrets` 红。
  - M4 群按钮传成 `WebActor` → `TestGroupButtonsRecord` 红。
  - M5 迁移里去掉触发器 → `TestAdminEventsAppendOnly` 红。
  - M6 工作空间记录接口去掉 `workspace_id` 条件 → 范围用例红。
  - M7 新增一个不登记的管理写接口 → `TestNoUnlistedWriteEndpoint` 红。
  - M8 拒绝申请不清密码哈希 → `TestRejectedSignupDropsPasswordHash` 与迁移测试红。
- [ ] 全量 `go vet ./... && go test -count=1 ./... && make build`；commit + push；按台账第 0 节回报主脑。

## 7. 待拍板

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| 1 | **本人做的、改变“谁能以什么身份进来”的操作要不要也记**：首台设备本人授权、凭个人邀请注册或加入、本人连接 / 断开 Telegram、本人改密码、首次启动建初始管理员 | A 记；B 只记派活清单里的管理操作 | **A**。规则说得清：凡改变谁能进哪个工作空间、凭什么凭据进的都记，不管是管理员还是本人。否则记录里会出现“设备被吊销”却找不到它是怎么来的、“tester-d 成了成员”却没有来源。每人一生只有几条，量很小。登录、登出不记（量大，且不改变权限） |
| 2 | **命令行里本来直接写库的命令也记**：`member add`（可能新建账号）、`account set`（改 Telegram 编号会改变群按钮认谁）、`token issue` / `token revoke`、`service add` | A 记，并改走 `core`；B 不记 | **A**。它们和网页上的管理操作同等分量（签发令牌比生成邀请更敏感），改走 `core` 后命令行与网页的业务规则也只剩一份 |
| 3 | **越权被拒的尝试要不要记** | A 不记；B 另起事务记“被拒绝” | **A**。与“业务失败不留记录”一致；管理接口本来只认密码会话，越权尝试在小团队里多半是误点；B 要在回滚后单独写库，且会被反复请求刷大。以后真要做安全告警，再单独设计 |
| 4 | **工作空间管理员能否看到与本空间成员相关的服务器级记录**（停用本空间成员、在服务器管理页批准本空间成员的设备） | A 不显示，只看本空间编号的记录；B 额外显示“操作对象当时是本空间在册成员”的服务器级记录 | **A**。边界清楚、好测；B 要在写记录时快照对象的成员资格，规则复杂。工作空间管理员能在成员列表里看到某人已停用，原因去问服务器管理员 |
| 5 | **防篡改做到哪一步** | A 代码无改删路径 + 触发器拦截；B 再加哈希链（每条带上一条的哈希）；C 再推送到外部只追加存储 | **A**。团队自部署、数据库管理员就是团队自己人，B、C 防的是管理员本人抹痕迹，第一阶段不值得；以后有合规要求再做 B |
| 6 | **已批准的注册申请也清掉密码哈希** | A 清（批准时清，迁移也清）；B 只按派活清拒绝的 | **A**。哈希已复制进账号表，申请行上的副本在本人改密码后成了旧密码的哈希，留着只增加泄露面；申请行没有别的地方再读它 |
| 7 | **记录保留多久** | A 永久保留；B 保留 N 天后清理 | **A**。小团队一年也就几千条；清理本身就是“删除记录”，与只追加冲突，要另立规矩 |

除非主脑另行指示，待拍板全部按建议做。

**主脑批复（`7781224`）：** ①②④⑤⑥⑦按建议；③改为“已认证身份调管理接口被拒要记”（D7 的威胁模型）。实现（T7）：
- 接口层 `handle()` 对只认密码会话的接口（工作空间管理、服务器管理、授权设备、接受邀请、查看记录）：编程工具令牌、设备密钥、`cohive web` 免密会话被挡，或密码会话但业务层返回 403，另起事务记 `access.denied`（类别“被拒的请求”）。附加信息 `reason`（`need_password_login` / `no_permission`）、`credential`（`password` / `device_web` / `token` / `device` / `telegram`）；对象是接口模板，不记路径里的实际值（邀请码、授权参数）。
- 新入口 `agent`（编程工具令牌），只允许出现在 `access.denied` 上（迁移 0008 尚未合入，直接改了它的入口取值）。
- 群里已连接 Telegram、但没有审批权限的人点按钮也记（`via = telegram`）。
- 不记：未认证的请求、访问自己不在的工作空间（404）。同一身份（账号 + 凭据种类）同一接口一分钟内只记一条（按主脑批复；每个服务进程内存计数）。

**为 M6-d 预留的动作**（主脑预告）：`server.settings`（修改服务器设置，类别“服务器设置”，附加信息 `external_url` 的 `{from, to}`）、`workspace.display_name`（修改工作空间显示名，附加信息 `display_name` 的 `{from, to}`）。只登记了中文名、类别与人话，没有业务入口；列在 `core.ReservedActions` 里，实现业务时从中删掉并补用例（测试会提醒）。
