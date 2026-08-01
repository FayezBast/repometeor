# RepoMeteor scoring

RepoMeteor momentum is transparent and deterministic. It is not GitHub Trending and does not attempt to reproduce GitHub's private ranking.

## Inputs

The `momentum-v1` formula uses only stored aggregate snapshots and repository metadata. The windows are 6 hours, 24 hours, and 7 days. A baseline may fall within a documented tolerance because scheduled collection rarely lands on an exact boundary. Future snapshots are ignored and stale histories are excluded.

Raw features are star velocity, relative star growth, acceleration between half-windows, fork velocity, recent push/release activity, and repository freshness. Relative growth uses a 20-star denominator floor. Activity and freshness use deterministic time decay.

## Normalization and formula

Skewed positive features are transformed with `log1p`; feature tails are winsorized at 1%; deterministic percentile ranks are calculated against the fully eligible cohort. Ties receive the same midpoint percentile. Missing acceleration receives a neutral percentile and lowers confidence instead of being fabricated as zero.

```text
raw = 100 × (
  0.32 × star_velocity_percentile +
  0.23 × relative_growth_percentile +
  0.17 × acceleration_percentile +
  0.10 × fork_velocity_percentile +
  0.13 × activity_percentile +
  0.05 × freshness_percentile
)

final = clamp(raw × (0.75 + 0.25 × confidence), 0, 100)
```

Scores are rounded to three decimals. Ties sort by star delta and then stable repository ID, so identical inputs and time always yield identical output.

## Confidence and eligibility

Confidence reflects observed history coverage, snapshot density, largest gaps, baseline quality, newest-snapshot freshness, and acceleration availability. Labels are high at 0.85+, medium at 0.60–0.849, and low below 0.60.

Repositories require at least five stars, a meaningful description, public non-fork/non-archived/non-disabled state, and a current snapshot. A repository with enough but incomplete history is provisional: it can appear in New Entrants, is normalized against the established cohort, and cannot distort or enter the primary leaderboard until adequately observed.

## Collections

- Rising Now: the primary 6-hour eligible ranking.
- Daily Momentum: the primary 24-hour eligible ranking.
- Weekly Momentum: the primary 7-day eligible ranking.
- Hidden Gems: 10–1,000 stars, positive movement, and medium-or-higher confidence.
- New Entrants: provisional repositories with measured star or fork movement.
- Established Movers: more than 10,000 stars, medium-or-higher confidence, and star velocity at or above the 60th percentile.

Neutral anomaly flags describe measurement patterns such as negative deltas, sparse history, or unusual jumps. They are never accusations. Up to three deterministic explanation sentences are stored with each score so the dashboard, API, CLI, and feed can state why a repository is rising.

Any feature, weight, eligibility, confidence, collection, or normalization change requires a formula-version bump, fixtures, migration consideration for stored scores, and updates to this document and `DECISIONS.md`.
