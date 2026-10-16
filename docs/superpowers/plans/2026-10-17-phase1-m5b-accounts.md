# 第一阶段·里程碑五 b：全局账号与多工作空间成员资格 设计与实现计划

**目标：** 把“每个工作空间一套成员”改成“全局账号 + 工作空间成员资格”。同一个人只有一个登录名、一个密码、一个 Telegram 连接，电脑配对一次即可用于他所在的全部工作空间；角色按工作空间分别设置。新增服务器管理员（建工作空间、指定工作空间管理员、停用账号），工作空间管理员可直接加入已有的人或给新人发邀请；移出工作空间时由管理员转交名下待处理的问题单。在真人试用前完成。

**依据：**
- `docs/brain/decisions.md`“账号模型与数据库”“账号、配对与通讯连接”“管理权限”三节（2026-10-16 lee 定）。
- `docs/brain/requirements.md` 第 6 节 lee 对 5 个问题的回答：一个项目一个群对应一个工作空间；有服务器管理员；登录名、显示名、密码、Telegram 连接全局一份，角色按工作空间；设备配对一次全部工作空间通用，第二台由任一所在工作空间的管理员批准；停用作用于整个人、移出作用于一个工作空间，移出时名下待处理问题单由管理员转交。
- `docs/plan.md` 第 5.4 节（多工作空间，本里程碑推翻其中“成员按工作空间独立、暂不做统一账号”）、第 9 节（安全红线）。

**前提（以 M5 返工合入 main 后的口径为准）：**
- 第六号迁移已合入，本里程碑只新增 `0007_accounts.sql`，不改写第六号。
- 首台设备免审的判定是“这个人从未有过设备”（吊销过的也算有过）；设备在交付令牌时才建，批准时不建。
- 管理接口只认“用密码登录的网页会话”。本文假设 M5 返工在 `web_sessions` 上记录了登录方式，记作 `method`（`password` 或 `device`，后者为 `cohive web` 免密会话）；列名以 M5 合入版本为准。
- 停用成员（台账 D8）并入本里程碑。

**不在本里程碑：** 工作空间归档、账号改名、工作空间改名、本人自行退出工作空间、网页上重置他人密码（仍用服务器命令行）、新设备私聊通知（队列 P3）、删除旧表 `login_codes`（队列 P3）。

## 1. 权限模型

### 1.1 参照

| 产品 | 服务器级 | 组织 / 群组级 | 项目 / 仓库级 | 停用与移出 |
|---|---|---|---|---|
| GitLab | 实例管理员（Administrator，需进入管理模式） | 群组成员，角色 Guest / Planner / Reporter / Developer / Maintainer / Owner，可继承到子群组与项目 | 项目成员，同一套角色 | 封禁（Block）只有实例管理员能做；群组 Owner 只能移出成员，移出时可勾选“同时取消他在议题与合并请求上的指派” |
| GitHub | 企业所有者；自部署版有站点管理员 | 组织 Owner / Member（另有账单、安全等专门角色） | 仓库角色 Read / Triage / Write / Maintain / Admin | 暂停账号（Suspend）只有站点管理员能做；组织 Owner 只能把人移出组织 |

两家的共同点，也是 Cohive 要借用的：
1. **账号是全局的，成员资格挂在组织或项目上**；角色跟着成员资格走，不跟着账号走。
2. **作用于整个人的操作（封禁、暂停）只有服务器级管理员能做**；组织或项目管理员只能管自己那一块（移出）。
3. **移出时处理他手上的待办**（GitLab 的“取消指派”）。
4. 组织或项目管理员能直接把已有账号加进来（GitLab 直接加入；GitHub 发邀请需本人接受，lee 已定 Cohive 走直接加入）。

### 1.2 Cohive 的最小角色集

| 角色 | 挂在哪 | 怎么得到 | 说明 |
|---|---|---|---|
| **服务器管理员** | 账号（`accounts.is_server_admin`） | 初始管理员；其他服务器管理员授予；服务器命令行授予 | 管全部工作空间的管理事务；本身不因此成为任何工作空间的成员 |
| **工作空间管理员** | 成员资格（`members.role = 'admin'`） | 服务器管理员指定；本工作空间管理员提升；个人邀请设为管理员 | 管本工作空间的成员、邀请、设置；审批本工作空间成员的新设备 |
| **工作空间成员** | 成员资格（`members.role = 'member'`） | 加入、邀请注册、加入申请获批 | 用看板、经编程工具收发问题单 |
| 本人 | — | — | 改自己的密码、管理自己的设备与 Telegram 连接 |
| 发起人 / 负责人 | 问题单上的关系 | 发出问题单、被路由或转交 | 问题单的发出、接单、退回、转交、请求验证、关闭、重开，仍按方案第 4 节与状态机，**不受角色影响** |

**为什么不照搬全部等级：**
- GitLab 把 Guest、Reporter、Developer 分开，是因为它们对应代码的读、写、合并权限；Cohive 不持有代码（红线第 3 条），问题单上的权限由“发起人 / 负责人”关系决定，不需要按等级区分。
- GitLab 的 Owner 与 Maintainer、GitHub 的 Owner 与 Admin 分开，主要是为了保护删除、计费、可见性这类“摧毁性”设置；Cohive 把删除工作空间、停用账号交给服务器管理员，工作空间内部只需要“管理员 / 成员”两级。
- 3–15 人的团队，一个工作空间通常一两位管理员。每多一个角色，权限矩阵和测试用例都要成倍增加，越权漏洞的面也更大。
- 以后真需要“只看不改”的人（例如产品经理旁观），再加一个“观察者”角色，见待拍板第 7 条。

### 1.3 谁能做什么

记号：✅ 可以；— 不可以；“本空间”指操作所在的那个工作空间；“网页”一律指**用密码登录的网页会话**（决定 D7）；“命令行”指服务器上的 `cohive-server`，相当于最高权限的兜底。

| 操作 | 服务器管理员 | 工作空间管理员 | 工作空间成员 | 本人 | 入口 |
|---|---|---|---|---|---|
| 建工作空间 | ✅ | — | — | — | 网页“服务器管理”、命令行 |
| 删除工作空间（只限没有问题单的空工作空间，见待拍板第 3 条） | ✅ | — | — | — | 网页“服务器管理”、命令行 |
| 指定 / 撤销工作空间管理员 | ✅ 任一工作空间 | ✅ 本空间（至少保留一名管理员） | — | — | 网页、命令行 |
| 授予 / 撤销服务器管理员 | ✅（至少保留一名，不能撤销自己最后一个） | — | — | — | 网页“服务器管理”、命令行 |
| 把已有账号直接加入工作空间 | ✅ 任一工作空间 | ✅ 本空间 | — | — | 网页、命令行 |
| 发个人邀请 / 团队邀请 | ✅ 任一工作空间 | ✅ 本空间 | — | — | 网页、命令行 |
| 批准 / 拒绝注册与加入申请（团队邀请） | ✅ 任一工作空间 | ✅ 本空间 | — | — | 网页；本空间群里的审批按钮 |
| 移出工作空间（含转交问题单与服务负责人） | ✅ 但须先是本空间成员（转交要记操作人，见 3.8） | ✅ 本空间（不能移出最后一名管理员） | — | —（本里程碑不做自行退出） | 网页、命令行 |
| 批准第二台及以后的设备 | ✅ | ✅ 只要此人是本空间的**在册**成员（作用于他的全部工作空间） | — | 首台免审 | 网页；此人所在各群的审批按钮 |
| 吊销设备 | ✅ | ✅ 此人是本空间在册成员时 | — | ✅ 自己的 | 网页 |
| 停用 / 启用账号（作用于整个人） | ✅（不能停用自己；不能停用最后一名服务器管理员） | —（见待拍板第 1 条） | — | — | 网页“服务器管理”、命令行 |
| 断开某人的 Telegram 连接 | ✅ | — | — | ✅ 自己的 | 网页 |
| 重置他人密码 | 命令行 | — | — | — | 命令行 `admin reset-password` |
| 改工作空间设置（群、公网地址、标题与简述开关） | ✅ 任一工作空间 | ✅ 本空间 | — | — | 网页、命令行 |
| 登记服务负责人 | 命令行 | 命令行（网页化不在本里程碑） | — | — | 命令行 |
| 查看本空间成员列表（名字、显示名、角色） | ✅ | ✅ | ✅ | — | 网页 |
| 查看成员的设备、Telegram 连接状态、待处理单数 | ✅ | ✅ 本空间在册成员 | — | ✅ 自己的 | 网页 |
| 按登录名查找账号（用于直接加入） | ✅ 全部账号列表 | ✅ 只能精确输入登录名，不能浏览（见待拍板第 6 条） | — | — | 网页 |
| 看板、问题单列表与详情 | 须是本空间成员（见待拍板第 2 条） | ✅ | ✅ | — | 网页 |
| 改派问题单的负责人或发起人（移出时转交） | 须是本空间成员 | ✅ 本空间 | — | — | 网页（移出流程内）、命令行 |
| 问题单发出 / 接单 / 退回 / 转交 / 请求验证 / 关闭 / 重开 | 按发起人与负责人关系，与角色无关 | 同左 | 同左 | — | 编程工具（MCP，人工确认） |
| 群里审批按钮（注册、加入、设备） | ✅ 若其 Telegram 已连接 | ✅ 本空间 | — | — | Telegram 群 |

