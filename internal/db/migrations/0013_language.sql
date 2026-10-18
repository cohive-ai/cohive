-- 第十三号迁移：多语言（方案 docs/superpowers/plans/2026-10-18-i18n-community.md 第 3 节）。
-- 1. 账号语言：个人设置，网页、成员命令行、MCP、会话钩子与发给本人的私聊消息都按它。
-- 2. 工作空间语言：群通知与群里机器人回复的语言，由工作空间管理员设置。
-- 3. 注册申请记下注册时的界面语言，获批建账号时写到账号上。
-- 三处都允许为空，空表示跟随服务器缺省语言（COHIVE_DEFAULT_LANGUAGE）；已有的账号、工作空间、申请一律留空，不批量写入某种语言。
-- 值是语言代码（如 en、zh-CN），由应用按已有的语言包校验；这里只限制字符与长度，社区新增语言不用再改表。
-- 只加可为空、没有默认值的列：PostgreSQL 只改系统目录，不重写表；CHECK 约束会扫描一遍现有行（全是空值，几张小表毫秒级），
-- 可以在有数据的库（含测试服）上直接执行。
ALTER TABLE accounts
    ADD COLUMN language text CHECK (language ~ '^[A-Za-z0-9-]{2,35}$');

ALTER TABLE workspaces
    ADD COLUMN language text CHECK (language ~ '^[A-Za-z0-9-]{2,35}$');

ALTER TABLE signups
    ADD COLUMN language text CHECK (language ~ '^[A-Za-z0-9-]{2,35}$');
