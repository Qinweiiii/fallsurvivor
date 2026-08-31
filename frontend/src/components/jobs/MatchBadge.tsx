import { Badge } from '@/components/ui/Badge';
import { scoreTone } from '@/lib/utils';

const toneMap = { high: 'mint', good: 'lilac', mid: 'sky', low: 'neutral' } as const;

/** 匹配度徽标。 */
export function MatchBadge({ score }: { score: number }) {
  return <Badge tone={toneMap[scoreTone(score)]}>{score}%</Badge>;
}
