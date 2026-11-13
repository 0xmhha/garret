---
name: garret
description: A private, local-network code review room, finished to the category standard.
colors:
  accent: "#1f5fbf"
  accent-hover: "#1a51a3"
  accent-subtle: "#e8f0fc"
  on-accent: "#ffffff"
  focus: "#1f5fbf"
  selection: "#c8dbf7"
  add-fg: "#17703a"
  add-bg: "#e3f5e8"
  add-bg-strong: "#c4ebcf"
  del-fg: "#b42121"
  del-bg: "#fde8e8"
  del-bg-strong: "#f8c9c9"
  warn-fg: "#7a4f00"
  warn-bg: "#fdf3d6"
  bg: "#ffffff"
  bg-subtle: "#f5f7f9"
  bg-muted: "#eef1f4"
  border: "#d5dbe1"
  border-strong: "#b8c1ca"
  fg: "#1b2027"
  fg-muted: "#586370"
  accent-dark: "#6aa5ff"
  accent-hover-dark: "#8ab8ff"
  accent-subtle-dark: "#17263d"
  on-accent-dark: "#0b1220"
  selection-dark: "#23406b"
  add-fg-dark: "#5fd17f"
  add-bg-dark: "#13301e"
  add-bg-strong-dark: "#1d4a2d"
  del-fg-dark: "#ff8a82"
  del-bg-dark: "#3a1618"
  del-bg-strong-dark: "#5a2124"
  warn-fg-dark: "#e8bc54"
  warn-bg-dark: "#33280f"
  bg-dark: "#11151a"
  bg-subtle-dark: "#171c22"
  bg-muted-dark: "#1e242b"
  border-dark: "#2e3640"
  border-strong-dark: "#45505c"
  fg-dark: "#e6eaef"
  fg-muted-dark: "#9aa5b1"
typography:
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Apple SD Gothic Neo, system-ui, sans-serif"
    fontSize: "24px"
    fontWeight: 600
    lineHeight: 1.25
  title-lg:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Apple SD Gothic Neo, system-ui, sans-serif"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: 1.25
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Apple SD Gothic Neo, system-ui, sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.3
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Apple SD Gothic Neo, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
    fontFeature: "tnum"
  body-sm:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Apple SD Gothic Neo, system-ui, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Apple SD Gothic Neo, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1.5
  code:
    fontFamily: "ui-monospace, SF Mono, Menlo, Consolas, monospace"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.5
rounded:
  sm: "4px"
  md: "6px"
  pill: "999px"
spacing:
  "1": "4px"
  "2": "8px"
  "3": "12px"
  "4": "16px"
  "5": "20px"
  "6": "24px"
  "8": "32px"
components:
  button:
    backgroundColor: "{colors.bg-subtle}"
    textColor: "{colors.fg}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "32px"
  button-hover:
    backgroundColor: "{colors.bg-muted}"
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "32px"
  button-primary-hover:
    backgroundColor: "{colors.accent-hover}"
  button-quiet:
    textColor: "{colors.fg}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "32px"
  button-danger:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.del-fg}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "32px"
  button-danger-hover:
    backgroundColor: "{colors.del-bg}"
  text-field:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "34px"
  count-unresolved:
    backgroundColor: "{colors.warn-bg}"
    textColor: "{colors.warn-fg}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    height: "18px"
  count-total:
    backgroundColor: "{colors.bg-muted}"
    textColor: "{colors.fg-muted}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    height: "18px"
  topbar:
    backgroundColor: "{colors.bg-subtle}"
    textColor: "{colors.fg-muted}"
    padding: "0 16px"
    height: "48px"
  thread:
    backgroundColor: "{colors.bg}"
    rounded: "{rounded.md}"
    padding: "12px 16px"
---

# Design System: garret

## Overview

**Creative North Star: "The Category Standard, Played Straight"**

garret looks like the code review tools its users already trust, and is finished to their level: a neutral ground, one blue, system type, 1px borders and small radii. Nothing is themed and nothing decorates. The diff, the threads and the verdict are the interface; the chrome stays quiet so engineers can read code for hours.

Density is that of a working tool: 14px body text, a strict 4px spacing grid, compact 32px controls, and colour that only ever carries meaning. Light and dark follow the system setting and get equal care; every colour token has a dark counterpart tuned for contrast rather than inverted.

Several choices are product constraints, not taste. The server sends a same-origin Content Security Policy with no inline styles or scripts and no third-party fonts or assets, so the system UI stack and the system monospace are intentional and permanent. Markdown HTML reaches the page only through a Trusted Types policy behind an allow-list sanitizer. Every screen meets WCAG 2.2 AA, checked by axe in the browser suite in Chrome and WebKit.

**Key Characteristics:**
- Neutral grey-blue ground in light and dark, following `prefers-color-scheme`.
- Colour is semantic only: blue acts, green and red mean change, amber means unresolved.
- System UI face for everything, system monospace for code, paths and branches.
- 4px spacing grid, 1px borders, 4px and 6px radii, pills for counts.
- Flat surfaces; the only shadow belongs to menus and popovers.
- Keyboard first: a visible 2px blue focus ring on every interactive element.

