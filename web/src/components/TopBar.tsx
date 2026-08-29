import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from './Icons';
import type { View } from '../App';

export function TopBar({
  view,
  onNavigate,
  onNewConnection,
  onPalette,
}: {
  view: View;
  onNavigate: (v: View) => void;
  onNewConnection: () => void;
  onPalette: () => void;
}) {
  const { t } = useI18n();
  const { sessions, settings } = useStore();
  const active = sessions.find((s) => s.state === 'connected');

  return (
    <header className="topbar">
      <div className="brand">
        <span className="brand-mark">
          <Icon.Box />
        </span>
        <span>{t('app.name')}</span>
      </div>

      <div className="grow" />

      {active && (
        <span className="pill ok" role="status" aria-live="polite">
          <span className="dot pulse" />
          {active.connectionName}
        </span>
      )}
      {!active && (
        <span className="pill">
          <span className="dot" />
          {t('connections.disconnected')}
        </span>
      )}

      <button
        className="btn"
        onClick={onPalette}
        aria-label={t('common.commandPalette')}
        title={`${t('common.commandPalette')} (Ctrl/⌘+K)`}
      >
        <Icon.Command size={15} />
        <span className="kbd">⌘K</span>
      </button>

      {view !== 'connections' && (
        <button className="btn primary" onClick={onNewConnection}>
          <Icon.Plus size={15} />
          {t('connections.new')}
        </button>
      )}

      {settings && (
        <button
          className="btn icon ghost"
          aria-label={t('settings.title')}
          onClick={() => onNavigate('settings')}
        >
          <Icon.Settings />
        </button>
      )}
    </header>
  );
}
