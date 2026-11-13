import DOMPurify from "dompurify";
import { Marked } from "marked";

/**
 * The only elements and attributes member Markdown may produce
 * (docs/design/security.md, Web application hardening).
 */
const allowedTags = [
  "p", "br", "hr", "strong", "em", "del", "code", "pre", "blockquote",
  "ul", "ol", "li", "a", "img", "h1", "h2", "h3", "h4", "h5", "h6",
  "table", "thead", "tbody", "tr", "th", "td",
];
const allowedAttrs = ["href", "src", "alt", "title", "align", "start"];
// Links: web, mail and same-origin paths only. Anything else (javascript:,
// data:, vbscript:) is removed by DOMPurify.
// A backslash is refused anywhere in the value: browsers read "/\host" as
// another host.
const allowedURI = /^(?:https?:|mailto:|\/(?![/\\])|#)(?!.*\\)/i;
// Images load only from garret's own attachment endpoint, so a comment
// cannot make every reader's browser fetch from another host.
const attachmentPath = /^\/api\/v1\/attachments\/[0-9A-Za-z]+$/;

const escapeHTML = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

const marked = new Marked({
  gfm: true,
  async: false,
  renderer: {
    // Raw HTML in the source is shown as text, never parsed as markup.
    html: ({ text }) => escapeHTML(text),
  },
});

// The one Trusted Types policy the CSP allows (transport.md 3.3). It is
// created here, once; the CSP refuses a second policy of the same name, so
// no other script can obtain it. createHTML passes strings through because
// it is only ever given DOMPurify's output, and DOMPurify's own parsing.
// DOMPurify requires createScriptURL as well; garret allows no script
// URLs, so it always yields an empty one.
const policyName = "garret-markdown";
interface TrustedTypePolicyFactory {
  createPolicy(name: string, rules: { createHTML(s: string): string; createScriptURL(s: string): string }): {
    createHTML(s: string): TrustedHTML;
  };
}
const factory = (globalThis as { trustedTypes?: TrustedTypePolicyFactory }).trustedTypes;
const policy = factory?.createPolicy(policyName, {
  createHTML: (s: string) => s,
  createScriptURL: () => "",
});

// Bound to the page's window explicitly: without one DOMPurify cannot sanitize.
const purifier = DOMPurify(window);
purifier.addHook("afterSanitizeAttributes", (node) => {
  if (node.nodeName === "A" && node.hasAttribute("href")) {
    node.setAttribute("rel", "noopener noreferrer");
  }
  if (node.nodeName === "IMG" && !attachmentPath.test(node.getAttribute("src") ?? "")) {
    node.remove();
  }
});

/**
 * renderMarkdown turns member Markdown into sanitized HTML: marked with raw
 * HTML shown as text, then DOMPurify with an allow-list. It is the only
 * function whose output may be assigned to innerHTML.
 */
export function renderMarkdown(source: string): SafeHTML {
  return renderWith(purifier, source);
}

/** Sanitized HTML: TrustedHTML where the browser has Trusted Types. */
export type SafeHTML = string | TrustedHTML;

/** The part of DOMPurify renderWith needs; tests pass a stand-in. */
export interface Sanitizer {
  readonly isSupported: boolean;
  sanitize(html: string, config: object): SafeHTML;
}

/**
 * renderWith renders with the given sanitizer. When the sanitizer cannot
 * work (DOMPurify returns its input unchanged then), the source is shown as
 * escaped text: this function never returns unsanitized HTML.
 */
export function renderWith(sanitizer: Sanitizer, source: string): SafeHTML {
  if (!sanitizer.isSupported) {
    const text = `<p>${escapeHTML(source)}</p>`;
    return policy ? policy.createHTML(text) : text;
  }
  const html = marked.parse(source) as string;
  return sanitizer.sanitize(html, {
    ALLOWED_TAGS: allowedTags,
    ALLOWED_ATTR: allowedAttrs,
    ALLOWED_URI_REGEXP: allowedURI,
    ALLOW_DATA_ATTR: false,
    ...(policy ? { TRUSTED_TYPES_POLICY: policy, RETURN_TRUSTED_TYPE: true } : { RETURN_TRUSTED_TYPE: false }),
  });
}
