import { useEffect, useState } from 'react';
import { api, ApiError, type Connection, type Protocol } from '../api/client';
import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Modal } from './Modal';
import { Icon } from './Icons';

const PROTOCOLS: { value: Protocol; label: string; defaultPort: number }[] = [
  { value: 'sftp', label: 'SFTP (SSH)', defaultPort: 22 },
  { value: 'ftps', label: 'FTPS (TLS)', defaultPort: 21 },
  { value: 'ftp', label: 'FTP (legacy, plaintext)', defaultPort: 21 },
];

export function ConnectionDialog({
  connection,
  onClose,
  onSaved,
}: {
  connection: Connection | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const { notify } = useStore();

  const [form, setForm] = useState(() => ({
    id: connection?.id ?? '',
    name: connection?.name ?? '',
    protocol: (connection?.protocol ?? 'sftp') as Protocol,
    host: connection?.host ?? '',
    port: connection?.port ?? 22,
    username: connection?.username ?? '',
    auth: connection?.auth ?? 'password',
    startDir: connection?.startDir ?? '',
    password: '',
    privateKeyPem: '',
    keyPassphrase: '',
    ftpsImplicit: connection?.settings?.ftpsImplicit ?? false,
    tlsVerify: connection?.settings?.tlsVerify ?? true,
    passiveMode: connection?.settings?.passiveMode ?? true,
    keepAlive: connection?.settings?.keepAliveSeconds ?? 30,
    showAdvanced: false,
  }));
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [secretNote] = useState(
    connection ? (connection.hasSecret || connection.hasPrivateKey) : false,
  );

  useEffect(() => {
    // Adjust port when switching protocol (only if user hasn't customized).
    setForm((f) => ({ ...f, port: PROTOCOLS.find((p) => p.value === f.protocol)!.defaultPort }));
  }, [form.protocol]);

  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const payload = () => ({
    id: form.id || undefined,
    name: form.name,
    protocol: form.protocol,
    host: form.host,
    port: Number(form.port),
    username: form.username,
    auth: form.auth,
    startDir: form.startDir || undefined,
    ftpsImplicit: form.ftpsImplicit,
    tlsVerify: form.tlsVerify,
    passiveMode: form.passiveMode,
    keepAliveSeconds: Number(form.keepAlive) || 0,
    password: form.password !== '' ? form.password : undefined,
    privateKeyPem: form.privateKeyPem !== '' ? form.privateKeyPem : undefined,
    keyPassphrase: form.keyPassphrase !== '' ? form.keyPassphrase : undefined,
  });

  const save = async (thenTest = false) => {
    setSaving(true);
    try {
      const r = await api.saveConnection(payload() as never);
      notify('success', t('connections.saved'));
      if (thenTest) {
        setTesting(true);
        try {
          await api.testConnection(r.connection.id);
          notify('success', t('connections.testOk'));
        } catch (e) {
          notify('error', e instanceof ApiError ? e.message : t('connections.testFailed'));
        }
        setTesting(false);
      }
      onSaved();
    } catch (e) {
      notify('error', e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={connection ? t('connections.edit') : t('connections.new')}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            {t('common.cancel')}
          </button>
          <button className="btn" disabled={testing} onClick={() => void save(true)}>
            <Icon.Check size={15} />
            {testing ? '…' : t('connections.test')}
          </button>
          <button className="btn primary" disabled={saving} onClick={() => void save(false)}>
            {saving ? '…' : t('common.save')}
          </button>
        </>
      }
    >
      <label className="field">
        <span>{t('connections.name')}</span>
        <input
          className="input"
          data-autofocus="true"
          value={form.name}
          onChange={(e) => set('name', e.target.value)}
          placeholder="My server"
        />
      </label>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 110px', gap: 10 }}>
        <label className="field">
          <span>{t('connections.host')}</span>
          <input
            className="input"
            value={form.host}
            onChange={(e) => set('host', e.target.value)}
            placeholder="example.com"
          />
        </label>
        <label className="field">
          <span>{t('connections.port')}</span>
          <input
            className="input"
            type="number"
            value={form.port}
            onChange={(e) => set('port', Number(e.target.value))}
          />
        </label>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
        <label className="field">
          <span>{t('connections.protocol')}</span>
          <select
            className="select"
            value={form.protocol}
            onChange={(e) => set('protocol', e.target.value as Protocol)}
          >
            {PROTOCOLS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>{t('connections.username')}</span>
          <input
            className="input"
            value={form.username}
            onChange={(e) => set('username', e.target.value)}
            placeholder={form.protocol === 'ftp' ? 'anonymous' : 'user'}
          />
        </label>
      </div>

      <label className="field">
        <span>{t('connections.authMethod')}</span>
        <select
          className="select"
          value={form.auth}
          onChange={(e) => set('auth', e.target.value as never)}
        >
          {form.protocol === 'sftp' ? (
            <>
              <option value="password">{t('connections.passwordAuth')}</option>
              <option value="key">{t('connections.keyAuth')}</option>
              <option value="keyagent">{t('connections.keyAgentAuth')}</option>
            </>
          ) : (
            <>
              <option value="password">{t('connections.passwordAuth')}</option>
              <option value="none">{t('connections.anonymousAuth')}</option>
            </>
          )}
        </select>
      </label>

      {form.auth === 'password' && (
        <label className="field">
          <span>{t('connections.password')}</span>
          <input
            className="input"
            type="password"
            value={form.password}
            autoComplete="new-password"
            onChange={(e) => set('password', e.target.value)}
            placeholder={secretNote ? '•••••••• (stored - leave blank to keep)' : ''}
          />
          <span className="hint">
            Stored on this device only ({'OS credential store or encrypted file'}). Never sent to
            any RemoraSFTP service.
          </span>
        </label>
      )}

      {form.auth === 'key' && (
        <>
          <label className="field">
            <span>{t('connections.privateKey')}</span>
            <textarea
              className="textarea"
              value={form.privateKeyPem}
              onChange={(e) => set('privateKeyPem', e.target.value)}
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;…&#10;-----END OPENSSH PRIVATE KEY-----"
            />
          </label>
          <label className="field">
            <span>{t('connections.passphrase')}</span>
            <input
              className="input"
              type="password"
              value={form.keyPassphrase}
              autoComplete="new-password"
              onChange={(e) => set('keyPassphrase', e.target.value)}
            />
          </label>
        </>
      )}

      <label className="field">
        <span>{t('connections.startDir')}</span>
        <input
          className="input mono"
          value={form.startDir}
          onChange={(e) => set('startDir', e.target.value)}
          placeholder="/"
        />
      </label>

      <button
        className="btn ghost sm"
        style={{ marginBottom: 10 }}
        onClick={() => set('showAdvanced', !form.showAdvanced)}
        aria-expanded={form.showAdvanced}
      >
        {form.showAdvanced ? '▾' : '▸'} {t('connections.advanced')}
      </button>

      {form.showAdvanced && (
        <div className="card" style={{ padding: 14, background: 'var(--bg-subtle)' }}>
          {form.protocol === 'ftps' && (
            <>
              <label className="row" style={{ gap: 8, marginBottom: 8 }}>
                <input
                  type="checkbox"
                  checked={form.ftpsImplicit}
                  onChange={(e) => set('ftpsImplicit', e.target.checked)}
                />
                {t('connections.ftpsImplicit')}
              </label>
              <label className="row" style={{ gap: 8, marginBottom: 8 }}>
                <input
                  type="checkbox"
                  checked={form.tlsVerify}
                  onChange={(e) => set('tlsVerify', e.target.checked)}
                />
                {t('connections.tlsVerify')}
              </label>
            </>
          )}
          {(form.protocol === 'ftp' || form.protocol === 'ftps') && (
            <label className="row" style={{ gap: 8, marginBottom: 8 }}>
              <input
                type="checkbox"
                checked={form.passiveMode}
                onChange={(e) => set('passiveMode', e.target.checked)}
              />
              {t('connections.passiveMode')}
            </label>
          )}
          {form.protocol === 'sftp' && (
            <label className="field">
              <span>{t('connections.keepAlive')}</span>
              <input
                className="input"
                type="number"
                value={form.keepAlive}
                onChange={(e) => set('keepAlive', Number(e.target.value))}
              />
            </label>
          )}
          {form.protocol === 'ftp' && (
            <p className="hint" style={{ color: 'var(--warning)' }}>
              ⚠ {t('files.capabilities.plainFtp')}
            </p>
          )}
        </div>
      )}
    </Modal>
  );
}
