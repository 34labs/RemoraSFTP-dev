import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon } from '../components/Icons';

export function Onboarding({ onCreate }: { onCreate: () => void }) {
  const { t } = useI18n();
  const store = useStore();

  const steps = [
    { icon: <Icon.Server />, title: t('onboarding.step1Title'), body: t('onboarding.step1Body') },
    { icon: <Icon.Transfers />, title: t('onboarding.step2Title'), body: t('onboarding.step2Body') },
    { icon: <Icon.Shield />, title: t('onboarding.step3Title'), body: t('onboarding.step3Body') },
  ];

  return (
    <div className="file-area" style={{ display: 'grid', placeItems: 'center' }}>
      <div style={{ maxWidth: 760, width: '100%', padding: 24 }}>
        <h1 style={{ fontSize: 26, margin: '0 0 8px' }}>{t('onboarding.welcome')}</h1>
        <p className="muted" style={{ fontSize: 15, margin: '0 0 26px' }}>
          {t('onboarding.intro')}
        </p>
        <div style={{ display: 'grid', gap: 14, gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))' }}>
          {steps.map((s) => (
            <div key={s.title} className="card" style={{ padding: 20 }}>
              <div
                style={{
                  width: 38,
                  height: 38,
                  borderRadius: 9,
                  background: 'var(--accent-subtle)',
                  color: 'var(--accent)',
                  display: 'grid',
                  placeItems: 'center',
                  marginBottom: 12,
                }}
              >
                {s.icon}
              </div>
              <h3 style={{ margin: '0 0 6px', fontSize: 15 }}>{s.title}</h3>
              <p className="muted" style={{ margin: 0, fontSize: 13 }}>{s.body}</p>
            </div>
          ))}
        </div>
        <div className="row" style={{ marginTop: 26, gap: 10 }}>
          <button className="btn primary" onClick={() => { void store.markOnboarded(); onCreate(); }}>
            <Icon.Plus size={15} />
            {t('onboarding.createFirst')}
          </button>
          <button className="btn ghost" onClick={() => void store.markOnboarded()}>
            {t('onboarding.skip')}
          </button>
        </div>
      </div>
    </div>
  );
}
