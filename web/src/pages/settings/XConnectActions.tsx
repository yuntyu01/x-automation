import type { IntegrationStatus } from '../../api/types';

// X 계정 연결·해제 버튼. 실제 OAuth 흐름은 서버의 /api/x/oauth/* 가 담당한다
export function XConnectActions(_props: { status: IntegrationStatus }) {
  return null;
}
