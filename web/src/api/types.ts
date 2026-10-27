// 서버 응답 타입 (Go 구조체의 JSON 이름과 맞춘다)

export type OperationMode = 'full_auto' | 'review' | 'publish_only';

export interface GenerationPolicy {
  text_enabled: boolean;
  image_enabled: boolean;
  timing: 'on_publish' | 'manual' | 'prepare';
  prepare_strategy: 'daily_at' | 'before_slot' | 'stock';
  daily_at: string;
  horizon_days: number;
  before_minutes: number;
  stock_min: number;
  stock_target: number;
  fill_on_start: boolean;
  default_prompt_id: string;
  text_model: string;
  daily_limit: number;
  max_regenerations: number;
  max_concurrent: number;
  max_attempts: number;
  text_only_fallback: boolean;
  policy_version: number;
}

export interface Policy {
  generation: GenerationPolicy;
  thread: { split_mode: 'two_parts' | 'overflow' | 'manual'; max_replies: number; numbering: boolean; long_post_enabled: boolean };
  image: { default_mode: 'none' | 'upload' | 'ai'; model: string; quality: string; size: string; style: string; count: number; placement: number };
  retention: { days: number };
  budget: { monthly_limit_micros: number; currency: string; on_limit: 'stop_generation' | 'stop_all' };
  pricing: {
    version: string;
    verified_at: string;
    source: string;
    currency: string;
    x_post_micros: number;
    x_post_with_url_micros: number;
    x_media_micros: number;
    text_input_per_mtok_micros: number;
    text_output_per_mtok_micros: number;
    image_per_image_micros: number;
  };
}

export interface Settings {
  automation_enabled: boolean;
  automation_changed_at: string | null;
  generation_paused: boolean;
  publishing_paused: boolean;
  operation_mode: OperationMode;
  timezone: string;
  setup_completed_at: string | null;
  policy: Policy;
  version: number;
  updated_at: string;
}

export interface RuntimeInfo {
  env: string;
  integration_mode: 'live' | 'mock';
  publishing_enabled: boolean;
  version: string;
}

export interface ConnectionView {
  status: 'disconnected' | 'connected' | 'expired' | 'error';
  status_detail?: string;
  username?: string;
  display_name?: string;
  scopes: string[];
  capabilities: Record<string, unknown>;
  connected_at?: string;
  token_expires_at?: string;
}

export interface IntegrationStatus {
  mode: 'live' | 'mock';
  publishing_enabled: boolean;
  x: { configured: boolean; connection: ConnectionView };
  ai: { configured: boolean; provider: string; text_model: string; image_model: string };
  r2: { configured: boolean; bucket: string; prefix: string; public_base_url: string; dev_url: boolean };
}

export interface NotificationItem {
  id: number;
  severity: 'critical' | 'warning' | 'info';
  category: string;
  group_key: string;
  title: string;
  body: string;
  link: string;
  count: number;
  first_at: string;
  last_at: string;
  read_at: string | null;
  resolved_at: string | null;
}

export const MODE_LABEL: Record<OperationMode, string> = {
  full_auto: '완전 자동',
  review: '검토 후 게시',
  publish_only: '게시만 자동',
};

// ── 콘텐츠 ────────────────────────────────────────────────────────────
export type Stage = 'draft' | 'review' | 'ready' | 'scheduled' | 'published' | 'problem' | 'archived';
export type SplitMode = 'two_parts' | 'overflow' | 'manual';
export type ImageMode = 'none' | 'upload' | 'ai';

export const STAGE_LABEL: Record<Stage, string> = {
  draft: '작성 중',
  review: '검토 대기',
  ready: '준비 완료',
  scheduled: '예약',
  published: '게시 완료',
  problem: '문제 발생',
  archived: '보관됨',
};

export const SPLIT_LABEL: Record<SplitMode, string> = {
  two_parts: '본문 + 답글 1개',
  overflow: '넘칠 때만 답글',
  manual: '직접 나누기',
};

export interface ThreadPart {
  index: number;
  text: string;
  weighted_length: number;
  valid: boolean;
  problem?: string;
}

export interface SplitResult {
  parts: ThreadPart[];
  mode: SplitMode;
  limit: number;
  requested_replies: number;
  replies: number;
  note: string;
  publishable: boolean;
  problems: string[];
  has_url: boolean;
  post_count: number;
}

export interface SplitOptions {
  numbering: boolean;
  max_replies: number;
}

export interface PartMedia {
  part_index: number;
  position: number;
  media_id: string;
  url?: string;
  thumb_url?: string;
  alt_text: string;
  state: string;
}

