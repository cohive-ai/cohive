-- 第十一号迁移：通讯工具与成员、工作空间解耦，为接入钉钉做准备（第 0 步）。
-- 1. 成员的通讯账号拆成独立表 chat_identities（原 accounts.telegram_user_id / telegram_username）；
-- 2. 工作空间的群拆成独立表 workspace_chats（原 workspaces.notify_channel / notify_target）；
-- 3. 群里审批消息的群编号、消息编号改成文本（钉钉的是 openConversationId 与 outTrackId）；
-- 4. 通讯账号连接请求可先记下“谁发来了码”，网页确认后才生效；
-- 5. 群绑定码；管理操作记录的途径加上 dingtalk。
-- Telegram 数据无损迁移：只有用户名或只有编号的半连接状态照原样保留。

-- 1. 通讯账号：一个 Cohive 账号在每种通讯工具上最多连一个；一个通讯账号全服务器只能连一个 Cohive 账号
CREATE TABLE chat_identities (
    id            bigserial PRIMARY KEY,
    account_id    bigint NOT NULL REFERENCES accounts(id),
    provider      text   NOT NULL CHECK (provider IN ('telegram', 'dingtalk')),
    tenant        text   NOT NULL DEFAULT '',   -- Telegram 为空；钉钉为 corpId
    user_id       text   NOT NULL DEFAULT '',   -- Telegram 用户编号；钉钉 userId。空表示只登记了用户名
    handle        text   NOT NULL DEFAULT '',   -- Telegram 用户名（显示与旧式 @）
    display_name  text   NOT NULL DEFAULT '',   -- 钉钉昵称
    linked_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, provider),
    CHECK (user_id <> '' OR handle <> '')
);
CREATE UNIQUE INDEX chat_identities_user ON chat_identities (provider, tenant, user_id) WHERE user_id <> '';

INSERT INTO chat_identities (account_id, provider, user_id, handle)
SELECT id, 'telegram', coalesce(telegram_user_id::text, ''), telegram_username
FROM accounts WHERE telegram_user_id IS NOT NULL OR telegram_username <> '';

DROP INDEX IF EXISTS accounts_telegram_user;
ALTER TABLE accounts DROP COLUMN telegram_user_id, DROP COLUMN telegram_username;

-- 2. 工作空间的群：一个群只绑一个工作空间（群里点审批按钮时由群反查工作空间）；
--    每个工作空间每种通讯工具最多一个群（第一期业务层再限制为总共一个）
CREATE TABLE workspace_chats (
    id            bigserial PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    provider      text   NOT NULL CHECK (provider IN ('telegram', 'dingtalk')),
    tenant        text   NOT NULL DEFAULT '',
    chat_id       text   NOT NULL CHECK (chat_id <> ''),
    title         text   NOT NULL DEFAULT '',
    bound_by      bigint REFERENCES accounts(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, tenant, chat_id),
    UNIQUE (workspace_id, provider)
);

-- 原来“关闭群通知”是把 notify_channel 清空（群编号可能还留着），这类工作空间迁移后没有群
INSERT INTO workspace_chats (workspace_id, provider, chat_id)
SELECT id, notify_channel, notify_target FROM workspaces
WHERE notify_channel <> '' AND notify_target <> '' ORDER BY id;

DROP INDEX IF EXISTS workspaces_notify_target;
ALTER TABLE workspaces DROP COLUMN notify_channel, DROP COLUMN notify_target;

-- 3. 群里的审批消息
ALTER TABLE bot_posts ADD COLUMN provider text NOT NULL DEFAULT 'telegram';
ALTER TABLE bot_posts ALTER COLUMN provider DROP DEFAULT;
ALTER TABLE bot_posts ALTER COLUMN chat_id TYPE text USING chat_id::text,
                      ALTER COLUMN message_id TYPE text USING message_id::text;
CREATE INDEX bot_posts_message ON bot_posts (provider, message_id);

-- 4. 通讯账号连接请求：钉钉先记下发码的人（awaiting_confirm），网页确认后才连接
ALTER TABLE link_requests
    ADD COLUMN candidate_tenant  text NOT NULL DEFAULT '',
    ADD COLUMN candidate_user_id text NOT NULL DEFAULT '',
    ADD COLUMN candidate_name    text NOT NULL DEFAULT '';

-- 5. 群绑定码：工作空间管理员在网页生成，在群里 @机器人 发出后完成绑定
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

ALTER TABLE admin_events DROP CONSTRAINT admin_events_via_check;
ALTER TABLE admin_events ADD CONSTRAINT admin_events_via_check
    CHECK (via IN ('web', 'telegram', 'dingtalk', 'server_cli', 'member_cli', 'agent', 'system', 'unknown'));
