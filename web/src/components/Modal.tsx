import type { ComponentChildren } from 'preact';
import { useEffect, useRef } from 'preact/hooks';
import { CloseIcon } from './Icons';

interface ModalProps {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ComponentChildren;
  footer?: ComponentChildren;
  wide?: boolean;
}

export function Modal({ open, title, onClose, children, footer, wide }: ModalProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) return;
    const prevFocus = document.activeElement as HTMLElement | null;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onCloseRef.current();
      }
    };
    window.addEventListener('keydown', onKey);
    // Move focus inside dialog once on open without re-running on parent state updates.
    const focusTimer = window.setTimeout(() => {
      const target =
        panelRef.current?.querySelector<HTMLElement>(
          '.modal-body input:not([disabled]), .modal-body select:not([disabled]), .modal-body textarea:not([disabled])',
        ) ??
        panelRef.current?.querySelector<HTMLElement>('button, input, select, textarea, a[href]');
      target?.focus();
    }, 0);
    return () => {
      window.clearTimeout(focusTimer);
      window.removeEventListener('keydown', onKey);
      prevFocus?.focus?.();
    };
  }, [open]);

  if (!open) return null;

  return (
    <div
      class="modal-backdrop"
      role="presentation"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={panelRef}
        class={`modal-panel${wide ? ' modal-panel--wide' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby="exa-modal-title"
      >
        <div class="modal-header">
          <h2 id="exa-modal-title" class="modal-title">
            {title}
          </h2>
          <button
            type="button"
            class="btn btn-sm btn-icon-only"
            onClick={onClose}
            aria-label="Close dialog"
          >
            <CloseIcon size={16} />
          </button>
        </div>
        <div class="modal-body">{children}</div>
        {footer ? <div class="modal-footer">{footer}</div> : null}
      </div>
    </div>
  );
}
