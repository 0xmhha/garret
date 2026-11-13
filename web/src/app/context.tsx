import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import type { Client } from "../api/client";
import { createTranslator, type Locale } from "../i18n/i18n";

interface Env {
  api: Client;
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: ReturnType<typeof createTranslator>;
}

const EnvContext = createContext<Env | null>(null);

interface Props {
  api: Client;
  locale: Locale;
  /** Called after the language changes, to keep the choice; the provider re-renders on its own. */
  onLocaleChange?: ((locale: Locale) => void) | undefined;
  children: ReactNode;
}

/** EnvProvider hands the API client, the language and the translator to every screen. */
export function EnvProvider({ api, locale: initial, onLocaleChange, children }: Props) {
  const [locale, setState] = useState(initial);
  const setLocale = useCallback(
    (next: Locale) => {
      setState(next);
      onLocaleChange?.(next);
    },
    [onLocaleChange],
  );
  const value = useMemo(() => ({ api, locale, setLocale, t: createTranslator(locale) }), [api, locale, setLocale]);
  return <EnvContext.Provider value={value}>{children}</EnvContext.Provider>;
}

function useEnv(): Env {
  const env = useContext(EnvContext);
  if (!env) throw new Error("EnvProvider is missing");
  return env;
}

export const useApi = () => useEnv().api;
export const useT = () => useEnv().t;
export const useLocale = () => useEnv().locale;
export const useSetLocale = () => useEnv().setLocale;
