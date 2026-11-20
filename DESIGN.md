# Exa Design Direction

## Dual-Surface Identity

Exa separates its two user surfaces cleanly while sharing one calm typography-led identity (`exa` wordmark in tight-tracked `IBM Plex Sans` paired with a 6px x 6px geometric baseline mark):

- **Public Status Page**: Light editorial default inspired by calm operational status publishing (`ENERGY 1`, `RHYTHM 2`, `MOTION 1`), with an optional user-persisted dark theme toggle.
- **Admin Console**: Obsidian Dark operations default built for high-signal telemetry, multi-monitor scanning, and fast triage, with an optional light theme toggle.

## Typography

- Primary UI and reading font: `IBM Plex Sans` with system fallback `-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif` across headings, status summaries, and incident narratives.
- Numeric telemetry font: `IBM Plex Mono` with fallback `ui-monospace, SFMono-Regular, Consolas, monospace`, strictly reserved for latency (`ms`), uptime percentages (`%`), HTTP status codes, UTC timestamps, push tokens, and API keys so tabular telemetry aligns vertically.

## Public Status Page Palette (Light Editorial Default)

| Token | Hex | Role and Rationale |
|---|---|---|
| `--pub-bg` | `#FFFFFF` | Clean editorial paper canvas maximizing readability for stakeholders. |
| `--pub-text` | `#18181B` | High-contrast zinc primary ink (>16:1 on white). |
| `--pub-text-muted` | `#52525B` | Secondary description and metadata ink (>7:1 on white). |
| `--pub-caption` | `#71717A` | Subtle axis and timestamp caption ink (~4.8:1 on white). |
| `--pub-up` | `#147D4B` | Operational emerald passing >5:1 on white surfaces. |
| `--pub-degraded` | `#9A6700` | Amber warning tone meeting WCAG AA on white (>4.6:1). |
| `--pub-down` | `#B91C1C` | Critical outage red with strong contrast (>6:1 on white). |

## Admin Console Palette (Obsidian Dark Operations Default)

| Token | Hex | Role and Rationale |
|---|---|---|
| `--ops-bg` | `#09090B` | Deep obsidian canvas eliminating glare during extended ops monitoring. |
| `--ops-sidebar` | `#0C0C0F` | Subtle step above canvas to separate navigation from workspace content. |
| `--ops-card` | `#111114` | Elevated card and table surface with clear depth over `#09090B`. |
| `--ops-border` | `#232329` | Crisp low-noise hairline border separating cards and data rows. |
| `--ops-text` | `#F4F4F5` | Primary zinc ink preventing pure-white halation while keeping >15:1 contrast. |
| `--ops-text-secondary` | `#A1A1AA` | Secondary label and body metadata ink (>7:1 on `#111114`). |
| `--ops-caption` | `#8E8E98` | Monospace axis tick and timestamp caption ink (>5.5:1 on `#111114`). |
| `--ops-btn-bg` | `#FAFAFA` | High-contrast primary action fill on dark surfaces. |
| `--ops-btn-fg` | `#09090B` | Crisp obsidian label ink on primary action buttons (>18:1 contrast). |
| `--ops-up` | `#4ADE80` | Operational green for status badges and checkmarks. |
| `--ops-up-bar` | `#2B8A61` | Calibrated availability bar green avoiding neon glare across 60-slot grids. |
| `--ops-degraded` | `#EAB354` | Degraded amber for warning states and partial downtime bars. |
| `--ops-degraded-bar` | `#E5AE4E` | High-legibility degraded bar tone on `#111114`. |
| `--ops-down` | `#F87171` | Outage red for failing monitor status indicators and text. |
| `--ops-down-bar` | `#E05252` | Outage bar fill tuned for dark card backgrounds. |
| `--ops-maint` | `#60A5FA` | Scheduled maintenance blue distinct from operational and warning states. |
| `--ops-alert-bg` | `#1A0F10` | Deep crimson-tinted surface for active incident and outage alert banners. |
| `--ops-alert-border` | `#5C2326` | Defined crimson border framing active alert banners cleanly. |

## Layout, Accessibility, and Telemetry Integrity

- All metrics on Overview and Monitor Detail (`uptime_24h`, `uptime_30d`, `uptime_90d`, `median_response_24h_ms`, `availability_24h`) derive strictly from real SQLite heartbeats, daily rollups, incidents, and maintenances, returning `null` when zero checks exist rather than fabricating values.
- Every status indicator pairs color with a distinct SVG shape icon and explicit text label.
- Three-state responsive breakpoints (`<640px`, `640px-1023px`, `>=1024px`) keep zero horizontal overflow, >=44px tap targets on interactive controls, high-contrast `:focus-visible` outlines, and `Escape` dismissal on all modals, drawers, and popovers.
