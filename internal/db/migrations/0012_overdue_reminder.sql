-- 第十二号迁移：超时催办。
-- 1. 问题单记下“进入当前状态（或换了当事人）的时间”与本轮已催到第几档：0 没催，1 已再 @ 当事人，2 已同时 @ 管理员（之后不再发）。
--    状态一变、负责人或发起人一换，就重新计时、档位归零（见 store.UpdateIssue、store.SetIssueSource）。
--    现有问题单从升级时刻开始计时，不拿历史时间补算，免得升级后群里一下子冒出一批催办。
-- 2. 工作空间的催办时长（小时）：待接单默认 4、待验证默认 24，0 表示关闭。
-- 只加带常量默认值的列与部分索引：PostgreSQL 11 起加这类列不重写表，可以在有数据的库（含测试服）上直接执行。
ALTER TABLE issues
    ADD COLUMN status_since timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN remind_level smallint NOT NULL DEFAULT 0 CHECK (remind_level BETWEEN 0 AND 2);

CREATE INDEX issues_remind_due ON issues (status_since)
    WHERE status IN ('OPEN', 'READY_FOR_VERIFICATION') AND remind_level < 2;

ALTER TABLE workspaces
    ADD COLUMN remind_open_hours   integer NOT NULL DEFAULT 4  CHECK (remind_open_hours BETWEEN 0 AND 720),
    ADD COLUMN remind_verify_hours integer NOT NULL DEFAULT 24 CHECK (remind_verify_hours BETWEEN 0 AND 720);
