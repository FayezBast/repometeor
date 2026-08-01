import { SubmitForm } from "@/components/SubmitForm";

export const metadata = { title: "Track a repository" };

export default function SubmitPage() {
  return (
    <div className="shell page-pad narrow-page">
      <header className="page-heading">
        <p className="eyebrow">Add a signal</p>
        <h1>Track a public repository.</h1>
        <p>
          Submit a GitHub repository and RepoMeteor will queue its first
          aggregate snapshot.
        </p>
      </header>
      <SubmitForm />
      <section className="timeline">
        <article>
          <span>01</span>
          <div>
            <h2>Validate</h2>
            <p>
              The API normalizes the repository name and checks whether it is
              already tracked.
            </p>
          </div>
        </article>
        <article>
          <span>02</span>
          <div>
            <h2>Collect</h2>
            <p>
              A high-priority worker job captures public aggregate repository
              metadata.
            </p>
          </div>
        </article>
        <article>
          <span>03</span>
          <div>
            <h2>Build history</h2>
            <p>
              Confidence improves as scheduled snapshots cover more of each
              ranking window.
            </p>
          </div>
        </article>
      </section>
      <div className="notice">
        <strong>History starts now</strong>
        <p>
          GitHub does not provide the aggregate historical series used here.
          RepoMeteor cannot reconstruct unavailable activity from before the
          first snapshot.
        </p>
      </div>
    </div>
  );
}
