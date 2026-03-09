CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS job_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL UNIQUE,
    job_type TEXT NOT NULL,
    provider TEXT NOT NULL,
    project_key TEXT NOT NULL,
    target_account TEXT,
    status TEXT NOT NULL,
    prompt_name TEXT,
    prompt_hash TEXT,
    issue_count INTEGER,
    report_path TEXT,
    raw_response_path TEXT,
    error_message TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_job_runs_job_type ON job_runs(job_type);
CREATE INDEX IF NOT EXISTS idx_job_runs_started_at ON job_runs(started_at);

CREATE TABLE IF NOT EXISTS notification_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    destination TEXT,
    status TEXT NOT NULL,
    response_summary TEXT,
    sent_at TEXT,
    FOREIGN KEY(job_id) REFERENCES job_runs(job_id)
);

CREATE INDEX IF NOT EXISTS idx_notification_logs_job_id ON notification_logs(job_id);

CREATE TABLE IF NOT EXISTS prompt_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    task_type TEXT NOT NULL,
    system_template TEXT NOT NULL,
    user_template TEXT NOT NULL,
    prompt_hash TEXT NOT NULL,
    rendered_prompt_path TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(job_id) REFERENCES job_runs(job_id)
);

CREATE INDEX IF NOT EXISTS idx_prompt_runs_job_id ON prompt_runs(job_id);
