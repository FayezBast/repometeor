import Link from "next/link";

export function APIUnavailable({ message }: { message?: string }) {
  return (
    <section className="empty-state" role="alert">
      <p className="eyebrow">Signal interrupted</p>
      <h2>The rankings API is not reachable.</h2>
      <p>
        {message ??
          "Start the RepoMeteor API and refresh this page. Live data is never replaced with demo data silently."}
      </p>
      <Link className="text-link" href="/docs/api">
        View API setup →
      </Link>
    </section>
  );
}

export function NoResults() {
  return (
    <section className="empty-state">
      <p className="eyebrow">No matches</p>
      <h2>No repositories cross this filter yet.</h2>
      <p>
        Try a broader language, collection, or star range. New history improves
        coverage over time.
      </p>
    </section>
  );
}
