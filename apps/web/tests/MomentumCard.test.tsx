import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MomentumCard } from "@/components/MomentumCard";

describe("MomentumCard", () => {
  it("renders ranking context and an accessible sparkline", () => {
    render(
      <MomentumCard
        repository={{
          owner: "orbit-labs",
          name: "signal-kit",
          fullName: "orbit-labs/signal-kit",
          description: "Typed signal processing primitives",
          url: "https://github.com/orbit-labs/signal-kit",
          homepageUrl: null,
          avatarUrl: null,
          language: "TypeScript",
          topics: ["signals"],
          license: "MIT",
          stars: 1240,
          forks: 92,
          openIssues: 14,
          isArchived: false,
          isFork: false,
          createdAt: "2026-01-01T00:00:00Z",
          pushedAt: null,
        }}
        momentum={{
          window: "24h",
          score: 92.4,
          rank: 1,
          starDelta: 312,
          starVelocity: 13,
          relativeGrowth: 0.25,
          acceleration: 2,
          forkDelta: 8,
          forkVelocity: 0.3,
          activityScore: 0.7,
          freshnessScore: 0.9,
          confidence: 0.92,
          confidenceLabel: "high",
          reasons: ["Growth is accelerating."],
          flags: [],
          collections: ["daily"],
          formulaVersion: "momentum-v1",
          scoredAt: "2026-08-01T00:00:00Z",
        }}
        sparkline={[900, 980, 1100, 1240]}
      />,
    );
    expect(
      screen.getByRole("link", { name: "orbit-labs/signal-kit" }),
    ).toBeInTheDocument();
    expect(screen.getByText("92.4")).toBeInTheDocument();
    expect(
      screen.getByRole("img", { name: /star history/i }),
    ).toBeInTheDocument();
  });
});
