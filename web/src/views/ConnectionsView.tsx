import { useState } from 'react';
import { api, ApiError, type Connection } from '../api/client';
import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from '../components/Icons';

export function ConnectionsView({
  onEdit,
  onCreate,
}: {
  onEdit: (id: string) => void;
  onCreate: () => void;
}) {
  const { t } = useI18n();
  const { connections, sessions, notify, refreshSessions, trustFromError } = useStore();
  const [busy, setBusy] = useState<string | null>(null);

  const sessionFor = (id: string) => sessions.find((s) => s.connectionId === id);

  const connect = async (c: Connection) => {
    setBusy(c.id);
    try {
      await api.connect(c.id);
      await refreshSessions();
      notify('success', t('live.connected', { name: c.name }));
    } catch (e) {
      if (e instanceof ApiError && (e.code === 'untrusted-host-key' || e.code === 'untrusted-certificate')) {
        trustFromError(e, c.id);
      } else {
        notify('error', e instanceof Error ? e.message : 'Connect failed');
      }
    } finally {
      setBusy(null);
    }
  };

  const disconnect = async (sessionId: string) => {
    await api.disconnect(sessionId);
    await refreshSessions();
  };

  const test = async (c: Connection) => {
    setBusy(c.id);
    try {
      await api.testConnection(c.id);
      notify('success', t('connections.testOk'));
    } catch (e) {
      if (e instanceof ApiError && (e.code === 'untrusted-host-key' || e.code === 'untrusted-certificate')) {
        trustFromError(e, c.id);
      } else {
        notify('error', e instanceof Error ? e.message : t('connections.testFailed'));
      }
    } finally {
      setBusy(null);
    }
  };

  const duplicate = async (id: string) => {
    await api.duplicateConnection(id);
    notify('success', 'Duplicated');
  };

  const remove = async (c: Connection) => {
    if (!window.confirm(t('connections.deleteConfirm', { name: c.name }))) return;
    await api.deleteConnection(c.id);
    notify('info', 'Deleted');
  };

  return (
    <div className="file-area" style={{ padding: 0 }}>
      <div className="spread" style={{ padding: '16px 20px', borderBottom: '1px solid var(--border)' }}>
        <h2 style={{ margin: 0, fontSize: 17 }}>{t('connections.title')}</h2>
        <button className="btn primary" onClick={onCreate}>
          <Icon.Plus size={15} />
          {t('connections.new')}
        </button>
      </div>

      {connections.length === 0 && (
        <div className="empty-state">
          <Icon.Plug className="big-icon" />
          <p>{t('connections.none')}</p>
          <p className="faint">{t('connections.noneHint')}</p>
        </div>
      )}

      <div style={{ padding: 16, display: 'grid', gap: 10 }}>
        {connections.map((c) => {
          const sess = sessionFor(c.id);
          const connected = sess?.state === 'connected';
          return (
            <div key={c.id} className="card spread" style={{ padding: 16 }}>
              <div className="row" style={{ gap: 12, minWidth: 0 }}>
                <span
                  style={{
                    width: 38,
                    height: 38,
                    borderRadius: 9,
                    background: connected ? 'var(--success-subtle)' : 'var(--bg-subtle)',
                    color: connected ? 'var(--success)' : 'var(--text-subtle)',
                    display: 'grid',
                    placeItems: 'center',
                  }}
                >
                  <Icon.Server size={20} />
                </span>
                <div style={{ minWidth: 0 }}>
                  <div className="row" style={{ gap: 8 }}>
                    <strong className="truncate">{c.name}</strong>
                    <span className="pill">{c.protocol}</span>
                    {c.protocol === 'ftp' && (
                      <span className="pill warn" title={t('files.capabilities.plainFtp')}>
                        insecure
                      </span>
                    )}
                    {connected ? (
                      <span className="pill ok">
                        <span className="dot" />
                        {t('connections.connected')}
                      </span>
                    ) : (
                      <span className="pill">{t('connections.disconnected')}</span>
                    )}
                  </div>
                  <div className="faint mono" style={{ fontSize: 12.5 }}>
                    {c.username}@{c.host}:{c.port}
                  </div>
                </div>
              </div>

              <div className="row" style={{ gap: 6 }}>
                {connected ? (
                  <button className="btn sm" onClick={() => void disconnect(sess!.id)}>
                    <Icon.PlugOff size={14} />
                    {t('connections.disconnect')}
                  </button>
                ) : (
                  <button className="btn sm primary" disabled={busy === c.id} onClick={() => void connect(c)}>
                    <Icon.Plug size={14} />
                    {busy === c.id ? t('connections.connecting') : t('connections.connect')}
                  </button>
                )}
                <button className="btn sm ghost" onClick={() => void test(c)} aria-label={t('connections.test')}>
                  {t('connections.test')}
                </button>
                <button className="btn icon sm ghost" onClick={() => onEdit(c.id)} aria-label={t('connections.edit')}>
                  <Icon.Edit size={15} />
                </button>
                <button className="btn icon sm ghost" onClick={() => void duplicate(c.id)} aria-label={t('connections.duplicate')}>
                  <Icon.Copy size={15} />
                </button>
                <button className="btn icon sm danger" onClick={() => void remove(c)} aria-label={t('connections.delete')}>
                  <Icon.Trash size={15} />
                </button>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
