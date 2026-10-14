-- 幂等记录绑定动作：同一个键不能被挪用到另一张问题单或另一个动作上。
ALTER TABLE idempotency ADD COLUMN action text NOT NULL DEFAULT '';
