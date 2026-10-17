package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
	"github.com/jackc/pgx/v5"
)

// 数据库判死锁时，inTx 返回的是“请重读后重试”（按版本冲突处理），不是原始的数据库错误。
func TestInTxTurnsDeadlockIntoRetry(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE t (id int PRIMARY KEY); INSERT INTO t VALUES (1), (2)`); err != nil {
		t.Fatal(err)
	}
	s := &Service{Pool: pool}
	// 两个事务各锁一行，再去锁对方那一行：必然死锁，数据库中止其中一个
	cross := func(first, second int, locked chan<- struct{}, other <-chan struct{}) <-chan error {
		ch := make(chan error, 1)
		go func() {
			ch <- s.inTx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `SELECT 1 FROM t WHERE id = $1 FOR UPDATE`, first); err != nil {
					return err
				}
				close(locked)
				<-other
				_, err := tx.Exec(ctx, `SELECT 1 FROM t WHERE id = $1 FOR UPDATE`, second)
				return err
			})
		}()
		return ch
	}
	aLocked, bLocked := make(chan struct{}), make(chan struct{})
	a := cross(1, 2, aLocked, bLocked)
	b := cross(2, 1, bLocked, aLocked)
	errA, errB := <-a, <-b
	if (errA == nil) == (errB == nil) {
		t.Fatalf("应恰好一个被中止：%v / %v", errA, errB)
	}
	err := errA
	if err == nil {
		err = errB
	}
	var re *RetryError
	if !errors.As(err, &re) || re.Code != "40P01" || !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("死锁应转成 RetryError 并按版本冲突处理：%#v", err)
	}
	if strings.Contains(err.Error(), "SQLSTATE") {
		t.Fatalf("不应把原始数据库错误给用户看：%v", err)
	}
}

// 可串行化事务的写偏斜被数据库中止（40001）时，同样转成“请重读后重试”。
func TestInTxTurnsSerializationFailureIntoRetry(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE t (id int PRIMARY KEY, v int NOT NULL); INSERT INTO t VALUES (1, 0), (2, 0)`); err != nil {
		t.Fatal(err)
	}
	s := &Service{Pool: pool}
	// 两个事务都先读全表，再各改一行：互相读到了对方要改的数据，后提交的一方必然串行化失败
	var read, wrote sync.WaitGroup
	read.Add(2)
	wrote.Add(2)
	skew := func(id int) <-chan error {
		ch := make(chan error, 1)
		go func() {
			ch <- s.inTx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL SERIALIZABLE`); err != nil {
					return err
				}
				var sum int
				err := tx.QueryRow(ctx, `SELECT sum(v) FROM t`).Scan(&sum)
				read.Done()
				read.Wait()
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE t SET v = $2 WHERE id = $1`, id, sum+1)
				}
				wrote.Done()
				wrote.Wait()
				return err
			})
		}()
		return ch
	}
	a, b := skew(1), skew(2)
	errA, errB := <-a, <-b
	if (errA == nil) == (errB == nil) {
		t.Fatalf("应恰好一个被中止：%v / %v", errA, errB)
	}
	err := errA
	if err == nil {
		err = errB
	}
	var re *RetryError
	if !errors.As(err, &re) || re.Code != "40001" || !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("串行化失败应转成 RetryError 并按版本冲突处理：%#v", err)
	}
	if strings.Contains(err.Error(), "SQLSTATE") {
		t.Fatalf("不应把原始数据库错误给用户看：%v", err)
	}
}