**权限判定的统一规则（实现时写成一个函数，所有管理入口共用）：**
- `canAdmin(actor, ws)`：`actor` 未停用，且 `actor.is_server_admin` 或 `actor` 在 `ws` 有在册（`status = 'active'`）且 `role = 'admin'` 的成员资格。
- `canManagePerson(actor, target)`（批设备、吊销设备、看设备）：`actor` 是服务器管理员，或存在某个工作空间 `ws`，`target` 在 `ws` 在册且 `canAdmin(actor, ws)`。
- 命令行调用时操作者为空，视为最高权限，但“至少一名管理员 / 服务器管理员”的约束照样生效。
- 一律在事务里按库里的最新状态重新判定（沿用 M5 `SetRole` 的做法：先锁行，再读操作者最新角色），不信请求开始时读到的角色。

## 2. 数据模型与迁移

### 2.1 选型：保留 `members`，把它的含义改为“成员资格”

现有外键几乎全部指向 `members`：`issues.source_member_id`、`issues.owner_id`、`issue_events.actor_member_id`、`notifications.actor_member_id` 与 `issue_owner_id`、`services.owner_id`、`tokens.member_id`、`signups.member_id`、`login_codes.member_id`。它们表达的都是“某人在某个工作空间里的身份”，正好就是成员资格。

| 方案 | 改动 | 结论 |
|---|---|---|
| **A 保留 `members` 作为成员资格**，新增 `accounts`；个人属性从 `members` 移到 `accounts` | 问题单、事件、通知、服务、令牌、幂等这些外键**一条都不动**；Go 里 `store.Member` 结构保留原字段（名字、显示名、Telegram 改由关联 `accounts` 读出），业务层、通知渲染、MCP 工具基本不改，改动集中在 store 层十来条 SQL | **采用** |
| B 新建 `memberships` 表，把上述外键全部改指过去，`members` 删除 | 改名带来的语义更直白，但要改写全部外键、几乎所有查询和测试夹具，迁移也更长 | 不采用：收益只是名字，代价是最核心的问题单链路全部重测 |

采用 A 后的约定：
- 代码与文档里“成员”= 成员资格（某账号在某工作空间里的一条记录），“账号”= 人。
- **成员资格不删除**：移出时 `status` 改为 `removed`，历史问题单、事件仍指向它；再次加入同一工作空间时**复用同一行**改回 `active`（`UNIQUE (account_id, workspace_id)` 保证），他以前的问题单自然接回来。
- 登录名不可修改（本里程碑不做改名），工作空间名不可修改（项目配置按名字引用，见待拍板第 9 条）。

### 2.2 表结构（第七号迁移之后）

**新表 `accounts`（人）**

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | bigserial PK | |
| `name` | text NOT NULL UNIQUE | 登录名，全局唯一，规则同 M5：`^[a-z0-9][a-z0-9_-]{0,31}$`；不可改 |
| `display_name` | text NOT NULL DEFAULT '' | 从 `members` 移来 |
| `password_hash` | text NOT NULL DEFAULT '' | 从 `members` 移来；空表示没设密码（命令行建的账号） |
| `must_change_password` | boolean NOT NULL DEFAULT false | 从 `members` 移来 |
| `telegram_user_id` | bigint，部分唯一索引 `WHERE telegram_user_id IS NOT NULL` | 从 `members` 移来；**全服务器唯一**（原为每工作空间唯一） |
| `telegram_username` | text NOT NULL DEFAULT '' | 从 `members` 移来，只用于 @ |
| `is_server_admin` | boolean NOT NULL DEFAULT false | 新增 |
| `disabled_at` | timestamptz | 新增；非空即停用 |
| `disabled_by` | bigint REFERENCES accounts(id) | 新增；命令行操作时为空 |
| `created_at` | timestamptz NOT NULL DEFAULT now() | |

**`members`（成员资格）**

| 变化 | 列 |
|---|---|
| 保留 | `id`、`workspace_id`、`role`、`created_at` |
| 新增 | `account_id bigint NOT NULL REFERENCES accounts(id)`、`UNIQUE (account_id, workspace_id)`；`status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','removed'))`；`removed_at timestamptz`；`removed_by bigint REFERENCES accounts(id)`；`added_by bigint REFERENCES accounts(id)` |
| 删除（已移到 `accounts`） | `name`、`display_name`、`telegram_username`、`telegram_user_id`、`password_hash`、`must_change_password`；连带删除 `UNIQUE (workspace_id, name)`、`members_workspace_telegram_user`、`members_telegram_user_id` |

**其余表**

| 表 | 变化 | 理由 |
|---|---|---|
| `devices` | `member_id` → `account_id`；新增 `secret_hash bytea UNIQUE`（设备密钥，见 3.6；迁移来的旧设备为空）；新增 `last_used_at timestamptz` | 一台电脑属于人，不属于某个工作空间 |
| `tokens` | **不变**：`member_id` 仍指成员资格，`device_id` 仍指设备；新增部分唯一索引 `(device_id, member_id, tool) WHERE revoked_at IS NULL AND device_id IS NOT NULL` | 令牌仍按“人 + 工具 + 工作空间”签发；同一台电脑同一工作空间同一工具只留一枚有效令牌，重签时吊销旧的 |
| `device_auths` | `member_id` → `account_id`；删除 `workspace_id`；`decided_by` 改指 `accounts`；索引 `device_auths_awaiting` 改为 `(status) WHERE status = 'awaiting_admin'` | 设备申请属于人；谁能批由他当时的成员资格决定，不在申请上固定工作空间 |
| `signups` | 新增 `account_id bigint REFERENCES accounts(id)`（已有账号凭团队邀请申请加入时非空；新人获批后回填）；`name`、`password_hash` 改为可空（已有账号申请时为空）；`decided_by` 改指 `accounts`；原 `signups_pending_name` 换成两个部分唯一索引：`(name) WHERE status='pending' AND account_id IS NULL`（新人待审名字全局占位）与 `(workspace_id, account_id) WHERE status='pending' AND account_id IS NOT NULL` | 团队邀请在多工作空间下既可能来新人，也可能来已有账号 |
| `invites` | `created_by` 改指 `accounts` | 服务器管理员不一定是该工作空间成员 |
| `link_requests` | `member_id` → `account_id` | Telegram 连接全局一份 |
| `web_sessions`、`web_login_codes` | `member_id` → `account_id`；`web_sessions.method` 保留 | 网页登录不再落在某个工作空间 |
| `workspaces` | 新增部分唯一索引 `(notify_channel, notify_target) WHERE notify_target <> ''`；新增 `created_by bigint REFERENCES accounts(id)` | 一个群只对应一个工作空间：群里点按钮时由群反查工作空间，必须唯一；也防止通知串到别的项目群 |
| 新表 `bot_posts` | `(id, kind text, ref_id bigint, workspace_id bigint, chat_id bigint, message_id bigint, created_at)`，索引 `(kind, ref_id)` | 设备申请会发到此人所在的多个群，处理后要把每个群里的审批消息都改成结果 |
| `issues`、`issue_events`、`notifications`、`services`、`idempotency`、`login_codes` | 不变 | 外键仍指成员资格 |

**问题单状态机**：新增动作 `reassign`（管理员改派），见 3.8。只加在 `issue` 包的规则表里，不动表结构；事件 `payload` 记 `{"field": "owner"|"source", "from": 成员编号, "to": 成员编号, "reason": "removed"}`。

### 2.3 第七号迁移 `0007_accounts.sql`

**迁移规则（M5 数据是“每个工作空间一套成员”）：**
1. **归并**：Telegram 用户编号相同的几条成员记录归为同一账号（这是“同一个人”最可靠的信号；M5 已保证同一工作空间内不重复，所以合并后不会出现同一账号在同一工作空间有两条成员资格）。其余成员记录一条对应一个账号。见待拍板第 4 条。
2. **合并账号取值**：登录名、显示名、Telegram 用户名取编号最小那条成员记录；密码取按编号排序第一条非空的密码及其 `must_change_password`；创建时间取最早。
3. **登录名撞名**：不同账号的登录名相同时，编号最小的保留原名，其余改为 `原名-w工作空间编号`（超长时截短原名部分，保证不超过 32 个字符）；改名后仍撞名则 `RAISE EXCEPTION` 中止迁移，由人工处理（测试数据下不应出现）。被改名的人登录时要用新名字，迁移说明里列出查询语句。
4. **服务器管理员**：在名为 `default` 的工作空间里是管理员的账号设为服务器管理员（M5 的初始管理员就在这里）。一个都没有时不设，服务启动时 `EnsureInitialAdmin` 发现没有服务器管理员会照常新建 `admin` 并写初始密码文件。见待拍板第 5 条。
5. **进行中的申请**：`device_auths` 里 `pending`、`awaiting_admin`、`approved` 状态的一律改为 `expired`（终端重新 `cohive login` 即可）；`link_requests` 里 `pending` 的改为 `failed`；待审 `signups` 若登录名与已有账号或别的待审申请撞名，编号最小的保留，其余改为 `rejected`。
6. **令牌去重**：同一 `(device_id, member_id, tool)` 有多枚有效令牌时，只保留最新一枚，其余吊销（M5 补签工具可能留下重复）。
7. **群绑定去重**：多个工作空间绑定同一个群时，编号最小的保留，其余清空群编号（群通知关闭），并 `RAISE NOTICE` 写进迁移日志。
8. 其余引用 `members` 的个人数据（设备、会话、网页登录码、连接请求、邀请创建人、审批人）全部按 `members.account_id` 换成账号编号。

