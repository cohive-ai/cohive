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
