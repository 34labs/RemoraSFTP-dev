import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from '../components/Icons';
import { formatDate } from '../util/format';
import { api } from '../api/client';

export function ActivityView() {
  const { t } = useI18n();
  const { activity, refreshActivity, notify } = useStore();

  const clear = async () => {
    if (!window.confirm(t('activity.clearConfirm'))) return;
    await api.clearActivity();
    await refreshActivity();
    notify('success', 'Log cleared');
  };

  return (
    <div className="file-area" style={{ padding: 0 }}>
      <div className="spread" style={{ padding: '16px 20px', borderBottom: '1px solid var(--border)' }}>
        <h2 style={{ margin: 0, fontSize: 17 }}>{t('activity.title')}</h2>
        <button className="btn sm" onClick={() => void clear()}>
          <Icon.Trash size={14} />
          {t('activity.clear')}
        </button>
      </div>
      <p className="hint" style={{ padding: '12px 20px', margin: 0 }}>{t('activity.hint')}</p>

      {activity.length === 0 && (
        <div className="empty-state">
          <Icon.Activity className="big-icon" />
          <p>{t('activity.empty')}</p>
        </div>
      )}

      <div style={{ padding: '0 20px' }}>
        {activity.map((a) => (
          <div
            key={a.id}
            className="spread"
            style={{ padding: '9px 0', borderBottom: '1px solid var(--border)', gap: 12 }}
          >
            <span className={`pill ${a.level === 'error' ? 'err' : a.level === 'warning' ? 'warn' : 'ok'}`}>
              <span className="dot" />
            </span>
            <span className="grow" style={{ fontSize: 13.5 }}>
              {a.message || a.type}
            </span>
            <span className="faint mono" style={{ fontSize: 12 }}>
              {formatDate(a.time)}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
