CREATE TABLE workspaces (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE members (
    id            bigserial PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    name          text NOT NULL,
    display_name  text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE tokens (
    id          bigserial PRIMARY KEY,
    member_id   bigint NOT NULL REFERENCES members(id),
    tool        text NOT NULL,
    token_hash  bytea NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);

CREATE TABLE services (
    id            bigserial PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces(id),
    name          text NOT NULL,
    owner_id      bigint NOT NULL REFERENCES members(id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE issues (
    id                 bigserial PRIMARY KEY,
    workspace_id       bigint NOT NULL REFERENCES workspaces(id),
    source_member_id   bigint NOT NULL REFERENCES members(id),
    source_tool        text NOT NULL,
    owner_id           bigint REFERENCES members(id),
    target_service     text NOT NULL DEFAULT '',
    severity           text NOT NULL DEFAULT 'normal',
    title              text NOT NULL,
    summary            text NOT NULL DEFAULT '',
    expected           text NOT NULL DEFAULT '',
    actual             text NOT NULL DEFAULT '',
    repro_script       text NOT NULL DEFAULT '',
    repro_env          text NOT NULL DEFAULT '',
    repro_exit_code    integer,
    evidence           text NOT NULL DEFAULT '',
    repo               text NOT NULL DEFAULT '',
    branch             text NOT NULL DEFAULT '',
    commit_sha         text NOT NULL DEFAULT '',
    status             text NOT NULL,
    version            integer NOT NULL DEFAULT 1,
    reopen_count       integer NOT NULL DEFAULT 0,
    escalated_at       timestamptz,
    escalation_reason  text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX issues_owner_status ON issues (owner_id, status);
CREATE INDEX issues_source_status ON issues (source_member_id, status);

CREATE TABLE issue_events (
    id               bigserial PRIMARY KEY,
    issue_id         bigint NOT NULL REFERENCES issues(id),
    actor_member_id  bigint NOT NULL REFERENCES members(id),
    actor_tool       text NOT NULL,
    action           text NOT NULL,
    from_status      text NOT NULL DEFAULT '',
    to_status        text NOT NULL DEFAULT '',
    note             text NOT NULL DEFAULT '',
    payload          jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX issue_events_issue ON issue_events (issue_id, id);

CREATE TABLE idempotency (
    token_id    bigint NOT NULL REFERENCES tokens(id),
    key         text NOT NULL,
    issue_id    bigint NOT NULL REFERENCES issues(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (token_id, key)
);
