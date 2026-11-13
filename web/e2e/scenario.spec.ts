import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { apiAs, contextFor, git, signIn, state, violations, watchCSP, type State } from "./state";

// Steps 4 to 6 of the end-to-end scenario (docs/design/testing.md). Each
// browser project pushes its own copy of bob's branch and reviews it, so the
// projects do not see each other's comments, revisions or verdicts.
test.describe.configure({ mode: "serial" });

const lenComment = "Is SIZE_MAX / size safe when size is 0?";
const testComment = "Add a case where the product overflows.";
const bobText = "Bob: bounds the count before the multiplication.";
const carolText = "Carol: checked on both platforms.";

let s: State;
let branch: string;
let review: number;
let contexts: BrowserContext[] = [];
let bob: Page;
let carol: Page;

test.beforeAll(async ({ browser }, info) => {
  s = state(info);
  branch = `fix-${info.project.name}`;
  git(s, "push", "-q", "origin", `fix:refs/heads/${branch}`);
  review = await openReview(s, branch);
  const [b, c] = await Promise.all([contextFor(browser, s, "bob"), contextFor(browser, s, "carol")]);
  contexts = [b, c];
  await Promise.all(contexts.map(watchCSP));
  [bob, carol] = await Promise.all([b.newPage(), c.newPage()]);
  await signIn(bob, s, "bob");
  await signIn(carol, s, "carol");
});

test.afterAll(async () => {
  await Promise.all(contexts.map((c) => c.close()));
});

test.afterEach(async () => {
  for (const page of [bob, carol]) expect(await violations(page)).toEqual([]);
});

const reviewURL = () => `${s.url}/p/${s.project}/r/${review}`;

test("step 4: carol comments on single commits; bob and carol write the description together", async () => {
  await carol.goto(`${reviewURL()}/files`);
  await carol.getByLabel("Show").selectOption({ label: "Commit 2 of 3: Bound the count before multiplying" });
  await commentOnLine(carol, "src/len.c", 6, lenComment);

  await carol.getByLabel("Show").selectOption({ label: "Commit 3 of 3: Test entries_len" });
  await commentOnLine(carol, "tests/len_test.c", 3, testComment);
  // Each comment stays on the commit it was written on.
  await expect(carol.getByRole("article").filter({ hasText: lenComment })).toHaveCount(0);
  await carol.getByLabel("Show").selectOption({ label: "Commit 2 of 3: Bound the count before multiplying" });
  await expect(carol.getByRole("article").filter({ hasText: lenComment })).toBeVisible();

  await Promise.all([bob.goto(reviewURL()), carol.goto(reviewURL())]);
  const editors = [bob, carol].map((p) => p.locator(".cm-content"));
  for (const p of [bob, carol]) await expect(p.getByText("Live")).toBeVisible();
  await Promise.all([type(bob, bobText), type(carol, carolText)]);
  // Both screens converge on the same text, holding both edits.
  await expect
    .poll(async () => {
      const [a = "", b = ""] = (await Promise.all(editors.map((e) => e.innerText()))).map(clean);
      return a === b && a.includes(bobText) && a.includes(carolText);
    })
    .toBe(true);
});

