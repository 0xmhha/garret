import { createContext, useContext, type ReactNode } from "react";
import type { Client } from "../api/client";
import { createTranslator, type Locale } from "../i18n/i18n";

interface Env {
  api: Client;
  locale: Locale;
  t: ReturnType<typeof createTranslator>;
}

const EnvContext = createContext<Env | null>(null);

/** EnvProvider hands the API client and the translator to every screen. */
export function EnvProvider({ api, locale, children }: { api: Client; locale: Locale; children: ReactNode }) {
  return <EnvContext.Provider value={{ api, locale, t: createTranslator(locale) }}>{children}</EnvContext.Provider>;
}

function useEnv(): Env {
  const env = useContext(EnvContext);
  if (!env) throw new Error("EnvProvider is missing");
  return env;
}

export const useApi = () => useEnv().api;
export const useT = () => useEnv().t;
export const useLocale = () => useEnv().locale;
