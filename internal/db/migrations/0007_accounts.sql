-- 全局账号 + 工作空间成员资格（M5b）。
-- members 保留，含义改为“成员资格”：问题单、事件、通知、服务、令牌的外键都不动；
-- 登录名、显示名、密码、Telegram 连接移到 accounts；设备、会话、连接请求、审批人改挂账号。
-- 迁移规则见 docs/superpowers/plans/2026-10-17-phase1-m5b-accounts.md 第 2.3 节。

CREATE TABLE accounts (
    id                    bigserial PRIMARY KEY,
    name                  text NOT NULL UNIQUE,              -- 登录名，全局唯一，不可改
    display_name          text NOT NULL DEFAULT '',
    password_hash         text NOT NULL DEFAULT '',          -- bcrypt；为空表示没有设置密码
    must_change_password  boolean NOT NULL DEFAULT false,
    telegram_user_id      bigint,
    telegram_username     text NOT NULL DEFAULT '',
    is_server_admin       boolean NOT NULL DEFAULT false,
    disabled_at           timestamptz,                       -- 非空即停用
    disabled_by           bigint REFERENCES accounts(id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    legacy_leader         bigint                             -- 迁移用，本文件末尾删除
);

-- 1. 每条成员记录归哪个账号：Telegram 用户编号相同的归到编号最小的那条（leader），其余一条一个账号
CREATE TEMP TABLE m2a ON COMMIT DROP AS
SELECT id AS member_id,
       min(id) OVER (PARTITION BY COALESCE('tg:' || telegram_user_id::text, 'm:' || id::text)) AS leader
FROM members;

-- 2. 每个 leader 一个账号；登录名撞名时编号最小的保留原名，其余加后缀 -w工作空间编号
CREATE TEMP TABLE new_accounts ON COMMIT DROP AS
SELECT l.id AS leader,
       CASE WHEN l.rn = 1 THEN l.name
            ELSE left(l.name, 32 - length('-w' || l.workspace_id)) || '-w' || l.workspace_id END AS name,
       l.display_name, l.telegram_user_id, l.telegram_username
FROM (SELECT m.*, row_number() OVER (PARTITION BY m.name ORDER BY m.id) AS rn
      FROM members m WHERE m.id IN (SELECT leader FROM m2a)) l;

DO $$
DECLARE
    item text;
BEGIN
    SELECT string_agg(name, '、') INTO item
    FROM (SELECT name FROM new_accounts GROUP BY name HAVING count(*) > 1) d;
    IF item IS NOT NULL THEN
        RAISE EXCEPTION '第七号迁移中止：加后缀后登录名仍重复（%），请先手工改名再迁移', item;
    END IF;
    FOR item IN SELECT m.name || ' → ' || n.name FROM new_accounts n JOIN members m ON m.id = n.leader
                WHERE n.name <> m.name ORDER BY n.leader LOOP
        RAISE NOTICE '第七号迁移：登录名重复，已改名 %', item;
    END LOOP;
END $$;

INSERT INTO accounts (name, display_name, password_hash, must_change_password,
                      telegram_user_id, telegram_username, created_at, legacy_leader)
SELECT n.name, n.display_name,
       COALESCE(pw.password_hash, ''), COALESCE(pw.must_change_password, false),
       n.telegram_user_id, n.telegram_username,
       (SELECT min(m.created_at) FROM members m JOIN m2a ON m2a.member_id = m.id WHERE m2a.leader = n.leader),
       n.leader
FROM new_accounts n
LEFT JOIN LATERAL (SELECT m.password_hash, m.must_change_password
                   FROM members m JOIN m2a ON m2a.member_id = m.id
                   WHERE m2a.leader = n.leader AND m.password_hash <> ''
                   ORDER BY m.id LIMIT 1) pw ON true
ORDER BY n.leader;

-- 3. 成员资格挂到账号
ALTER TABLE members ADD COLUMN account_id bigint REFERENCES accounts(id);
UPDATE members m SET account_id = a.id
FROM m2a JOIN accounts a ON a.legacy_leader = m2a.leader
WHERE m.id = m2a.member_id;
ALTER TABLE members ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE members ADD CONSTRAINT members_account_workspace UNIQUE (account_id, workspace_id);
ALTER TABLE members
    ADD COLUMN status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed')),
    ADD COLUMN removed_at timestamptz,
    ADD COLUMN removed_by bigint REFERENCES accounts(id),
    ADD COLUMN added_by   bigint REFERENCES accounts(id);

-- 4. 服务器管理员：default 工作空间的管理员
UPDATE accounts a SET is_server_admin = true
FROM members m JOIN workspaces w ON w.id = m.workspace_id
WHERE m.account_id = a.id AND w.name = 'default' AND m.role = 'admin';

-- 5. 进行中的设备申请作废；已批准未领取的，批准时建的设备一并吊销（与 M5 过期未领的处理一致）
UPDATE tokens SET revoked_at = now()
WHERE revoked_at IS NULL AND device_id IN (SELECT device_id FROM device_auths WHERE status = 'approved' AND device_id IS NOT NULL);
UPDATE devices SET revoked_at = now()
WHERE revoked_at IS NULL AND id IN (SELECT device_id FROM device_auths WHERE status = 'approved' AND device_id IS NOT NULL);
UPDATE device_auths SET status = 'expired' WHERE status IN ('pending', 'awaiting_admin', 'approved');

-- 6. 设备属于账号；新增设备密钥（领取时才生成）
ALTER TABLE devices ADD COLUMN account_id bigint REFERENCES accounts(id);
UPDATE devices d SET account_id = m.account_id FROM members m WHERE m.id = d.member_id;
ALTER TABLE devices ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE devices DROP COLUMN member_id;
ALTER TABLE devices ADD COLUMN secret_hash bytea UNIQUE, ADD COLUMN last_used_at timestamptz;
CREATE INDEX devices_account ON devices (account_id);

-- 7. 设备申请属于账号；不再固定工作空间；审批人改指账号
ALTER TABLE device_auths
    ADD COLUMN account_id bigint REFERENCES accounts(id),
    ADD COLUMN decided_by_account bigint REFERENCES accounts(id);
UPDATE device_auths x SET account_id = m.account_id FROM members m WHERE m.id = x.member_id;
UPDATE device_auths x SET decided_by_account = m.account_id FROM members m WHERE m.id = x.decided_by;
DROP INDEX device_auths_awaiting;
ALTER TABLE device_auths DROP COLUMN member_id, DROP COLUMN workspace_id, DROP COLUMN decided_by;
ALTER TABLE device_auths RENAME COLUMN decided_by_account TO decided_by;
CREATE INDEX device_auths_awaiting ON device_auths (status) WHERE status = 'awaiting_admin';

-- 8. 注册申请：已有账号凭团队邀请申请加入时只有 account_id；新人获批后回填 account_id
ALTER TABLE signups
    ADD COLUMN account_id bigint REFERENCES accounts(id),
    ADD COLUMN decided_by_account bigint REFERENCES accounts(id);
UPDATE signups s SET account_id = m.account_id FROM members m WHERE m.id = s.member_id;
UPDATE signups s SET decided_by_account = m.account_id FROM members m WHERE m.id = s.decided_by;
ALTER TABLE signups DROP COLUMN decided_by;
ALTER TABLE signups RENAME COLUMN decided_by_account TO decided_by;
ALTER TABLE signups ALTER COLUMN name DROP NOT NULL, ALTER COLUMN password_hash DROP NOT NULL;
-- 待审新人申请的登录名改为全局占位：与已有账号或更早的待审申请撞名的，作废
UPDATE signups s SET status = 'rejected', decided_at = now()
WHERE s.status = 'pending' AND s.account_id IS NULL AND (
      EXISTS (SELECT 1 FROM accounts a WHERE a.name = s.name)
   OR EXISTS (SELECT 1 FROM signups o WHERE o.status = 'pending' AND o.account_id IS NULL AND o.name = s.name AND o.id < s.id));
DROP INDEX signups_pending_name;
CREATE UNIQUE INDEX signups_pending_new_name ON signups (name) WHERE status = 'pending' AND account_id IS NULL;
CREATE UNIQUE INDEX signups_pending_join ON signups (workspace_id, account_id) WHERE status = 'pending' AND account_id IS NOT NULL;

-- 9. 邀请创建人改指账号（服务器管理员不一定是该工作空间成员）
ALTER TABLE invites ADD COLUMN created_by_account bigint REFERENCES accounts(id);
UPDATE invites i SET created_by_account = m.account_id FROM members m WHERE m.id = i.created_by;
ALTER TABLE invites DROP COLUMN created_by;
ALTER TABLE invites RENAME COLUMN created_by_account TO created_by;

-- 10. 通讯连接、网页会话、一次性网页登录码改挂账号
UPDATE link_requests SET status = 'failed', detail = '系统升级，请重新连接' WHERE status = 'pending';
ALTER TABLE link_requests ADD COLUMN account_id bigint REFERENCES accounts(id);
UPDATE link_requests l SET account_id = m.account_id FROM members m WHERE m.id = l.member_id;
ALTER TABLE link_requests ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE link_requests DROP COLUMN member_id;

ALTER TABLE web_sessions ADD COLUMN account_id bigint REFERENCES accounts(id);
UPDATE web_sessions s SET account_id = m.account_id FROM members m WHERE m.id = s.member_id;
ALTER TABLE web_sessions ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE web_sessions DROP COLUMN member_id;
CREATE INDEX web_sessions_account ON web_sessions (account_id);

ALTER TABLE web_login_codes ADD COLUMN account_id bigint REFERENCES accounts(id);
UPDATE web_login_codes c SET account_id = m.account_id FROM members m WHERE m.id = c.member_id;
ALTER TABLE web_login_codes ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE web_login_codes DROP COLUMN member_id;

-- 11. 同一台电脑、同一成员资格、同一工具只留一枚有效令牌（保留最新的）
UPDATE tokens t SET revoked_at = now()
WHERE t.revoked_at IS NULL AND t.device_id IS NOT NULL AND EXISTS (
    SELECT 1 FROM tokens o WHERE o.revoked_at IS NULL AND o.device_id = t.device_id
      AND o.member_id = t.member_id AND o.tool = t.tool AND o.id > t.id);
CREATE UNIQUE INDEX tokens_device_member_tool ON tokens (device_id, member_id, tool)
    WHERE revoked_at IS NULL AND device_id IS NOT NULL;

-- 12. 一个群只绑一个工作空间：重复的保留编号最小的，其余清空群绑定
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN SELECT w.name, w.notify_target FROM workspaces w
             WHERE w.notify_target <> '' AND EXISTS (
                 SELECT 1 FROM workspaces o WHERE o.notify_channel = w.notify_channel
                   AND o.notify_target = w.notify_target AND o.id < w.id)
             ORDER BY w.id LOOP
        RAISE NOTICE '第七号迁移：工作空间 % 与更早的工作空间绑定了同一个群 %，已清空它的群绑定', r.name, r.notify_target;
    END LOOP;
END $$;
UPDATE workspaces w SET notify_channel = '', notify_target = ''
WHERE w.notify_target <> '' AND EXISTS (
    SELECT 1 FROM workspaces o WHERE o.notify_channel = w.notify_channel
      AND o.notify_target = w.notify_target AND o.id < w.id);
CREATE UNIQUE INDEX workspaces_notify_target ON workspaces (notify_channel, notify_target) WHERE notify_target <> '';
ALTER TABLE workspaces ADD COLUMN created_by bigint REFERENCES accounts(id);

-- 13. 群里的审批消息：设备申请会发到此人所在的多个群，处理后要把每条都改成结果
CREATE TABLE bot_posts (
    id            bigserial PRIMARY KEY,
    kind          text NOT NULL,          -- su（注册与加入申请）、da（设备申请）
    ref_id        bigint NOT NULL,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    chat_id       bigint NOT NULL,
    message_id    bigint NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bot_posts_ref ON bot_posts (kind, ref_id);

-- 14. 删掉 members 上已搬到 accounts 的列（相关唯一约束与索引随列一起删除）
ALTER TABLE members
    DROP COLUMN name,
    DROP COLUMN display_name,
    DROP COLUMN telegram_username,
    DROP COLUMN telegram_user_id,
    DROP COLUMN password_hash,
    DROP COLUMN must_change_password;
ALTER TABLE accounts DROP COLUMN legacy_leader;
CREATE UNIQUE INDEX accounts_telegram_user ON accounts (telegram_user_id) WHERE telegram_user_id IS NOT NULL;
CREATE INDEX members_account ON members (account_id);
