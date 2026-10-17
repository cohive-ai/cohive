-- 会话内变动提醒：与状态变更同一事务、按收件人展开写入，与工作空间有没有配群无关。
-- 编程工具的会话钩子按“我的提醒编号”拉取；“已读”不在服务端（游标存在各会话本机）。
-- 只存单号、动作、谁，不存标题与正文。
CREATE TABLE member_alerts (
    id               bigserial PRIMARY KEY,
    workspace_id     bigint NOT NULL REFERENCES workspaces(id),
    member_id        bigint NOT NULL REFERENCES members(id),   -- 收件人
    issue_id         bigint NOT NULL REFERENCES issues(id),
    kind             text   NOT NULL,
    actor_member_id  bigint NOT NULL REFERENCES members(id),
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX member_alerts_member ON member_alerts (member_id, id);
