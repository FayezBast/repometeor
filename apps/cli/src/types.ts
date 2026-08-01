import { z } from "zod";

export const repositorySchema = z.object({
  owner: z.string(),
  name: z.string(),
  fullName: z.string(),
  description: z.string(),
  url: z.string(),
  language: z.string().nullable(),
  topics: z.array(z.string()),
  license: z.string().nullable(),
  stars: z.number().int(),
  forks: z.number().int(),
  openIssues: z.number().int(),
});

export const momentumSchema = z
  .object({
    window: z.string(),
    score: z.number(),
    rank: z.number().int().nullable(),
    starDelta: z.number().int(),
    starVelocity: z.number(),
    relativeGrowth: z.number(),
    acceleration: z.number(),
    forkDelta: z.number().int(),
    confidence: z.number(),
    confidenceLabel: z.string(),
    reasons: z.array(z.string()),
    flags: z.array(z.string()),
    formulaVersion: z.string(),
    scoredAt: z.string(),
  })
  .passthrough();

export const trendingSchema = z.object({
  data: z.array(
    z.object({
      repository: repositorySchema.passthrough(),
      momentum: momentumSchema,
      sparkline: z.array(z.number()),
    }),
  ),
  meta: z.object({
    window: z.string(),
    collection: z.string(),
    generatedAt: z.string(),
    demoMode: z.boolean(),
    resultCount: z.number(),
    formulaVersion: z.string(),
  }),
  links: z.object({ nextCursor: z.string().nullable() }),
});

export const detailSchema = z
  .object({
    repository: repositorySchema.passthrough(),
    momentum: z.record(z.string(), momentumSchema),
    tracking: z
      .object({
        state: z.string(),
        tier: z.string(),
        trackingSince: z.string().nullable(),
      })
      .passthrough(),
  })
  .passthrough();

export const compareSchema = z.object({
  data: z.array(
    z
      .object({
        repository: repositorySchema.passthrough(),
        momentum: momentumSchema.nullable(),
        available: z.boolean(),
        note: z.string().nullable(),
        summary: z.object({
          startStars: z.number(),
          endStars: z.number(),
          absoluteGrowth: z.number(),
          relativeGrowth: z.number(),
          forkGrowth: z.number(),
        }),
      })
      .passthrough(),
  ),
  meta: z
    .object({ window: z.string(), range: z.string(), demoMode: z.boolean() })
    .passthrough(),
});

export const trackSchema = z.object({
  status: z.string(),
  repository: z.string(),
  message: z.string(),
  webUrl: z.string().nullable(),
});

export type Trending = z.infer<typeof trendingSchema>;
export type Detail = z.infer<typeof detailSchema>;
export type Comparison = z.infer<typeof compareSchema>;
export type Track = z.infer<typeof trackSchema>;
