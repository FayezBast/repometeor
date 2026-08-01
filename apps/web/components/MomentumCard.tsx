import Link from "next/link";
import { formatDelta, formatNumber, relativeTime } from "@/lib/format";
import type { Momentum, Repository } from "@/lib/types";
import { Sparkline } from "@/components/Sparkline";

/**
 * Human-readable wording for anomaly flag codes.
 *
 * The codes are deliberately neutral — RepoMeteor describes an unusual data
 * pattern, it never alleges that a project did anything wrong — and the copy
 * here keeps that framing.
 */
const FLAG_COPY: Record<string, string> = {
  unusual_star_fork_ratio: "Unusual growth pattern; interpret cautiously.",
  stale_repository_spike: "Stars rising on a repository with no recent pushes.",
  insufficient_history: "Limited history; ranked provisionally.",
  large_single_interval_change: "Most growth landed in a single interval.",
  snapshot_gap: "Part of this window is missing from collection.",
  recently_discovered: "Tracking began inside this window.",
};

function describeFlags(flags: string[]) {
  const [first] = flags;
  return FLAG_COPY[first] ?? "Interpret this score cautiously.";
}

export function MomentumCard({
  repository,
  momentum,
  sparkline,
}: {
  repository: Repository;
  momentum: Momentum;
  sparkline: number[];
}) {
  const primaryReason = momentum.reasons[0];

  return (
    <article className="momentum-card">
      <div
        className="rank"
        aria-label={
          momentum.rank ? `Rank ${momentum.rank}` : "Provisional ranking"
        }
      >
        {momentum.rank ? String(momentum.rank).padStart(2, "0") : "—"}
      </div>
      <div className="repo-main">
        <div className="repo-heading">
          <div>
            <p className="eyebrow">{repository.language ?? "Other"}</p>
            <h3>
              <Link href={`/r/${repository.owner}/${repository.name}`}>
                {repository.fullName}
              </Link>
            </h3>
          </div>
          <Sparkline
            values={sparkline}
            label={`${repository.fullName} star history`}
          />
        </div>
        <p className="description">
          {repository.description || "No repository description provided."}
        </p>

        {/*
          The primary explanation is the point of the product: a ranking a
          reader cannot interrogate is just an opaque leaderboard. The sentence
          is generated deterministically at scoring time and stored, so what is
          shown here is exactly what produced the score.
        */}
        {primaryReason ? <p className="reason">{primaryReason}</p> : null}

        <div className="card-meta">
          <div className="topic-row" aria-label="Topics">
            {repository.topics.slice(0, 3).map((topic) => (
              <span key={topic}>{topic}</span>
            ))}
          </div>
          {repository.pushedAt ? (
            <span className="pushed">
              Updated{" "}
              <time dateTime={repository.pushedAt}>
                {relativeTime(repository.pushedAt)}
              </time>
            </span>
          ) : null}
        </div>

        {/*
          Flags are conveyed by text, not by colour alone, so the warning is
          available to a reader who cannot distinguish the accent colour.
        */}
        {momentum.flags.length > 0 ? (
          <p className="card-flags">
            <span aria-hidden="true">⚠</span> {describeFlags(momentum.flags)}
          </p>
        ) : null}
      </div>
      <dl className="score-grid">
        <div>
          <dt>Momentum</dt>
          <dd className="score-value">{momentum.score.toFixed(1)}</dd>
        </div>
        <div>
          <dt>Stars</dt>
          <dd>{formatNumber(repository.stars)}</dd>
        </div>
        <div>
          <dt>+{momentum.window}</dt>
          <dd className="positive">{formatDelta(momentum.starDelta)}</dd>
        </div>
        <div>
          <dt>Confidence</dt>
          <dd>{momentum.confidenceLabel}</dd>
        </div>
      </dl>
    </article>
  );
}
