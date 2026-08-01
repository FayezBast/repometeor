import type { z } from "zod";
import {
  compareResponseSchema,
  detailResponseSchema,
  facetsResponseSchema,
  historyResponseSchema,
  methodologyResponseSchema,
  trendingResponseSchema,
} from "@/lib/types";

const PUBLIC_API_URL =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
const SERVER_API_URL = process.env.INTERNAL_API_URL ?? PUBLIC_API_URL;

export class APIError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message);
    this.name = "APIError";
  }
}

async function request<T>(
  path: string,
  schema: z.ZodType<T>,
  signal?: AbortSignal,
): Promise<T> {
  const timeout = AbortSignal.timeout(8_000);
  const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
  let response: Response;

  try {
    response = await fetch(new URL(path, SERVER_API_URL), {
      headers: { Accept: "application/json" },
      next: { revalidate: 60 },
      signal: combined,
    });
  } catch (error) {
    throw new APIError(
      error instanceof Error && error.name === "TimeoutError"
        ? "RepoMeteor API timed out."
        : "RepoMeteor API is unavailable.",
    );
  }

  if (!response.ok) {
    throw new APIError(
      `RepoMeteor API returned ${response.status}.`,
      response.status,
    );
  }

  const parsed = schema.safeParse(await response.json());
  if (!parsed.success) {
    throw new APIError("RepoMeteor API returned an unexpected response.");
  }
  return parsed.data;
}

function withQuery(path: string, params: Record<string, string | undefined>) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value) query.set(key, value);
  }
  const encoded = query.toString();
  return encoded ? `${path}?${encoded}` : path;
}

export const api = {
  publicURL: PUBLIC_API_URL,
  trending: (params: Record<string, string | undefined> = {}) =>
    request(withQuery("/v1/trending", params), trendingResponseSchema),
  languages: () => request("/v1/languages", facetsResponseSchema),
  topics: () => request("/v1/topics", facetsResponseSchema),
  detail: (owner: string, repo: string) =>
    request(
      `/v1/repositories/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}`,
      detailResponseSchema,
    ),
  history: (owner: string, repo: string, range = "7d") =>
    request(
      withQuery(
        `/v1/repositories/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/history`,
        {
          range,
        },
      ),
      historyResponseSchema,
    ),
  compare: (repositories: string[], window = "24h", range = "7d") =>
    request(
      withQuery("/v1/compare", {
        repositories: repositories.join(","),
        window,
        range,
      }),
      compareResponseSchema,
    ),
  methodology: () => request("/v1/methodology", methodologyResponseSchema),
};
