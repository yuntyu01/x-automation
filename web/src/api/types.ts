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