**SQL 骨架（实现时补全，整个迁移在一个事务里）：**

```sql
CREATE TABLE accounts (
    id                    bigserial PRIMARY KEY,
    name                  text NOT NULL UNIQUE,
    display_name          text NOT NULL DEFAULT '',
    password_hash         text NOT NULL DEFAULT '',
    must_change_password  boolean NOT NULL DEFAULT false,
    telegram_user_id      bigint,
    telegram_username     text NOT NULL DEFAULT '',
    is_server_admin       boolean NOT NULL DEFAULT false,
    disabled_at           timestamptz,
    disabled_by           bigint REFERENCES accounts(id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    legacy_leader         bigint            -- 迁移用，末尾删除
);

-- 1. 每条成员记录的归属：同一 Telegram 编号归到编号最小的那条（leader）
CREATE TEMP TABLE m2a ON COMMIT DROP AS
SELECT id AS member_id,
       min(id) OVER (PARTITION BY COALESCE('tg:' || telegram_user_id, 'm:' || id)) AS leader
FROM members;

-- 2. 每个 leader 建一个账号；撞名的加后缀
INSERT INTO accounts (name, display_name, password_hash, must_change_password,
                      telegram_user_id, telegram_username, created_at, legacy_leader)
SELECT CASE WHEN rn = 1 THEN l.name
            ELSE left(l.name, 32 - length('-w' || l.workspace_id)) || '-w' || l.workspace_id END,
       l.display_name, pw.hash, pw.must_change, l.telegram_user_id, l.telegram_username, g.created_at, l.id
FROM (SELECT m.*, row_number() OVER (PARTITION BY m.name ORDER BY m.id) AS rn
      FROM members m WHERE m.id IN (SELECT DISTINCT leader FROM m2a)) l
JOIN LATERAL (SELECT min(m.created_at) AS created_at FROM members m JOIN m2a ON m2a.member_id = m.id
              WHERE m2a.leader = l.id) g ON true
LEFT JOIN LATERAL (SELECT m.password_hash AS hash, m.must_change_password AS must_change
                   FROM members m JOIN m2a ON m2a.member_id = m.id
                   WHERE m2a.leader = l.id AND m.password_hash <> '' ORDER BY m.id LIMIT 1) pw ON true;
-- 后缀名再撞：上面的 INSERT 会因 accounts.name 唯一约束直接失败，事务回滚，等同中止迁移
-- pw 为空（没有密码）时 password_hash 用默认空串：实现时用 COALESCE

-- 3. 成员资格挂到账号
ALTER TABLE members ADD COLUMN account_id bigint REFERENCES accounts(id);
UPDATE members m SET account_id = a.id FROM m2a JOIN accounts a ON a.legacy_leader = m2a.leader
WHERE m.id = m2a.member_id;
ALTER TABLE members ALTER COLUMN account_id SET NOT NULL,
    ADD CONSTRAINT members_account_workspace UNIQUE (account_id, workspace_id),
    ADD COLUMN status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed')),
    ADD COLUMN removed_at timestamptz,
    ADD COLUMN removed_by bigint REFERENCES accounts(id),
    ADD COLUMN added_by bigint REFERENCES accounts(id);

-- 4. 服务器管理员
UPDATE accounts a SET is_server_admin = true FROM members m JOIN workspaces w ON w.id = m.workspace_id
WHERE m.account_id = a.id AND w.name = 'default' AND m.role = 'admin';

-- 5. 个人数据换成账号（以 devices 为例，其余同理：device_auths、link_requests、web_sessions、web_login_codes）
ALTER TABLE devices ADD COLUMN account_id bigint REFERENCES accounts(id);
UPDATE devices d SET account_id = m.account_id FROM members m WHERE m.id = d.member_id;
ALTER TABLE devices ALTER COLUMN account_id SET NOT NULL, DROP COLUMN member_id,
    ADD COLUMN secret_hash bytea UNIQUE, ADD COLUMN last_used_at timestamptz;
-- 审批人、创建人（invites.created_by、signups.decided_by、device_auths.decided_by）同样换成账号编号

-- 6. 进行中的申请作废、令牌去重、待审注册撞名、群绑定去重（略，按 2.3 规则 5–7）

-- 7. 删 members 上已搬走的列与索引，删迁移临时列
ALTER TABLE members DROP CONSTRAINT members_workspace_id_name_key,
    DROP COLUMN name, DROP COLUMN display_name, DROP COLUMN telegram_username,
    DROP COLUMN telegram_user_id, DROP COLUMN password_hash, DROP COLUMN must_change_password;
ALTER TABLE accounts DROP COLUMN legacy_leader;
CREATE UNIQUE INDEX accounts_telegram_user ON accounts (telegram_user_id) WHERE telegram_user_id IS NOT NULL;
```

**上线与回滚：** 迁移是单向的。上线前对目标库做一次 `pg_dump`；回滚即恢复备份并换回 M5 程序。按迁移文件规则，测试服先在独立库（建议 `cohive_m5b`，从 `cohive_m5` 复制）上跑通再切换。

### 2.4 Go 层形状

- 新增 `store.Account{ID, Name, DisplayName, TelegramUsername, TelegramUserID, ServerAdmin, DisabledAt}`。
- `store.Member` 保留现有字段（`Name`、`DisplayName`、`TelegramUsername`、`TelegramUserID` 改由 `JOIN accounts` 读出），新增 `AccountID`、`Status`、`AccountDisabled`。原 `memberColumns` 改为 `memberSelect = "... FROM members m JOIN accounts a ON a.id = m.account_id"` 一处定义，所有读成员的查询共用。
- `store.Identity` 新增 `AccountID`。`LookupToken` 关联账号，增加条件 `m.status = 'active' AND a.disabled_at IS NULL`（吊销是主防线，这是第二道）。
- 问题单查询里的 `JOIN members sm` / `om`、事件里的 `JOIN members m` 各补一次 `JOIN accounts` 取名字；返回结构不变。

## 3. 流程

### 3.1 初始管理员与服务器管理员

- `EnsureInitialAdmin` 的判定从“没有任何管理员”改为“没有任何未停用的服务器管理员”。满足时：建账号 `admin`（撞名则 `admin-2`…），`is_server_admin = true`，随机密码写数据目录 `initial_admin_password`（权限 600，不进日志），首次登录强制改密；同时让它成为 `default` 工作空间的管理员，保持 M5 的开箱体验（登录后直接能发邀请）。
- 初始密码文件只在**该账号本人**改密后删除（判定改为按账号名，不再看工作空间）。
- 授予 / 撤销服务器管理员：网页“服务器管理 → 账号”或 `cohive-server account server-admin --name X --on|--off`。服务器上至少保留一名未停用的服务器管理员；判定时锁住全部服务器管理员行（同 M5 `LockAdmins` 的做法），两人同时互相撤销时后一个失败。
- 忘记密码：`cohive-server admin reset-password --name X`（去掉 `--workspace`），显示一次新密码，账号下次登录必须改密，全部网页会话失效。

### 3.2 建工作空间（网页）

1. 服务器管理员在“服务器管理 → 工作空间”填名字（规则同登录名，建后不可改），可选填首位管理员的登录名（默认是自己）。
2. 同一事务：建 `workspaces` 行（`created_by` 记操作者）；给首位管理员建（或复用并改回在册）`role = 'admin'` 的成员资格。
3. 页面跳到新工作空间的“管理 → 工作空间”，提示下一步：把机器人拉进项目群、群里发 `/chatid`、填群编号；然后加人或发邀请。
4. 群编号已被别的工作空间占用时报 `409`“这个群已经绑定了工作空间 X”。

### 3.3 加入已有的人 vs 邀请新人

