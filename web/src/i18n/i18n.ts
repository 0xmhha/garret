import en from "./en.json";
import ko from "./ko.json";

/** The interface languages (PRODUCT.md). */
export type Locale = "en" | "ko";

type Plural = { readonly one?: string; readonly other: string };
type Message = string | Plural;
type Catalog = Readonly<Record<string, Message>>;

/** A message key; English is the source catalog. */
export type MessageKey = keyof typeof en;

/** Every catalog, keyed by locale. A test keeps their keys identical. */
export const catalogs: Readonly<Record<Locale, Catalog>> = { en, ko };

const fallback: Locale = "en";
const placeholder = /\{(\w+)\}/g;

/**
 * pickLocale returns Korean when the first supported language the browser
 * prefers is Korean, and English otherwise.
 */
export function pickLocale(languages: readonly string[]): Locale {
  for (const tag of languages) {
    const base = tag.toLowerCase().split("-")[0];
    if (base === "ko" || base === "en") return base;
  }
  return fallback;
}

/** Values for placeholders; `count` also selects the plural form. */
export type Values = Readonly<Record<string, string | number>>;

/**
 * createTranslator returns t(key, values) for locale. Placeholders are
 * replaced in one pass, so a value is never read as markup or as another
 * placeholder; a placeholder without a value stays visible.
 */
export function createTranslator(locale: Locale) {
  const catalog = catalogs[locale];
  const plurals = new Intl.PluralRules(locale);
  const numbers = new Intl.NumberFormat(locale);
  return function t(key: MessageKey, values: Values = {}): string {
    const message = catalog[key] ?? catalogs[fallback][key] ?? key;
    const text = typeof message === "string" ? message : pluralForm(message, plurals, values["count"]);
    return text.replace(placeholder, (whole, name: string) => {
      const v = values[name];
      if (v === undefined) return whole;
      return typeof v === "number" ? numbers.format(v) : v;
    });
  };
}

function pluralForm(message: Plural, rules: Intl.PluralRules, count: string | number | undefined): string {
  if (typeof count !== "number") return message.other;
  return rules.select(count) === "one" && message.one !== undefined ? message.one : message.other;
}

const relativeUnits: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
  ["second", 1],
];

/** formatRelative describes date relative to now, such as "3 minutes ago". */
export function formatRelative(locale: Locale, date: Date, now: Date): string {
  const seconds = Math.round((date.getTime() - now.getTime()) / 1000);
  const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (Math.abs(seconds) < 45) return format.format(0, "second");
  for (const [unit, size] of relativeUnits) {
    if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit);
  }
  return format.format(seconds, "second");
}

/** formatDate writes date and time in the locale's medium date and short time. */
export function formatDate(locale: Locale, date: Date): string {
  if (!Number.isFinite(date.getTime())) return "";
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
}
