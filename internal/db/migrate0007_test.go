package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// m5Fixture 是第六号迁移之后、M5 形态（每个工作空间一套成员）的数据。
const m5Fixture = `
INSERT INTO workspaces (id, name, notify_channel, notify_target) VALUES
  (1, 'default', '', ''), (2, 'pay', 'telegram', '-100'), (3, 'ops', 'telegram', '-100');

INSERT INTO members (id, workspace_id, name, display_name, role, password_hash, must_change_password, telegram_user_id, telegram_username) VALUES
  (1, 1, 'admin', '管理员', 'admin', 'h_admin', true,  NULL, ''),
  (2, 1, 'lee',   'Lee',    'member','h_lee1',  false, 100,  'leetg'),
  (3, 2, 'lee',   'Lee2',   'member','h_lee2',  true,  100,  'leetg2'),
  (4, 2, 'ann',   'Ann',    'member','',        false, NULL, ''),
  (5, 3, 'ann',   'Ann3',   'member','h_ann5',  false, NULL, ''),
  (6, 2, 'bob',   'Bob',    'admin', 'h_bob',   false, 200,  'bobtg');

INSERT INTO devices (id, member_id, name) VALUES (1, 2, 'mac1'), (2, 3, 'mac2'), (3, 6, 'bobmac');

INSERT INTO tokens (id, member_id, tool, token_hash, device_id) VALUES
  (1, 2, 'claude-code', 'h1', 1),
  (2, 2, 'claude-code', 'h2', 1),
  (3, 3, 'codex',       'h3', 2),
  (4, 6, 'claude-code', 'h4', 3);

INSERT INTO device_auths (id, nonce, poll_secret_hash, tools, device_name, status, member_id, workspace_id, device_id, decided_by, expires_at) VALUES
  (1, 'da_1', 'p', '{claude-code}', 'x', 'pending',        NULL, NULL, NULL, NULL, now() + interval '1 hour'),
  (2, 'da_2', 'p', '{codex}',       'y', 'awaiting_admin', 3,    2,    NULL, NULL, now() + interval '1 hour'),
  (3, 'da_3', 'p', '{claude-code}', 'bobmac', 'approved',  6,    2,    3,    1,    now() + interval '1 hour'),
  (4, 'da_4', 'p', '{claude-code}', 'mac1',   'delivered', 2,    1,    1,    1,    now() + interval '1 hour');

INSERT INTO invites (id, workspace_id, code_hash, kind, created_by, expires_at) VALUES (1, 2, 'c', 'team', 6, now() + interval '1 day');

INSERT INTO signups (id, invite_id, workspace_id, name, password_hash, status, member_id, decided_by) VALUES
  (1, 1, 2, 'ann',  'hs1', 'pending',  NULL, NULL),
  (2, 1, 3, 'dora', 'hs2', 'pending',  NULL, NULL),
  (3, 1, 2, 'carl', 'hs3', 'pending',  NULL, NULL),
  (4, 1, 2, 'bob',  'hs4', 'approved', 6,    1);
-- 第六号迁移的待审唯一索引按工作空间：另一个工作空间里同名的待审申请
INSERT INTO signups (id, invite_id, workspace_id, name, password_hash, status) VALUES (5, 1, 3, 'carl', 'hs5', 'pending');

INSERT INTO link_requests (id, member_id, provider, nonce, status, expires_at) VALUES
  (1, 4, 'telegram', 'ln_1', 'pending', now() + interval '1 hour'),
  (2, 2, 'telegram', 'ln_2', 'linked',  now() + interval '1 hour');

INSERT INTO web_sessions (id_hash, member_id, via, expires_at) VALUES ('s1', 3, 'password', now() + interval '1 day');
INSERT INTO web_login_codes (code_hash, member_id, expires_at) VALUES ('w1', 5, now() + interval '1 hour');

INSERT INTO services (workspace_id, name, owner_id) VALUES (2, 'api', 6);
INSERT INTO issues (id, workspace_id, source_member_id, source_tool, owner_id, title, status) VALUES (1, 2, 3, 'codex', 6, '登录失败', 'OPEN');
INSERT INTO issue_events (issue_id, actor_member_id, actor_tool, action) VALUES (1, 3, 'codex', 'submit');
INSERT INTO notifications (workspace_id, issue_id, event, actor_member_id, actor_tool, recipients) VALUES (2, 1, 'assigned', 3, 'codex', '[6]');
`

