import { createContext, useContext, useMemo } from 'react';
import { en, type Messages } from './en';
import { id } from './id';

const locales: Record<string, Messages> = { en, id };

export const languages = [
  { code: 'en', name: 'English' },
  { code: 'id', name: 'Bahasa Indonesia' },
];

type I18nContextValue = {
  lang: string;
  t: (key: string, vars?: Record<string, string | number>) => string;
  messages: Messages;
};

const I18nContext = createContext<I18nContextValue>({
  lang: 'en',
  t: (k) => k,
  messages: en,
});

function lookup(messages: Messages, key: string): string | string[] | undefined {
  const parts = key.split('.');
  let cur: unknown = messages;
  for (const p of parts) {
    if (cur && typeof cur === 'object' && p in (cur as Record<string, unknown>)) {
      cur = (cur as Record<string, unknown>)[p];
    } else {
      return undefined;
    }
  }
  if (typeof cur === 'string') return cur;
  if (Array.isArray(cur)) return cur;
  return undefined;
}

function interpolate(s: string, vars?: Record<string, string | number>): string {
  if (!vars) return s;
  return s.replace(/\{\{(\w+)\}\}/g, (_, k) => (vars[k] !== undefined ? String(vars[k]) : ''));
}

export function I18nProvider({ lang, children }: { lang: string; children: React.ReactNode }) {
  const value = useMemo<I18nContextValue>(() => {
    const messages = locales[lang] ?? en;
    const t = (key: string, vars?: Record<string, string | number>) => {
      const found = lookup(messages, key) ?? lookup(en, key) ?? key;
      if (Array.isArray(found)) return found.join('\n');
      return interpolate(found, vars);
    };
    return { lang, t, messages };
  }, [lang]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  return useContext(I18nContext);
}