| 情形 | 做法 | 结果 |
|---|---|---|
| **已有账号**，管理员知道他的登录名 | 工作空间管理员在“管理 → 成员 → 添加已有账号”输入登录名（精确匹配），确认显示名后选角色，点“加入” | 立即成为在册成员；以前被移出过的复用原成员资格；若他已连接 Telegram，群里发一条“X 已加入本工作空间”并 @ 他；被停用的账号不能加入 |
| **新人** | 发**个人邀请**（一次性，可设为管理员） | 新人打开链接注册 → 建账号 + 成员资格，立即生效并登录 |
| **新人**，一批人 | 发**团队邀请**（多人可用） | 新人注册 → 生成待审申请，管理员批准后建账号 + 成员资格 |
| 已有账号的人收到了邀请链接 | 打开链接时若已登录（或点“已有账号？登录后加入”），页面显示“以 X 身份加入工作空间 Y” | 个人邀请：接受即成为成员（角色按邀请）；团队邀请：生成“加入申请”，管理员批准后成为成员。不再新建账号 |

**邀请语义在多工作空间下的变化：**
- 邀请始终属于一个工作空间；**用掉邀请得到的是“这个工作空间的成员资格”**，新建账号只是新人的附带结果。
- 个人邀请：一次性；新人注册或已有账号接受，二选一，用掉即作废。
- 团队邀请：可用次数内多人使用；每次使用生成一条待审申请——新人的叫“注册申请”（含登录名、密码哈希），已有账号的叫“加入申请”（只有 `account_id`）。管理员在同一个“待审”列表里处理，列表标明是新人还是已有账号。
- 新人注册时登录名**全服务器唯一**：与已有账号、别的工作空间的待审注册申请都不能重复。
- 已经是该工作空间在册成员的人打开邀请：提示“你已经是 Y 的成员”，不消耗次数。
- 接受邀请必须是**密码登录的网页会话**（`cohive web` 免密会话不行），理由见第 5 节第 2 条。

### 3.4 登录与网页工作空间切换

- 登录只填登录名与密码，不再选工作空间；M5 的 `409` 多工作空间歧义、`AmbiguousLoginError` 一并删除。
- 登录成功返回账号信息与他所在的工作空间列表。失败一律“登录名或密码不对”；密码正确但账号已停用时才提示“账号已停用，请联系服务器管理员”（不泄露账号是否存在）；团队注册待审时提示等待批准（沿用 M5）。
- 网页路由按工作空间分前缀：`/w/{工作空间名}/`（看板）、`/w/{工作空间名}/issues`、`/w/{工作空间名}/issues/{编号}`、`/w/{工作空间名}/admin`。账号级页面不带前缀：`/me`、`/password`、`/device/{nonce}`、`/server`（服务器管理）、`/i/{code}`。
- 顶栏加工作空间切换下拉框（列出 `me.workspaces`，标出角色）。`/` 跳到上次用的工作空间（浏览器本地记一下，读写失败不影响），没有记录时跳到列表第一个；一个工作空间都没有时显示“你还没有加入任何工作空间，请联系管理员”，服务器管理员额外看到“新建工作空间”。
- 用路径而不是服务端“当前工作空间”状态：多个标签页可同时看不同工作空间，链接可分享，群消息里的链接也带工作空间前缀。
- 服务器管理员访问自己不是成员的工作空间：管理页可进，看板与问题单返回 404 并提示“你不是这个工作空间的成员，可在管理页把自己加进来”（见待拍板第 2 条）。

### 3.5 设备配对

设备属于账号。终端发起、浏览器授权的步骤与 M5 相同，变化在于“谁的设备”“谁来批”“交付什么”：

1. `cohive login` 发起，终端显示链接与二维码，开始轮询（同 M5）。
2. 浏览器打开 `/device/{nonce}`，要求**密码登录的网页会话**。页面显示设备名、工具，以及“批准后这台电脑可用于你所在的工作空间：A、B”。
3. 本人点“授权”。账号从未有过设备 → 直接批准；否则进入 `awaiting_admin`，页面提示“已提交，等待你所在工作空间的管理员批准”。
4. 等待批准时，审批入口：
   - 网页：此人**在册**所在的每个工作空间的“管理 → 待审”都能看到；服务器管理员在“服务器管理 → 待审设备”能看到全部。
   - 群：此人在册所在、且配置了群的每个工作空间群里各发一条审批消息（记进 `bot_posts`）。任一处批准或拒绝后，其余群里的消息都改成结果并去掉按钮。
   - 此人一个在册工作空间都没有时，只有服务器管理员能批。
5. 批准人资格在批准那一刻重算：`canManagePerson(批准人, 申请人)`。申请人在批准前被移出了某工作空间，该工作空间的管理员就不再能批。
6. 终端领取（`device/poll`）时，同一事务：锁账号行确认未停用 → 建设备（含设备密钥）→ 为申请人**每个在册工作空间 × 每个申请的工具**签发令牌 → 状态改 `delivered`。设备密钥和令牌只交付这一次。
7. 账号的“首台”判定：`SELECT count(*) FROM devices WHERE account_id = ?`，含已吊销的。迁移合并的账号，其各成员原有设备都算。

### 3.6 令牌签发与新工作空间的令牌

**令牌仍按“人 + 工具 + 工作空间”**：`tokens.member_id` 指成员资格。编程工具用哪个工作空间，由项目里 `cohive init` 写下的 `cohive mcp-proxy --server S --workspace W --tool T` 决定（同 M5，项目配置不含秘密）。

**新增设备密钥**（`cohive_dev_` 开头，库里只存哈希，存在 `devices.secret_hash`）：配对时随令牌一起交付，存进本机凭据文件。它只证明“这是某账号的一台已批准的电脑”，**只能**调用三个接口：
- `POST /api/v1/device/tokens`：为某个自己在册的工作空间、某些工具签发令牌；
- `GET /api/v1/device/workspaces`：列出自己在册的工作空间；
- `POST /api/v1/auth/web-login`：`cohive web` 免密登录（得到 `method = 'device'` 的会话）。

它不能调 MCP、不能调问题单与管理接口。之所以不用现有令牌证明设备：令牌属于某个工作空间，人被移出那个工作空间后令牌就吊销了；如果他只在那一个工作空间，这台电脑就再也证明不了自己，只能重新配对、还要管理员批准。设备密钥只随设备吊销、账号停用而失效。

**加入新工作空间后本机怎么拿令牌（不必重新配对）：**
- **自动**：成员把新项目克隆下来（项目配置已提交在仓库里），打开编程工具。`cohive mcp-proxy` 在凭据文件里找不到 `(S, W, T)` 的令牌时，用 S 的设备密钥调 `device/tokens {workspace: W, tools: [T]}`，拿到后写入凭据文件再继续转发；在标准错误输出写一行“已为工作空间 W 签发 T 的令牌”。他不是 W 的成员时，转发器报错退出：“你还不是工作空间 W 的成员，请联系它的管理员”。
- **令牌失效后自动换新**：转发器收到服务端 `401` 时，用设备密钥重签一次再重试（覆盖“被移出后又被加回来”的情况）；重签也失败就报错退出，不循环。
- **手动**：`cohive init --workspace W` 在当前项目写配置的同时为 `--tools` 里的工具签发 W 的令牌；不给 `--workspace` 且账号在多个工作空间时，列出 `device/workspaces` 的结果并要求指定（不做交互选择，智能体也可能执行它）。
- 每次签发都吊销同一 `(设备, 成员资格, 工具)` 的旧令牌，凭据文件不会越积越多。

**本机凭据文件 `~/.config/cohive/credentials.json`（权限 600）新格式：**

```json
{
  "devices": [
    {"server": "https://cohive.example.com", "account": "lee", "device_id": 12,
     "secret": "cohive_dev_...", "created_at": "2026-10-20T10:00:00Z"}
  ],
  "logins": [
    {"server": "https://cohive.example.com", "workspace": "pay", "member": "lee",
     "tool": "claude-code", "token": "cohive_...", "device_id": 12, "created_at": "..."}
  ]
}
```

`logins` 字段不变（`member` 现在填登录名），旧文件照常能读。没有 `devices` 记录的旧电脑（M5 配对的）继续用已有令牌，但新工作空间的自动签发与 `cohive web` 会提示“这台电脑是旧版配对，请执行 cohive login --force 重新配对”（见待拍板第 8 条）。

### 3.7 Telegram 连接与群

- **连接全局一份**：个人中心发起，机器人 `/start ln_xxx` 完成，写到 `accounts` 上。一个 Telegram 账号只能连接一个 Cohive 账号；冲突提示“这个 Telegram 账号已经连接了账号 X，请先在网页上解除”。
- **@ 到人**：群通知渲染时从成员资格关联账号取 `telegram_username`。通知只发到问题单所属工作空间绑定的群（不变）。被停用的账号已断开连接，不再 @。
- **一个群只绑一个工作空间**（唯一索引）。
- **群里审批按钮的校验**（全部在业务层，同一事务内）：
  1. 由回调里的群编号 `callback.message.chat.id` 反查工作空间 `ws`；查不到则拒绝。按钮数据里的工作空间编号必须与之一致（防止转发或伪造回调数据）。
  2. 由点击者 Telegram 编号找到账号；没有、或已停用，提示“你的 Telegram 账号还没有连接到 Cohive，或没有审批权限”。
  3. 要求 `canAdmin(点击者账号, ws)`。
  4. 注册与加入申请：申请必须属于 `ws`。设备申请：申请人必须是 `ws` 的在册成员（与网页 `canManagePerson` 一致）。
  5. 处理后按 `bot_posts` 把所有相关群里的审批消息改成结果。
