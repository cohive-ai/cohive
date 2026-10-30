-- 第十六号迁移：问题单关注者（抄送、自行关注）与通知里的关注者、留言 @ 名单。
-- 只建新表、加带常量默认值的列：PostgreSQL 11 起加这类列不重写表，可以在有数据的库（含测试服）上直接执行。
CREATE TABLE issue_watchers (
    issue_id    bigint NOT NULL REFERENCES issues(id),
    member_id   bigint NOT NULL REFERENCES members(id),
    added_by    bigint NOT NULL REFERENCES members(id),  -- 自己关注时等于 member_id；发单抄送时是发起人
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, member_id)
);
CREATE INDEX issue_watchers_member ON issue_watchers (member_id);
ALTER TABLE notifications
    ADD COLUMN watchers jsonb NOT NULL DEFAULT '[]'::jsonb,  -- 关注者：告知性质，首行以“关注”列出
    ADD COLUMN mentions jsonb NOT NULL DEFAULT '[]'::jsonb;  -- 这条留言 @ 到的人（只就这一条）
ALTER TABLE member_alerts ADD COLUMN about text NOT NULL DEFAULT '';  -- kind 为 watching/mentioned 时记原事件名
