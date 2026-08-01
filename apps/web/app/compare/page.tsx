import Link from "next/link";
import { APIUnavailable } from "@/components/EmptyState";
import { api, APIError } from "@/lib/api";
import { formatDelta, formatNumber } from "@/lib/format";

export const dynamic = "force-dynamic";

export default async function ComparePage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const raw =
    typeof params.repositories === "string" ? params.repositories : "";
  const repositories = raw
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean)
    .slice(0, 4);
  const window = typeof params.window === "string" ? params.window : "24h";
  let comparison = null;
  let errorMessage = "";
  if (repositories.length >= 2) {
    try {
      comparison = await api.compare(
        repositories,
        window,
        window === "7d" ? "30d" : "7d",
      );
    } catch (error) {
      errorMessage =
        error instanceof APIError
          ? error.message
          : "Comparison is unavailable.";
    }
  }

  return (
    <div className="shell page-pad">
      <header className="page-heading">
        <p className="eyebrow">Compare</p>
        <h1>Momentum, side by side</h1>
        <p>
          Compare relative growth over the same observed range—not just lifetime
          popularity.
        </p>
      </header>
      <form className="compare-form" action="/compare">
        <label>
          Repositories{" "}
          <input
            name="repositories"
            defaultValue={raw}
            placeholder="owner/one, owner/two"
            required
          />
        </label>
        <label>
          Window
          <select name="window" defaultValue={window}>
            <option value="6h">6 hours</option>
            <option value="24h">24 hours</option>
            <option value="7d">7 days</option>
          </select>
        </label>
        <button className="button">Compare</button>
      </form>
      <p className="field-help">
        Enter two to four comma-separated repositories.
      </p>
      {errorMessage ? <APIUnavailable message={errorMessage} /> : null}
      {comparison ? (
        <div className="comparison-grid">
          {comparison.data.map((entry, index) => (
            <article
              className="comparison-card"
              key={entry.repository.fullName}
              style={{ "--index": index } as React.CSSProperties}
            >
              <p className="eyebrow">
                Signal {String(index + 1).padStart(2, "0")}
              </p>
              <h2>
                <Link
                  href={`/r/${entry.repository.owner}/${entry.repository.name}`}
                >
                  {entry.repository.fullName}
                </Link>
              </h2>
              <strong className="compare-score">
                {entry.momentum?.score.toFixed(1) ?? "—"}
              </strong>
              <span>momentum · {window}</span>
              <dl>
                <div>
                  <dt>Stars</dt>
                  <dd>{formatNumber(entry.repository.stars)}</dd>
                </div>
                <div>
                  <dt>Growth</dt>
                  <dd>{formatDelta(entry.summary.absoluteGrowth)}</dd>
                </div>
                <div>
                  <dt>Relative</dt>
                  <dd>{(entry.summary.relativeGrowth * 100).toFixed(1)}%</dd>
                </div>
                <div>
                  <dt>Fork growth</dt>
                  <dd>{formatDelta(entry.summary.forkGrowth)}</dd>
                </div>
              </dl>
              {entry.note ? <p className="fine-print">{entry.note}</p> : null}
            </article>
          ))}
        </div>
      ) : repositories.length < 2 ? (
        <section className="empty-state">
          <p className="eyebrow">Ready for coordinates</p>
          <h2>Add at least two repositories.</h2>
          <p>
            We align their stored snapshots to one range and show where each
            project is actually accelerating.
          </p>
        </section>
      ) : null}
    </div>
  );
}