test("step 5: bob force-pushes; revision 2 appears and carol's comments move with the code", async () => {
  await carol.goto(reviewURL());
  await expect(carol.getByRole("link", { name: "Revisions (1)" })).toBeVisible();

  // Revision 2: the last commit is rewritten and adds a line on top of
  // src/len.c, so the line carol commented on moves from 6 to 7.
  const local = `work-${branch}`;
  git(s, "checkout", "-q", "-B", local, "fix");
  const header = "/* Length helpers. */\n";
  writeRepoFile(s, "src/len.c", header + git(s, "show", "fix:src/len.c"));
  git(s, "commit", "-q", "-a", "--amend", "--no-edit");
  git(s, "push", "-q", "--force", "origin", `HEAD:refs/heads/${branch}`);
  git(s, "checkout", "-q", "fix");

  // The event stream brings revision 2 without a reload.
  await expect(carol.getByRole("link", { name: "Revisions (2)" })).toBeVisible();

  await carol.goto(`${reviewURL()}/files?rev=2`);
  await carol.getByLabel("Show").selectOption({ label: "Changes since revision 1" });
  await expect(carol.getByRole("table", { name: "Changes in src/len.c" })).toContainText("/* Length helpers. */");
  await expect(carol.locator(".files__path")).toHaveText(["src/len.c"]);

  await carol.getByLabel("Show").selectOption({ label: "All changes" });
  await fileButton(carol, "src/len.c").click();
  await expect(threadLine(carol, lenComment)).toHaveText("7");
  await fileButton(carol, "tests/len_test.c").click();
  await expect(threadLine(carol, testComment)).toHaveText("3");
});

test("step 6: carol approves and completes; verdict and state are recorded with user and time", async () => {
  await carol.goto(reviewURL());
  await carol.getByRole("button", { name: "Approve" }).click();
  await expect(carol.getByText("Carol: approved")).toBeVisible();
  await carol.getByRole("button", { name: "Review state" }).click();
  await carol.getByRole("menuitem", { name: "Complete" }).click();
  await expect(carol.locator(".review__meta .state")).toHaveText("Completed");

  const history = carol.getByRole("region", { name: "History" }).getByRole("listitem");
  for (const what of [/Carol approved/, /Carol changed the state to/]) {
    const item = history.filter({ hasText: what });
    await expect(item).toHaveCount(1);
    await expect(item.locator("time")).toHaveAttribute("datetime", /^\d{4}-\d\d-\d\dT/);
  }
  // Bob sees the change without a reload.
  await bob.goto(reviewURL());
  await expect(bob.locator(".review__meta .state")).toHaveText("Completed");
});

/** openReview opens bob's review of branch through the API and returns its number. */
async function openReview(st: State, branchName: string): Promise<number> {
  const api = await apiAs(st, "bob");
  try {
    const r = (await api.post(`/api/v1/projects/${st.project}/reviews`, {
      source_branch: `refs/heads/${branchName}`,
      target_branch: "refs/heads/main",
      title: `Bound the length field (${branchName})`,
    })) as { number: number };
    return r.number;
  } finally {
    await api.dispose();
  }
}

async function commentOnLine(page: Page, path: string, line: number, body: string) {
  await fileButton(page, path).click();
  const table = page.getByRole("table", { name: `Changes in ${path}` });
  await table.getByRole("button", { name: `Comment on line ${line}`, exact: true }).click();
  await table.getByRole("textbox", { name: `Comment on line ${line}` }).fill(body);
  await table.getByRole("button", { name: "Comment", exact: true }).click();
  // Saved: the thread shows it and the composer is gone.
  await expect(table.getByRole("article").filter({ hasText: body })).toBeVisible();
  await expect(table.getByRole("textbox")).toHaveCount(0);
}

/** fileButton is the file list entry for path; its name also carries the change status. */
function fileButton(page: Page, path: string) {
  return page.getByRole("navigation", { name: "Changed files" }).getByRole("button").filter({ hasText: path });
}

/** threadLine is the new-side line number of the diff row a thread sits under. */
function threadLine(page: Page, body: string) {
  return page
    .locator("tr.diff__threads", { hasText: body })
    .locator("xpath=preceding-sibling::tr[contains(@class, 'diff__line')][1]")
    .locator("td.diff__no")
    .nth(1);
}

async function type(page: Page, text: string) {
  await page.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type(` ${text}`, { delay: 15 });
}

// The editor marks widget boundaries with U+2060, which is not text.
const clean = (t: string) => t.replaceAll("⁠", "").trim();

function writeRepoFile(st: State, path: string, text: string) {
  writeFileSync(join(st.repo, path), text);
}
