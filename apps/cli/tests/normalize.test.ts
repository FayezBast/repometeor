import { describe, expect, it } from "vitest";
import { normalizeAPIURL, normalizeRepository } from "../src/normalize.js";

describe("normalization", () => {
  it.each([
    ["owner/repo", "owner/repo"],
    ["https://github.com/Owner/Repo/", "Owner/Repo"],
  ])("normalizes %s", (input, expected) =>
    expect(normalizeRepository(input)).toBe(expected),
  );
  it.each([
    "https://gitlab.com/o/r",
    "https://github.com/o/r/issues",
    "missing-slash",
  ])("rejects %s", (input) =>
    expect(() => normalizeRepository(input)).toThrow(),
  );
  it("removes API paths and credentials are rejected", () => {
    expect(normalizeAPIURL("http://localhost:8080/")).toBe(
      "http://localhost:8080",
    );
    expect(normalizeAPIURL("https://api.example.test/ignored/path")).toBe(
      "https://api.example.test",
    );
    expect(() => normalizeAPIURL("https://token@example.com")).toThrow();
  });
});
