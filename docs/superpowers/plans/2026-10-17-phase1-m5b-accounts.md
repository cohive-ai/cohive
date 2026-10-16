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
