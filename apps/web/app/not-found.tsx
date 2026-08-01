import Link from "next/link";

export default function NotFound() {
  return (
    <div className="shell page-pad">
      <section className="empty-state">
        <p className="eyebrow">404 · Off trajectory</p>
        <h1>This signal could not be found.</h1>
        <p>The repository may not be tracked yet, or the route has moved.</p>
        <Link className="button" href="/explore">
          Return to the radar
        </Link>
      </section>
    </div>
  );
}
