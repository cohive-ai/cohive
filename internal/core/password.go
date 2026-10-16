package core

import (
	"context"

	"golang.org/x/crypto/bcrypt"
)

// hashSlots 限制同时进行的密码哈希与比对数：bcrypt 每次要几十毫秒 CPU，
// 不加上限时，大量登录或注册请求可以把服务的 CPU 占满。
var hashSlots = make(chan struct{}, 4)

func withHashSlot(ctx context.Context, fn func()) error {
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-hashSlots }()
	fn()
	return nil
}

// hashPassword 校验密码长度后计算 bcrypt 哈希。
func hashPassword(ctx context.Context, pw string) (string, error) {
	if len([]rune(pw)) < minPasswordLength {
		return "", inputErr("密码至少 %d 位", minPasswordLength)
	}
	if len(pw) > 72 {
		return "", inputErr("密码太长（最多 72 字节）")
	}
	return bcryptHash(ctx, pw)
}

func bcryptHash(ctx context.Context, pw string) (string, error) {
	var h []byte
	var err error
	if serr := withHashSlot(ctx, func() { h, err = bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost) }); serr != nil {
		return "", serr
	}
	return string(h), err
}

// passwordMatches 比对密码与哈希；哈希为空时视为不匹配。
func passwordMatches(ctx context.Context, hash, pw string) (bool, error) {
	if hash == "" {
		return false, nil
	}
	var ok bool
	err := withHashSlot(ctx, func() { ok = bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil })
	return ok, err
}

// DummyPasswordCheck 做一次不会成功的比对，让被锁定或不存在的账号与正常账号耗时相同，
// 不能借响应时间探测登录名。
func (s *Service) DummyPasswordCheck(ctx context.Context, pw string) {
	_, _ = passwordMatches(ctx, string(dummyHash), pw)
}
