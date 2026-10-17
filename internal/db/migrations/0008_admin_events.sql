-- 管理操作记录：只追加。与业务操作在同一事务里写入，业务失败不留记录。
-- 附加信息里绝不放密码、密码哈希、令牌、邀请码、设备密钥等秘密。
CREATE TABLE admin_events (
    id                bigserial PRIMARY KEY,
    at                timestamptz NOT NULL DEFAULT now(),
    actor_account_id  bigint REFERENCES accounts(id),   -- 服务器命令行、系统为空；账号从不删除
    actor_name        text NOT NULL DEFAULT '',         -- 操作人登录名快照
    via               text NOT NULL CHECK (via IN ('web', 'telegram', 'server_cli', 'member_cli', 'system', 'unknown')),
    workspace_id      bigint,                           -- 工作空间可删除，不设外键
    workspace_name    text NOT NULL DEFAULT '',         -- 工作空间名快照
    action            text NOT NULL,
    target_kind       text NOT NULL,
    target_id         bigint,
    target_name       text NOT NULL DEFAULT '',
    detail            jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX admin_events_workspace ON admin_events (workspace_id, id DESC);
CREATE INDEX admin_events_actor ON admin_events (actor_account_id, id DESC);
CREATE INDEX admin_events_action ON admin_events (action, id DESC);

-- 拦截修改与删除：防程序缺陷与误操作，不防能改库结构的数据库管理员
CREATE FUNCTION admin_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '管理操作记录只能追加，不能修改或删除';
END
$$;
CREATE TRIGGER admin_events_no_change BEFORE UPDATE OR DELETE ON admin_events
    FOR EACH ROW EXECUTE FUNCTION admin_events_append_only();
CREATE TRIGGER admin_events_no_truncate BEFORE TRUNCATE ON admin_events
    FOR EACH STATEMENT EXECUTE FUNCTION admin_events_append_only();

-- 补历史：只补库里记了操作人的审批，入口一律记为“未知（功能上线前）”。
-- 不补：decided_by 为空的已决申请（signups 里是第七号迁移按撞名自动拒绝的；device_auths 里是本人授权的首台设备与本人拒绝）；
-- 其余管理操作（角色、邀请、吊销、加人移出、停用、服务器管理员、工作空间建删与设置、重置密码）库里没有操作人与时间，无从补。
-- device_auths 没有决定时间：批准的取设备创建时间（设备在批准时建），拒绝的取申请时间并标 time_approx。
INSERT INTO admin_events (at, actor_account_id, actor_name, via, workspace_id, workspace_name, action,
                          target_kind, target_id, target_name, detail)
SELECT h.at, h.actor_id, a.name, 'unknown', h.ws_id, coalesce(w.name, ''), h.action, h.kind, h.target_id, h.target_name, h.detail
FROM (
    SELECT s.decided_at AS at, s.decided_by AS actor_id, s.workspace_id AS ws_id,
           CASE WHEN s.name IS NULL THEN 'join.' ELSE 'signup.' END
             || CASE WHEN s.status = 'approved' THEN 'approve' ELSE 'reject' END AS action,
           'signup' AS kind, s.id AS target_id, coalesce(s.name, ap.name, '') AS target_name,
           jsonb_build_object('applicant', coalesce(s.name, ap.name, ''), 'backfilled', true) AS detail
    FROM signups s LEFT JOIN accounts ap ON ap.id = s.account_id
    WHERE s.decided_by IS NOT NULL AND s.decided_at IS NOT NULL AND s.status IN ('approved', 'rejected')
    UNION ALL
    SELECT coalesce(d.created_at, x.created_at), x.decided_by, NULL::bigint,
           CASE WHEN x.device_id IS NOT NULL THEN 'device.approve' ELSE 'device.deny' END,
           'device_auth', x.id, x.device_name,
           jsonb_build_object('owner', coalesce(ow.name, ''), 'device_name', x.device_name, 'tools', to_jsonb(x.tools),
                              'backfilled', true)
             || CASE WHEN d.id IS NULL THEN '{"time_approx": true}'::jsonb ELSE '{}'::jsonb END
    FROM device_auths x
    LEFT JOIN devices d ON d.id = x.device_id
    LEFT JOIN accounts ow ON ow.id = x.account_id
    WHERE x.decided_by IS NOT NULL
) h
JOIN accounts a ON a.id = h.actor_id
LEFT JOIN workspaces w ON w.id = h.ws_id
ORDER BY h.at, h.kind, h.target_id;

-- 注册申请里的密码哈希：拒绝的没有用处；批准的已复制进账号表，申请行上的副本在本人改密码后就是旧密码的哈希。都清掉。
UPDATE signups SET password_hash = NULL WHERE status IN ('rejected', 'approved') AND password_hash IS NOT NULL;
