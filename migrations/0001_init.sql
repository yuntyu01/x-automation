-- 핵심 스키마. 상태는 책임별로 나눈다 (생성·검토·예약·게시 결과를 한 status에 섞지 않는다).
-- 외부 X ID는 정밀도 손실이 없도록 text로 저장한다.
-- 게시 이력이 지워지지 않게 게시 관련 참조는 ON DELETE RESTRICT를 기본으로 한다.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ── 운영 설정 (단일 행) ──────────────────────────────────────────────
CREATE TABLE app_settings (
    id                    smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    automation_enabled    boolean NOT NULL DEFAULT false,      -- 앱의 "자동 운영" 스위치. 재배포로 초기화하지 않는다
    automation_changed_at timestamptz,
    generation_paused     boolean NOT NULL DEFAULT false,
    publishing_paused     boolean NOT NULL DEFAULT false,
    operation_mode        text NOT NULL DEFAULT 'review'
                          CHECK (operation_mode IN ('full_auto', 'review', 'publish_only')),
    timezone              text NOT NULL DEFAULT 'Asia/Seoul',
    setup_completed_at    timestamptz,
    config                jsonb NOT NULL DEFAULT '{}'::jsonb,  -- 생성·분할·이미지·보관·예산 정책 (Go 구조체로 검증)
    version               integer NOT NULL DEFAULT 1,
    updated_at            timestamptz NOT NULL DEFAULT now()
);
INSERT INTO app_settings (id) VALUES (1);

-- ── 프롬프트 ─────────────────────────────────────────────────────────
CREATE TABLE prompts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL,
    category        text NOT NULL DEFAULT '',
    description     text NOT NULL DEFAULT '',
    is_example      boolean NOT NULL DEFAULT false,
    active          boolean NOT NULL DEFAULT true,
    current_version integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE prompt_versions (
    prompt_id  uuid NOT NULL REFERENCES prompts (id) ON DELETE RESTRICT,
    version    integer NOT NULL,
    body       text NOT NULL,
    variables  jsonb NOT NULL DEFAULT '[]'::jsonb,  -- [{name,label,type,options,required,default,max}]
    defaults   jsonb NOT NULL DEFAULT '{}'::jsonb,  -- 모델·길이·말투·이미지 옵션 기본값
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (prompt_id, version)
);

-- ── 콘텐츠 묶음과 revision ──────────────────────────────────────────
CREATE TABLE contents (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title                text NOT NULL DEFAULT '',
    category             text NOT NULL DEFAULT '',
    tags                 text[] NOT NULL DEFAULT '{}',
    source               text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'ai', 'import')),
    is_demo              boolean NOT NULL DEFAULT false,
    current_revision_id  uuid,          -- 편집 대상
    approved_revision_id uuid,          -- 게시 가능한 승인본
    generation_state     text NOT NULL DEFAULT 'none'
                         CHECK (generation_state IN ('none', 'queued', 'running', 'succeeded', 'failed')),
    generation_error     text,
    archived_at          timestamptz,
    edit_version         integer NOT NULL DEFAULT 1,  -- 자동 저장 충돌 감지
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contents_updated_idx ON contents (updated_at DESC);
CREATE INDEX contents_category_idx ON contents (category);
CREATE INDEX contents_search_idx ON contents USING gin (to_tsvector('simple', title));

