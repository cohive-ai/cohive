-- 群通知：工作空间的通知配置、成员的平台标识、问题单的群通知简述、待发通知表。
ALTER TABLE workspaces
    ADD COLUMN notify_channel    text    NOT NULL DEFAULT '',
    ADD COLUMN notify_target     text    NOT NULL DEFAULT '',
    ADD COLUMN notify_show_title boolean NOT NULL DEFAULT true,
    ADD COLUMN notify_show_brief boolean NOT NULL DEFAULT true,
    ADD COLUMN public_url        text    NOT NULL DEFAULT '';

ALTER TABLE members ADD COLUMN telegram_username text NOT NULL DEFAULT '';

ALTER TABLE issues ADD COLUMN brief text NOT NULL DEFAULT '';

CREATE TABLE notifications (
    id               bigserial PRIMARY KEY,
    workspace_id     bigint NOT NULL REFERENCES workspaces(id),
    issue_id         bigint NOT NULL REFERENCES issues(id),
    event            text NOT NULL,
    actor_member_id  bigint NOT NULL REFERENCES members(id),
    actor_tool       text NOT NULL,
    recipients       jsonb NOT NULL DEFAULT '[]'::jsonb,
    status           text NOT NULL DEFAULT 'pending',
    attempts         integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    last_error       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    sent_at          timestamptz
);

CREATE INDEX notifications_due ON notifications (next_attempt_at) WHERE status = 'pending';
