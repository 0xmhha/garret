import { describe, expect, it } from "vitest";
import en from "./en.json";
import ko from "./ko.json";
import { catalogs, createTranslator, formatDate, formatRelative, formatSize, pickLocale } from "./i18n";

describe("pickLocale", () => {
  it.each([
    [["ko-KR", "en-US"], "ko"],
    [["en-GB", "ko"], "en"],
    [["fr-FR", "ko-KR"], "ko"],
    [["fr-FR"], "en"],
    [[], "en"],
  ])("%j -> %s", (languages, want) => {
    expect(pickLocale(languages)).toBe(want);
  });
});

describe("createTranslator", () => {
  const t = createTranslator("en");
  const tk = createTranslator("ko");

  it("returns plain messages", () => {
    expect(t("app.loading")).toBe("Loading…");
    expect(tk("app.loading")).toBe("불러오는 중…");
  });

  it("fills placeholders", () => {
    expect(t("review.openedBy", { author: "bob" })).toBe("Opened by bob");
    expect(tk("review.openedBy", { author: "bob" })).toBe("bob 님이 열었습니다");
  });

  it("picks plural forms with Intl.PluralRules", () => {
    expect(t("shell.unread", { count: 1 })).toBe("1 unread notification");
    expect(t("shell.unread", { count: 3 })).toBe("3 unread notifications");
    expect(tk("shell.unread", { count: 1 })).toBe("읽지 않은 알림 1개");
  });

  it("leaves an unknown placeholder visible rather than dropping text", () => {
    expect(t("review.openedBy")).toBe("Opened by {author}");
  });

  it("does not interpret values as markup or placeholders", () => {
    expect(t("review.openedBy", { author: "<b>{count}</b>" })).toBe("Opened by <b>{count}</b>");
  });
});

describe("catalogs", () => {
  const keys = (c: Record<string, unknown>) => Object.keys(c).sort();

  it("have the same keys in every language", () => {
    expect(keys(ko)).toEqual(keys(en));
    expect(Object.keys(catalogs).sort()).toEqual(["en", "ko"]);
  });

  it("give every plural message an other form", () => {
    for (const catalog of [en, ko]) {
      for (const value of Object.values(catalog)) {
        if (typeof value === "object") expect(value).toHaveProperty("other");
      }
    }
  });
});

describe("formatRelative", () => {
  const now = new Date("2026-11-13T12:00:00Z");
  it.each([
    ["en", new Date("2026-11-13T11:57:00Z"), "3 minutes ago"],
    ["ko", new Date("2026-11-13T11:57:00Z"), "3분 전"],
    ["en", new Date("2026-11-11T12:00:00Z"), "2 days ago"],
    ["en", new Date("2026-11-13T11:59:58Z"), "now"],
  ] as const)("%s %s", (locale, date, want) => {
    expect(formatRelative(locale, date, now)).toBe(want);
  });
});

describe("formatDate", () => {
  it("writes a medium date with a short time", () => {
    expect(formatDate("en", new Date(2026, 10, 13, 9, 5))).toBe("Nov 13, 2026, 9:05 AM");
  });
  it("returns nothing for an invalid date", () => {
    expect(formatDate("ko", new Date("nope"))).toBe("");
  });
});

describe("formatSize", () => {
  it.each([
    [0, "0 byte"],
    [999, "999 byte"],
    [1500, "1.5 kB"],
    [2_400_000, "2.4 MB"],
    [26_214_400, "26.2 MB"],
  ])("%d -> %s", (bytes, want) => expect(formatSize("en", bytes).replace(/ /g, " ")).toMatch(new RegExp(`^${want.replace(".", "\\.")}s?$`)));
});
