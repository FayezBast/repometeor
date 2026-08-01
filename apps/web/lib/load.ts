import { APIError } from "@/lib/api";

/**
 * The outcome of a server-component data load.
 *
 * Pages use this instead of wrapping their markup in try/catch. Constructing
 * JSX inside a try block hides render-time errors behind the catch intended for
 * the fetch, which is why `react-hooks/error-boundaries` forbids it: a bug in
 * the markup would silently render the "API unavailable" state instead of
 * surfacing. Separating "load" from "render" keeps the catch around exactly the
 * operation that is expected to fail.
 */
export type LoadResult<T> =
  | { ok: true; data: T }
  | { ok: false; error: APIError | Error; status?: number };

/**
 * Runs a data load and captures failure instead of throwing.
 *
 * The returned status lets a page distinguish a genuine 404 — which should
 * render Next.js's not-found page — from an unreachable API, which should
 * render the unavailable state.
 */
export async function load<T>(fn: () => Promise<T>): Promise<LoadResult<T>> {
  try {
    return { ok: true, data: await fn() };
  } catch (error) {
    if (error instanceof APIError) {
      return { ok: false, error, status: error.status };
    }
    return {
      ok: false,
      error: error instanceof Error ? error : new Error("Unknown error"),
    };
  }
}

/**
 * The message to show for a failed load, or undefined to use the default copy.
 *
 * Only APIError messages are surfaced: they are written for readers. Any other
 * error could carry internal detail, so it is deliberately not displayed.
 */
export function loadErrorMessage(result: {
  ok: false;
  error: APIError | Error;
}): string | undefined {
  return result.error instanceof APIError ? result.error.message : undefined;
}