func migratedM5(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0006"); err != nil {
		t.Fatalf("迁移到第六号失败：%v", err)
	}
	if _, err := pool.Exec(ctx, m5Fixture); err != nil {
		t.Fatalf("写入 M5 数据失败：%v", err)
	}
	if err := db.MigrateUpTo(ctx, pool, "0007"); err != nil {
		t.Fatalf("第七号迁移失败：%v", err)
	}
	return pool
}

func TestMigrate0007Accounts(t *testing.T) {
	ctx := context.Background()
	pool := migratedM5(t)
	one := func(sql string, args ...any) any {
		t.Helper()
		var v any
		if err := pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%s：%v", sql, err)
		}
		return v
	}
	eq := func(what string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s：得到 %v，应为 %v", what, got, want)
		}
	}

	eq("账号数（lee 两条合并为一个）", one(`SELECT count(*) FROM accounts`), int64(5))
	// 合并：同一 Telegram 编号的两条成员记录归同一账号，取编号最小那条的名字与密码
	eq("lee 两条成员资格同一账号", one(`SELECT count(DISTINCT account_id) FROM members WHERE id IN (2, 3)`), int64(1))
	eq("lee 的密码取编号最小且非空的", one(`SELECT password_hash FROM accounts WHERE name = 'lee'`), "h_lee1")
	eq("lee 的改密标记随密码", one(`SELECT must_change_password FROM accounts WHERE name = 'lee'`), false)
	eq("lee 的 Telegram 用户名", one(`SELECT telegram_username FROM accounts WHERE name = 'lee'`), "leetg")
	eq("lee 的显示名", one(`SELECT display_name FROM accounts WHERE name = 'lee'`), "Lee")
	// 撞名：不同人同名，编号大的加后缀
	eq("ann 第一条保留原名", one(`SELECT a.name FROM members m JOIN accounts a ON a.id = m.account_id WHERE m.id = 4`), "ann")
	eq("ann 第二条加后缀", one(`SELECT a.name FROM members m JOIN accounts a ON a.id = m.account_id WHERE m.id = 5`), "ann-w3")
	eq("ann-w3 带上自己的密码", one(`SELECT password_hash FROM accounts WHERE name = 'ann-w3'`), "h_ann5")
	// 服务器管理员：只有 default 的管理员
	eq("admin 是服务器管理员", one(`SELECT is_server_admin FROM accounts WHERE name = 'admin'`), true)
	eq("pay 的管理员不是服务器管理员", one(`SELECT is_server_admin FROM accounts WHERE name = 'bob'`), false)
	eq("bob 仍是 pay 的管理员", one(`SELECT role FROM members WHERE id = 6`), "admin")
	eq("成员资格默认在册", one(`SELECT count(*) FROM members WHERE status = 'active'`), int64(6))

	// 设备归账号；已批准未领取的设备与令牌被吊销；重复令牌只留最新
	eq("lee 的两台设备", one(`SELECT count(*) FROM devices d JOIN accounts a ON a.id = d.account_id WHERE a.name = 'lee'`), int64(2))
	eq("已批准未领的设备吊销", one(`SELECT revoked_at IS NOT NULL FROM devices WHERE id = 3`), true)
	eq("已批准未领的设备上的令牌吊销", one(`SELECT revoked_at IS NOT NULL FROM tokens WHERE id = 4`), true)
	eq("重复令牌旧的吊销", one(`SELECT revoked_at IS NOT NULL FROM tokens WHERE id = 1`), true)
	eq("重复令牌新的保留", one(`SELECT revoked_at IS NULL FROM tokens WHERE id = 2`), true)
	eq("其他令牌不动", one(`SELECT revoked_at IS NULL FROM tokens WHERE id = 3`), true)

	// 设备申请
	eq("进行中的申请作废", one(`SELECT count(*) FROM device_auths WHERE id IN (1, 2, 3) AND status = 'expired'`), int64(3))
	eq("已交付的不动", one(`SELECT status FROM device_auths WHERE id = 4`), "delivered")
	eq("申请人换成账号", one(`SELECT a.name FROM device_auths x JOIN accounts a ON a.id = x.account_id WHERE x.id = 4`), "lee")
	eq("审批人换成账号", one(`SELECT a.name FROM device_auths x JOIN accounts a ON a.id = x.decided_by WHERE x.id = 4`), "admin")

	// 注册申请
	eq("与已有账号撞名的待审申请作废", one(`SELECT status FROM signups WHERE id = 1`), "rejected")
	eq("不撞名的待审申请保留", one(`SELECT status FROM signups WHERE id = 2`), "pending")
	eq("两个工作空间同名待审，编号小的保留", one(`SELECT status FROM signups WHERE id = 3`), "pending")
	eq("两个工作空间同名待审，编号大的作废", one(`SELECT status FROM signups WHERE id = 5`), "rejected")
	eq("已批准申请回填账号", one(`SELECT a.name FROM signups s JOIN accounts a ON a.id = s.account_id WHERE s.id = 4`), "bob")
	eq("已批准申请的审批人", one(`SELECT a.name FROM signups s JOIN accounts a ON a.id = s.decided_by WHERE s.id = 4`), "admin")

	eq("邀请创建人换成账号", one(`SELECT a.name FROM invites i JOIN accounts a ON a.id = i.created_by WHERE i.id = 1`), "bob")
	eq("进行中的连接请求作废", one(`SELECT status FROM link_requests WHERE id = 1`), "failed")
	eq("连接请求归账号", one(`SELECT a.name FROM link_requests l JOIN accounts a ON a.id = l.account_id WHERE l.id = 1`), "ann")
	eq("网页会话归账号", one(`SELECT a.name FROM web_sessions s JOIN accounts a ON a.id = s.account_id`), "lee")
	eq("网页登录码归账号", one(`SELECT a.name FROM web_login_codes c JOIN accounts a ON a.id = c.account_id`), "ann-w3")

	// 问题单、事件、通知、服务的外键不动，经成员资格仍能关联到人
	eq("问题单发起人", one(`SELECT a.name FROM issues i JOIN members m ON m.id = i.source_member_id JOIN accounts a ON a.id = m.account_id`), "lee")
	eq("问题单负责人", one(`SELECT a.name FROM issues i JOIN members m ON m.id = i.owner_id JOIN accounts a ON a.id = m.account_id`), "bob")
	eq("事件操作人", one(`SELECT a.name FROM issue_events e JOIN members m ON m.id = e.actor_member_id JOIN accounts a ON a.id = m.account_id`), "lee")
	eq("通知操作人", one(`SELECT m.workspace_id FROM notifications n JOIN members m ON m.id = n.actor_member_id`), int64(2))
	eq("服务负责人", one(`SELECT a.name FROM services s JOIN members m ON m.id = s.owner_id JOIN accounts a ON a.id = m.account_id`), "bob")

	// 一个群只绑一个工作空间
	eq("编号小的保留群绑定", one(`SELECT notify_target FROM workspaces WHERE id = 2`), "-100")
	eq("编号大的清空群绑定", one(`SELECT notify_target FROM workspaces WHERE id = 3`), "")
	eq("编号大的关闭群通知", one(`SELECT notify_channel FROM workspaces WHERE id = 3`), "")

	// 结构：members 上的个人列已删除，新唯一约束生效
	eq("members 不再有 name 列", one(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'members' AND column_name IN ('name','password_hash','telegram_user_id')`), int64(0))
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (name, telegram_user_id) VALUES ('zz', 100)`); err == nil {
		t.Error("Telegram 编号应全服务器唯一")
	}
	if _, err := pool.Exec(ctx, `UPDATE workspaces SET notify_channel = 'telegram', notify_target = '-100' WHERE id = 3`); err == nil {
		t.Error("一个群应只能绑一个工作空间")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO signups (invite_id, workspace_id, name, password_hash) VALUES (1, 2, 'dora', 'x')`); err == nil {
		t.Error("新人待审申请的登录名应全局占位")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO members (workspace_id, account_id) SELECT 2, account_id FROM members WHERE id = 3`); err == nil {
		t.Error("同一账号在同一工作空间只能有一条成员资格")
	}
}

func TestMigrate0007AbortsOnUnresolvableName(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0006"); err != nil {
		t.Fatal(err)
	}
	// zed 在两个工作空间：第二条应改为 zed-w2，但已有人就叫 zed-w2
	if _, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name) VALUES (1, 'default'), (2, 'pay'), (3, 'ops');
		INSERT INTO members (workspace_id, name) VALUES (1, 'zed'), (2, 'zed'), (3, 'zed-w2');`); err != nil {
		t.Fatal(err)
	}
	err := db.Migrate(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "zed-w2") {
		t.Fatalf("应中止迁移并指出重复的登录名，得到 %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'accounts'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("迁移失败应整体回滚，不应留下 accounts 表")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM members WHERE name = 'zed'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("迁移失败后原数据应原样保留：%d %v", n, err)
	}
}
