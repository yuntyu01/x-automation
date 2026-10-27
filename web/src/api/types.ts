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
