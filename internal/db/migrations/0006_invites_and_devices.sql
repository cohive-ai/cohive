-- 成员角色与网页账号
ALTER TABLE members ADD COLUMN role text NOT NULL DEFAULT 'member';
ALTER TABLE members ADD COLUMN password_hash text NOT NULL DEFAULT '';      -- bcrypt；为空表示没有设置密码
ALTER TABLE members ADD COLUMN must_change_password boolean NOT NULL DEFAULT false;

-- 设备：每台电脑一条，令牌挂在设备上；吊销设备即吊销它的全部令牌
CREATE TABLE devices (
    id          bigserial PRIMARY KEY,
    member_id   bigint NOT NULL REFERENCES members(id),
    name        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
-- 管理员手工签发的令牌（cohive-server token issue）不属于任何设备
ALTER TABLE tokens ADD COLUMN device_id bigint REFERENCES devices(id);

-- 邀请：personal（一次性，注册即生效）、team（可多人使用，注册后需管理员批准）
CREATE TABLE invites (
    id            bigserial PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    code_hash     bytea NOT NULL UNIQUE,
    kind          text NOT NULL,
    role          text NOT NULL DEFAULT 'member',
    max_uses      integer NOT NULL DEFAULT 1,
    uses          integer NOT NULL DEFAULT 0,
    created_by    bigint REFERENCES members(id),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- 团队邀请的注册申请
CREATE TABLE signups (
    id             bigserial PRIMARY KEY,
    invite_id      bigint NOT NULL REFERENCES invites(id),
    workspace_id   bigint NOT NULL REFERENCES workspaces(id),
    name           text NOT NULL,
    display_name   text NOT NULL DEFAULT '',
    password_hash  text NOT NULL,
    status         text NOT NULL DEFAULT 'pending',  -- pending、approved、rejected
    member_id      bigint REFERENCES members(id),
    decided_by     bigint REFERENCES members(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    decided_at     timestamptz
);
-- 同一工作空间里待审的成员名不能重复
CREATE UNIQUE INDEX signups_pending_name ON signups (workspace_id, name) WHERE status = 'pending';

-- 设备授权：终端发起，成员在浏览器里授权；非首台设备还需管理员批准
CREATE TABLE device_auths (
    id                bigserial PRIMARY KEY,
    nonce             text NOT NULL UNIQUE,
    poll_secret_hash  bytea NOT NULL,
    tools             text[] NOT NULL,
    device_name       text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT 'pending',  -- pending、awaiting_admin、approved、delivered、denied、expired
    member_id         bigint REFERENCES members(id),
    workspace_id      bigint REFERENCES workspaces(id),
    device_id         bigint REFERENCES devices(id),
    decided_by        bigint REFERENCES members(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    expires_at        timestamptz NOT NULL
);
CREATE INDEX device_auths_awaiting ON device_auths (workspace_id) WHERE status = 'awaiting_admin';

-- 通讯工具连接：在个人中心发起，成员在通讯工具里打开带参数的链接完成绑定
CREATE TABLE link_requests (
    id          bigserial PRIMARY KEY,
    member_id   bigint NOT NULL REFERENCES members(id),
    provider    text NOT NULL,
    nonce       text NOT NULL UNIQUE,
    status      text NOT NULL DEFAULT 'pending',  -- pending、linked、failed
    detail      text NOT NULL DEFAULT '',
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- 网页会话与一次性网页登录码（cohive web）
CREATE TABLE web_sessions (
    id_hash     bytea PRIMARY KEY,
    member_id   bigint NOT NULL REFERENCES members(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

CREATE TABLE web_login_codes (
    code_hash   bytea PRIMARY KEY,
    member_id   bigint NOT NULL REFERENCES members(id),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);
