import Link from "next/link";

export const metadata = { title: "About" };

export default function AboutPage() {
  return (
    <div className="shell page-pad article-page">
      <header className="page-heading">
        <p className="eyebrow">About RepoMeteor</p>
        <h1>Early signals, without the mystery.</h1>
        <p>
          RepoMeteor is an open-source radar for repositories building genuine
          momentum.
        </p>
      </header>
      <section>
        <h2>Why it exists</h2>
        <p>
          Lifetime stars mostly tell you what was already popular. RepoMeteor
          asks a different question: which useful projects are moving faster now
          than their current size would predict?
        </p>
        <p>
          It periodically stores public aggregate repository statistics,
          computes deterministic features across 6-hour, 24-hour, and 7-day
          windows, then publishes the ranking and its reasoning.
        </p>
      </section>
      <section className="three-up compact">
        <article>
          <span>Public</span>
          <h3>No account</h3>
          <p>Browse, query, subscribe, or self-host without signing in.</p>
        </article>
        <article>
          <span>Private by design</span>
          <h3>No identity tracking</h3>
          <p>
            No stargazer usernames, user profiles, or individual event
            timestamps.
          </p>
        </article>
        <article>
          <span>Auditable</span>
          <h3>No opaque ranking</h3>
          <p>
            Scores, features, confidence, flags, and formula versions stay
            visible.
          </p>
        </article>
      </section>
      <section>
        <h2>Independent and open source</h2>
        <p>
          RepoMeteor is an independent open-source project and is not affiliated
          with or endorsed by GitHub. It does not reproduce GitHub Trending and
          does not scrape Trending pages.
        </p>
        <div className="hero-actions">
          <Link className="button" href="/explore">
            Explore rankings
          </Link>
          <Link className="text-link" href="/methodology">
            Read the methodology →
          </Link>
        </div>
      </section>
    </div>
  );
}
