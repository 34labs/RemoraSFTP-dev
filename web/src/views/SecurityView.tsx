import { useEffect, useState } from 'react';
import { api, type TrustEntry } from '../api/client';
import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from '../components/Icons';
import { formatDate } from '../util/format';

export function SecurityView() {
  const { t } = useI18n();
  const { settings, notify } = useStore();
  const [backend, setBackend] = useState('');
  const [trusted, setTrusted] = useState<TrustEntry[]>([]);

  useEffect(() => {
    api.handshake().then((h) => setBackend(h.credentialStore)).catch(() => { });
    api.trusted().then((r) => setTrusted(r.trusted)).catch(() => { });
  }, []);

  const revoke = async (id: string) => {
    await api.untrust(id);
    setTrusted((prev) => prev.filter((x) => x.id !== id));
    notify('info', 'Trust revoked');
  };

  return (
    <div className="file-area">
      <div style={{ maxWidth: 840, margin: '0 auto', padding: 24 }}>
        <h2 style={{ fontSize: 19, marginTop: 0 }}>{t('security.title')}</h2>
        <p className="muted">{t('security.subtitle')}</p>

        <div className="diagram" aria-label="Architecture diagram">
          <span className="node">
            <Icon.Files size={18} /> {t('security.title') === '' ? '' : 'Your browser'}
          </span>
          <span className="arrow">→</span>
          <span className="node">
            <Icon.Box size={18} /> Local RemoraSFTP engine
            <br />
            <small className="faint">(this device, 127.0.0.1)</small>
          </span>
          <span className="arrow">→</span>
          <span className="node">
            <Icon.Server size={18} /> Your FTP / FTPS / SFTP server
          </span>
        </div>

        <div className="card" style={{ padding: 20, marginTop: 18 }}>
          <Section icon={<Icon.Server />} title="Local engine">
            {t('security.localEngine')}
          </Section>
          <Section icon={<Icon.Transfers />} title="Direct transfers">
            {t('security.directTransfers')}
          </Section>
          <Section icon={<Icon.Shield />} title="No cloud account">
            {t('security.noCloudAccount')}
          </Section>
          <Section icon={<Icon.Shield />} title="Credentials">
            {t('security.credentials')}
            <p className="hint">
              {t('security.credentialBackend')}: <strong>{backend}</strong>
            </p>
          </Section>
          <Section icon={<Icon.Shield />} title="Host keys & certificates">
            {t('security.hostKeys')}
          </Section>
          <Section icon={<Icon.Plug />} title="Local API authentication">
            {t('security.localAuth')}
          </Section>
          <Section icon={<Icon.Activity />} title="Logs">
            {t('security.logs')}
          </Section>
          <Section icon={<Icon.Files />} title="Temporary files">
            {t('security.temp')}
          </Section>
        </div>

        <h3 style={{ marginTop: 24 }}>{t('security.notDoing')}</h3>
        <ul style={{ lineHeight: 1.9 }}>
          {(t('security.notDoingList') as string).split('\n').map((line, i) => (
            <li key={i}>
              <Icon.Check size={14} style={{ color: 'var(--success)', verticalAlign: '-2px' }} />{' '}
              {line}
            </li>
          ))}
        </ul>

        <h3 style={{ marginTop: 28 }}>{t('security.trustedHosts')}</h3>
        {trusted.length === 0 && <p className="muted">-</p>}
        {trusted.map((e) => (
          <div key={e.id} className="spread card" style={{ padding: '10px 14px', marginBottom: 8 }}>
            <div>
              <strong className="mono">{e.id}</strong>
              <div className="faint mono" style={{ fontSize: 12, wordBreak: 'break-all' }}>
                {e.fingerprint}
              </div>
              <div className="faint" style={{ fontSize: 12 }}>
                {e.kind} · {formatDate(e.trustedAt)}
              </div>
            </div>
            <button className="btn danger sm" onClick={() => void revoke(e.id)}>
              {t('security.revoke')}
            </button>
          </div>
        ))}

        <p className="hint" style={{ marginTop: 30 }}>
          Version {settings ? '' : ''}
          RemoraSFTP never claims "nothing ever leaves your device": by design you connect to remote
          servers, so file data is sent to and from the servers you choose. That traffic is always
          direct between your device and that server - never through a RemoraSFTP relay.
        </p>
      </div>
    </div>
  );
}

function Section({
  icon,
  title,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="row" style={{ alignItems: 'flex-start', gap: 12, padding: '10px 0' }}>
      <span style={{ color: 'var(--accent)', marginTop: 2 }}>{icon}</span>
      <div>
        <strong>{title}</strong>
        <p className="muted" style={{ margin: '3px 0 0' }}>
          {children}
        </p>
      </div>
    </div>
  );
}
