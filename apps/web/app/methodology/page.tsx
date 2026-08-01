import { APIUnavailable } from "@/components/EmptyState";
import { api } from "@/lib/api";
import { load, loadErrorMessage } from "@/lib/load";

export const dynamic = "force-dynamic";

export default async function MethodologyPage() {
  const result = await load(() => api.methodology());
  if (!result.ok) {
    return (
      <div className="shell page-pad">
        <APIUnavailable message={loadErrorMessage(result)} />
      </div>
    );
  }
  const method = result.data;

  return (
    <div className="shell page-pad article-page">
      <header className="page-heading">
        <p className="eyebrow">Formula {method.formulaVersion}</p>
        <h1>A ranking you can inspect.</h1>
        <p>{method.description}</p>
      </header>
      <section className="formula-card">
        <span>Confidence-adjusted momentum</span>
        <code>{method.formula}</code>
        <p>
          The score is clamped from 0 to 100 after cohort normalization and
          confidence adjustment.
        </p>
      </section>
      <section>
        <h2>What contributes</h2>
        <div className="weight-grid">
          {Object.entries(method.weights).map(([name, weight]) => (
            <article key={name}>
              <strong>{Math.round(weight * 100)}%</strong>
              <h3>{name.replaceAll("_", " ")}</h3>
              <p>Percentile-normalized within the eligible cohort.</p>
            </article>
          ))}
        </div>
      </section>
      <section>
        <h2>Windows and confidence</h2>
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>Window</th>
                <th>Baseline tolerance</th>
                <th>Minimum history</th>
                <th>Stale after</th>
              </tr>
            </thead>
            <tbody>
              {method.windows.map((item) => (
                <tr key={item.name}>
                  <th>{item.name}</th>
                  <td>{item.baselineTolerance}</td>
                  <td>{item.minObservedHistory}</td>
                  <td>{item.staleAfter}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p>{method.confidence.description}</p>
      </section>
      <section>
        <h2>Eligibility</h2>
        <p>
          Repositories need at least {method.eligibility.minStars} stars and a
          meaningful description. Forks, archived repositories, disabled
          repositories, and incomplete records do not rank.
        </p>
        <ul>
          {method.eligibility.rules.map((rule) => (
            <li key={rule}>{rule}</li>
          ))}
        </ul>
      </section>
      <section>
        <h2>Honest data boundaries</h2>
        <p>{method.snapshots.description}</p>
        <p>{method.snapshots.privacyNote}</p>
        <div className="notice">
          <strong>Not GitHub Trending</strong>
          <p>
            RepoMeteor measures its own aggregate snapshot history. It does not
            reproduce or claim access to GitHub&apos;s private Trending
            algorithm.
          </p>
        </div>
      </section>
    </div>
  );
}
