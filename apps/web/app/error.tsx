"use client";

export default function GlobalError({
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <div className="shell page-pad">
      <section className="empty-state" role="alert">
        <p className="eyebrow">Unexpected turbulence</p>
        <h1>This page could not finish loading.</h1>
        <p>No data was substituted or hidden. Try the request again.</p>
        <button className="button" onClick={reset}>
          Try again
        </button>
      </section>
    </div>
  );
}
