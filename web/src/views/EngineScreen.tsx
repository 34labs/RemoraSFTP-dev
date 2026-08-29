import { useI18n } from '../i18n/i18n';
import { Icon } from '../components/Icons';
import type { EngineState } from '../state/store';

export function EngineScreen({ state }: { state: EngineState }) {
  const { t } = useI18n();
  const unavailable = state === 'unavailable';
  const unauthorized = state === 'unauthorized';
  return (
    <div
      style={{
        height: '100%',
        display: 'grid',
        placeItems: 'center',
        padding: 24,
        background: 'var(--bg)',
      }}
    >
      <div className="card" style={{ maxWidth: 560, padding: 36, textAlign: 'center' }}>
        <span className="brand-mark" style={{ width: 44, height: 44, margin: '0 auto 16px' }}>
          <Icon.Box size={24} />
        </span>
        <h1 style={{ fontSize: 22, margin: '0 0 8px' }}>{t('app.name')}</h1>
        <p className="muted" style={{ margin: '0 0 20px' }}>
          {unavailable ? t('engine.unreachable') : t('engine.starting')}
        </p>
        {unavailable && (
          <>
            <div
              className="pill err"
              style={{ marginBottom: 18 }}
              role="status"
              aria-live="assertive"
            >
              <span className="dot" />
              {t('engine.statusStopped')}
            </div>
            <p className="muted" style={{ textAlign: 'left', fontSize: 13 }}>
              {t('engine.howto')}
            </p>
            <pre
              className="mono"
              style={{
                textAlign: 'left',
                background: 'var(--bg-subtle)',
                padding: 12,
                borderRadius: 8,
                margin: '12px 0',
              }}
            >
              remorasftp start
            </pre>
          </>
        )}
        {unauthorized && (
          <>
            <div
              className="pill warn"
              style={{ marginBottom: 18 }}
              role="status"
              aria-live="assertive"
            >
              <span className="dot" />
              {t('engine.restartRequired')}
            </div>
            <p className="muted" style={{ textAlign: 'left', fontSize: 13 }}>
              {t('engine.restartHint')}
            </p>
          </>
        )}
        {!unavailable && (
          <div className="pill ok" role="status" aria-live="polite">
            <span className="dot pulse" />
            {t('engine.starting')}
          </div>
        )}
      </div>
    </div>
  );
}
