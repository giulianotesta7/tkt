/**
 * Ticket attachments journey (issue #366, S2/S3/S5/S7).
 *
 * The canonical browser path of a file: a requester creates a ticket carrying a
 * screenshot, an agent answers with a staff-only note plus a public PDF, the
 * requester's detail lists exactly the files that visitor may see, the PDF
 * arrives as a download with its original name and its exact bytes, the
 * staff-only file answers 404 to the same requester, and an upload whose bytes
 * do not match its extension is refused with the localized message while
 * writing nothing.
 *
 * Layer boundary: the size/count/type limits, the content sniffing, the download
 * headers for every content type, and exhaustive authorization stay in Go
 * (`internal/adapters/http/handlers_attachments_test.go`,
 * `internal/domain/attachment_test.go`). Here we prove one critical journey and
 * the structural contract of the two new visible controls it renders.
 */

import { readFile } from "node:fs/promises";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { base, createUserAsAdmin, loginAs, loginAsSeeded, logout } from "./helpers/auth.js";
import {
  assertCanonicalScreen,
  assertNoHorizontalOverflow,
  assertNothingClipped,
  collectObservability,
  expectHairlineBorder,
} from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";

// The smallest payloads the domain sniffer accepts. The magic bytes are what is
// validated, not the extension; both fixtures stay under a kilobyte, so the
// listing prints them in plain bytes.
const PNG = {
  name: "captura.png",
  mimeType: "image/png",
  buffer: Buffer.from("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01", "latin1"),
};
const PDF = {
  name: "reporte.pdf",
  mimeType: "application/pdf",
  buffer: Buffer.from("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"),
};
const TXT = {
  name: "nota-interna.txt",
  mimeType: "text/plain",
  buffer: Buffer.from("internal note\n"),
};
// A .png name over text bytes: the extension lies, so the content check is what
// refuses it (same fixture shape as the Go handler test).
const FAKE = {
  name: "falsa.png",
  mimeType: "image/png",
  buffer: Buffer.from("this is a text file wearing a png name"),
};

/** The refusal the catalogue renders for a file whose bytes deny its extension. */
const REFUSED_MESSAGE = `${FAKE.name} cannot be attached: only images, PDF, Office documents, text files and zip files are allowed`;

/** Below a kilobyte the detail prints the raw byte count (`tickets.detail.attachments`). */
function humanSize(bytes: number): string {
  return `${bytes} B`;
}

/** The description card of the ticket detail, which carries the creation files. */
function descriptionCard(page: Page): Locator {
  return page
    .locator("#ticket-detail .card")
    .filter({ has: page.getByRole("heading", { name: "Description", exact: true }) });
}

/** One timeline comment, resolved by the body it carries. */
function timelineComment(page: Page, body: string): Locator {
  return page.locator("#timeline .timeline-comment").filter({ hasText: body });
}

/**
 * The attachment list must keep its rows inside its own box and inside the
 * viewport: a long file name wraps (`overflow-wrap:anywhere`) instead of pushing
 * the card or the page sideways.
 */
async function expectAttachmentListFits(list: Locator, viewport: number): Promise<void> {
  const listMetrics = await list.evaluate((el) => ({
    clientWidth: el.clientWidth,
    scrollWidth: el.scrollWidth,
  }));
  expect(
    listMetrics.scrollWidth,
    `the attachment list must fit its box: shows ${listMetrics.clientWidth}px of ${listMetrics.scrollWidth}px`,
  ).toBeLessThanOrEqual(listMetrics.clientWidth + 1);

  const rows = await list.locator("li").evaluateAll((items) =>
    items.map((item) => {
      const row = item.getBoundingClientRect();
      return {
        right: row.right,
        clientWidth: item.clientWidth,
        scrollWidth: item.scrollWidth,
      };
    }),
  );
  expect(rows.length, "the list must carry the files this visitor may see").toBeGreaterThan(0);
  for (const row of rows) {
    expect(
      row.scrollWidth,
      `an attachment row must fit its box: shows ${row.clientWidth}px of ${row.scrollWidth}px`,
    ).toBeLessThanOrEqual(row.clientWidth + 1);
    expect(
      Math.ceil(row.right),
      `an attachment row must stay inside the ${viewport}px viewport`,
    ).toBeLessThanOrEqual(viewport);
  }
}

