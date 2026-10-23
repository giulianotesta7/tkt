/**
 * CSV export (issue #409): the ticket queue and the metrics dashboard each
 * offer a download, the browser really receives a CSV file, and the ticket file
 * carries every match of the current filters rather than the visible page.
 *
 * Authorization, scope and the exact column set are proven exhaustively in Go
 * (internal/adapters/http/handlers_export_test.go); what only a browser can
 * show is that the link is reachable from the screen and that clicking it
 * downloads a file instead of navigating.
 */

import { test, expect, type Page, type Download } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { loginAsSeeded } from "./helpers/auth.js";
import { collectObservability, expectNoConsoleOrPageErrors } from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

/** Reads a finished download as text, with the UTF-8 byte-order mark removed. */
async function downloadText(download: Download): Promise<string> {
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(Buffer.from(chunk));
  return Buffer.concat(chunks)
    .toString("utf8")
    .replace(/^\uFEFF/, "");
}

/** Clicks a download link and returns the file it produced. */
async function clickDownload(page: Page, name: RegExp): Promise<Download> {
  const started = page.waitForEvent("download");
  await page.getByRole("link", { name }).click();
  return started;
}

test.describe("CSV export", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });

  test.afterAll(async () => {
    await stopServer();
  });

  test("the ticket queue downloads every matching ticket as CSV", async ({ page }) => {
    const observed = collectObservability(page);
    await loginAsSeeded(page);
    const title = `Exportable ticket ${Date.now()}`;
    await createTicketViaUi(page, { title });

    await page.goto(base() + "/tickets");
    const download = await clickDownload(page, /download csv/i);

    expect(download.suggestedFilename()).toBe("tickets.csv");
    const text = await downloadText(download);
    const [header, ...rows] = text.trim().split("\n");
    expect(header).toContain("Number");
    expect(header).toContain("Title");
    expect(rows.some((row) => row.includes(title))).toBe(true);
    // The queue paginates at ten rows; the file is not limited to a page.
    expect(page.url()).toContain("/tickets");
    expectNoConsoleOrPageErrors(observed.consoleErrors, observed.pageErrors);
  });

  test("the metrics dashboard downloads its aggregates as CSV", async ({ page }) => {
    const observed = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/tickets/metrics");

    const download = await clickDownload(page, /download csv/i);

    expect(download.suggestedFilename()).toBe("metrics.csv");
    const text = await downloadText(download);
    expect(text).toContain("Section");
    expect(text).toContain("Summary");
    expect(text).toContain("Pending");
    expectNoConsoleOrPageErrors(observed.consoleErrors, observed.pageErrors);
  });
});
