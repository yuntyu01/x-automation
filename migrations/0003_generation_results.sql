-- 부분 재생성 결과는 바로 덮어쓰지 않고 비교·적용할 수 있게 따로 둔다.
CREATE TABLE generation_results (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id       uuid NOT NULL REFERENCES generation_requests (id) ON DELETE RESTRICT,
    content_id       uuid NOT NULL REFERENCES contents (id) ON DELETE RESTRICT,
    base_revision_id uuid NOT NULL REFERENCES content_revisions (id) ON DELETE RESTRICT,
    kind             text NOT NULL CHECK (kind IN ('text', 'image')),
    target           text NOT NULL CHECK (target IN ('all', 'part', 'image')),
    part_index       integer,
    text             text,
    media_id         uuid REFERENCES media_assets (id) ON DELETE RESTRICT,
    state            text NOT NULL DEFAULT 'new' CHECK (state IN ('new', 'applied', 'discarded')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    decided_at       timestamptz
);
CREATE INDEX generation_results_content_idx ON generation_results (content_id, created_at DESC);

-- 생성으로 만든 콘텐츠가 어느 요청에서 왔는지 (부족분 계산·검토 대기 확보분 집계)
ALTER TABLE contents ADD COLUMN generation_request_id uuid REFERENCES generation_requests (id) ON DELETE RESTRICT;
CREATE INDEX contents_generation_request_idx ON contents (generation_request_id);

-- 자동 생성 전략이 한 기간·예약에 대해 판단한 기록. 같은 키는 한 번만 판단한다 (부족분 0이어도 남긴다).
CREATE TABLE generation_marks (
    key         text PRIMARY KEY,               -- daily:<날짜>:v<정책>, before:<예약>:v<정책>, stock:v<정책>:<시각> …
    trigger     text NOT NULL,
    shortfall   integer NOT NULL DEFAULT 0,
    request_id  uuid REFERENCES generation_requests (id) ON DELETE SET NULL,
    note        text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX generation_marks_created_idx ON generation_marks (created_at DESC);