export interface Revision {
  id: string;
  content_id: string;
  number: number;
  raw_text: string;
  split_mode: SplitMode;
  split_options: SplitOptions;
  image_mode: ImageMode;
  origin: 'user' | 'ai' | 'import' | 'restore';
  prompt_id: string | null;
  prompt_version: number | null;
  model: string | null;
  review_state: 'unreviewed' | 'pending' | 'approved' | 'rejected';
  approved_at: string | null;
  frozen_at: string | null;
  validation: { note: string; problems: string[]; replies: number; requested_replies: number; post_count: number; has_url: boolean; limit: number };
  publishable: boolean;
  created_at: string;
  updated_at: string;
  parts: ThreadPart[];
  media: PartMedia[];
}

export interface ContentDetail {
  id: string;
  title: string;
  category: string;
  tags: string[];
  source: 'manual' | 'ai' | 'import';
  is_demo: boolean;
  current_revision_id: string;
  approved_revision_id: string | null;
  generation_state: string;
  generation_error: string | null;
  archived_at: string | null;
  edit_version: number;
  created_at: string;
  updated_at: string;
  stage: Stage;
  revision: Revision;
  approved_number: number | null;
  revision_count: number;
  reservation_id: string | null;
  reservation_at: string | null;
  reservation_revision_id: string | null;
  last_run_state: string | null;
}

export interface ContentItem {
  id: string;
  title: string;
  category: string;
  tags: string[];
  source: string;
  is_demo: boolean;
  stage: Stage;
  preview: string;
  part_count: number;
  image_mode: ImageMode;
  review_state: string;
  publishable: boolean;
  problems: string[];
  generation_state: string;
  reservation_at: string | null;
  reservation_state: string | null;
  wait_reason: string | null;
  last_run_state: string | null;
  last_published_at: string | null;
  revision_number: number;
  created_at: string;
  updated_at: string;
}

export interface ContentPage {
  items: ContentItem[];
  total: number;
  counts: Record<string, number>;
  limit: number;
  offset: number;
}

export interface RevisionSummary {
  id: string;
  number: number;
  origin: string;
  review_state: string;
  approved_at: string | null;
  frozen_at: string | null;
  publishable: boolean;
  preview: string;
  created_at: string;
  updated_at: string;
  published: boolean;
}

export interface BulkResult {
  updated: number;
  skipped: Record<string, string>;
}

// ── 프롬프트 ──────────────────────────────────────────────────────────
export interface PromptVariable {
  name: string;
  label: string;
  type: 'text' | 'textarea' | 'select' | 'number';
  options?: string[];
  required: boolean;
  default: string;
  max?: number;
  min?: number;
  help?: string;
}

export interface PromptDefaults {
  model: string;
  length: string;
  tone: string;
  image_mode: string;
  image_prompt: string;
  split_mode: string;
}

export interface PromptVersion {
  version: number;
  body: string;
  variables: PromptVariable[];
  defaults: PromptDefaults;
  note: string;
  created_at: string;
}

export interface Prompt {
  id: string;
  name: string;
  category: string;
  description: string;
  is_example: boolean;
  active: boolean;
  current_version: number;
  created_at: string;
  updated_at: string;
  version: PromptVersion;
  usage_count: number;
}

// ── 생성 ──────────────────────────────────────────────────────────────
export interface GenerationOptions {
  text: boolean;
  image: boolean;
  text_model?: string;
  image_model?: string;
  image_quality?: string;
  image_size?: string;
  image_count?: number;
  image_placement?: number;
  image_prompt?: string;
  length?: '' | 'short' | 'medium' | 'long';
  tone?: string;
  text_only_fallback: boolean;
  target?: 'all' | 'part' | 'image';
  part_index?: number;
  instruction?: string;
}

export type GenerationTrigger = 'manual' | 'on_publish' | 'daily_at' | 'before_slot' | 'stock' | 'fill_now' | 'regenerate';
export type GenerationState = 'queued' | 'running' | 'succeeded' | 'partial' | 'failed' | 'canceled' | 'unknown';

export interface GenerationRequest {
  id: string;
  idempotency_key: string;
  trigger: GenerationTrigger;
  policy_version: number | null;
  prompt_id: string | null;
  prompt_version: number | null;
  prompt_name: string | null;
  variables: Record<string, string>;
  options: GenerationOptions;
  quantity: number;
  reservation_id: string | null;
  content_id: string | null;
  place_into_slots: boolean;
  state: GenerationState;
  produced: number;
  failed: number;
  error: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  running: number;
  queued: number;
}

