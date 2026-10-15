-- 登录码：成员私聊机器人领取，一次性、限时，换取令牌。成员按 Telegram 用户编号绑定。
ALTER TABLE members ADD COLUMN telegram_user_id bigint;
CREATE INDEX members_telegram_user_id ON members (telegram_user_id) WHERE telegram_user_id IS NOT NULL;

CREATE TABLE login_codes (
    id          bigserial PRIMARY KEY,
    member_id   bigint NOT NULL REFERENCES members(id),
    code_hash   bytea NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
