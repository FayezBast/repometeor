import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { APIUnavailable } from "@/components/EmptyState";
import { HistoryChart } from "@/components/HistoryChart";
import { api } from "@/lib/api";
import { load, loadErrorMessage } from "@/lib/load";
import { formatDelta, formatNumber, relativeTime } from "@/lib/format";

export const dynamic = "force-dynamic";

type RouteParams = Promise<{ owner: string; repository: string }>;

export async function generateMetadata({
  params,
}: {
  params: RouteParams;
}): Promise<Metadata> {
  const { owner, repository } = await params;
  try {
    const detail = await api.detail(owner, repository);
    const momentum = detail.momentum["24h"];
    return {
      title: detail.repository.fullName,
      description: momentum
        ? `${detail.repository.fullName} has ${momentum.score.toFixed(1)} RepoMeteor momentum with ${formatDelta(momentum.starDelta)} stars in 24h.`
        : `Momentum history for ${detail.repository.fullName}.`,
    };
  } catch {
    return { title: `${owner}/${repository}` };
  }
}

export default async function RepositoryPage({
  params,
}: {
  params: RouteParams;
}) {
  const { owner, repository } = await params;
  const result = await load(() =>
    Promise.all([
      api.detail(owner, repository),
      api.history(owner, repository, "7d"),
    ]),
  );
  if (!result.ok) {
    // A confirmed 404 is a different outcome from an unreachable API: the
    // repository genuinely is not tracked, so Next.js should render its
    // not-found page rather than an "API unavailable" message.
    if (result.status === 404) notFound();
    return (
      <div className="shell page-pad">
        <APIUnavailable message={loadErrorMessage(result)} />
      </div>
    );
  }
  const [detail, history] = result.data;

  const momentum = detail.momentum["24h"] ?? Object.values(detail.momentum)[0];
  return (
    <div className="shell page-pad repo-page">
      <div className="repo-title-row">
        <div>
          <p className="eyebrow">Repository signal</p>
          <h1>{detail.repository.fullName}</h1>
          <p>{detail.repository.description}</p>
        </div>
        <a className="button button-secondary" href={detail.repository.url}>
          View on GitHub ↗
        </a>
      </div>
      {momentum ? (
        <section className="score-hero">
          <div>
            <span>RepoMeteor momentum · {momentum.window}</span>
            <strong>{momentum.score.toFixed(1)}</strong>
            <small>
              {momentum.rank ? `Rank #${momentum.rank}` : "Provisional"}
            </small>
          </div>
          <dl>
            <div>
              <dt>Stars</dt>
              <dd>{formatNumber(detail.repository.stars)}</dd>
            </div>
            <div>
              <dt>Growth</dt>
              <dd className="positive">{formatDelta(momentum.starDelta)}</dd>
            </div>
            <div>
              <dt>Velocity</dt>
              <dd>{momentum.starVelocity.toFixed(1)}/h</dd>
            </div>
            <div>
              <dt>Confidence</dt>
              <dd>
                {momentum.confidenceLabel} ·{" "}
                {Math.round(momentum.confidence * 100)}%
              </dd>
            </div>
          </dl>
        </section>
      ) : null}
      <div className="detail-grid">
        <section className="panel chart-panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">Observed history</p>
              <h2>Stars over 7 days</h2>
            </div>
            <span>{history.meta.pointCount} snapshots</span>
          </div>
          <HistoryChart points={history.data} />
          <p className="fine-print">
            Tracking since{" "}
            {history.meta.trackingSince
              ? relativeTime(history.meta.trackingSince)
              : "history unavailable"}
            . RepoMeteor cannot reconstruct activity from before tracking began.
          </p>
        </section>
        <aside className="panel why-panel">
          <p className="eyebrow">Why it is rising</p>
          <h2>Signals behind the score</h2>
          {momentum?.reasons.length ? (
            <ol>
              {momentum.reasons.map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ol>
          ) : (
            <p>
              Explanations appear after enough snapshot history is available.
            </p>
          )}
          {momentum?.flags.length ? (
            <div className="notice">
              <strong>Data note</strong>
              <p>{momentum.flags.join(", ")}</p>
            </div>
          ) : null}
        </aside>
      </div>
      <section className="panel repo-facts">
        <h2>Repository profile</h2>
        <dl>
          <div>
            <dt>Language</dt>
            <dd>{detail.repository.language ?? "Not specified"}</dd>
          </div>
          <div>
            <dt>Forks</dt>
            <dd>{formatNumber(detail.repository.forks)}</dd>
          </div>
          <div>
            <dt>License</dt>
            <dd>{detail.repository.license ?? "Not specified"}</dd>
          </div>
          <div>
            <dt>Tracking tier</dt>
            <dd>{detail.tracking.tier}</dd>
          </div>
        </dl>
        <div className="topic-row">
          {detail.repository.topics.map((topic) => (
            <span key={topic}>{topic}</span>
          ))}
        </div>
      </section>
      <section className="panel badge-panel">
        <div>
          <p className="eyebrow">Share the signal</p>
          <h2>README badge</h2>
          <p>Badge values update with the latest completed scoring run.</p>
        </div>
        {/*
          The snippet scrolls horizontally on narrow viewports. A scrollable
          region that cannot receive focus is unreachable by keyboard, so it is
          made focusable and given an accessible name.
        */}
        <pre tabIndex={0} role="region" aria-label="README badge snippet">
          <code>{detail.links.badgeMarkdown}</code>
        </pre>
      </section>
      <Link
        className="text-link"
        href={`/compare?repositories=${encodeURIComponent(detail.repository.fullName + ",")}`}
      >
        Compare this repository →
      </Link>
    </div>
  );
}
