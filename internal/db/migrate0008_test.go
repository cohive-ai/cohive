package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// m5bFixture 是第七号迁移之后的数据：已有审批，但还没有管理操作记录。
const m5bFixture = `
INSERT INTO workspaces (id, name) VALUES (2, 'pay');
INSERT INTO accounts (id, name, display_name, password_hash) VALUES
  (1, 'admin', '管理员', 'h_admin'), (2, 'lee', 'Lee', 'h_lee'), (3, 'ann', 'Ann', 'h_ann'), (4, 'bob', 'Bob', 'h_bob');
INSERT INTO members (id, workspace_id, account_id, role) VALUES (1, 2, 1, 'admin'), (2, 2, 2, 'member'), (3, 2, 4, 'member');
INSERT INTO invites (id, workspace_id, code_hash, kind, created_by, expires_at) VALUES (1, 2, 'c', 'team', 1, now() + interval '1 day');

INSERT INTO signups (id, invite_id, workspace_id, account_id, name, password_hash, status, member_id, decided_by, decided_at) VALUES
  (1, 1, 2, 2,    'lee',  'hs1', 'approved', 2,    1,    '2026-10-01 10:00+00'),  -- 管理员批准的新人注册
  (2, 1, 2, NULL, 'carl', 'hs2', 'rejected', NULL, 1,    '2026-10-02 10:00+00'),  -- 管理员拒绝的新人注册
  (3, 1, 2, 4,    NULL,   NULL,  'approved', 3,    1,    '2026-10-03 10:00+00'),  -- 管理员批准的加入申请
  (4, 1, 2, NULL, 'dora', 'hs4', 'rejected', NULL, NULL, '2026-10-04 10:00+00'),  -- 第七号迁移按撞名自动拒绝
  (5, 1, 2, NULL, 'eve',  'hs5', 'pending',  NULL, NULL, NULL);

INSERT INTO devices (id, account_id, name, created_at, revoked_at) VALUES
  (1, 2, 'mac-lee',  '2026-10-05 10:00+00', NULL),
  (2, 3, 'mac-ann',  '2026-10-06 10:00+00', '2026-10-06 11:00+00'),
  (3, 4, 'mac-bob',  '2026-09-30 10:00+00', NULL);
INSERT INTO device_auths (id, nonce, poll_secret_hash, tools, device_name, status, account_id, device_id, decided_by, created_at, expires_at) VALUES
  (1, 'n1', 'p', '{claude-code}', 'mac-lee', 'delivered', 2, 1,    1,    '2026-10-05 09:00+00', now()),  -- 管理员批准并已领取
  (2, 'n2', 'p', '{codex}',       'mac-ann', 'denied',    3, 2,    1,    '2026-10-06 09:00+00', now()),  -- 管理员批准后、领取前被吊销
  (3, 'n3', 'p', '{codex}',       'evil',    'denied',    3, NULL, 1,    '2026-10-07 09:00+00', now()),  -- 管理员拒绝
  (4, 'n4', 'p', '{claude-code}', 'mac-bob', 'delivered', 4, 3,    NULL, '2026-09-30 09:00+00', now());  -- 本人授权的首台设备
`

func TestMigrate0008BackfillsDecisions(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0007"); err != nil {
		t.Fatalf("迁移到第七号失败：%v", err)
	}
	if _, err := pool.Exec(ctx, m5bFixture); err != nil {
		t.Fatalf("写入第七号之后的数据失败：%v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第八号迁移失败：%v", err)
	}

	type row struct {
		at                         time.Time
		actor                      *int64
		actorName, via, ws, action string
		kind, target               string
		targetID                   int64
		wsID                       *int64
		detail                     map[string]any
	}
	rows, err := pool.Query(ctx, `SELECT at, actor_account_id, actor_name, via, workspace_id, workspace_name, action,
		target_kind, target_id, target_name, detail FROM admin_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []row
	for rows.Next() {
		var r row
		var raw []byte
		if err := rows.Scan(&r.at, &r.actor, &r.actorName, &r.via, &r.wsID, &r.ws, &r.action, &r.kind, &r.targetID, &r.target, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.detail); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []struct {
		at, action, kind, target, ws string
		targetID                     int64
		approx                       bool
	}{
		{"2026-10-01T10:00:00Z", "signup.approve", "signup", "lee", "pay", 1, false},
		{"2026-10-02T10:00:00Z", "signup.reject", "signup", "carl", "pay", 2, false},
		{"2026-10-03T10:00:00Z", "join.approve", "signup", "bob", "pay", 3, false},
		{"2026-10-05T10:00:00Z", "device.approve", "device_auth", "mac-lee", "", 1, false},
		{"2026-10-06T10:00:00Z", "device.approve", "device_auth", "mac-ann", "", 2, false},
		{"2026-10-07T09:00:00Z", "device.deny", "device_auth", "evil", "", 3, true},
	}
	if len(got) != len(want) {
		t.Fatalf("应补出 %d 条记录（自动拒绝与首台设备不补），得到 %d 条：%+v", len(want), len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if g.at.UTC().Format(time.RFC3339) != w.at || g.action != w.action || g.kind != w.kind || g.target != w.target ||
			g.targetID != w.targetID || g.ws != w.ws {
			t.Errorf("第 %d 条：得到 %s %s %s#%d %q 工作空间 %q，应为 %+v", i+1, g.at.UTC().Format(time.RFC3339), g.action, g.kind,
				g.targetID, g.target, g.ws, w)
		}
		if g.actor == nil || *g.actor != 1 || g.actorName != "admin" || g.via != "unknown" {
			t.Errorf("第 %d 条：操作人应为 admin、入口未知，得到 %v %q %q", i+1, g.actor, g.actorName, g.via)
		}
		if (w.ws == "") != (g.wsID == nil) {
			t.Errorf("第 %d 条：工作空间编号 %v 与名字 %q 不一致", i+1, g.wsID, g.ws)
		}
		if g.detail["backfilled"] != true {
			t.Errorf("第 %d 条：附加信息应标 backfilled：%v", i+1, g.detail)
		}
		if _, ok := g.detail["time_approx"]; ok != w.approx {
			t.Errorf("第 %d 条：time_approx 应为 %v：%v", i+1, w.approx, g.detail)
		}
	}
	if d := got[4].detail; d["owner"] != "ann" || d["device_name"] != "mac-ann" {
		t.Errorf("设备记录应带申请人与设备名：%v", d)
	}

	// 已决申请的密码哈希清掉，待审的保留
	var cleared, kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE password_hash IS NULL AND status <> 'pending'),
		count(*) FILTER (WHERE password_hash IS NOT NULL AND status = 'pending') FROM signups`).Scan(&cleared, &kept); err != nil {
		t.Fatal(err)
	}
	if cleared != 4 || kept != 1 {
		t.Fatalf("已决申请的密码哈希应全部清掉（4 条）、待审的保留（1 条），得到 %d、%d", cleared, kept)
	}
}
