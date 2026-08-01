import Link from "next/link";

export function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="shell footer-grid">
        <div>
          <strong>RepoMeteor</strong>
          <p>Transparent early signals for open-source momentum.</p>
        </div>
        <div className="footer-links">
          <Link href="/about">About</Link>
          <Link href="/methodology">Scoring</Link>
          <Link href="/docs/api">API</Link>
          <a href={process.env.NEXT_PUBLIC_GITHUB_REPO_URL ?? "#"}>Source</a>
        </div>
        <p className="disclaimer">
          RepoMeteor is an independent open-source project and is not affiliated
          with or endorsed by GitHub.
        </p>
      </div>
    </footer>
  );
}
