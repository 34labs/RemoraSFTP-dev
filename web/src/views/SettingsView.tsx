import { useEffect, useState } from 'react';
import { api, type Settings } from '../api/client';
import { useI18n } from '../i18n/i18n';
import { languages } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from '../components/Icons';

export function SettingsView() {
  const { t } = useI18n();
  const { settings, setSettings, notify } = useStore();
  const [draft, setDraft] = useState<Settings | null>(settings);

  useEffect(() => setDraft(settings), [settings]);
  if (!draft) return <p className="muted" style={{ padding: 20 }}>{t('common.loading')}</p>;

  const update = (patch: Partial<Settings>) => setDraft({ ...draft, ...patch });

  const save = async () => {
    await setSettings(draft);
    notify('success', t('settings.saved'));
  };

  return (
    <div className="file-area">
      <div style={{ maxWidth: 640, margin: '0 auto', padding: 24 }}>
        <h2 style={{ marginTop: 0 }}>{t('settings.title')}</h2>

        <div className="card" style={{ padding: 20, marginBottom: 16 }}>
          <h3 style={{ marginTop: 0 }}>{t('settings.appearance')}</h3>
          <label className="field">
            <span>{t('settings.language')}</span>
            <select
              className="select"
              value={draft.language}
              onChange={(e) => update({ language: e.target.value })}
            >
              {languages.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.name}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>{t('settings.theme')}</span>
            <select
              className="select"
              value={draft.theme}
              onChange={(e) => update({ theme: e.target.value as Settings['theme'] })}
            >
              <option value="system">{t('settings.themeSystem')}</option>
              <option value="light">{t('settings.themeLight')}</option>
              <option value="dark">{t('settings.themeDark')}</option>
            </select>
          </label>
          <label className="field">
            <span>{t('settings.defaultView')}</span>
            <select
              className="select"
              value={draft.defaultView}
              onChange={(e) => update({ defaultView: e.target.value as 'list' | 'grid' })}
            >
              <option value="list">{t('files.listView')}</option>
              <option value="grid">{t('files.gridView')}</option>
            </select>
          </label>
          <Toggle
            label={t('settings.reducedMotion')}
            checked={draft.reducedMotion}
            onChange={(v) => update({ reducedMotion: v })}
          />
        </div>

        <div className="card" style={{ padding: 20, marginBottom: 16 }}>
          <h3 style={{ marginTop: 0 }}>{t('settings.behavior')}</h3>
          <Toggle label={t('settings.confirmDeletes')} checked={draft.confirmDeletes} onChange={(v) => update({ confirmDeletes: v })} />
          <Toggle label={t('settings.openBrowserOnStart')} checked={draft.openBrowserOnStart} onChange={(v) => update({ openBrowserOnStart: v })} />
          <Toggle label={t('settings.showHidden')} checked={draft.showHidden} onChange={(v) => update({ showHidden: v })} />
          <label className="field">
            <span>{t('settings.concurrentTransfers')}</span>
            <input
              className="input"
              type="number"
              min={1}
              max={10}
              value={draft.concurrentTransfers}
              onChange={(e) => update({ concurrentTransfers: Math.max(1, Number(e.target.value)) })}
            />
          </label>
          <label className="field">
            <span>{t('settings.logLevel')}</span>
            <select
              className="select"
              value={draft.logLevel}
              onChange={(e) => update({ logLevel: e.target.value as Settings['logLevel'] })}
            >
              <option value="off">{t('settings.logOff')}</option>
              <option value="error">{t('settings.logError')}</option>
              <option value="warning">{t('settings.logWarning')}</option>
              <option value="info">{t('settings.logInfo')}</option>
            </select>
          </label>
        </div>

        <div className="card" style={{ padding: 20, marginBottom: 16, borderColor: 'var(--warning)' }}>
          <h3 style={{ marginTop: 0 }}>{t('settings.advanced')}</h3>
          <Toggle
            label={t('settings.remoteAccess')}
            checked={draft.remoteAccess}
            onChange={(v) => update({ remoteAccess: v })}
          />
          <p className="hint" style={{ color: 'var(--warning)' }}>⚠ {t('settings.remoteAccessWarn')}</p>
        </div>

        <button className="btn primary" onClick={() => void save()}>
          <Icon.Check size={15} />
          {t('settings.save')}
        </button>
      </div>
    </div>
  );
}

function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="row" style={{ gap: 10, marginBottom: 12, cursor: 'pointer' }}>
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <span>{label}</span>
    </label>
  );
}
