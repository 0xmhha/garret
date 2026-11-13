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
const allowedURI = /^(?:https?:|mailto:|\/(?!\/)|#)/i;
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
export function renderMarkdown(source: string): string {
  const html = marked.parse(source) as string;
  return purifier.sanitize(html, {
    ALLOWED_TAGS: allowedTags,
    ALLOWED_ATTR: allowedAttrs,
    ALLOWED_URI_REGEXP: allowedURI,
    ALLOW_DATA_ATTR: false,
    RETURN_TRUSTED_TYPE: false,
  });
}
