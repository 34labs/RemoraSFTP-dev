import { useState } from 'react';
import { api, type Entry, type SessionInfo } from '../api/client';
import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from './Icons';
import { formatBytes, formatDate } from '../util/format';

export function Inspector({
  entry,
  session,
  onClose,
  onChange,
}: {
  entry: Entry;
  session: SessionInfo;
  onClose: () => void;
  onChange: () => void;
}) {
  const { t } = useI18n();
  const { notify } = useStore();
  const [mode, setMode] = useState(entry.permissions ?? '');
  const caps = session.caps ?? session.capabilities;

  const applyChmod = async () => {
    if (!session) return;
    try {
      await api.chmod(session.id, entry.path, mode.replace(/[^0-7]/g, ''));
      notify('success', 'Permissions updated');
      onChange();
    } catch (e) {
      notify('error', e instanceof Error ? e.message : 'chmod failed');
    }
  };

  return (
    <aside className="inspector" aria-label={t('inspector.title')}>
      <div className="spread" style={{ marginBottom: 12 }}>
        <h3 style={{ margin: 0, fontSize: 14 }}>{t('inspector.title')}</h3>
        <button className="btn icon ghost" aria-label={t('common.close')} onClick={onClose}>
          <Icon.Close size={15} />
        </button>
      </div>

      <div className="row" style={{ gap: 10, marginBottom: 14 }}>
        <span className="file-icon" style={{ width: 28, height: 28, color: entry.type === 'dir' ? 'var(--warning)' : 'var(--text-subtle)' }}>
          {entry.type === 'dir' ? <Icon.Folder size={28} /> : entry.isSymlink ? <Icon.Link size={28} /> : <Icon.File size={28} />}
        </span>
        <strong className="truncate" title={entry.name}>{entry.name}</strong>
      </div>

      <dl className="kv">
        <dt>{t('inspector.type')}</dt>
        <dd>{entry.type}</dd>
        <dt>{t('inspector.mimeType')}</dt>
        <dd className="mono">{entry.mimeType || '-'}</dd>
        <dt>{t('inspector.size')}</dt>
        <dd>{entry.type === 'dir' ? '-' : formatBytes(entry.size)}</dd>
        <dt>{t('inspector.modified')}</dt>
        <dd>{formatDate(entry.modTime)}</dd>
        <dt>{t('inspector.path')}</dt>
        <dd className="mono" style={{ wordBreak: 'break-all' }}>{entry.path}</dd>
        {entry.permissions && (
          <>
            <dt>{t('inspector.permissions')}</dt>
            <dd className="mono">{entry.permissions}</dd>
          </>
        )}
        {entry.owner && (
          <>
            <dt>{t('inspector.owner')}</dt>
            <dd className="mono">{entry.owner}</dd>
          </>
        )}
        {entry.group && (
          <>
            <dt>{t('inspector.group')}</dt>
            <dd className="mono">{entry.group}</dd>
          </>
        )}
        {entry.isSymlink && (
          <>
            <dt>{t('inspector.target')}</dt>
            <dd className="mono">{entry.linkTarget}</dd>
          </>
        )}
      </dl>

      {caps?.chmod && entry.type !== 'dir' && false /* permissions editing */}
      {caps?.chmod && (
        <div style={{ marginTop: 18 }}>
          <label className="field">
            <span>{t('inspector.changePermissions')} (octal)</span>
            <input
              className="input mono"
              value={mode}
              onChange={(e) => setMode(e.target.value)}
              placeholder="0644"
              maxLength={4}
            />
          </label>
          <button className="btn sm" onClick={() => void applyChmod()}>
            {t('inspector.apply')}
          </button>
        </div>
      )}
      {!caps?.chmod && (
        <p className="hint" style={{ marginTop: 14 }}>{t('files.capabilities.chmodUnsupported')}</p>
      )}
    </aside>
  );
}
