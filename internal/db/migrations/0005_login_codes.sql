-- 登录码：成员私聊机器人领取，一次性、限时，换取令牌。成员按 Telegram 用户编号绑定。
ALTER TABLE members ADD COLUMN telegram_user_id bigint;
-- 同一个工作空间里，一个 Telegram 账号只能绑定一个成员（一个人一个身份，不同工具用不同令牌区分）
CREATE UNIQUE INDEX members_workspace_telegram_user ON members (workspace_id, telegram_user_id) WHERE telegram_user_id IS NOT NULL;
CREATE INDEX members_telegram_user_id ON members (telegram_user_id) WHERE telegram_user_id IS NOT NULL;

CREATE TABLE login_codes (
    id          bigserial PRIMARY KEY,
    member_id   bigint NOT NULL REFERENCES members(id),
    code_hash   bytea NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
