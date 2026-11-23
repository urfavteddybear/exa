interface IconProps {
  size?: number;
  className?: string;
}

export function PulseLogoIcon({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 28 28" fill="none" aria-hidden="true">
      <rect width="28" height="28" rx="6" fill="var(--accent)" />
      <path
        d="M4.5 14H8.8L11.3 8.2L15.1 19.8L17.7 14H23.5"
        stroke="var(--accent-on)"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  );
}

export function CheckCircleIcon({ size = 18, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 20 20" fill="none" aria-hidden="true" class={className}>
      <circle cx="10" cy="10" r="8.25" stroke="currentColor" stroke-width="1.8" />
      <path d="M6.4 10.3L8.9 12.8L13.8 7.6" stroke="currentColor" stroke-width="1.9" stroke-linecap="square" />
    </svg>
  );
}

export function TriangleAlertIcon({ size = 18, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 20 20" fill="none" aria-hidden="true" class={className}>
      <path d="M10 2.5L18.2 16.8H1.8L10 2.5Z" stroke="currentColor" stroke-width="1.8" stroke-linejoin="miter" />
      <path d="M10 7.6V11.8" stroke="currentColor" stroke-width="1.8" stroke-linecap="square" />
      <rect x="9.1" y="13.5" width="1.8" height="1.8" fill="currentColor" />
    </svg>
  );
}

export function XOctagonIcon({ size = 18, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 20 20" fill="none" aria-hidden="true" class={className}>
      <polygon
        points="6.6,2 13.4,2 18,6.6 18,13.4 13.4,18 6.6,18 2,13.4 2,6.6"
        stroke="currentColor"
        stroke-width="1.8"
      />
      <path d="M7.2 7.2L12.8 12.8M12.8 7.2L7.2 12.8" stroke="currentColor" stroke-width="1.8" stroke-linecap="square" />
    </svg>
  );
}

export function WrenchIcon({ size = 18, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 20 20" fill="none" aria-hidden="true" class={className}>
      <path
        d="M12.9 3.2A4.3 4.3 0 0 0 8.2 8.7L3.3 13.6L6.4 16.7L11.3 11.8A4.3 4.3 0 0 0 16.8 7.1L14.2 9.7L12.1 7.6L14.7 5A4.4 4.4 0 0 0 12.9 3.2Z"
        stroke="currentColor"
        stroke-width="1.7"
        stroke-linejoin="miter"
      />
    </svg>
  );
}

export function MinusCircleIcon({ size = 18, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 20 20" fill="none" aria-hidden="true" class={className}>
      <circle cx="10" cy="10" r="8.25" stroke="currentColor" stroke-width="1.8" />
      <path d="M6.2 10H13.8" stroke="currentColor" stroke-width="1.9" stroke-linecap="square" />
    </svg>
  );
}

export function ChevronIcon({ size = 16, open = false }: IconProps & { open?: boolean }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
      style={{ transform: open ? 'rotate(90deg)' : 'rotate(0deg)', transition: 'transform 120ms ease' }}
    >
      <path d="M5.5 3.5L10.5 8L5.5 12.5" stroke="currentColor" stroke-width="1.7" stroke-linecap="square" />
    </svg>
  );
}

export function BellIcon({ size = 16 }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M8 2.2C5.9 2.2 4.3 3.8 4.3 5.9V9.3L2.8 11.2H13.2L11.7 9.3V5.9C11.7 3.8 10.1 2.2 8 2.2Z"
        stroke="currentColor"
        stroke-width="1.5"
      />
      <path d="M6.4 12.8A1.7 1.7 0 0 0 9.6 12.8" stroke="currentColor" stroke-width="1.5" />
    </svg>
  );
}

export function SunIcon({ size = 16 }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="8" cy="8" r="3" stroke="currentColor" stroke-width="1.5" />
      <path
        d="M8 1.5V3M8 13V14.5M1.5 8H3M13 8H14.5M3.4 3.4L4.5 4.5M11.5 11.5L12.6 12.6M12.6 3.4L11.5 4.5M4.5 11.5L3.4 12.6"
        stroke="currentColor"
        stroke-width="1.5"
        stroke-linecap="square"
      />
    </svg>
  );
}

export function MoonIcon({ size = 16 }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M12.8 10.4A5.4 5.4 0 0 1 5.6 3.2A5.5 5.5 0 1 0 12.8 10.4Z"
        stroke="currentColor"
        stroke-width="1.5"
        stroke-linejoin="miter"
      />
    </svg>
  );
}

export function CopyIcon({ size = 15 }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="5.5" y="5.5" width="8" height="8" stroke="currentColor" stroke-width="1.5" />
      <path d="M10.5 5.5V2.5H2.5V10.5H5.5" stroke="currentColor" stroke-width="1.5" />
    </svg>
  );
}

export function MenuIcon({ size = 16 }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M2 4H14M2 8H14M2 12H14" stroke="currentColor" stroke-width="1.7" stroke-linecap="square" />
    </svg>
  );
}

export function CloseIcon({ size = 16 }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3.5 3.5L12.5 12.5M12.5 3.5L3.5 12.5" stroke="currentColor" stroke-width="1.7" stroke-linecap="square" />
    </svg>
  );
}
