import { STAGE_LABEL, type Stage } from '../api/types';
import { Badge, type Tone } from './ui/Badge';

const TONE: Record<Stage, Tone> = {
  draft: 'neutral',
  review: 'warning',
  ready: 'success',
  scheduled: 'scheduled',
  published: 'info',
  problem: 'danger',
  archived: 'neutral',
};

export function StageBadge({ stage }: { stage: Stage }) {
  return <Badge tone={TONE[stage]}>{STAGE_LABEL[stage]}</Badge>;
}
