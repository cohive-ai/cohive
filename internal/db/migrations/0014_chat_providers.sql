-- 第十四号迁移：通讯工具取值放开到已规划的全部平台，接 Slack（以及之后的飞书、Lark、Discord）时不必再改表。
-- 只换三个取值约束：通讯账号表与工作空间群表的 provider，管理操作记录的 via。
-- 安全性：新约束是旧约束的超集，现有行（只有 telegram、dingtalk 与原有途径）都满足；不改列、不重写表。
-- 加约束时会扫描一遍现有行并短暂锁表（几张小表，毫秒级），服务停着或跑着升级都可以。
ALTER TABLE chat_identities DROP CONSTRAINT chat_identities_provider_check;
ALTER TABLE chat_identities ADD CONSTRAINT chat_identities_provider_check
    CHECK (provider IN ('telegram', 'dingtalk', 'slack', 'feishu', 'lark', 'discord'));

ALTER TABLE workspace_chats DROP CONSTRAINT workspace_chats_provider_check;
ALTER TABLE workspace_chats ADD CONSTRAINT workspace_chats_provider_check
    CHECK (provider IN ('telegram', 'dingtalk', 'slack', 'feishu', 'lark', 'discord'));

ALTER TABLE admin_events DROP CONSTRAINT admin_events_via_check;
ALTER TABLE admin_events ADD CONSTRAINT admin_events_via_check
    CHECK (via IN ('web', 'telegram', 'dingtalk', 'slack', 'feishu', 'lark', 'discord', 'server_cli', 'member_cli', 'agent', 'system', 'unknown'));