- `/whoami`：私聊显示连接的账号；群里额外显示他在本群工作空间的角色。`/chatid` 不变。
- 群审批按钮作为 D7“只认密码登录的网页会话”的既定例外保留（2026-10-16 已定，Telegram 账号本身是第二个凭据，智能体拿不到）。多工作空间下它批的设备作用于此人全部工作空间，这一点在审批消息里写明：“批准后这台电脑可用于 X 所在的全部工作空间”。

### 3.8 移出工作空间（含转交）

1. 工作空间管理员在“管理 → 成员”点某人的“移出”。页面先调 `GET .../handover`，列出：
   - 他作为**负责人**、尚未结束（不是草稿、已关闭、已退回）的问题单；
   - 他作为**发起人**、尚未结束且非草稿的问题单（之后的验证、关闭要有人接手）；
   - 他作为**负责人登记**的服务（不转交的话，新问题单仍会路由给他）。
2. 管理员为每一项选接手人（本空间在册、未停用的成员；可“全部转给同一人”），提交。
3. 服务端同一事务：锁成员资格行 → 确认操作者 `canAdmin` 且自己是本空间在册成员（改派要记事件操作人）→ 校验清单**完整**且每张问题单的 `expected_version` 未变，否则 `409` 并返回最新清单 → 逐张执行 `reassign`（改 `owner_id` 或 `source_member_id`，版本号加一，写事件，给新接手人入队一条群通知，动作文案“转交”）→ 改服务负责人 → 成员资格改 `removed`、记 `removed_at`、`removed_by` → 吊销该成员资格的全部令牌。
4. 不能移出本空间最后一名在册管理员（锁管理员行判定，同 M5）。
5. 移出不影响他的账号、设备、Telegram 连接和其他工作空间。他的草稿留着（只有本人可见，移出后也看不到）。
6. 他的网页会话不注销，但所有 `/w/{该工作空间}/...` 请求立即 404；编程工具里的令牌立即失效。
7. 命令行兜底：`cohive-server member remove --workspace W --name X --handover-to Y`（全部转给 Y）。

`reassign` 是管理动作，不走编程工具的人工确认（管理员在网页上点确认本身就是人做的决定），也不提供 MCP 工具。

### 3.9 停用与启用（作用于整个人）

- **停用**（服务器管理员，网页或命令行），同一事务：锁账号行 → 不能停用自己、不能停用最后一名未停用的服务器管理员 → `disabled_at`、`disabled_by` → 吊销他全部设备（设备密钥随之失效）与全部令牌 → 删除全部网页会话与未用的网页登录码 → 断开 Telegram 连接 → 把他 `pending` 的设备申请改为 `denied`。
- 之后：登录被拒；令牌、设备密钥、会话都无效；`LookupToken`、会话查询、设备密钥查询都再检查一次 `disabled_at IS NULL`。
- **不要求转交**：停用可能是临时的（电脑丢了、休长假）。管理页成员列表给停用的人标“已停用”和待处理单数，管理员可随时对他单独做 3.8 的改派（不移出）——为此 `handover` 与改派接口也接受“只改派、不移出”的调用（`remove: false`）。
- 他在各工作空间的成员资格保持在册（角色不变），看板上标“已停用”；登记为服务负责人的，新问题单仍会路由给他，管理页对此给出提醒。
- **启用**：清空 `disabled_at`。设备不恢复：他要重新 `cohive login`，因为“有过设备”，需要管理员批准（与 M5 探针“吊销全部设备后再配对仍需审批”一致）。Telegram 要重新连接。

### 3.10 删除工作空间

只允许删除**没有任何问题单**（含草稿）的工作空间：同一事务删掉它的邀请、待审申请、服务、成员资格（及其令牌）、通知、群绑定和 `bot_posts`，最后删工作空间行。有问题单时 `409`“工作空间里还有问题单，不能删除”。用于建错名字等情形；有数据的工作空间的归档以后再做（见待拍板第 3 条）。

## 4. 接口、网页、命令行改动清单

通用约定沿用 M5：均返回 JSON，错误为 `{"error": "中文说明"}`；网页用会话 Cookie `cohive_session`，非 GET 请求要求同源 `Origin`；命令行与编程工具用 `Authorization: Bearer`。

**鉴权分四类**，每个接口标明接受哪几类：

| 记号 | 含义 |
|---|---|
| 〔密码会话〕 | `web_sessions.method = 'password'` 的网页会话 |
| 〔会话〕 | 任意网页会话（含 `cohive web` 免密会话） |
| 〔令牌〕 | 成员令牌 `cohive_...`（属于某个成员资格，即某个工作空间） |
| 〔设备〕 | 设备密钥 `cohive_dev_...` |

鉴权中间件按前缀分流：`cohive_dev_` 开头的只在标了〔设备〕的接口上认，其余接口一律 `401`；`/mcp` 只认〔令牌〕。

**工作空间前缀** `/api/v1/w/{ws}/...`：〔会话〕要求账号在 `ws` 在册（管理接口另按 `canAdmin`）；〔令牌〕要求令牌所属工作空间就是 `ws`；不满足一律 `404 {"error": "工作空间不存在或你不是它的成员"}`，不区分是哪种情况。

### 4.1 账号与登录

| 接口 | 鉴权 | 请求 | 响应 | 变化 |
|---|---|---|---|---|
| `POST /api/v1/auth/login` | 公开，限频 | `{name, password}` | `200 {account: Account, must_change_password, workspaces: [WorkspaceRef]}` 并设 Cookie（`method = 'password'`）；`401` 登录名或密码不对；`403` 待审 / 已停用 | 改：去掉 `workspace` 入参与 `409` |
| `POST /api/v1/auth/logout` | 〔会话〕 | — | `204` | 不变 |
| `POST /api/v1/auth/password` | 〔会话〕 | `{old_password, new_password}` | `204` | 不变（作用于账号） |
| `GET /api/v1/me` | 〔会话〕〔令牌〕 | — | `{account: Account, session: {method: "password"|"device"|"token", tool}, workspaces: [WorkspaceRef], workspace?: WorkspaceRef}`；`workspace` 只在〔令牌〕时出现，是令牌所属的工作空间 | 改：结构重排；`cohive status` 读 `workspace` 与 `account.name` |
| `POST /api/v1/auth/web-login` | 〔设备〕或已绑定设备的〔令牌〕 | `{}` | `{url}` | 改：接受设备密钥 |
| `GET /auth/once/{code}` | 公开 | — | 设 Cookie（`method = 'device'`）后跳 `/` | 不变 |

```
Account      = {id, name, display_name, server_admin: bool, disabled: bool, has_password: bool,
                telegram: {username, linked: bool}}
WorkspaceRef = {name, role: "admin"|"member"}
```

### 4.2 邀请、注册、加入

| 接口 | 鉴权 | 请求 | 响应 | 变化 |
|---|---|---|---|---|
| `GET /api/v1/invites/{code}` | 公开（带 Cookie 时额外返回本人信息） | — | `{workspace, kind: "personal"|"team", role, valid, signed_in_as?: name, already_member?: bool}` | 改：加 `role`、`signed_in_as`、`already_member` |
| `POST /api/v1/register` | 公开，限频 | `{code, name, display_name, password}` | 个人邀请 `200 {status: "active", account: Account, workspace: WorkspaceRef}` 并设 Cookie；团队邀请 `200 {status: "pending"}`；`400` 登录名已被使用 / 已有人申请 | 改：登录名全局查重 |
| `POST /api/v1/invites/{code}/accept` | 〔密码会话〕 | `{}` | 个人邀请 `200 {status: "active", workspace: WorkspaceRef}`；团队邀请 `200 {status: "pending"}`；`409` 已是成员 / 已有待审加入申请；`400` 邀请无效 | **新增** |

### 4.3 设备

| 接口 | 鉴权 | 请求 | 响应 | 变化 |
|---|---|---|---|---|
| `POST /api/v1/device/start` | 公开，限频 | `{tools, device_name}` | `{request_id, poll_secret, url, expires_in}` | 不变 |
| `GET /api/v1/device/auth/{nonce}` | 〔密码会话〕 | — | `{device_name, tools, created_at, status, first_device, workspaces: [name]}` | 改：加 `workspaces`；鉴权收紧为密码会话 |
| `POST /api/v1/device/auth/{nonce}` | 〔密码会话〕 | `{approve}` | `{status: "approved"|"awaiting_admin"|"denied"}` | 改：鉴权收紧 |
| `POST /api/v1/device/poll` | 公开（凭 `poll_secret`） | `{request_id, poll_secret}` | `{status, account?, device_id?, device_secret?, workspaces?: [{workspace, role, tokens: {工具: 令牌}}]}`；后四项只在交付那一次出现 | 改：交付设备密钥与多工作空间令牌；去掉 `member`、`workspace`、`tokens` |
| `POST /api/v1/device/tokens` | 〔设备〕 | `{workspace, tools}` | `{account, workspace, tokens: {工具: 令牌}}`；`404` 不是该工作空间在册成员；`401` 设备已吊销或账号已停用 | **新增**，取代 `POST /api/v1/devices/self/tools` |
| `GET /api/v1/device/workspaces` | 〔设备〕 | — | `{account, items: [WorkspaceRef]}` | **新增** |
| `POST /api/v1/devices/self/tools` | — | — | — | **删除** |