/**
 * Select the seeded "General" category on the creation form, leaving the form
 * filled but not submitted, so a journey can prove a refusal that only happens
 * after the POST.
 */
async function fillCreationForm(
  page: Page,
  o: { title: string; description: string },
): Promise<void> {
  await page.goto(base() + "/tickets/new");
  await expect(page.getByRole("heading", { name: "Create a ticket" })).toBeVisible();
  const categoryLink = page.locator(".catalog-category").filter({ hasText: "General" }).first();
  if ((await categoryLink.count()) === 0) {
    throw new Error(`no catalog category "General" on the creation catalog at ${page.url()}`);
  }
  await categoryLink.click();
  await expect(page.locator(".selected-catalog-path")).toContainText("General");
  await page.getByLabel(/title/i).fill(o.title);
  await page.getByLabel(/description/i).fill(o.description);
  await page.getByLabel(/priority/i).selectOption("medium");
}

test.describe("Ticket attachments", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("a requester attaches a screenshot, an agent answers with a public and an internal file, and the requester downloads only what they may see", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const suffix = Date.now().toString(36).slice(2, 8);
    const requesterEmail = `attachments-${suffix}@tkt.test`;
    const requesterPassword = "Secret123!";

    await loginAsSeeded(page);
    await createUserAsAdmin(page, {
      name: `Rita Adjuntos ${suffix}`,
      email: requesterEmail,
      password: requesterPassword,
    });
    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);

    // ---------- S2: the requester attaches the screenshot at creation ----------

    const title = `Adjunto journey ${suffix}`;
    const description = "the printer shows this error";
    const ticketId = await createTicketViaUi(page, {
      title,
      description,
      category: "General",
      priority: "medium",
      attachments: [PNG],
    });
    const detailPath = `/tickets/${ticketId}`;
    await page.goto(base() + detailPath);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    // The creation file sits under the description, named as uploaded and with
    // its human size.
    const creationList = descriptionCard(page).locator("ul.attachments");
    await expect(creationList).toHaveCount(1);
    await expect(creationList.locator("a.attachment-name")).toHaveText(PNG.name);
    await expect(creationList.locator(".attachment-size")).toHaveText(humanSize(PNG.buffer.length));

    // ---------- S2/S3: an agent answers with a staff-only and a public file ----------

    await logout(page);
    await loginAsSeeded(page);
    await page.goto(base() + detailPath);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    const internalBody = `internal note ${suffix}`;
    await page.getByLabel(/comment body/i).fill(internalBody);
    await page.locator("#comment-internal").check();
    await page.locator('input[id="comment-attachments"]').setInputFiles([TXT]);
    const internalPost = waitForExactPost(page, `${detailPath}/comments`);
    await page.getByRole("button", { name: /add comment/i }).click();
    expect((await internalPost).status(), "a comment with a file is a native 303 POST").toBe(303);
    await expect(page).toHaveURL(new RegExp(`${detailPath}$`));

    const internalComment = timelineComment(page, internalBody);
    await expect(internalComment).toHaveCount(1);
    await expect(internalComment).toHaveClass(/internal/);
    const internalLink = internalComment.locator("a.attachment-name");
    await expect(internalLink, "the agent reads the staff-only file").toHaveText(TXT.name);
    const internalHref = await internalLink.getAttribute("href");
    expect(internalHref, "the staff-only file is linked by its download route").toMatch(
      /^\/attachments\/\d+$/,
    );
    if (!internalHref) {
      throw new Error(
        `the staff-only attachment "${TXT.name}" has no download href on a.timeline-comment.internal a.attachment-name at ${page.url()}`,
      );
    }

    const publicBody = `public answer ${suffix}`;
    await page.getByLabel(/comment body/i).fill(publicBody);
    await expect(page.locator("#comment-internal")).not.toBeChecked();
    await page.locator('input[id="comment-attachments"]').setInputFiles([PDF]);
    const publicPost = waitForExactPost(page, `${detailPath}/comments`);
    await page.getByRole("button", { name: /add comment/i }).click();
    expect((await publicPost).status()).toBe(303);
    await expect(page).toHaveURL(new RegExp(`${detailPath}$`));

    const publicComment = timelineComment(page, publicBody);
    await expect(publicComment).toHaveCount(1);
    const pdfLink = publicComment.locator("a.attachment-name");
    await expect(pdfLink).toHaveText(PDF.name);
    const pdfHref = await pdfLink.getAttribute("href");
    expect(pdfHref, "the public file is linked by its download route").toMatch(
      /^\/attachments\/\d+$/,
    );
    if (!pdfHref) {
      throw new Error(
        `the public attachment "${PDF.name}" has no download href on a.attachment-name at ${page.url()}`,
      );
    }

    // ---------- S3: the requester reads only what they may see ----------

    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);
    await page.goto(base() + detailPath);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    await expect(descriptionCard(page).locator("a.attachment-name")).toHaveText(PNG.name);
    const requesterPublicComment = timelineComment(page, publicBody);
    await expect(requesterPublicComment.locator("a.attachment-name")).toHaveText(PDF.name);
    // The staff-only file is invisible three ways: the internal comment is not
    // rendered, its file name appears nowhere, and no link carries its URL.
    await expect(page.locator("#timeline .timeline-comment.internal")).toHaveCount(0);
    await expect(page.locator("#timeline")).not.toContainText(internalBody);
    await expect(page.getByText(TXT.name, { exact: true })).toHaveCount(0);
    expect(
      await page.content(),
      `the requester's page must not carry ${internalHref}`,
    ).not.toContain(internalHref);

    const detailObs = collectObservability(page);
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail with attachments",
      url: page.url(),
      role: "requester",
      consoleErrors: detailObs.consoleErrors,
      pageErrors: detailObs.pageErrors,
      failedRequests: detailObs.failedRequests,
      failedResponses: detailObs.failedResponses,
    });

    // The two attachment lists (description and comment) keep their contract at
    // the phone breakpoint: nothing clipped, no page overflow, every row inside
    // its box and the viewport.
    await expectAttachmentListFits(descriptionCard(page).locator("ul.attachments"), 1280);
    await expectAttachmentListFits(requesterPublicComment.locator("ul.attachments"), 1280);
    await page.setViewportSize({ width: 390, height: 844 });
    await assertNoHorizontalOverflow(page, 390);
    await assertNothingClipped(page);
    await expectAttachmentListFits(descriptionCard(page).locator("ul.attachments"), 390);
    await expectAttachmentListFits(requesterPublicComment.locator("ul.attachments"), 390);
    await page.setViewportSize({ width: 1280, height: 800 });

    // ---------- S7: the download keeps the name and the bytes ----------

    const [download] = await Promise.all([page.waitForEvent("download"), pdfLink.click()]);
    expect(download.suggestedFilename(), "the download keeps the original file name").toBe(
      PDF.name,
    );
    const downloadPath = await download.path();
    expect(downloadPath, "the download must produce a file").not.toBeNull();
    const downloadedBytes = await readFile(downloadPath as string);
    expect(
      downloadedBytes.equals(PDF.buffer),
      `the downloaded bytes must be the uploaded file, got ${downloadedBytes.length} bytes`,
    ).toBe(true);
    expect(new URL(page.url()).pathname, "a download must not navigate").toBe(detailPath);

    // The same file as a plain request: attachment disposition with the original
    // name, the proved content type, and no inline execution of an upload.
    const pdfResponse = await page.request.get(base() + pdfHref);
    expect(pdfResponse.status()).toBe(200);
    const pdfHeaders = pdfResponse.headers();
    expect(pdfHeaders["content-type"]).toBe("application/pdf");
    expect(pdfHeaders["content-disposition"]).toBe(`attachment; filename=${PDF.name}`);
    expect(pdfHeaders["x-content-type-options"]).toBe("nosniff");
    expect(Buffer.from(await pdfResponse.body()).equals(PDF.buffer)).toBe(true);

    // ---------- S3: the staff-only file answers 404 for the requester ----------

    const refusedDownload = await page.request.get(base() + internalHref);
    expect(
      refusedDownload.status(),
      "a file on an internal comment must not reveal that it exists",
    ).toBe(404);
    expect(
      Buffer.from(await refusedDownload.body()).includes(TXT.buffer),
      "a refused download must not answer the file's bytes",
    ).toBe(false);

    // ---------- S5: a refused upload shows the message and writes nothing ----------

    const refusedTitle = `Refused upload ${suffix}`;
    await fillCreationForm(page, { title: refusedTitle, description: "the bytes lie" });

    // The filled creation form is the surface that offers the files: one
    // multiple input named `attachments`, on the shared control step, with the
    // catalogue's hint, and it keeps its contract at both breakpoints.
    const creationInput = page.locator('input[type="file"][name="attachments"]');
    await expect(creationInput).toHaveCount(1);
    await expect(creationInput).toHaveAttribute("multiple", "");
    await expect(creationInput).toHaveAttribute("accept", /\.png/);
    await expect(page.getByText("Up to 5 files, 10 MB each", { exact: true })).toBeVisible();
    await expectHairlineBorder(creationInput, "ticket creation file input");
    // Measured, never looked at: the control is an 8px-radius field on the shared
    // 13.5px control step, not a browser default.
    const inputMetrics = await creationInput.evaluate((el) => {
      const style = getComputedStyle(el);
      return { radius: style.borderTopLeftRadius, fontSize: style.fontSize };
    });
    expect(inputMetrics.radius, "a control is the 8px shape").toBe("8px");
    expect(inputMetrics.fontSize, "a control sits on the 13.5px control step").toBe("13.5px");

    const creationObs = collectObservability(page);
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket creation with a file input",
      url: page.url(),
      role: "requester",
      consoleErrors: creationObs.consoleErrors,
      pageErrors: creationObs.pageErrors,
      failedRequests: creationObs.failedRequests,
      failedResponses: creationObs.failedResponses,
    });

    await page.setViewportSize({ width: 390, height: 844 });
    await assertNoHorizontalOverflow(page, 390);
    await assertNothingClipped(page);
    await expect(creationInput).toBeVisible();
    const mobileInputBox = await creationInput.boundingBox();
    expect(mobileInputBox, `the file input has no geometry at ${page.url()}`).not.toBeNull();
    expect(
      Math.ceil(mobileInputBox!.x + mobileInputBox!.width),
      "the file input must stay inside the 390px viewport",
    ).toBeLessThanOrEqual(390);
    await page.setViewportSize({ width: 1280, height: 800 });

    await page.locator('input[type="file"][name="attachments"]').setInputFiles([FAKE]);
    const refusedPost = waitForExactPost(page, "/tickets");
    await page.getByRole("button", { name: /create ticket/i }).click();
    const refusedResponse = await refusedPost;
    expect(refusedResponse.status(), "a refused file is a 422 re-render").toBe(422);

    const errorBanner = page.getByRole("alert");
    await expect(errorBanner).toHaveCount(1);
    await expect(errorBanner).toHaveText(REFUSED_MESSAGE);
    // The form is re-rendered with what the requester typed, so they can retry
    // with a file the content check accepts.
    await expect(page.getByLabel(/title/i)).toHaveValue(refusedTitle);
    await expect(page.locator("#ticket-detail")).toHaveCount(0);

    // Nothing was written: the refused title has no ticket, and the requester's
    // own ticket still carries exactly the one creation file.
    await page.goto(base() + "/tickets");
    await expect(page.getByRole("link", { name: refusedTitle, exact: true })).toHaveCount(0);
    await page.goto(base() + detailPath);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(descriptionCard(page).locator("ul.attachments li")).toHaveCount(1);
    await expect(page.locator("#timeline ul.attachments li")).toHaveCount(1);
  });
});
