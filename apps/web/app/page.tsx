import Link from "next/link";
import { APIUnavailable, NoResults } from "@/components/EmptyState";
import { MomentumCard } from "@/components/MomentumCard";
import { api } from "@/lib/api";
import { load, loadErrorMessage } from "@/lib/load";
import { formatNumber, relativeTime } from "@/lib/format";

export const dynamic = "force-dynamic";

const windows = [
  ["6h", "Now"],
  ["24h", "Daily"],
  ["7d", "Weekly"],
];

export default async function Home({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const window =
    typeof params.window === "string" &&
    ["6h", "24h", "7d"].includes(params.window)
      ? params.window
      : "24h";

  const result = await load(() => api.trending({ window, limit: "12" }));
  if (!result.ok) {
    return (
      <div className="shell page-pad">
        <APIUnavailable message={loadErrorMessage(result)} />
      </div>
    );
  }
  const response = result.data;

  const totalStars = response.data.reduce(
    (sum, item) => sum + item.momentum.starDelta,
    0,
  );

  return (
    <>
      <section className="hero shell">
        <div className="hero-copy">
          <p className="eyebrow">
            <span className="live-dot" /> Open-source radar · {window}
          </p>
          <h1>
            Catch rising repositories <em>before they hit orbit.</em>
          </h1>
          <p className="hero-lede">
            RepoMeteor tracks public aggregate snapshots and explains which
            projects are accelerating—not just which are already famous.
          </p>
          <div className="hero-actions">
            <Link className="button" href="/explore">
              Explore the radar
            </Link>
            <Link className="text-link" href="/methodology">
              See how scoring works →
            </Link>
          </div>
        </div>
        <div className="radar-card" aria-label="Current ranking summary">
          <div className="radar-orbit orbit-one" />
          <div className="radar-orbit orbit-two" />
          <div className="radar-pulse" />
          <p>Current scan</p>
          <strong>{response.meta.resultCount}</strong>
          <span>rising signals</span>
          <dl>
            <div>
              <dt>Stars gained</dt>
              <dd>+{formatNumber(totalStars)}</dd>
            </div>
            <div>
              <dt>Formula</dt>
              <dd>{response.meta.formulaVersion}</dd>
            </div>
          </dl>
        </div>
      </section>

      <section
        className="shell signal-section"
        aria-labelledby="ranking-heading"
      >
        <div className="section-heading">
          <div>
            <p className="eyebrow">Momentum rankings</p>
            <h2 id="ranking-heading">Projects gaining altitude</h2>
            <p>
              Updated {relativeTime(response.meta.generatedAt)} · Built from
              RepoMeteor&apos;s own aggregate history
            </p>
          </div>
          <div className="segmented" aria-label="Ranking window">
            {windows.map(([value, label]) => (
              <Link
                key={value}
                href={`/?window=${value}`}
                aria-current={window === value ? "page" : undefined}
              >
                <span>{label}</span>
                <small>{value}</small>
              </Link>
            ))}
          </div>
        </div>

        <div className="collection-links" aria-label="Curated collections">
          <Link href="/explore?collection=hidden-gems">Hidden gems</Link>
          <Link href="/explore?collection=new-entrants">New entrants</Link>
          <Link href="/explore?collection=established">Established</Link>
          <Link href="/explore?collection=weekly">Weekly climbers</Link>
        </div>

        {response.data.length ? (
          <div className="ranking-list">
            {response.data.map((item) => (
              <MomentumCard key={item.repository.fullName} {...item} />
            ))}
          </div>
        ) : (
          <NoResults />
        )}

        <div className="center-action">
          <Link className="button button-secondary" href="/explore">
            Open full explorer
          </Link>
        </div>
      </section>

      <section className="method-strip">
        <div className="shell three-up">
          <article>
            <span>01</span>
            <h3>Aggregate only</h3>
            <p>
              No stargazer identities or private event history—just public
              repository totals sampled over time.
            </p>
          </article>
          <article>
            <span>02</span>
            <h3>Size-aware</h3>
            <p>
              Relative growth and cohort percentiles give smaller repositories a
              genuine chance to surface.
            </p>
          </article>
          <article>
            <span>03</span>
            <h3>Explainable</h3>
            <p>
              Every score includes its features, confidence, formula version,
              and deterministic reasons.
            </p>
          </article>
        </div>
      </section>
    </>
  );
}