### 4.4 个人中心

| 接口 | 鉴权 | 响应 | 变化 |
|---|---|---|---|
| `GET /api/v1/me/devices` | 〔会话〕 | `{items: [{id, name, created_at, revoked_at, last_used_at, tokens: [{workspace, tool}]}]}` | 改：`tools` 换成按工作空间列出的 `tokens` |
| `POST /api/v1/me/devices/{id}/revoke` | 〔会话〕 | `204` | 不变 |
| `POST /api/v1/me/links`、`GET /api/v1/me/links/{id}`、`DELETE /api/v1/me/links/telegram` | 〔会话〕 | 同 M5 | 不变（作用于账号） |

### 4.5 工作空间内（前缀 `/api/v1/w/{ws}`）

**看板与问题单**（〔会话〕〔令牌〕，要求在册）：

| 接口 | 变化 |
|---|---|
| `GET /api/v1/w/{ws}/board` | 由 `GET /api/v1/board` 移来；成员列加 `disabled` 标记 |
| `GET /api/v1/w/{ws}/issues` | 由 `GET /api/v1/issues` 移来 |
| `GET /api/v1/w/{ws}/issues/{id}` | 由 `GET /api/v1/issues/{id}` 移来；问题单不属于 `ws` 时 `404` |

**工作空间管理**（〔密码会话〕，要求 `canAdmin(账号, ws)`；全部由 `/api/v1/admin/...` 移来或新增）：

| 接口 | 请求 | 响应 | 变化 |
|---|---|---|---|
| `GET .../admin/pending` | — | `{signups: [{id, kind: "new"|"existing", name, display_name, created_at}], devices: [{id, account, display_name, device_name, tools, created_at, workspaces: [name]}]}` | 改：注册与加入申请合并；设备申请列出此人全部在册工作空间 |
| `POST .../admin/signups/{id}` | `{approve}` | `204` | 移来；申请须属于 `ws` |
| `POST .../admin/device-auths/{id}` | `{approve}` | `204` | 移来；申请人须是 `ws` 在册成员 |
| `GET .../admin/members` | — | `{items: [{member_id, account, display_name, role, status, disabled, telegram_username, telegram_linked, devices, pending_issues, owned_services}]}` | 改：`id` 改名 `member_id`；加 `status`、`disabled`、`pending_issues`、`owned_services`；默认只列在册，`?status=removed` 列已移出 |
| `GET .../admin/accounts/{name}` | — | `{name, display_name, member_status: "none"|"active"|"removed", disabled}`；`404` | **新增**：按登录名精确查找 |
| `POST .../admin/members` | `{account, role}` | `201 {member_id}`；`404` 没有这个账号；`409` 已是在册成员；`400` 账号已停用 | **新增**：直接加入已有账号（移出过的复用原成员资格） |
| `POST .../admin/members/{member_id}` | `{role}` | `204`；`400` 至少保留一名管理员 | 移来 |
| `GET .../admin/members/{member_id}/handover` | — | `Handover` | **新增** |
| `POST .../admin/members/{member_id}/handover` | `HandoverPlan` | `204`；`409 {error, handover: Handover}` | **新增**：只改派、不移出（停用的人用） |
| `POST .../admin/members/{member_id}/remove` | `HandoverPlan` | `204`；`409 {error, handover: Handover}`；`400` 最后一名管理员 | **新增**：改派并移出 |
| `GET .../admin/devices` | — | `{items: [{id, account, name, created_at, revoked_at, last_used_at, workspaces: [name]}]}` | 移来；范围是本空间在册成员的设备 |
| `POST .../admin/devices/{id}/revoke` | — | `204` | 移来；设备主人须是本空间在册成员 |
| `GET .../admin/invites`、`POST .../admin/invites`、`POST .../admin/invites/{id}/revoke` | 同 M5 | 同 M5 | 移来 |
| `GET .../admin/workspace` | — | `{name, public_url, telegram_chat, show_title, show_brief, bot_enabled}` | 移来 |
| `POST .../admin/workspace` | `{public_url, telegram_chat, show_title, show_brief}` | `204`；`409` 群已绑定别的工作空间 | 移来；加群唯一校验 |
| `DELETE /api/v1/admin/members/{id}/telegram` | — | — | **删除**（改由本人或服务器管理员断开） |

```
Handover     = {member_id, account,
                issues:   [{id, title, status, as: "owner"|"source", version}],
                services: [{name}]}
HandoverPlan = {issues:   [{id, as: "owner"|"source", to_member_id, expected_version}],
                services: [{name, to_member_id}]}
```
`HandoverPlan` 必须覆盖 `Handover` 的全部条目；接手人须是本空间在册、未停用的成员，且不能是被移出的人自己。

### 4.6 服务器管理（前缀 `/api/v1/server`，〔密码会话〕，要求服务器管理员）

| 接口 | 请求 | 响应 |
|---|---|---|
| `GET /api/v1/server/workspaces` | — | `{items: [{name, created_at, members, admins: [name], open_issues, telegram_bound, i_am_member}]}` |
| `POST /api/v1/server/workspaces` | `{name, admin?}`（`admin` 为登录名，默认自己） | `201 {name}`；`400` 名字不合法；`409` 已存在 |
| `DELETE /api/v1/server/workspaces/{name}` | — | `204`；`409` 还有问题单 |
| `GET /api/v1/server/accounts` | `?q=`（前缀筛选，可空） | `{items: [{name, display_name, server_admin, disabled, telegram_linked, devices, workspaces: [{name, role, status}]}]}` |
| `POST /api/v1/server/accounts/{name}` | `{server_admin: bool}` | `204`；`400` 至少保留一名服务器管理员 |
| `POST /api/v1/server/accounts/{name}/disable` | `{}` | `204`；`400` 不能停用自己 / 最后一名服务器管理员 |
| `POST /api/v1/server/accounts/{name}/enable` | `{}` | `204` |
| `DELETE /api/v1/server/accounts/{name}/telegram` | — | `204` |
| `GET /api/v1/server/pending-devices` | — | `{items: [{id, account, device_name, tools, created_at, workspaces: [name]}]}`（全部待批设备） |
| `POST /api/v1/server/device-auths/{id}` | `{approve}` | `204` |

### 4.7 网页

| 页面 | 变化 |
|---|---|
| `/login` | 去掉工作空间选择；登录后跳 `/` |
| `/` | 跳转到上次用的或第一个工作空间；没有工作空间时显示提示页 |
| 顶栏 | 加工作空间切换下拉框；服务器管理员多一个“服务器管理”入口 |
| `/w/$ws/`、`/w/$ws/issues`、`/w/$ws/issues/$id` | 由 `/`、`/issues`、`/issues/$id` 移来；旧路径跳转到对应的新路径（用上次的工作空间），群消息里的旧链接不失效 |
| `/w/$ws/admin` | 由 `/admin` 移来。“成员”页加“添加已有账号”“移出”（转交对话框：每行一个接手人下拉框，顶部“全部转给”）、“改派”（停用的人）、“查看已移出”；去掉“断开他人 Telegram”。“待审”页标出新人 / 已有账号；设备申请显示“批准后用于：A、B” |
| `/server` | **新增**：工作空间（新建、删除、进入管理）、账号（搜索、停用 / 启用、服务器管理员开关、断开 Telegram）、待批设备 |
| `/i/$code` | 未登录：注册表单 + “已有账号？登录后加入”；已登录：显示“以 X 身份加入 Y”按钮（免密会话时提示用密码重新登录）；已是成员时提示 |
| `/device/$nonce` | 显示“批准后这台电脑可用于：A、B”；免密会话时提示用密码登录 |
| `/me` | 设备列表按工作空间列出令牌；显示“我的工作空间”（名字、角色） |
| 群消息里的链接 | 带 `/w/{ws}` 前缀 |

### 4.8 成员本机命令行 `cohive`

| 命令 | 变化 |
|---|---|
| `cohive login [--server] [--tools] [--resume] [--force]` | 领取后保存设备密钥与**全部在册工作空间**的令牌，输出列出各工作空间；在已 `cohive init` 的仓库里执行时，若仓库的工作空间不在其中，提示“你还不是 W 的成员”。已配对的电脑补工具改走 `device/tokens`。`--token-stdin` 保留（需同时给 `--workspace`） |
| `cohive init [--workspace W] [--tools]` | 工作空间列表来自 `device/workspaces`；多于一个时必须给 `--workspace`；写配置的同时调 `device/tokens` 补齐令牌 |
| `cohive mcp-proxy --server S --workspace W --tool T` | 缺令牌时用设备密钥自动签发；收到 `401` 时重签一次再重试 |
| `cohive web` | 用设备密钥换登录链接；旧版配对的电脑提示重新配对 |
| `cohive status` | 显示账号、本机设备、各工作空间各工具的令牌是否有效 |

