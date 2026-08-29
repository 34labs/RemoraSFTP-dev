import { useEffect, useMemo, useRef, useState } from 'react';
import { useI18n } from '../i18n/i18n';
import { Icon } from './Icons';
import type { View } from '../App';

interface Cmd {
  id: string;
  label: string;
  icon: React.ReactNode;
  run: () => void;
}

export function CommandPalette({
  open,
  onClose,
  onNavigate,
}: {
  open: boolean;
  onClose: () => void;
  onNavigate: (v: View) => void;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState('');
  const [index, setIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const commands: Cmd[] = useMemo(
    () => [
      { id: 'files', label: t('nav.files'), icon: <Icon.Files size={16} />, run: () => onNavigate('files') },
      { id: 'connections', label: t('nav.connections'), icon: <Icon.Plug size={16} />, run: () => onNavigate('connections') },
      { id: 'transfers', label: t('nav.transfers'), icon: <Icon.Transfers size={16} />, run: () => onNavigate('transfers') },
      { id: 'activity', label: t('nav.activity'), icon: <Icon.Activity size={16} />, run: () => onNavigate('activity') },
      { id: 'security', label: t('nav.security'), icon: <Icon.Shield size={16} />, run: () => onNavigate('security') },
      { id: 'settings', label: t('nav.settings'), icon: <Icon.Settings size={16} />, run: () => onNavigate('settings') },
      { id: 'new', label: t('connections.new'), icon: <Icon.Plus size={16} />, run: () => { onNavigate('connections'); } },
    ],
    [t, onNavigate],
  );

  const filtered = commands.filter((c) => c.label.toLowerCase().includes(query.toLowerCase()));

  useEffect(() => {
    if (open) {
      setQuery('');
      setIndex(0);
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  }, [open]);

  if (!open) return null;

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setIndex((i) => Math.min(filtered.length - 1, i + 1));
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setIndex((i) => Math.max(0, i - 1));
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      const cmd = filtered[index];
      if (cmd) {
        cmd.run();
        onClose();
      }
    }
  };

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette" role="dialog" aria-modal="true" aria-label={t('common.commandPalette')}>
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setIndex(0);
          }}
          onKeyDown={onKey}
          placeholder={t('common.typeCommand')}
          aria-label={t('common.typeCommand')}
        />
        <div className="palette-list" role="listbox">
          {filtered.length === 0 && <div className="muted" style={{ padding: 14 }}>{t('common.noResults')}</div>}
          {filtered.map((c, i) => (
            <button
              key={c.id}
              role="option"
              aria-selected={i === index}
              className={`palette-item ${i === index ? 'active' : ''}`}
              onMouseEnter={() => setIndex(i)}
              onClick={() => {
                c.run();
                onClose();
              }}
            >
              {c.icon}
              {c.label}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
