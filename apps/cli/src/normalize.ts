import { ClientError } from "./client.js";

const segment = /^[A-Za-z0-9_.-]+$/;

export function normalizeRepository(input: string) {
  let candidate = input.trim();
  if (candidate.startsWith("http://") || candidate.startsWith("https://")) {
    let url: URL;
    try {
      url = new URL(candidate);
    } catch {
      throw new ClientError(`Invalid repository: ${input}`);
    }
    if (
      url.protocol !== "https:" ||
      url.hostname.toLowerCase() !== "github.com"
    ) {
      throw new ClientError(
        "Repository URLs must use https://github.com/owner/repository.",
      );
    }
    const pieces = url.pathname.split("/").filter(Boolean);
    if (pieces.length !== 2)
      throw new ClientError(
        "Use a repository URL without issue, pull request, or file paths.",
      );
    candidate = pieces.join("/");
  }
  const pieces = candidate.replace(/\/$/, "").split("/");
  if (
    pieces.length !== 2 ||
    pieces.some((part) => !segment.test(part) || part === "." || part === "..")
  ) {
    throw new ClientError(
      `Invalid repository "${input}". Use owner/repository.`,
    );
  }
  return pieces.join("/");
}

export function normalizeAPIURL(input: string) {
  let url: URL;
  try {
    url = new URL(input);
  } catch {
    throw new ClientError(`Invalid API URL: ${input}`);
  }
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password
  ) {
    throw new ClientError(
      "API URL must be an http(s) URL without embedded credentials.",
    );
  }
  // Commands always address API routes from the origin root. Silently
  // retaining a supplied path would be misleading because `new URL("/v1/…")`
  // discards it later, so normalize to the origin explicitly.
  url.pathname = "/";
  url.search = "";
  url.hash = "";
  return url.toString().replace(/\/$/, "");
}
