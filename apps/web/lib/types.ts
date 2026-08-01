import { z } from "zod";

export const repositorySchema = z.object({
  owner: z.string(),
  name: z.string(),
  fullName: z.string(),
  description: z.string(),
  url: z.url(),
  homepageUrl: z.url().nullable(),
  avatarUrl: z.url().nullable(),
  language: z.string().nullable(),
  topics: z.array(z.string()),
  license: z.string().nullable(),
  stars: z.number().int().nonnegative(),
  forks: z.number().int().nonnegative(),
  openIssues: z.number().int().nonnegative(),
  isArchived: z.boolean(),
  isFork: z.boolean(),
  createdAt: z.string(),
  pushedAt: z.string().nullable(),
});

export const momentumSchema = z.object({
  window: z.string(),
  score: z.number(),
  rank: z.number().int().nullable(),
  starDelta: z.number().int(),
  starVelocity: z.number(),
  relativeGrowth: z.number(),
  acceleration: z.number(),
  forkDelta: z.number().int(),
  forkVelocity: z.number(),
  activityScore: z.number(),
  freshnessScore: z.number(),
  confidence: z.number(),
  confidenceLabel: z.string(),
  reasons: z.array(z.string()),
  flags: z.array(z.string()),
  collections: z.array(z.string()),
  formulaVersion: z.string(),
  scoredAt: z.string(),
});

export const trendingResponseSchema = z.object({
  data: z.array(
    z.object({
      repository: repositorySchema,
      momentum: momentumSchema,
      sparkline: z.array(z.number().int()),
    }),
  ),
  meta: z.object({
    window: z.string(),
    collection: z.string(),
    generatedAt: z.string(),
    demoMode: z.boolean(),
    resultCount: z.number().int(),
    formulaVersion: z.string(),
  }),
  links: z.object({ nextCursor: z.string().nullable() }),
});

export const historyPointSchema = z.object({
  at: z.string(),
  stars: z.number().int(),
  forks: z.number().int(),
  starDelta: z.number().int(),
  forkDelta: z.number().int(),
  score: z.number().nullable().optional(),
});

export const historyResponseSchema = z.object({
  data: z.array(historyPointSchema),
  meta: z.object({
    repository: z.string(),
    range: z.string(),
    resolution: z.string(),
    from: z.string(),
    to: z.string(),
    trackingSince: z.string().nullable(),
    pointCount: z.number().int(),
    demoMode: z.boolean(),
  }),
});

export const detailResponseSchema = z.object({
  repository: repositorySchema,
  momentum: z.record(z.string(), momentumSchema),
  tracking: z.object({
    state: z.string(),
    tier: z.string(),
    trackingSince: z.string().nullable(),
    firstSeenAt: z.string(),
    lastCheckedAt: z.string().nullable(),
  }),
  links: z.object({
    self: z.string(),
    web: z.string(),
    github: z.string(),
    badge: z.string(),
    history: z.string(),
    feed: z.string(),
    badgeMarkdown: z.string(),
  }),
  meta: z.object({
    demoMode: z.boolean(),
    generatedAt: z.string(),
    formulaVersion: z.string(),
    canonicalFullName: z.string().optional(),
  }),
});

export const compareResponseSchema = z.object({
  data: z.array(
    z.object({
      repository: repositorySchema,
      points: z.array(historyPointSchema),
      momentum: momentumSchema.nullable(),
      available: z.boolean(),
      note: z.string().nullable(),
      trackingSince: z.string().nullable(),
      summary: z.object({
        startStars: z.number().int(),
        endStars: z.number().int(),
        absoluteGrowth: z.number().int(),
        relativeGrowth: z.number(),
        forkGrowth: z.number().int(),
      }),
    }),
  ),
  meta: z.object({
    window: z.string(),
    range: z.string(),
    resolution: z.string(),
    from: z.string(),
    to: z.string(),
    demoMode: z.boolean(),
  }),
});

export const facetsResponseSchema = z.object({
  data: z.array(z.object({ value: z.string(), count: z.number().int() })),
  meta: z.object({ resultCount: z.number().int(), demoMode: z.boolean() }),
});

export const methodologyResponseSchema = z.object({
  formulaVersion: z.string(),
  description: z.string(),
  weights: z.record(z.string(), z.number()),
  windows: z.array(
    z.object({
      name: z.string(),
      duration: z.string(),
      baselineTolerance: z.string(),
      minObservedHistory: z.string(),
      staleAfter: z.string(),
    }),
  ),
  confidence: z.object({
    description: z.string(),
    weights: z.record(z.string(), z.number()),
    labels: z.record(z.string(), z.string()),
    penalties: z.record(z.string(), z.number()),
  }),
  eligibility: z.object({
    minStars: z.number().int(),
    minDescriptionLength: z.number().int(),
    excludedStates: z.array(z.string()),
    rules: z.array(z.string()),
  }),
  collections: z.array(
    z.object({ name: z.string(), defaultWindow: z.string(), rule: z.string() }),
  ),
  anomalyFlags: z.array(
    z.object({ code: z.string(), description: z.string() }),
  ),
  snapshots: z.object({
    description: z.string(),
    interval: z.string(),
    source: z.string(),
    privacyNote: z.string(),
  }),
  formula: z.string(),
  lastUpdated: z.string(),
  demoMode: z.boolean(),
});

export type Repository = z.infer<typeof repositorySchema>;
export type Momentum = z.infer<typeof momentumSchema>;
export type TrendingResponse = z.infer<typeof trendingResponseSchema>;
export type HistoryResponse = z.infer<typeof historyResponseSchema>;
export type DetailResponse = z.infer<typeof detailResponseSchema>;
export type CompareResponse = z.infer<typeof compareResponseSchema>;
export type MethodologyResponse = z.infer<typeof methodologyResponseSchema>;
