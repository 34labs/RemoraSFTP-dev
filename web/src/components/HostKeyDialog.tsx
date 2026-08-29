import { useState } from 'react';
import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Modal } from './Modal';
import { Icon } from './Icons';

// HostKeyDialog surfaces unknown SSH host keys / unverified FTPS certificates
// and requires an explicit trust decision. There is no "always trust blindly"
// path without reviewing the fingerprint.
export function HostKeyDialog() {
  const { t } = useI18n();
  const { trustPrompt, setTrustPrompt, notify } = useStore();
  const [busy, setBusy] = useState(false);

  if (!trustPrompt) return null;

  const confirm = async () => {
    setBusy(true);
    try {
      await trustPrompt.onTrust();
      notify('success', t('hostkey.trusted'));
    } finally {
      setBusy(false);
      setTrustPrompt(null);
    }
  };

  return (
    <Modal
      title={t('hostkey.title')}
      onClose={() => setTrustPrompt(null)}
      footer={
        <>
          <button className="btn" onClick={() => setTrustPrompt(null)}>
            {t('hostkey.cancel')}
          </button>
          <button className="btn primary" disabled={busy} onClick={() => void confirm()}>
            <Icon.Shield size={15} />
            {t('hostkey.trustAlways')}
          </button>
        </>
      }
    >
      <p className="muted">
        {trustPrompt.kind === 'ssh' ? t('hostkey.unknownHost') : t('hostkey.unknownCert')}
      </p>

      <div className="card" style={{ padding: 14, background: 'var(--bg-subtle)' }}>
        <dl className="kv" style={{ margin: 0 }}>
          <dt>{t('hostkey.fingerprint')}</dt>
          <dd className="mono" style={{ wordBreak: 'break-all' }}>
            {trustPrompt.fingerprint}
          </dd>
          {trustPrompt.keyType && (
            <>
              <dt>{t('hostkey.keyType')}</dt>
              <dd className="mono">{trustPrompt.keyType}</dd>
            </>
          )}
          {trustPrompt.subject && (
            <>
              <dt>{t('hostkey.subject')}</dt>
              <dd>{trustPrompt.subject}</dd>
            </>
          )}
        </dl>
      </div>

      <p className="hint" style={{ marginTop: 12, color: 'var(--warning)' }}>
        ⚠ {t('hostkey.warning')}
      </p>
    </Modal>
  );
}
