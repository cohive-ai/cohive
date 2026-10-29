-- 第十五号迁移：工作空间归档。
-- 归档后的工作空间只读（只能查看，不能改），不再催办、不再发群通知；取消归档即恢复。
-- 只加可空、无默认值的列：PostgreSQL 加这类列不重写表，可以在有数据的库（含测试服）上直接执行。
-- archived_at 非空即为已归档；archived_by 是执行归档的账号（服务器命令行不归档，所以一定有账号）。
ALTER TABLE workspaces
    ADD COLUMN archived_at timestamptz,
    ADD COLUMN archived_by bigint REFERENCES accounts(id);
