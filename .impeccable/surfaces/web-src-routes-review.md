---
version: 1
slug: "web-src-routes-review"
primary_target: "web/src/routes/review"
related_targets: []
---

# Review screen

Scope: the review screen (`/p/<project>/r/<number>`), the first surface of garret's web interface. Visitor mode: Operate.

Audience and task: two to six engineers on their own laptops, reviewing an undisclosed fix for hours. They read a revision's diff file by file, compare revisions, comment on lines, port and resolve threads, edit the description together, and give a verdict.

Content: review header (title, number, author, source and target branches, state, verdicts), revisions with commits, file list with review progress, diff views (revision, commit, range, revision pair), threads, the collaborative description, activity.

Constraints: PRODUCT.md (WCAG 2.2 AA, English and Korean catalogs, same-origin CSP: no inline scripts or styles, no third-party fonts or assets), Chrome and Safari, keyboard first.

## Direction contract

THESIS: The category standard, played straight and finished to the level of GitHub pull requests, Gerrit and Linear. It refuses decoration: no themed world, no cards-and-gradients dashboard, no novelty.

OWN-WORLD: Neutral light ground with a dark mode of equal care; one blue for links, focus and the primary action; green and red only for additions, removals and verdicts; amber only for unresolved threads. System UI face and a monospace for code, a strict 4px spacing grid, 1px borders, small radii, no shadows except menus.

STORY: The reviewer sees what changed in the current revision, what is still unresolved, and where they stopped; they read, comment, resolve and give a verdict without leaving the keyboard.

FIRST VIEWPORT: Header with title, number, state, branches and verdict actions on the right. Below it tabs (Conversation, Revisions, Files). In Files: a revision picker (current vs. base, or a revision pair) above a two-column layout, the file tree with reviewed checkmarks on the left at about 280px, the unified diff with inline threads filling the rest. The primary action, Approve, sits top right.

FORM: The category standard (canon), taken by the user over the dealt hand; seed:819613fbb45c7143.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