CREATE TABLE content_revisions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    content_id      uuid NOT NULL REFERENCES contents (id) ON DELETE RESTRICT,
    number          integer NOT NULL,
    raw_text        text NOT NULL DEFAULT '',            -- 분할 전 원문 (보존)
    split_mode      text NOT NULL DEFAULT 'two_parts'
                    CHECK (split_mode IN ('two_parts', 'overflow', 'manual')),
    split_options   jsonb NOT NULL DEFAULT '{}'::jsonb,   -- 번호 표기·답글 상한·수동 분할 위치
    image_mode      text NOT NULL DEFAULT 'none' CHECK (image_mode IN ('none', 'upload', 'ai')),
    origin          text NOT NULL DEFAULT 'user' CHECK (origin IN ('user', 'ai', 'import', 'restore')),
    prompt_id       uuid REFERENCES prompts (id) ON DELETE RESTRICT,
    prompt_version  integer,
    model           text,
    gen_options     jsonb NOT NULL DEFAULT '{}'::jsonb,
    review_state    text NOT NULL DEFAULT 'unreviewed'
                    CHECK (review_state IN ('unreviewed', 'pending', 'approved', 'rejected')),
    approved_at     timestamptz,
    frozen_at       timestamptz,                          -- 승인·게시로 불변이 된 시각
    validation      jsonb NOT NULL DEFAULT '{}'::jsonb,   -- 길이·이미지 검증 결과 요약
    publishable     boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (content_id, number),
    FOREIGN KEY (prompt_id, prompt_version) REFERENCES prompt_versions (prompt_id, version) ON DELETE RESTRICT
);
ALTER TABLE contents
    ADD CONSTRAINT contents_current_rev_fk FOREIGN KEY (current_revision_id) REFERENCES content_revisions (id) ON DELETE RESTRICT,
    ADD CONSTRAINT contents_approved_rev_fk FOREIGN KEY (approved_revision_id) REFERENCES content_revisions (id) ON DELETE RESTRICT;
CREATE INDEX content_revisions_content_idx ON content_revisions (content_id, number DESC);
CREATE INDEX content_revisions_search_idx ON content_revisions USING gin (to_tsvector('simple', raw_text));

CREATE TABLE thread_parts (
    revision_id     uuid NOT NULL REFERENCES content_revisions (id) ON DELETE CASCADE,
    position        integer NOT NULL CHECK (position >= 0),   -- 0 = 본문
    text            text NOT NULL,
    weighted_length integer NOT NULL,
    valid           boolean NOT NULL,
    PRIMARY KEY (revision_id, position)
);