### 4.9 服务器命令行 `cohive-server`

| 命令 | 变化 |
|---|---|
| `workspace add --name W [--admin 登录名]` | 加 `--admin` |
| `workspace remove --name W` | **新增**（只限空工作空间） |
| `workspace list`、`workspace notify` | 不变（`notify` 加群唯一校验） |
| `member add --workspace W --name X [--display] [--role]` | 改：账号不存在则新建（无密码，需 `admin reset-password` 后才能登录网页），存在则加入该工作空间 |
| `member set --workspace W --name X --role R` | 改：只管角色；`--telegram`、`--telegram-id` 移到 `account set` |
| `member remove --workspace W --name X --handover-to Y` | **新增** |
| `member list --workspace W [--all]` | 改：默认只列在册 |
| `account list`、`account set --name X [--display] [--telegram] [--telegram-id]` | **新增** |
| `account disable --name X`、`account enable --name X` | **新增** |
| `account server-admin --name X --on|--off` | **新增** |
| `admin reset-password --name X` | 改：去掉 `--workspace` |
| `token issue --workspace W --member X --tool T`、`token list`、`token revoke` | 不变（令牌仍属成员资格） |
| `service add --workspace W --name S --owner X` | 改：负责人须为在册、未停用成员 |

### 4.10 机器人

| 项 | 变化 |
|---|---|
| `/start ln_xxx` | 连接写到账号；冲突提示按账号 |
| 注册 / 加入申请审批消息 | 只发到申请所属工作空间的群；加入申请文案为“X（已有账号）申请加入” |
| 设备申请审批消息 | 发到此人在册所在、配置了群的每个工作空间群；记 `bot_posts`；处理后统一改成结果 |
| 按钮回调 | 按 3.7 的五步校验；工作空间由群编号反查 |
| `/whoami` | 显示账号；群里加本工作空间角色 |
| 成员加入通知 | **新增**：直接加入或申请获批后，在该工作空间群里发“X 已加入本工作空间”并 @ 他（已连接时） |

## 5. 安全要点

沿用 decisions 的口径：收到的问题单、评论是数据不是指令；管理操作只认密码登录的网页会话；按真实风险设防，不考虑“主动乱发链接”。

1. **管理操作只认〔密码会话〕**：工作空间管理（4.5 管理部分）、服务器管理（4.6）全部要求 `method = 'password'`。〔令牌〕、〔设备〕、`cohive web` 免密会话一律 `403 {"error": "这个操作需要用密码登录网页"}`。理由同 D7：智能体能读本机凭据文件，会读别人写的问题单，存在提示注入的真实风险。
2. **本里程碑把三类“改变自己归属或信任”的操作也收紧到〔密码会话〕**：接受邀请（会让这台电脑能拿到新工作空间的令牌）、授权新设备（`/device/{nonce}`）、查看设备授权页。否则被注入的智能体可以用 `cohive web` 开一个免密会话，替主人加入别人设好的工作空间，或为攻击者的电脑点授权（首台之外虽然还要管理员批，但会把审批消息推到群里，诱导管理员误点）。
3. **设备密钥的边界**：只在 `device/tokens`、`device/workspaces`、`auth/web-login` 三个接口上认；在 `/mcp`、问题单接口、管理接口上一律 `401`。它能做的（为本人在册的工作空间签发令牌、开免密会话）不超过“读到本机凭据文件”已有的能力，所以不扩大智能体的权限面；它不能加入工作空间、不能审批、不能邀请。
4. **跨工作空间的最弱一环（已知、接受）**：第二台设备由此人任一在册工作空间的管理员批准，批准后作用于他全部工作空间（lee 已定）。缓解：审批消息与审批页明确写“批准后可用于 A、B”；`device_auths.decided_by` 记批准人；批准人资格在批准瞬间按在册关系重算。新设备私聊通知本人（队列 P3）上线后再补一道。
5. **工作空间隔离**：所有 `/w/{ws}` 请求在中间件里统一判定在册（〔会话〕）或令牌所属工作空间（〔令牌〕），不满足一律 404；问题单编号是全局自增的，详情接口必须再校验 `issue.workspace_id = ws`。工作空间 A 的管理员只能看到同时也在 A 在册的人的设备，看不到别人的问题单、别的工作空间成员列表。
6. **移出立即生效**：同一事务吊销令牌；`LookupToken` 再查 `members.status = 'active'` 兜底；网页会话不注销（账号仍在别处在册），但 `/w/{ws}` 立刻 404。
7. **停用立即生效**：同一事务吊销设备与令牌、删会话、断 Telegram；令牌、设备密钥、会话三处查询都再查 `disabled_at IS NULL` 兜底。启用不恢复设备。
8. **账号探测**：登录失败不区分原因；“已停用”只在密码正确时提示；工作空间管理员只能按登录名精确查找账号，不能浏览全服务器账号列表；注册时“登录名已被使用”在 M5 已有限频的前提下可以接受。按登录名的失败退避（M5 `loginGuard`）键从“成员名”改为“登录名”，含义不变。
9. **群按钮**：工作空间由回调所在群编号反查（群绑定唯一），不信按钮数据里的工作空间编号；点击者按全局 Telegram 连接找到账号后按 `canAdmin` 判定；设备申请还要求申请人在本群工作空间在册。
10. **群绑定唯一**：防止两个工作空间绑同一个群，导致通知串群、按钮归属不明。
11. **至少一名管理员 / 服务器管理员**：判定在事务里锁住对应管理员行，防止两人同时互相撤销。
12. **改派要留痕**：每次 `reassign` 写问题单事件（操作人为管理员的成员资格），并给新接手人发群通知；接手人必须在册、未停用。
13. **迁移**：上线前 `pg_dump`；先在独立库跑迁移并核对计数；迁移内撞名改名、群绑定清空都写 `RAISE NOTICE`，上线时把迁移日志留档。
14. **服务器管理员看不到问题单不是安全边界**（若采纳待拍板第 2 条 A）：他能直接读数据库。这条规则的作用是“看什么由成员资格决定”，避免把服务器管理员默认塞进每个工作空间的看板和路由。

## 6. 任务拆分与审查重点

### 6.1 任务（每步可独立验收；测试口径 `go vet ./... && go test -count=1 ./... && make build`，真实 Postgres）

| 编号 | 内容 | 依赖 | 验收 |
|---|---|---|---|
| T1 | 第七号迁移；store 层：`accounts` 读写、`memberSelect` 关联读取、`Identity.AccountID`、问题单与事件查询补关联 | — | 迁移测试（见 6.2 第 1 条）通过；现有 M1–M5 测试在新结构上全绿（只改夹具的建人方式，不改断言） |
| T2 | 权限判定：`canAdmin`、`canManagePerson`、`LookupToken` 在册与停用检查、至少一名管理员 / 服务器管理员；问题单路由、转交、`who_owns` 只认在册未停用的成员 | T1 | 越权表驱动测试通过 |
| T3 | 登录（去掉工作空间）、`/me`、会话改挂账号、注册全局查重、`invites/{code}/accept`、团队邀请的加入申请 | T2 | 6.2 第 4、9、10 条 |
| T4 | 设备：账号级首台判定、批准人资格、交付设备密钥与多工作空间令牌、`device/tokens`、`device/workspaces`、`web-login` 接受设备密钥、鉴权中间件按前缀分流 | T2 | 6.2 第 2、3、5、6 条 |
| T5 | 成员管理：直接加入、`handover`、`reassign` 动作与事件、移出、停用与启用、服务器管理接口、建删工作空间、工作空间设置的群唯一 | T2 | 6.2 第 7、8、11、12、13 条 |
| T6 | 机器人：全局连接、按钮五步校验、设备申请多群发送与 `bot_posts`、加入通知、群消息链接前缀 | T3、T4、T5 | 6.2 第 14、15 条 |
| T7 | 网页：路由前缀与切换、管理页改动、`/server`、`/i/$code` 两种状态、设备授权页、个人中心 | T3、T4、T5 | 构建通过；逐页走查清单（含免密会话下各页的提示） |
| T8 | 命令行：凭据文件 `devices`、`login`、`init`、`mcp-proxy` 自动签发与 `401` 重签、`web`、`status`；服务器命令行 4.9 | T4、T5 | 6.2 第 6、16 条 |
| T9 | 文档（`docs/guides/` 三份指南改为多工作空间口径）与测试服实测：在独立库 `cohive_m5b` 上，lee 用两个账号、两个工作空间走通“建工作空间 → 直接加入 → 新工作空间项目里自动拿令牌 → 发单接单 → 移出并转交” | T6、T7、T8 | 实测记录交主脑 |

