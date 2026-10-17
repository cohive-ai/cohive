-- 对外地址改为服务器级（参照 GitLab 的 external_url），工作空间拆成不可改的路径（name）与可改的显示名。

-- 服务器级设置：只有一行
CREATE TABLE server_settings (
    id            boolean PRIMARY KEY DEFAULT true CHECK (id),
    external_url  text NOT NULL DEFAULT '',     -- 空表示没设；只含协议、主机名与可选端口，不带路径
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES accounts(id)
);

-- 把工作空间里编号最小的、能认出来的公网地址提升为服务器级：协议与主机名转小写，去掉路径、查询参数与末尾的 /。
-- 完全认不出的值丢弃。两种情况都写 NOTICE 进迁移日志。
DO $$
DECLARE
    picked text := '';
    w record;
    m text[];
BEGIN
    FOR w IN SELECT id, name, trim(public_url) AS u FROM workspaces WHERE trim(public_url) <> '' ORDER BY id LOOP
        m := regexp_match(w.u, '^(https?)://([^/?#@\s]+)([/?#].*)?$', 'i');
        IF m IS NULL THEN
            RAISE NOTICE '工作空间 % 的公网地址 % 不是 http(s) 地址，已丢弃', w.name, w.u;
            CONTINUE;
        END IF;
        IF picked = '' THEN
            picked := lower(m[1]) || '://' || lower(m[2]);
            IF coalesce(m[3], '') NOT IN ('', '/') THEN
                RAISE NOTICE '工作空间 % 的公网地址 % 带路径，提升为服务器级对外地址时去掉了路径：%', w.name, w.u, picked;
            END IF;
        ELSIF lower(m[1]) || '://' || lower(m[2]) <> picked THEN
            RAISE NOTICE '工作空间 % 的公网地址 % 与提升的对外地址 % 不同，已丢弃', w.name, w.u, picked;
        END IF;
    END LOOP;
    INSERT INTO server_settings (external_url) VALUES (picked);
END
$$;

ALTER TABLE workspaces DROP COLUMN public_url;
ALTER TABLE workspaces ADD COLUMN display_name text NOT NULL DEFAULT '';  -- 空表示与路径相同
