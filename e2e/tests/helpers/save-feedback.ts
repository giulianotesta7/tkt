import type { Request } from "@playwright/test";

export function isHtmxPost(
  request: Request,
  options: { path: string; action?: string; target: string },
): boolean {
  if (request.method() !== "POST") return false;
  if (request.headers()["hx-request"] !== "true") return false;
  if (request.headers()["hx-target"] !== options.target.replace(/^#/, "")) return false;
  if (new URL(request.url()).pathname !== options.path) return false;
  if (options.action === undefined) return true;
  return new URLSearchParams(request.postData() ?? "").get("action") === options.action;
}
