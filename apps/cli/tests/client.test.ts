import { createServer } from "node:http";
import { afterEach, describe, expect, it } from "vitest";
import { Client } from "../src/client.js";

const servers: ReturnType<typeof createServer>[] = [];
afterEach(() => servers.splice(0).forEach((server) => server.close()));

async function fixture(body: unknown, status = 200) {
  const server = createServer((request, response) => {
    response.writeHead(status, { "content-type": "application/json" });
    response.end(JSON.stringify(body));
  });
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string")
    throw new Error("fixture did not bind");
  return `http://127.0.0.1:${address.port}`;
}

describe("Client", () => {
  it("validates a trending response from a local API", async () => {
    const baseURL = await fixture({
      data: [],
      meta: {
        window: "24h",
        collection: "daily",
        generatedAt: "2026-08-01T00:00:00Z",
        demoMode: true,
        resultCount: 0,
        formulaVersion: "momentum-v1",
      },
      links: { nextCursor: null },
    });
    const response = await new Client(baseURL, 1000).trending({
      window: "24h",
    });
    expect(response.meta.demoMode).toBe(true);
  });
  it("returns a friendly API error", async () => {
    const baseURL = await fixture({ detail: "Too many requests." }, 429);
    await expect(new Client(baseURL, 1000).trending({})).rejects.toMatchObject({
      message: "Too many requests.",
      exitCode: 3,
    });
  });
});
