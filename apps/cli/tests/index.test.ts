import { afterEach, describe, expect, it, vi } from "vitest";
import { main } from "../src/index.js";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllEnvs();
});

describe("CLI commands", () => {
  it("prints machine-readable config with flag precedence", async () => {
    vi.stubEnv("REPOMETEOR_API_URL", "https://env.example.test");
    const stdout = vi
      .spyOn(process.stdout, "write")
      .mockImplementation(() => true);

    const exitCode = await main([
      "node",
      "repometeor",
      "--api-url",
      "https://flag.example.test/base",
      "--json",
      "--no-color",
      "--timeout",
      "2500",
      "config",
    ]);

    expect(exitCode).toBe(0);
    const output = stdout.mock.calls.map(([chunk]) => String(chunk)).join("");
    expect(JSON.parse(output)).toEqual({
      apiUrl: "https://flag.example.test",
      version: "0.1.0",
      color: false,
      timeout: 2500,
    });
  });

  it("uses a friendly non-zero exit for an invalid compare count", async () => {
    const stderr = vi
      .spyOn(process.stderr, "write")
      .mockImplementation(() => true);

    const exitCode = await main([
      "node",
      "repometeor",
      "compare",
      "owner/only-one",
    ]);

    expect(exitCode).toBe(1);
    expect(stderr).toHaveBeenCalledWith(
      "repometeor: Compare requires two to four repositories.\n",
    );
  });
});