## Colors

A cool neutral ground with four hues, each owning one job and nothing else.

### Primary
- **Review Blue** (#1f5fbf, dark #6aa5ff): links, the focus ring, the primary action (Approve, submitting a comment), and current location (the active tab underline, the selected file row, text selection). Hover deepens it (#1a51a3, dark #8ab8ff). Its pale tint (accent-subtle) marks the selected row.

### Secondary
- **Addition Green** (#17703a on #e3f5e8, stronger #c4ebcf for line numbers): added lines, `A` status letters, `+` counts, and the Approve verdict.
- **Removal Red** (#b42121 on #fde8e8, stronger #f8c9c9): removed lines, `D` status letters, `-` counts, the Request changes verdict, field errors, and danger buttons.

### Tertiary
- **Unresolved Amber** (#7a4f00 on #fdf3d6, dark #e8bc54 on #33280f): unresolved work only. The header's unresolved pill, tab counts of open threads, a thread's Unresolved badge, and the open-thread markers in the file tree.

### Neutral
- **Paper** (#ffffff, dark #11151a): page and diff background, thread cards, inputs.
- **Subtle Ground** (#f5f7f9, dark #171c22): the top bar, default buttons, table heads, thread action rows, inline thread rows in the diff.
- **Muted Ground** (#eef1f4, dark #1e242b): button hover, inline code and branch names, neutral pills and totals.
- **Hairline** (#d5dbe1, dark #2e3640): every 1px divider and container border.
- **Strong Hairline** (#b8c1ca, dark #45505c): input borders, hovered button borders, scrollbars, the Markdown blockquote rule.
- **Ink** (#1b2027, dark #e6eaef): primary text.
- **Muted Ink** (#586370, dark #9aa5b1): metadata, timestamps, totals, M/R/C status letters, the Resolved badge, the Live status.

### Named Rules

**The One Job Rule.** Each hue has one meaning. Blue acts or marks where you are; green and red mean added, removed, or a verdict; amber means unresolved work. Anything else is neutral grey.

**The Amber Is Owed Work Rule.** Amber appears only where a person still has something to resolve. Totals, revision counts, Resolved threads and a healthy Live connection are grey.

**The Grey Totals Rule.** A count that is not unresolved work (revisions, files, items) uses the neutral pill: muted ground, muted ink.

## Typography

**Display Font:** none; the product has no display tier.
**Body Font:** system UI (-apple-system, BlinkMacSystemFont, Segoe UI, Apple SD Gothic Neo, system-ui, sans-serif)
**Label/Mono Font:** system monospace (ui-monospace, SF Mono, Menlo, Consolas, monospace)

**Character:** The platform's own face, so garret reads like a native tool on macOS and covers Korean through Apple SD Gothic Neo. Monospace marks anything that is code: diff lines, file paths, branch names, digests, one-time secrets.

### Hierarchy
- **Headline** (600, 24px, 1.25): review titles and shell page titles. The review number sits beside the title in muted ink at weight 400.
- **Title Large** (600, 20px, 1.25): the sign-in card title.
- **Title** (600, 16px, 1.3): section titles, settings section headings, project names in lists. The top bar brand uses this size at 700.
- **Body** (400, 14px, 1.5, tabular numerals): all running UI text and comment bodies; Markdown is capped at 72ch.
- **Body Small** (400, 13px): metadata lines, tables, breadcrumbs, form labels (at 600), the file tree.
- **Label** (600, 12px): pills, counts, badges, comment metadata.
- **Code** (400, 13px mono): diffs, paths, branches; status letters use 12px mono at 600.

### Named Rules

**The Platform Face Rule.** Type is the system UI stack and the system monospace, always. The CSP forbids third-party fonts; never add a webfont.

**The Sentence Case Rule.** Labels, tabs and headings are sentence case with no added tracking. There is no uppercase small-label style.

## Layout

Everything sits on a 4px grid (4, 8, 12, 16, 20, 24, 32px). Shell pages (projects, project, discussion, inbox, account, administration) use one centred frame of 960px maximum width with 32px vertical and 24px horizontal padding. The review screen is a working surface and fills the window width with 24px side padding.

The review header holds the title, number, state and branches, with verdict actions top right, then tabs (Conversation, Revisions, Files). Files is a two-column grid: a 280px sticky file tree, then the diff in the remaining width. Conversation pairs the main column with a 360px side column; project and discussion pages pair a main column with a narrower side column (2:1, or 320px).

At 960px and below every two-column grid collapses to one column, and the file tree stacks above the diff with a bottom hairline instead of a right one. At 720px the top bar wraps. Every grid track uses `minmax(0, 1fr)` so a long diff line scrolls inside its own block instead of widening the page.

**The Two Frames Rule.** Shell pages are centred at 960px; the review body is full width at 24px padding. A new page picks one of the two, never a third width.

## Elevation & Depth

The system is flat. Depth comes from tonal layering (paper, subtle ground, muted ground) and 1px hairlines. The single shadow belongs to floating layers: menus and popovers.

### Shadow Vocabulary
- **Menu** (`box-shadow: 0 8px 24px rgb(15 23 32 / 0.14), 0 1px 3px rgb(15 23 32 / 0.08)`; dark `0 8px 24px rgb(0 0 0 / 0.45), 0 1px 3px rgb(0 0 0 / 0.3)`): menus and popovers only.

### Named Rules

**The Menus Only Rule.** A shadow means "this floats above the page". Cards, threads, buttons and panels never cast one.

## Shapes

Small, consistent corners: 4px for inline chips, code, menu items and the add-comment button; 6px for buttons, inputs, threads, lists, tables, notices and the danger zone. Fully rounded pills (999px) are reserved for counts, badges and states, and the small round dot for connection and unread state. Borders are 1px everywhere; the only 2px lines are the focus ring and the active tab underline.

**The No Side Stripe Rule.** Containers, notices, list items and threads never carry a coloured left or right stripe. Meaning goes into a pill, a badge or a tinted background. The 1px neutral rule on a Markdown blockquote is the medium's own device and stays.

## Components

### Buttons
- **Shape:** gently rounded (6px), 32px tall, 12px side padding, 8px icon gap, label at 500 weight.
- **Default:** subtle ground with a hairline border and ink text; hover moves to muted ground and the strong hairline; pressed shows the hairline grey.
- **Primary:** Review Blue fill and border with white text (dark: deep navy text on light blue); hover deepens. One primary per region: Approve in the review header, submit in a composer.
- **Quiet:** transparent with no border, for Cancel and secondary in-line actions.
- **Danger:** paper background, red outline and red text; hover adds the pale red tint. Used only for irreversible actions.
- **Focus / Disabled:** 2px blue outline at 2px offset; disabled at 55% opacity with a not-allowed cursor. Transitions are 120ms ease-out on background and border.

**The Confirm Before Loss Rule.** An irreversible action is never one press. A revoke or delete starts as a default button that swaps for a prompt, a red Danger confirm and a quiet Cancel, with focus on Cancel. Purging a project takes a red-outlined danger zone where the project name must be typed.

### Chips and Counts
- **Style:** 12px label at 600 in a full pill, 1px by 8px padding (counts are 18px tall).
- **Unresolved:** amber tint and amber ink. **Total / neutral:** muted ground and muted ink. **Verdict text:** green for Approve, red for Request changes.

### Cards / Containers
- **Corner Style:** 6px.
- **Background:** paper; action rows and table heads on subtle ground.
- **Shadow Strategy:** none (see Elevation & Depth).
- **Border:** 1px hairline; stacked items divide with a top hairline.
- **Internal Padding:** 12px by 16px for list items and comments; 16px for forms and the danger zone.

### Inputs / Fields
- **Style:** 34px tall, paper background, strong hairline border, 6px radius, 12px side padding; labels above at 13px 600 with a 4px gap.
- **Focus:** the border turns blue with a 2px blue outline inset by 1px.
- **Error:** red border and a 13px red message below.

### Navigation
- **Top bar:** 48px, subtle ground, bottom hairline. Brand in ink at 16px 700; nav links in muted ink at 500, ink when current. The connection state sits at right in 12px: a filled dot while live, a hollow dot and bold ink when offline, with no hue.
- **Tabs:** muted ink at 500 with 20px gaps; the active tab turns ink with a 2px blue underline.

### Diff
The signature component. A monospace table with line numbers, a sign column and code. Added lines sit on the green tint with stronger green line numbers; removed lines on the red tint likewise. Hunk headers are muted ink on the pale blue tint. Inline threads sit in a subtle-ground row aligned past the gutter, and a 20px blue add-comment button appears on line hover or keyboard focus.

### Threads
A bordered 6px card of comments, each 12px by 16px with a 12px metadata line (author in ink at 600). The action row below sits on subtle ground. The state badge is amber "Unresolved" while open and grey "Resolved" once closed; a resolved thread fades to 80% opacity.

## Do's and Don'ts

### Do:
- **Do** take every value from `tokens.css`: 4px spacing steps, 4px and 6px radii, 1px borders.
- **Do** give each hue its one job: blue acts, green and red mean change or a verdict, amber means unresolved.
- **Do** show totals and non-urgent counts in the grey neutral pill.
- **Do** put every irreversible action behind a confirm step, ending in a red-outlined Danger button.
- **Do** design the dark mode as carefully as the light one, using the `-dark` tokens.
- **Do** keep a visible 2px blue focus ring on every interactive element.
- **Do** use authored 16px SVG icons on a 1.5px stroke in `currentColor`.

### Don't:
- **Don't** use amber for anything except unresolved work.
- **Don't** use green or red for status that is not an addition, a removal or a verdict.
- **Don't** add a shadow to anything that does not float.
- **Don't** add coloured side stripes to cards, notices, threads or list items.
- **Don't** load a webfont, an inline style or an inline script; the CSP forbids all three.
- **Don't** inject Markdown HTML except through the sanitizer and its Trusted Types policy.
- **Don't** add uppercase eyebrow labels, gradients or decorative imagery.
- **Don't** invent a third page width; use the 960px shell frame or the full-width review frame.
