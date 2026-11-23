import { useEffect, useRef, useState } from 'preact/hooks';
import type { Monitor } from '../types';
import { ChevronIcon } from './Icons';

interface MonitorMultiSelectProps {
  monitors: Monitor[];
  value: string[];
  onChange: (ids: string[]) => void;
  label: string;
}

// Compact dropdown with a searchable checkbox list, so long monitor lists don't take over a form.
export function MonitorMultiSelect({ monitors, value, onChange, label }: MonitorMultiSelectProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const listId = useRef(`msel-${Math.random().toString(36).slice(2)}`).current;

  useEffect(() => {
    if (!open) return;
    searchRef.current?.focus();
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  const selected = monitors.filter((m) => value.includes(m.id));
  const summary =
    selected.length === 0
      ? 'None selected'
      : selected.length <= 2
        ? selected.map((m) => m.name).join(', ')
        : `${selected[0].name}, ${selected[1].name} +${selected.length - 2} more`;

  const q = query.trim().toLowerCase();
  const visible = q ? monitors.filter((m) => m.name.toLowerCase().includes(q)) : monitors;

  const toggle = (id: string, checked: boolean) =>
    onChange(checked ? [...value, id] : value.filter((v) => v !== id));

  return (
    <div
      class="multi-select"
      ref={rootRef}
      onKeyDown={(e) => {
        if (e.key === 'Escape' && open) {
          // Close only the dropdown, not the surrounding modal.
          e.stopPropagation();
          setOpen(false);
          triggerRef.current?.focus();
        }
      }}
    >
      <button
        ref={triggerRef}
        type="button"
        class="select multi-select-trigger"
        aria-label={`${label}: ${selected.length} selected`}
        aria-expanded={open}
        aria-controls={listId}
        title={selected.map((m) => m.name).join(', ')}
        onClick={() => setOpen((o) => !o)}
      >
        <span class={selected.length === 0 ? 'multi-select-placeholder' : 'multi-select-summary'}>{summary}</span>
        <span class={`multi-select-chevron${open ? ' is-open' : ''}`}>
          <ChevronIcon size={14} />
        </span>
      </button>

      {open ? (
        <div class="multi-select-panel" id={listId}>
          <input
            ref={searchRef}
            class="input multi-select-search"
            type="search"
            placeholder="Search monitors"
            aria-label="Search monitors"
            value={query}
            onInput={(e) => setQuery((e.currentTarget as HTMLInputElement).value)}
          />
          <div class="multi-select-list" role="group" aria-label={label}>
            {visible.length === 0 ? (
              <p class="form-help multi-select-empty">No monitors match "{query}".</p>
            ) : (
              visible.map((m) => (
                <label key={m.id} class="checkbox-row multi-select-option">
                  <input
                    type="checkbox"
                    checked={value.includes(m.id)}
                    onChange={(e) => toggle(m.id, (e.currentTarget as HTMLInputElement).checked)}
                  />
                  <span>{m.name}</span>
                </label>
              ))
            )}
          </div>
          {value.length > 0 ? (
            <div class="multi-select-footer">
              <span class="form-help">{value.length} selected</span>
              <button type="button" class="btn btn-sm" onClick={() => onChange([])}>
                Clear
              </button>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
