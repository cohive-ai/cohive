-- 成员角色：管理员可以审批加入申请
ALTER TABLE members ADD COLUMN role text NOT NULL DEFAULT 'member';

-- 加入工作空间默认需要管理员审批（同 GitLab 的申请加入）
ALTER TABLE workspaces ADD COLUMN join_requires_approval boolean NOT NULL DEFAULT true;

CREATE TABLE join_requests (
    id                 bigserial PRIMARY KEY,
    workspace_id       bigint NOT NULL REFERENCES workspaces(id),
    telegram_user_id   bigint NOT NULL,
    telegram_username  text NOT NULL DEFAULT '',
    display_name       text NOT NULL DEFAULT '',
    status             text NOT NULL DEFAULT 'pending',
    decided_by         bigint REFERENCES members(id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    decided_at         timestamptz
);
-- 同一工作空间同一账号只能有一条待审申请
CREATE UNIQUE INDEX join_requests_pending ON join_requests (workspace_id, telegram_user_id) WHERE status = 'pending';

-- 设备授权登录：终端发起，机器人私聊里点确认，终端凭轮询密钥取令牌
CREATE TABLE device_logins (
    id                 bigserial PRIMARY KEY,
    poll_secret_hash   bytea NOT NULL,
    start_nonce        text NOT NULL UNIQUE,
    tools              text[] NOT NULL,
    device_name        text NOT NULL DEFAULT '',
    workspace_name     text NOT NULL DEFAULT '',
    telegram_user_id   bigint,
    member_id          bigint REFERENCES members(id),
    status             text NOT NULL DEFAULT 'pending',
    expires_at         timestamptz NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
