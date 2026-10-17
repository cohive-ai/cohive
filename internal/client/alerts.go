package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// AlertItem 是一条与我有关的新动态：只有单号、动作、谁。
type AlertItem struct {
	ID      int64     `json:"id"`
	IssueID int64     `json:"issue_id"`
	Kind    string    `json:"kind"`
	Actor   string    `json:"actor"`
	At      time.Time `json:"at"`
}

// Alerts 是 GET /api/v1/w/{ws}/alerts 的结果。
type Alerts struct {
	Cursor  int64       `json:"cursor"`
	Items   []AlertItem `json:"items"`
	More    bool        `json:"more"`
	Pending struct {
		Count int `json:"count"`
		Top   []struct {
			IssueID int64  `json:"issue_id"`
			Needs   string `json:"needs"`
		} `json:"top"`
	} `json:"pending"`
}

// FetchAlerts 拉取与我有关的新动态；after 为空时只要待办摘要与当前游标。
func FetchAlerts(ctx context.Context, server, workspace, token string, after *int64) (Alerts, int, error) {
	path := "/api/v1/w/" + url.PathEscape(workspace) + "/alerts"
	if after != nil {
		path += "?after=" + strconv.FormatInt(*after, 10)
	}
	var out Alerts
	code, err := get(ctx, NormalizeServer(server), path, token, &out)
	return out, code, err
}

// HookState 是一个编程工具会话的提醒游标，存在本机，一个会话一份：
// 同一会话里同一条只提醒一次，不同会话各自提醒一次。
type HookState struct {
	Server    string    `json:"server"`
	Workspace string    `json:"workspace"`
	Cursor    int64     `json:"cursor"`
	Seen      []int64   `json:"seen,omitempty"` // 最近提醒过的编号，用于去掉服务端补发的近期提醒
	CheckedAt time.Time `json:"checked_at"`
}

const hookSeenKeep = 50

// HookStateDir 返回会话游标目录：COHIVE_CACHE_DIR 优先，否则是系统的用户缓存目录下的 cohive/hooks。
func HookStateDir() (string, error) {
	if d := os.Getenv("COHIVE_CACHE_DIR"); d != "" {
		return filepath.Join(d, "hooks"), nil
	}
	d, err := os.UserCacheDir()
	if err != nil || d == "" {
		return "", errors.New("找不到用户缓存目录")
	}
	return filepath.Join(d, "cohive", "hooks"), nil
}

var unsafeSession = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// HookStatePath 返回某个会话的游标文件；会话编号里的其他字符一律去掉，避免拼出目录之外的路径。
func HookStatePath(tool, sessionID string) (string, error) {
	sid := unsafeSession.ReplaceAllString(sessionID, "")
	if len(sid) > 100 {
		sid = sid[:100]
	}
	if sid == "" {
		return "", errors.New("没有会话编号")
	}
	dir, err := HookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, unsafeSession.ReplaceAllString(tool, "")+"-"+sid+".json"), nil
}

// LoadHookState 读取会话游标；没有或损坏时返回 false。
func LoadHookState(path string) (HookState, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return HookState{}, false
	}
	var s HookState
	if json.Unmarshal(b, &s) != nil {
		return HookState{}, false
	}
	return s, true
}

// Saw 记下已提醒过的编号，只保留最近的若干个。
func (s *HookState) Saw(id int64) {
	s.Seen = append(s.Seen, id)
	if len(s.Seen) > hookSeenKeep {
		s.Seen = s.Seen[len(s.Seen)-hookSeenKeep:]
	}
}

// HasSeen 判断这个编号是否提醒过。
func (s HookState) HasSeen(id int64) bool {
	for _, x := range s.Seen {
		if x == id {
			return true
		}
	}
	return false
}

// SaveHookState 原子写入会话游标（权限 600）。
func SaveHookState(path string, s HookState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// PruneHookStates 删除超过 maxAge 未改动的会话游标。
func PruneHookStates(maxAge time.Duration) {
	dir, err := HookStateDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && time.Since(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
