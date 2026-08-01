import Image from "next/image";
import Link from "next/link";

const nav = [
  ["Explore", "/explore"],
  ["Compare", "/compare"],
  ["Methodology", "/methodology"],
  ["API", "/docs/api"],
];

export function SiteHeader() {
  const demoMode = process.env.NEXT_PUBLIC_DEMO_MODE !== "false";

  return (
    <>
      {demoMode ? (
        <div className="demo-bar" role="status">
          <span>Demo data</span>
          <p>Fictional repositories · no GitHub requests</p>
        </div>
      ) : null}
      <header className="site-header">
        <div className="shell header-inner">
          <Link className="brand" href="/" aria-label="RepoMeteor home">
            <Image src="/logo.svg" alt="" width={34} height={34} priority />
            <span>RepoMeteor</span>
          </Link>
          <nav aria-label="Primary navigation">
            {nav.map(([label, href]) => (
              <Link href={href} key={href}>
                {label}
              </Link>
            ))}
          </nav>
          <Link className="button button-small" href="/submit">
            Track a repo
          </Link>
        </div>
      </header>
    </>
  );
}