export const TRIGGER_LABEL: Record<GenerationTrigger, string> = {
  manual: '버튼 생성',
  on_publish: '게시 시각 생성',
  daily_at: '매일 지정 시각',
  before_slot: '예약 전 생성',
  stock: '재고 보충',
  fill_now: '바로 채우기',
  regenerate: '다시 만들기',
};

export const GEN_STATE_LABEL: Record<GenerationState, string> = {
  queued: '대기',
  running: '생성 중',
  succeeded: '완료',
  partial: '일부 완료',
  failed: '실패',
  canceled: '취소됨',
  unknown: '확인 필요',
};

export interface PlanCounts {
  needed: number;
  bound: number;
  ready: number;
  in_flight: number;
  review_pending: number;
  remaining_today: number;
  shortfall: number;
  capped: boolean;
}

export interface GenerationPlan {
  active: boolean;
  blockers: string[];
  timing: GenerationPolicy['timing'];
  strategy: GenerationPolicy['prepare_strategy'];
  policy_version: number;
  next_run_at: string | null;
  next_run_note: string;
  window_start: string | null;
  window_end: string | null;
  counts: PlanCounts;
  stock_min: number;
  stock_target: number;
  upcoming: { reservation_id: string; slot_at: string; due_at: string; generating: boolean }[];
  recent: { key: string; trigger: GenerationTrigger; shortfall: number; request_id: string | null; note: string | null; created_at: string }[];
}

export interface GenerationResult {
  id: string;
  request_id: string;
  kind: 'text' | 'image';
  target: 'all' | 'part' | 'image';
  part_index: number | null;
  text: string | null;
  media_id: string | null;
  state: 'new' | 'applied' | 'discarded';
  created_at: string;
  decided_at: string | null;
  thumb_url?: string;
  url?: string;
  base_revision_number: number;
}

export interface UsageSummary {
  period: string;
  currency: string;
  spent_micros: number;
  held_micros: number;
  limit_micros: number;
  on_limit: string;
  by_category: Record<string, number>;
  counts: Record<string, number>;
  uncertain_count: number;
  pricing_known: boolean;
  pricing_verified_at: string;
  exceeded: boolean;
}

// ── 시간표·예약 ───────────────────────────────────────────────────────
export interface ScheduleRules {
  daily_target: number;
  base_interval_min: number;
  jitter_min_min: number;
  jitter_max_min: number;
  reply_gap_sec: number;
  end_margin_min: number;
  substitute_unready: boolean;
  policy_version: number;
  version: number;
}

export interface ScheduleWindow {
  id?: string;
  label: string;
  weekdays: number[];
  start_minute: number;
  end_minute: number;
  enabled: boolean;
  sort_order?: number;
}

export interface ScheduleSettings {
  rules: ScheduleRules;
  windows: ScheduleWindow[];
  exceptions: { date: string; note: string }[];
  timezone: string;
  capacity: [number, number];
}

export type ReservationState = 'open' | 'assigned' | 'waiting' | 'publishing' | 'partial' | 'unknown' | 'published' | 'failed' | 'canceled';

export interface ReservationItem {
  id: string;
  slot_at: string;
  original_slot_at: string;
  jitter_sec: number;
  local_date: string;
  pinned: boolean;
  manual: boolean;
  state: ReservationState;
  wait_reason: string | null;
  delay_note: string | null;
  generation_due_at: string | null;
  generating: boolean;
  content_id: string | null;
  title: string | null;
  preview: string | null;
  stage: Stage | null;
  part_count: number;
  image_count: number;
  unapproved_change: boolean;
  window: string;
}

export interface ScheduleDay {
  date: string;
  target: number;
  count: number;
  posts: number;
  posts_approx: boolean;
  excluded: boolean;
  exclude_note: string;
  capacity_min: number;
  capacity_max: number;
  intervals: { start: string; end: string; labels: string[] }[];
  items: ReservationItem[];
}

export interface SchedulePlanReport {
  seed: number;
  from: string;
  to: string;
  slots: { at: string; local_date: string; window: string; jitter_sec: number; title: string | null }[];
  days: { date: string; target: number; count: number; shortfall: number; capacity_min: number; capacity_max: number; excluded: boolean; partial: boolean }[];
  canceled: number;
  reassigned: number;
  released: number;
  pinned: number;
}

export const RES_STATE_LABEL: Record<ReservationState, string> = {
  open: '빈 예약',
  assigned: '준비 완료',
  waiting: '대기',
  publishing: '게시 중',
  partial: '일부 완료',
  unknown: '확인 필요',
  published: '게시 완료',
  failed: '실패',
  canceled: '건너뜀',
};
