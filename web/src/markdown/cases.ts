/**
 * Inputs the sanitizer must defuse, shared by the unit tests (jsdom) and the
 * browser suite (web/e2e, real Chrome and WebKit), so both check the same
 * cases.
 */

/** Markup that could run script or load from elsewhere, by name. */
export const markupAttacks: readonly (readonly [string, string])[] = [
  ["script tag", "<script>alert(1)</script>"],
  ["event attribute", '<img src=x onerror="alert(1)">'],
  ["iframe", '<iframe src="https://evil.example"></iframe>'],
  ["style attribute", '<p style="background:url(https://evil.example)">x</p>'],
  ["svg", "<svg><script>alert(1)</script></svg>"],
  ["form", '<form action="https://evil.example"><input name=a></form>'],
];

/** No rendered element may match this after markupAttacks. */
export const forbiddenElements = "script, iframe, svg, form, input, [onerror], [style]";

/** Links whose target must not survive. */
export const dangerousLinks: readonly string[] = [
  "[x](javascript:alert(1))",
  "[x](JAVASCRIPT:alert(1))",
  "[x](data:text/html;base64,PHNjcmlwdD4=)",
  "[x](vbscript:msgbox)",
  "[x](/&#92;evil.example)",
  "[x](/&#x5c;evil.example)",
  "[x](/&#x09;&#x5c;evil.example)",
];

/** A surviving href must not match this. */
export const dangerousHref = /^(javascript|data|vbscript):|\\/i;

/** Images that must be dropped: other origins, data URLs, API paths that are not attachments. */
export const droppedImages: readonly string[] = [
  "![x](https://evil.example/pixel.png)",
  "![x](//evil.example/pixel.png)",
  "![x](data:image/png;base64,AAAA)",
  "![x](/api/v1/me)",
];
