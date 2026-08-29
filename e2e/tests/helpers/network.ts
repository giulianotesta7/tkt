import type { Page, Response } from "@playwright/test";

export function waitForExactPost(page: Page, expectedUrl: string): Promise<Response> {
  const expected = new URL(expectedUrl, page.url());
  return page.waitForResponse((response) => {
    const actual = new URL(response.url());
    return (
      actual.pathname === expected.pathname &&
      actual.search === expected.search &&
      response.request().method() === "POST"
    );
  });
}
