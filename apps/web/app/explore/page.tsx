import { APIUnavailable, NoResults } from "@/components/EmptyState";
import { MomentumCard } from "@/components/MomentumCard";
import { api } from "@/lib/api";
import { load, loadErrorMessage } from "@/lib/load";

export const dynamic = "force-dynamic";

function value(input: string | string[] | undefined) {
  return typeof input === "string" ? input : "";
}

export default async function Explore({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const raw = await searchParams;
  const query = {
    window: value(raw.window) || "24h",
    collection: value(raw.collection),
    language: value(raw.language),
    topic: value(raw.topic),
    confidence: value(raw.confidence),
    minStars: value(raw.minStars),
    maxStars: value(raw.maxStars),
    limit: "50",
  };

  const result = await load(() =>
    Promise.all([api.trending(query), api.languages(), api.topics()]),
  );
  if (!result.ok) {
    return (
      <div className="shell page-pad">
        <APIUnavailable message={loadErrorMessage(result)} />
      </div>
    );
  }
  const [response, languages, topics] = result.data;

  return (
    <div className="shell page-pad">
      <header className="page-heading">
        <p className="eyebrow">Explore</p>
        <h1>Open-source momentum radar</h1>
        <p>
          Filter the current cohort without losing the scoring context behind
          each result.
        </p>
      </header>
      <form className="filter-panel" action="/explore">
        <label>
          Window
          <select name="window" defaultValue={query.window}>
            <option value="6h">6 hours</option>
            <option value="24h">24 hours</option>
            <option value="7d">7 days</option>
          </select>
        </label>
        <label>
          Collection
          <select name="collection" defaultValue={query.collection}>
            <option value="">Rising</option>
            <option value="hidden-gems">Hidden gems</option>
            <option value="new-entrants">New entrants</option>
            <option value="established">Established</option>
            <option value="weekly">Weekly</option>
          </select>
        </label>
        <label>
          Language
          <select name="language" defaultValue={query.language}>
            <option value="">All languages</option>
            {languages.data.map((item) => (
              <option key={item.value} value={item.value}>
                {item.value} ({item.count})
              </option>
            ))}
          </select>
        </label>
        <label>
          Topic
          <select name="topic" defaultValue={query.topic}>
            <option value="">All topics</option>
            {topics.data.slice(0, 30).map((item) => (
              <option key={item.value} value={item.value}>
                {item.value} ({item.count})
              </option>
            ))}
          </select>
        </label>
        <label>
          Confidence
          <select name="confidence" defaultValue={query.confidence}>
            <option value="">Any confidence</option>
            <option value="high">High</option>
            <option value="medium">Medium</option>
            <option value="low">Low</option>
          </select>
        </label>
        <label>
          Minimum stars
          <input
            type="number"
            min="0"
            name="minStars"
            defaultValue={query.minStars}
            placeholder="0"
          />
        </label>
        <button className="button">Apply filters</button>
      </form>
      <div className="results-heading">
        {/*
          Repository cards render an <h3>. Without an <h2> here the document
          would jump straight from the page <h1> to <h3>, which a screen-reader
          user navigating by heading level experiences as a missing section.
        */}
        <h2 id="results-heading">
          <strong>{response.meta.resultCount}</strong> signals in this cohort
        </h2>
        <span>
          {response.meta.collection} · {response.meta.window}
        </span>
      </div>
      {response.data.length ? (
        <div className="ranking-list" aria-labelledby="results-heading">
          {response.data.map((item) => (
            <MomentumCard key={item.repository.fullName} {...item} />
          ))}
        </div>
      ) : (
        <NoResults />
      )}
    </div>
  );
}
