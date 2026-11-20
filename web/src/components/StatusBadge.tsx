import type { StatusTone } from '../utils/format';
import {
  CheckCircleIcon,
  MinusCircleIcon,
  TriangleAlertIcon,
  WrenchIcon,
  XOctagonIcon,
} from './Icons';

export function ToneIcon({ tone, size = 16 }: { tone: StatusTone; size?: number }) {
  switch (tone) {
    case 'up':
      return <CheckCircleIcon size={size} />;
    case 'degraded':
      return <TriangleAlertIcon size={size} />;
    case 'down':
      return <XOctagonIcon size={size} />;
    case 'maint':
      return <WrenchIcon size={size} />;
    default:
      return <MinusCircleIcon size={size} />;
  }
}

export function StatusBadge({
  tone,
  label,
  size = 16,
}: {
  tone: StatusTone;
  label: string;
  size?: number;
}) {
  return (
    <span class={`status-badge status-badge--${tone}`}>
      <ToneIcon tone={tone} size={size} />
      <span>{label}</span>
    </span>
  );
}