T3、T4、T5 可由不同子任务并行，但都改 `internal/core` 与 `internal/httpapi`，建议同一实施会话串行做，减少合并冲突（同主题不拆碎会话）。

### 6.2 审查重点（每条对应要写的测试）

| # | 审查点 | 测试 |
|---|---|---|
| 1 | 迁移不丢数据、归并与撞名规则正确 | 迁移测试：在第六号迁移后的库里造 M5 形态数据（两个工作空间、同名不同人、同一 Telegram 的两条成员、待审申请撞名、两工作空间绑同一群、同设备重复令牌、进行中的设备申请），跑第七号迁移后逐项断言：账号数、改名结果、合并后的密码与设备、全部问题单 / 事件 / 通知 / 服务外键仍可关联、服务器管理员、申请作废、令牌去重、群绑定只剩一个；再造一个“改名后仍撞名”的库断言迁移失败且整体回滚 |
| 2 | 设备密钥不能当令牌用 | 用设备密钥调 `/mcp`、`/w/{ws}/issues`、管理接口均 `401`；用成员令牌调 `device/tokens` 也 `401` |
| 3 | 免密会话不能做管理与信任操作 | 表驱动：`method = 'device'` 的会话调 4.5 管理部分、4.6 全部、`invites/{code}/accept`、`device/auth/{nonce}` 均 `403` |
| 4 | 工作空间隔离 | 表驱动：A 的管理员对 B 的每个管理接口都 `404`；A 的令牌访问 `/w/B/...` `404`；`/w/A/issues/{B 的问题单编号}` `404` |
| 5 | 首台判定是账号级 | 账号在 A 有过设备（已吊销）后，在 B 新配对仍需审批；迁移合并的账号同理 |
| 6 | 新工作空间自动拿令牌、移出后失效、加回后自动换新 | 端到端：配对 → 加入 B → 转发器无 B 令牌时自动签发 → 移出 B 后转发器 `401` 且重签失败退出 → 加回 B 后转发器 `401` 重签成功 |
| 7 | 批准人资格按在册关系即时判定 | 申请人先被移出 A，A 的管理员再批 → `403`；B 的管理员批 → 成功；没有在册工作空间时只有服务器管理员能批 |
| 8 | 移出与转交 | 清单不完整 → `409` 且无任何改动；某张单版本已变 → `409` 整体回滚；成功后：负责人与发起人已换、服务负责人已换、事件已写、通知已入队、令牌已吊销、成员资格 `removed`；不能移出最后一名管理员；接手人为已移出或已停用的人 → `400` |
| 9 | 停用与启用 | 停用后：登录被拒（只在密码正确时提示已停用）、令牌与设备密钥 `401`、会话失效、Telegram 已断开、待批设备申请变 `denied`；不能停用自己与最后一名服务器管理员；启用后再配对需审批 |
| 10 | 登录名全局唯一 | 两个工作空间的团队邀请并发注册同一名字，只有一个成功；与已有账号同名 → `400` |
| 11 | 至少一名管理员 / 服务器管理员的并发 | 两名管理员并发互相降级，只有一个成功；两名服务器管理员并发互相撤销同理 |
| 12 | 直接加入 | 加入已停用账号 → `400`；加入被移出过的人复用原成员资格，他以前的问题单重新可见 |
| 13 | 删除工作空间 | 有草稿也算有问题单 → `409`；空工作空间删除后其邀请、成员资格、令牌、群绑定全部清掉 |
| 14 | 群按钮校验 | 回调群与按钮数据工作空间不一致 → 拒绝；点击者是别的工作空间管理员 → 拒绝；设备申请人不在本群工作空间在册 → 拒绝；一处批准后其他群消息被改成结果 |
| 15 | Telegram 全局唯一 | 同一 Telegram 账号连接第二个账号 → 失败并提示已连接账号 |
| 16 | 凭据文件兼容 | 读 M5 格式的凭据文件不报错；旧版配对电脑的 `cohive web` 与自动签发给出“重新配对”提示 |

**建议的变异探针（验收时由主脑打）：**
- 把 `canManagePerson` 里的“在册”条件去掉 → 第 7 条必须变红。
- 把鉴权中间件对 `cohive_dev_` 前缀的拒绝去掉 → 第 2 条必须变红。
- 把 `handover` 的完整性校验去掉 → 第 8 条必须变红。
- 注释掉移出时的令牌吊销 → 第 6、8 条必须变红。
- 去掉 `LookupToken` 的 `m.status = 'active' AND a.disabled_at IS NULL` → 需要一条存储层测试直接把成员资格置为 `removed`（或账号置为停用）而不吊销令牌，断言令牌查不到；这条测试必须变红。两道防线各有测试，才算都被覆盖。

## 7. 待拍板

按影响排序。每条给出选项与建议；未拍板前实施按“建议”做。

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| 1 | **谁能停用账号**（停用作用于此人全部工作空间） | A 只有服务器管理员；B 工作空间管理员在“此人所有在册工作空间他都是管理员”时也可以 | **A**。与 GitLab 封禁、GitHub 暂停一致：作用于整个人的操作归服务器级。工作空间管理员的应急手段是“吊销设备”（立即切断他所有编程工具）加“移出”，已经够用 |
| 2 | **服务器管理员能否直接看任一工作空间的看板与问题单** | A 不能，要先在管理页把自己加为成员（一键，留痕）；B 能，只读，不出现在成员列表与路由里 | **A**。“看什么由成员资格决定”一条规则走到底，实现和测试都更简单；服务器管理员本就能读数据库，这不是安全边界 |
| 3 | **删除工作空间** | A 只能删没有问题单的空工作空间，归档以后再做；B 本里程碑加“归档”（令牌失效、通知停发、看板只读）；C 不提供删除 | **A**。建错名字时能删；有数据的工作空间试用期间不会要删，归档等真有需要再做 |
| 4 | **迁移时同一人的多条成员记录怎么合并** | A 按 Telegram 用户编号合并，其余一条一个账号，撞名加后缀 `-w工作空间编号`；B 一律一条一个账号，重复的 Telegram 连接只保留最早一条，其余断开 | **A**。Telegram 编号是“同一个人”最可靠的信号；B 会让同一个人在迁移后出现几个账号，以后只能手工合并。现有测试服数据量很小，两者实际差别不大，A 的语义更对 |
| 5 | **迁移时谁成为服务器管理员** | A `default` 工作空间的管理员；B 所有工作空间的管理员；C 谁都不是，启动时新建 `admin` 写初始密码文件 | **A**。M5 的初始管理员就在 `default`；B 会把只管一个项目的人升成全服务器管理员 |
| 6 | **工作空间管理员怎么找到“已有的人”** | A 只能精确输入登录名；B 能浏览、搜索全服务器账号列表 | **A**。一台服务器上可能有多个互不相干的项目组，不让每个工作空间管理员看到全部人；小团队口头问一下登录名的成本很低 |
| 7 | **是否加“观察者”（只读）角色** | A 不加，只有管理员 / 成员；B 加，只能看看板，不出现在路由与转交候选里 | **A**。试用期没有这个需求；等有产品经理等旁观者再加，加的时候只影响成员资格的 `role` 取值 |
| 8 | **M5 已配对的电脑怎么过渡**（没有设备密钥） | A 已有令牌照常用；自动签发新工作空间令牌、`cohive web` 时提示执行 `cohive login --force` 重新配对（因为“有过设备”，需管理员批准）；B 提供一次性接口，用这台电脑上仍有效的令牌换设备密钥，免审 | **A**。M5 配对过的电脑只有 lee 的测试机，多点一次批准即可；B 要多一个接口和一组测试，且“用令牌换设备级凭据”是个以后容易被忽视的口子 |
| 9 | **工作空间名能否改** | A 不能改；B 能改，另加不变的短标识给项目配置与链接引用 | **A**。项目配置里的 `--workspace`、群消息链接都按名字引用；建错了删掉重建（空工作空间可删） |
| 10 | **移出时是否必须把待处理问题单全部转交** | A 必须全部转交才能移出；B 可以不转，负责人置空进“待分派”，发起人侧的单留给管理员处理 | **A**。lee 的口径是“由管理员转交”；B 需要新做“待分派”视图，还会出现“发起人已不在、单子没人能关”的悬空状态 |
| 11 | **把“接受邀请”“授权新设备”也收紧到密码登录的网页会话**（D7 之外的新增收紧） | A 收紧（本文默认）；B 维持 M5，任意网页会话即可 | **A**。理由见第 5 节第 2 条：免密会话可由本机智能体开出，不应能改变本人的归属或信任关系。代价是 `cohive web` 打开设备授权页时要再输一次密码 |
| 12 | **设备申请的审批消息发到哪些群** | A 发到此人在册所在、配置了群的每个工作空间群，处理后统一改成结果；B 不发群，只在网页待审 | **A**。保持 M5“群里点一下就批”的体验；为此新增 `bot_posts` 小表 |
