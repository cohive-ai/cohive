-- 第十七号迁移：通知可以是发给某个成员的私聊（关注者、留言里被 @ 的人改为机器人私聊，不再进群消息）。
-- 只加一个可空、无默认值的列：PostgreSQL 不重写表，可以在有数据的库（含测试服）上直接执行。
ALTER TABLE notifications ADD COLUMN direct_member_id bigint REFERENCES members(id);  -- 非空表示这是一条发给该成员的私聊（Recipients 为空，原因记在 watchers / mentions 里）
