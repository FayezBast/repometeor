import type { z } from "zod";
import {
  compareSchema,
  detailSchema,
  trackSchema,
  trendingSchema,
} from "./types.js";

export class ClientError extends Error {
  constructor(
    message: string,
    readonly exitCode = 1,
  ) {
    super(message);
    this.name = "ClientError";
  }
}

export class Client {
  constructor(
    readonly baseURL: string,
    readonly timeout: number,
  ) {}

  private async request<T>(
    path: string,
    schema: z.ZodType<T>,
    init?: RequestInit,
  ): Promise<T> {
    let response: Response;
    try {
      response = await fetch(new URL(path, this.baseURL), {
        ...init,
        headers: { Accept: "application/json", ...init?.headers },
        signal: AbortSignal.timeout(this.timeout),
      });
    } catch (error) {
      if (
        error instanceof Error &&
        (error.name === "TimeoutError" || error.name === "AbortError")
      ) {
        throw new ClientError(`Request timed out after ${this.timeout}ms.`, 2);
      }
      throw new ClientError(
        `Could not connect to ${this.baseURL}. Check the API URL and whether RepoMeteor is running.`,
        2,
      );
    }
    if (!response.ok) {
      let detail = `API returned ${response.status}.`;
      try {
        const body = (await response.json()) as { detail?: string };
        if (body.detail) detail = body.detail;
      } catch {
        /* status is enough */
      }
      throw new ClientError(detail, response.status === 429 ? 3 : 1);
    }
    const parsed = schema.safeParse(await response.json());
    if (!parsed.success)
      throw new ClientError(
        "The API returned a response this CLI version does not understand.",
      );
    return parsed.data;
  }

  trending(params: Record<string, string | undefined>) {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(params))
      if (value) query.set(key, value);
    return this.request(`/v1/trending?${query}`, trendingSchema);
  }
  inspect(repository: string) {
    return this.request(`/v1/repositories/${repository}`, detailSchema);
  }
  compare(repositories: string[], window: string) {
    const query = new URLSearchParams({
      repositories: repositories.join(","),
      window,
    });
    return this.request(`/v1/compare?${query}`, compareSchema);
  }
  track(repository: string) {
    return this.request("/v1/repositories/track", trackSchema, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ repository }),
    });
  }
}
