-- 内置生图任务中心。
-- 任务与输入/输出图片短期存储，默认由服务内清理任务保留 24 小时。

CREATE TABLE IF NOT EXISTS image_tasks (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode              VARCHAR(20) NOT NULL CHECK (mode IN ('generation', 'edit')),
    status            VARCHAR(20) NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    model             VARCHAR(100) NOT NULL,
    prompt            TEXT NOT NULL,
    size              VARCHAR(50) NOT NULL DEFAULT '',
    n                 INTEGER NOT NULL DEFAULT 1,
    quality           VARCHAR(50) NOT NULL DEFAULT '',
    request_json      TEXT NOT NULL DEFAULT '',
    response_json     TEXT NOT NULL DEFAULT '',
    error_message     TEXT NOT NULL DEFAULT '',
    input_images_json TEXT NOT NULL DEFAULT '',
    input_mask_json   TEXT NOT NULL DEFAULT '',
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS imagetask_user_id_created_at
    ON image_tasks (user_id, created_at);

CREATE INDEX IF NOT EXISTS imagetask_user_id_status
    ON image_tasks (user_id, status);

CREATE INDEX IF NOT EXISTS imagetask_status_updated_at
    ON image_tasks (status, updated_at);

CREATE INDEX IF NOT EXISTS imagetask_finished_at
    ON image_tasks (finished_at);
