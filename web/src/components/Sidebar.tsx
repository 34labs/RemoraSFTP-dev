import { useI18n } from '../i18n/i18n';
import { Icon } from './Icons';
import type { View } from '../App';

export function Sidebar({
  view,
  onNavigate,
  onNewConnection,
}: {
  view: View;
  onNavigate: (v: View) => void;
  onNewConnection: () => void;
}) {
  const { t } = useI18n();
  const items: { id: View; icon: React.ReactNode; label: string }[] = [
    { id: 'files', icon: <Icon.Files />, label: t('nav.files') },
    { id: 'connections', icon: <Icon.Plug />, label: t('nav.connections') },
    { id: 'transfers', icon: <Icon.Transfers />, label: t('nav.transfers') },
    { id: 'activity', icon: <Icon.Activity />, label: t('nav.activity') },
  ];

  return (
    <nav className="sidebar" aria-label="Primary">
      {items.map((it) => (
        <button
          key={it.id}
          className={`nav-item ${view === it.id ? 'active' : ''}`}
          aria-current={view === it.id ? 'page' : undefined}
          onClick={() => onNavigate(it.id)}
        >
          {it.icon}
          {it.label}
        </button>
      ))}

      <div className="nav-section">{t('settings.title')}</div>
      <button
        className={`nav-item ${view === 'security' ? 'active' : ''}`}
        aria-current={view === 'security' ? 'page' : undefined}
        onClick={() => onNavigate('security')}
      >
        <Icon.Shield />
        {t('nav.security')}
      </button>
      <button
        className={`nav-item ${view === 'settings' ? 'active' : ''}`}
        aria-current={view === 'settings' ? 'page' : undefined}
        onClick={() => onNavigate('settings')}
      >
        <Icon.Settings />
        {t('nav.settings')}
      </button>

      <div className="grow" />
      <button className="btn" onClick={onNewConnection}>
        <Icon.Plus size={15} />
        {t('connections.new')}
      </button>
    </nav>
  );
}
