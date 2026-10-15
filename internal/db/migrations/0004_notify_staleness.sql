-- 通知写入时记下问题单的状态与负责人，投递前核对；已有新进展的通知标为 skipped，不再发送。
ALTER TABLE notifications
    ADD COLUMN issue_status   text NOT NULL DEFAULT '',
    ADD COLUMN issue_owner_id bigint;
