import { api } from "@/lib/api";

const examples = [
  [
    "Trending",
    "GET",
    "/v1/trending?window=24h&collection=hidden-gems&limit=20",
  ],
  ["Repository detail", "GET", "/v1/repositories/orbit-labs/signal-kit"],
  ["History", "GET", "/v1/repositories/orbit-labs/signal-kit/history?range=7d"],
  ["Track", "POST", "/v1/repositories/track"],
  ["Badge", "GET", "/v1/badges/orbit-labs/signal-kit.svg?window=24h"],
  ["Atom feed", "GET", "/v1/feeds/trending.xml?window=24h"],
];

export default function APIDocsPage() {
  return (
    <div className="shell page-pad article-page">
      <header className="page-heading">
        <p className="eyebrow">Public API · v1</p>
        <h1>Momentum data, without a gate.</h1>
        <p>
          JSON endpoints, Atom feeds, and SVG badges are public. Responses
          include cache headers, request IDs, and transparent metadata.
        </p>
      </header>
      <section className="api-base">
        <span>Base URL</span>
        <code>{api.publicURL}</code>
      </section>
      <section>
        <h2>Quick start</h2>
        <pre>
          <code>{`curl --fail --show-error \\\n  '${api.publicURL}/v1/trending?window=24h&limit=10'`}</code>
        </pre>
        <p>
          Use the returned <code>links.nextCursor</code> as the next
          request&apos;s <code>cursor</code>. Do not construct cursors yourself.
        </p>
      </section>
      <section>
        <h2>Endpoints</h2>
        <div className="endpoint-list">
          {examples.map(([label, method, path]) => (
            <article key={label}>
              <div>
                <span className={`method method-${method.toLowerCase()}`}>
                  {method}
                </span>
                <strong>{label}</strong>
              </div>
              <code>{path}</code>
            </article>
          ))}
        </div>
      </section>
      <section>
        <h2>Track a repository</h2>
        <pre>
          <code>{`curl --fail --show-error \\\n  -X POST '${api.publicURL}/v1/repositories/track' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"repository":"owner/repository"}'`}</code>
        </pre>
        <p>
          Tracking is asynchronous and returns <code>202 Accepted</code>.
          History starts with the first snapshot; unavailable past activity is
          never invented.
        </p>
      </section>
      <section>
        <h2>Errors and limits</h2>
        <p>
          Errors use <code>application/problem+json</code>. Read routes are
          rate-limited per client, and tracking submissions have a stricter
          hourly limit. Respect <code>Retry-After</code> on <code>429</code>{" "}
          responses.
        </p>
        <p>
          The complete OpenAPI 3.1 contract lives at{" "}
          <code>contracts/openapi.yaml</code> in the source repository.
        </p>
      </section>
    </div>
  );
}
