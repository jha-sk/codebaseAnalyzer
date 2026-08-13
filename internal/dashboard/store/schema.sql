CREATE TABLE IF NOT EXISTS repos (
    id                BIGSERIAL PRIMARY KEY,
    remote_url        TEXT NOT NULL UNIQUE,
    ingest_token_hash TEXT NOT NULL,
    registered_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS runs (
    id         BIGSERIAL PRIMARY KEY,
    repo_id    BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    branch     TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    pushed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    critical   INT NOT NULL DEFAULT 0,
    high       INT NOT NULL DEFAULT 0,
    medium     INT NOT NULL DEFAULT 0,
    low        INT NOT NULL DEFAULT 0,
    tools      JSONB NOT NULL DEFAULT '[]'::jsonb,
    UNIQUE (repo_id, commit_sha)
);

CREATE TABLE IF NOT EXISTS findings (
    id          BIGSERIAL PRIMARY KEY,
    run_id      BIGINT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    file        TEXT NOT NULL,
    line        INT NOT NULL,
    tool        TEXT NOT NULL,
    rule_id     TEXT NOT NULL,
    category    TEXT NOT NULL,
    severity    TEXT NOT NULL,
    message     TEXT NOT NULL,
    explanation TEXT NOT NULL DEFAULT ''
);

-- CREATE TABLE IF NOT EXISTS above will not alter an already-existing table,
-- so fix_pattern is added separately: idempotent DDL applied at boot, it
-- picks up existing deployments the same way a migration would.
ALTER TABLE findings ADD COLUMN IF NOT EXISTS fix_pattern TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS findings_run_idx ON findings (run_id);
CREATE INDEX IF NOT EXISTS runs_branch_idx ON runs (repo_id, branch, pushed_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS repos_token_idx ON repos (ingest_token_hash);
