import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from '../components/Icons';
import { formatBytes, formatSpeed, formatEta, formatDate } from '../util/format';
import { api, type TransferJob } from '../api/client';

export function TransfersView() {
  const { t } = useI18n();
  const { transfers, notify } = useStore();

  const pct = (j: TransferJob) => (j.total > 0 ? Math.min(100, Math.round((j.done / j.total) * 100)) : 0);

  return (
    <div className="file-area" style={{ padding: 0 }}>
      <div className="spread" style={{ padding: '16px 20px', borderBottom: '1px solid var(--border)' }}>
        <h2 style={{ margin: 0, fontSize: 17 }}>{t('transfers.title')}</h2>
        <button
          className="btn sm"
          onClick={() => {
            void api.clearTransfers();
            notify('info', 'Cleared completed transfers');
          }}
        >
          {t('transfers.clearDone')}
        </button>
      </div>

      {transfers.length === 0 && (
        <div className="empty-state">
          <Icon.Transfers className="big-icon" />
          <p>{t('transfers.empty')}</p>
          <p className="faint">{t('transfers.emptyHint')}</p>
        </div>
      )}

      {transfers.map((j) => (
        <div key={j.id} className="transfer-row">
          <div className="spread">
            <div className="row" style={{ gap: 10, minWidth: 0 }}>
              {j.direction === 'upload' ? <Icon.Upload size={17} /> : <Icon.Download size={17} />}
              <div style={{ minWidth: 0 }}>
                <div className="truncate" style={{ fontWeight: 600 }}>
                  {j.name}
                </div>
                <div className="faint mono" style={{ fontSize: 12 }}>
                  {j.remotePath}
                </div>
              </div>
            </div>
            <div className="row" style={{ gap: 8 }}>
              <StatusPill status={j.status} />
              {j.status === 'running' || j.status === 'queued' ? (
                <button className="btn sm" onClick={() => void api.cancelTransfer(j.id)}>
                  {t('transfers.cancel')}
                </button>
              ) : (
                j.status === 'failed' && (
                  <button className="btn sm" onClick={() => void api.retryTransfer(j.id)}>
                    {t('transfers.retry')}
                  </button>
                )
              )}
            </div>
          </div>
          <div className="progress-track" role="progressbar" aria-valuenow={pct(j)} aria-valuemin={0} aria-valuemax={100} aria-label={j.name}>
            <div
              className={`progress-fill ${j.status === 'completed' ? 'done' : j.status === 'failed' ? 'failed' : ''}`}
              style={{ width: `${pct(j)}%` }}
            />
          </div>
          <div className="spread faint" style={{ fontSize: 12, marginTop: 5 }}>
            <span>
              {j.total > 0
                ? t('transfers.of', { done: formatBytes(j.done), total: formatBytes(j.total) })
                : formatBytes(j.done)}
              {j.status === 'running' && j.speed > 0 && ` · ${formatSpeed(j.speed)} · ${t('transfers.eta', { time: formatEta(j.etaSeconds) })}`}
            </span>
            {j.status === 'failed' && <span style={{ color: 'var(--danger)' }}>{j.error}</span>}
            <span>{j.startedAt ? formatDate(j.startedAt) : ''}</span>
          </div>
        </div>
      ))}
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const cls = status === 'completed' ? 'ok' : status === 'failed' ? 'err' : status === 'running' ? '' : 'warn';
  return <span className={`pill ${cls}`}>{status}</span>;
}