-- ── 이미지 ───────────────────────────────────────────────────────────
CREATE TABLE media_assets (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    storage_key         text NOT NULL UNIQUE,
    thumb_key           text,
    parent_id           uuid REFERENCES media_assets (id) ON DELETE RESTRICT,  -- 자르기 원본
    source              text NOT NULL CHECK (source IN ('upload', 'ai', 'crop')),
    content_type        text NOT NULL,
    bytes               bigint NOT NULL,
    width               integer NOT NULL,
    height              integer NOT NULL,
    sha256              text NOT NULL,
    alt_text            text NOT NULL DEFAULT '',
    prompt              text,
    model               text,
    state               text NOT NULL DEFAULT 'active'
                        CHECK (state IN ('pending', 'active', 'deleting', 'deleted', 'delete_failed')),
    last_published_at   timestamptz,   -- 이미지가 들어간 게시 부분의 마지막 성공 시각
    retention_anchor_at timestamptz,   -- 보관 기준 (묶음 완료 시각). 한 번도 게시 안 했으면 NULL
    delete_attempts     integer NOT NULL DEFAULT 0,
    delete_error        text,
    deleted_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_assets_retention_idx ON media_assets (retention_anchor_at) WHERE state = 'active' AND retention_anchor_at IS NOT NULL;
CREATE INDEX media_assets_sha_idx ON media_assets (sha256);

CREATE TABLE part_media (
    revision_id uuid NOT NULL REFERENCES content_revisions (id) ON DELETE CASCADE,
    part_index  integer NOT NULL CHECK (part_index >= 0),
    media_id    uuid NOT NULL REFERENCES media_assets (id) ON DELETE RESTRICT,
    position    integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (revision_id, part_index, position)
);
CREATE INDEX part_media_media_idx ON part_media (media_id);

-- ── 시간표·예약 ──────────────────────────────────────────────────────
CREATE TABLE schedule_rules (
    id                  smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    daily_target        integer NOT NULL DEFAULT 20 CHECK (daily_target BETWEEN 0 AND 200),
    base_interval_min   integer NOT NULL DEFAULT 30 CHECK (base_interval_min BETWEEN 1 AND 1440),
    jitter_min_min      integer NOT NULL DEFAULT 0 CHECK (jitter_min_min >= 0),
    jitter_max_min      integer NOT NULL DEFAULT 5 CHECK (jitter_max_min >= jitter_min_min),
    reply_gap_sec       integer NOT NULL DEFAULT 0 CHECK (reply_gap_sec >= 0),
    end_margin_min      integer NOT NULL DEFAULT 5 CHECK (end_margin_min >= 0),  -- 구간 종료 직전 새 묶음 시작 금지
    timezone            text NOT NULL DEFAULT 'Asia/Seoul',
    policy_version      integer NOT NULL DEFAULT 1,
    updated_at          timestamptz NOT NULL DEFAULT now()
);
INSERT INTO schedule_rules (id) VALUES (1);

CREATE TABLE schedule_windows (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    label        text NOT NULL,
    weekdays     smallint[] NOT NULL,                       -- 0=일 … 6=토 (시작 요일 기준)
    start_minute integer NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute   integer NOT NULL CHECK (end_minute BETWEEN 0 AND 1440), -- end <= start면 다음 날로 이어짐
    enabled      boolean NOT NULL DEFAULT true,
    sort_order   integer NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE schedule_exceptions (
    local_date date PRIMARY KEY,
    kind       text NOT NULL DEFAULT 'skip' CHECK (kind IN ('skip')),
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE reservations (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slot_at          timestamptz NOT NULL,         -- 현재 예정 시각
    original_slot_at timestamptz NOT NULL,         -- 처음 확정한 시각
    jitter_seconds   integer NOT NULL DEFAULT 0,   -- 생성 시 한 번 확정한 추가 지연
    local_date       date NOT NULL,                -- 시간대 기준 날짜 (일일 상한 집계)
    content_id       uuid REFERENCES contents (id) ON DELETE RESTRICT,
    revision_id      uuid REFERENCES content_revisions (id) ON DELETE RESTRICT, -- 게시할 revision을 명시
    pinned           boolean NOT NULL DEFAULT false,  -- 시간 고정
    manual           boolean NOT NULL DEFAULT false,  -- "지금 게시" 수동 예외
    state            text NOT NULL DEFAULT 'open'
                     CHECK (state IN ('open', 'assigned', 'waiting', 'publishing', 'partial', 'unknown',
                                      'published', 'failed', 'canceled')),
    wait_reason      text,                         -- 미준비·미승인·이미지 실패·구간 종료 등
    delay_note       text,                         -- 재배치 사유 (원래 시각은 original_slot_at)
    generation_due_at timestamptz,                 -- 생성 예정 시각 (게시 시각 생성·예약 전 N분)
    rule_version     integer NOT NULL DEFAULT 1,
    version          integer NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reservations_slot_idx ON reservations (slot_at) WHERE state IN ('open', 'assigned', 'waiting');
CREATE INDEX reservations_date_idx ON reservations (local_date);
-- 한 콘텐츠는 진행 중인 예약 하나에만 배정한다
CREATE UNIQUE INDEX reservations_content_active_uq ON reservations (content_id)
    WHERE content_id IS NOT NULL AND state IN ('assigned', 'waiting', 'publishing', 'partial', 'unknown');

-- ── 작업 큐 ──────────────────────────────────────────────────────────
CREATE TABLE jobs (
    id               bigserial PRIMARY KEY,
    kind             text NOT NULL,
    unique_key       text NOT NULL,                -- 비즈니스 고유키 (같은 대상 중복 작업 방지)
    payload          jsonb NOT NULL DEFAULT '{}'::jsonb,
    state            text NOT NULL DEFAULT 'queued'
                     CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'canceled', 'unknown')),
    run_at           timestamptz NOT NULL DEFAULT now(),
    attempts         integer NOT NULL DEFAULT 0,
    max_attempts     integer NOT NULL DEFAULT 5,
    lease_owner      text,
    lease_expires_at timestamptz,
    lease_version    bigint NOT NULL DEFAULT 0,
    last_error       text,
    error_class      text CHECK (error_class IN ('retryable', 'config', 'review', 'unknown', 'canceled')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz,
    UNIQUE (kind, unique_key)
);
CREATE INDEX jobs_due_idx ON jobs (run_at) WHERE state = 'queued';
CREATE INDEX jobs_lease_idx ON jobs (lease_expires_at) WHERE state = 'running';

CREATE TABLE generation_requests (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key text NOT NULL UNIQUE,          -- 같은 예약·같은 기간·정책의 동시 보충 중복 방지
    trigger         text NOT NULL CHECK (trigger IN ('manual', 'on_publish', 'daily_at', 'before_slot', 'stock', 'fill_now', 'regenerate')),
    policy_version  integer,
    prompt_id       uuid REFERENCES prompts (id) ON DELETE RESTRICT,
    prompt_version  integer,
    variables       jsonb NOT NULL DEFAULT '{}'::jsonb,
    options         jsonb NOT NULL DEFAULT '{}'::jsonb,   -- 글/이미지 사용·모델·품질
    quantity        integer NOT NULL CHECK (quantity BETWEEN 1 AND 100),
    reservation_id  uuid REFERENCES reservations (id) ON DELETE RESTRICT,
    content_id      uuid REFERENCES contents (id) ON DELETE RESTRICT,  -- 부분 재생성 대상
    place_into_slots boolean NOT NULL DEFAULT false,
    state           text NOT NULL DEFAULT 'queued'
                    CHECK (state IN ('queued', 'running', 'succeeded', 'partial', 'failed', 'canceled', 'unknown')),
    produced        integer NOT NULL DEFAULT 0,
    failed          integer NOT NULL DEFAULT 0,
    error           text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);
CREATE INDEX generation_requests_state_idx ON generation_requests (state, created_at DESC);

-- ── 게시 실행 ────────────────────────────────────────────────────────
CREATE TABLE publish_runs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id uuid NOT NULL REFERENCES reservations (id) ON DELETE RESTRICT,
    content_id     uuid NOT NULL REFERENCES contents (id) ON DELETE RESTRICT,
    revision_id    uuid NOT NULL REFERENCES content_revisions (id) ON DELETE RESTRICT,
    trigger        text NOT NULL CHECK (trigger IN ('schedule', 'manual')),
    state          text NOT NULL DEFAULT 'pending'
                   CHECK (state IN ('pending', 'running', 'partial', 'unknown', 'completed', 'failed', 'canceled')),
    started_at     timestamptz,
    completed_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
-- 예약 하나에 활성 실행은 하나뿐이다 (자동 실행·지금 게시·재개 경쟁 방지)
CREATE UNIQUE INDEX publish_runs_active_uq ON publish_runs (reservation_id)
    WHERE state IN ('pending', 'running', 'partial', 'unknown');

CREATE TABLE publish_parts (
    run_id        uuid NOT NULL REFERENCES publish_runs (id) ON DELETE RESTRICT,
    position      integer NOT NULL CHECK (position >= 0),
    text          text NOT NULL,
    media_ids     uuid[] NOT NULL DEFAULT '{}',
    x_media_ids   text[] NOT NULL DEFAULT '{}',
    state         text NOT NULL DEFAULT 'pending'
                  CHECK (state IN ('pending', 'sending', 'succeeded', 'failed', 'unknown')),
    in_reply_to   text,
    x_post_id     text,
    x_url         text,
    attempts      integer NOT NULL DEFAULT 0,
    not_before    timestamptz,                  -- 답글 간격·다음 허용 구간
    intent_at     timestamptz,                  -- 전송 의도 기록 시각
    succeeded_at  timestamptz,
    error         text,
    error_class   text,
    PRIMARY KEY (run_id, position)
);

CREATE TABLE operation_attempts (
    id          bigserial PRIMARY KEY,
    kind        text NOT NULL,           -- x.post · x.media · ai.text · ai.image · r2.put · r2.delete …
    ref_type    text NOT NULL,
    ref_id      text NOT NULL,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    outcome     text CHECK (outcome IN ('success', 'error', 'unknown')),
    http_status integer,
    request_id  text,
    error       text
);
CREATE INDEX operation_attempts_ref_idx ON operation_attempts (ref_type, ref_id, started_at DESC);

-- ── 연결·세션 ────────────────────────────────────────────────────────
CREATE TABLE connections (
    provider          text PRIMARY KEY CHECK (provider IN ('x')),
    account_id        text,
    username          text,
    display_name      text,
    access_token_enc  bytea,
    refresh_token_enc bytea,
    token_expires_at  timestamptz,
    scopes            text[] NOT NULL DEFAULT '{}',
    status            text NOT NULL DEFAULT 'disconnected'
                      CHECK (status IN ('disconnected', 'connected', 'expired', 'error')),
    status_detail     text,
    capabilities      jsonb NOT NULL DEFAULT '{}'::jsonb,
    connected_at      timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_states (
    state             text PRIMARY KEY,
    session_hash      bytea NOT NULL,
    code_verifier_enc bytea NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    expires_at        timestamptz NOT NULL,
    used_at           timestamptz
);

CREATE TABLE sessions (
    id_hash      bytea PRIMARY KEY,         -- 세션 ID 원문은 저장하지 않는다
    username     text NOT NULL,
    csrf_token   text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE login_attempts (
    id         bigserial PRIMARY KEY,
    ip         text NOT NULL,
    username   text NOT NULL,
    success    boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_attempts_ip_idx ON login_attempts (ip, created_at DESC);
CREATE INDEX login_attempts_user_idx ON login_attempts (username, created_at DESC);

-- ── 알림 ─────────────────────────────────────────────────────────────
CREATE TABLE notifications (
    id         bigserial PRIMARY KEY,
    severity   text NOT NULL CHECK (severity IN ('critical', 'warning', 'info')),
    category   text NOT NULL,
    group_key  text NOT NULL,         -- 같은 원인 반복을 하나로 묶는다
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    link       text NOT NULL DEFAULT '',
    count      integer NOT NULL DEFAULT 1,
    first_at   timestamptz NOT NULL DEFAULT now(),
    last_at    timestamptz NOT NULL DEFAULT now(),
    read_at    timestamptz,
    resolved_at timestamptz
);
CREATE UNIQUE INDEX notifications_open_group_uq ON notifications (group_key) WHERE read_at IS NULL AND resolved_at IS NULL;
CREATE INDEX notifications_last_idx ON notifications (last_at DESC);

-- ── 사용량·예산 ──────────────────────────────────────────────────────
CREATE TABLE usage_ledger (
    id             bigserial PRIMARY KEY,
    category       text NOT NULL CHECK (category IN ('ai_text', 'ai_image', 'x_post', 'x_media', 'storage')),
    ref_key        text NOT NULL,
    units          jsonb NOT NULL DEFAULT '{}'::jsonb,
    estimated_micros bigint NOT NULL DEFAULT 0,   -- 추정 비용 (통화 단위 × 1e6)
    currency       text NOT NULL DEFAULT 'USD',
    rate_version   text NOT NULL DEFAULT '',
    certainty      text NOT NULL DEFAULT 'estimated' CHECK (certainty IN ('estimated', 'uncertain')),
    period_month   date NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (category, ref_key)                    -- 재시도해도 같은 사용을 두 번 기록하지 않는다
);
CREATE INDEX usage_ledger_period_idx ON usage_ledger (period_month, category);

CREATE TABLE budget_reservations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ref_key       text NOT NULL UNIQUE,
    category      text NOT NULL,
    amount_micros bigint NOT NULL CHECK (amount_micros >= 0),
    state         text NOT NULL DEFAULT 'held' CHECK (state IN ('held', 'settled', 'released')),
    period_month  date NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    closed_at     timestamptz
);
CREATE INDEX budget_reservations_period_idx ON budget_reservations (period_month) WHERE state = 'held';

-- 제안 초기값 (첫 설정에서 바꿀 수 있다). 새벽 00:00~06:00, 낮 12:00~17:00, 매일
INSERT INTO schedule_windows (label, weekdays, start_minute, end_minute, sort_order) VALUES
    ('새벽', '{0,1,2,3,4,5,6}', 0, 360, 0),
    ('낮', '{0,1,2,3,4,5,6}', 720, 1020, 1);
