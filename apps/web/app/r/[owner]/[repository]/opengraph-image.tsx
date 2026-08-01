import { ImageResponse } from "next/og";
import { api } from "@/lib/api";

export const alt = "Repository momentum on RepoMeteor";
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

type RouteParams = Promise<{ owner: string; repository: string }>;

export default async function RepositoryOpenGraphImage({
  params,
}: {
  params: RouteParams;
}) {
  const { owner, repository } = await params;

  let fullName = `${owner}/${repository}`;
  let score: string | undefined;
  let delta: string | undefined;
  let confidence: string | undefined;

  try {
    const detail = await api.detail(owner, repository);
    const momentum =
      detail.momentum["24h"] ?? Object.values(detail.momentum)[0];
    fullName = detail.repository.fullName;
    if (momentum) {
      score = momentum.score.toFixed(1);
      delta = `${momentum.starDelta >= 0 ? "+" : ""}${momentum.starDelta} stars / ${momentum.window}`;
      confidence = `${momentum.confidenceLabel} confidence`;
    }
  } catch {
    // A share card should still render while the API is unavailable.
  }

  return new ImageResponse(
    <div
      style={{
        width: "100%",
        height: "100%",
        display: "flex",
        flexDirection: "column",
        justifyContent: "space-between",
        padding: "72px 84px",
        color: "#f6f7f9",
        background:
          "radial-gradient(circle at 82% 24%, #442520 0, #111722 32%, #090c12 72%)",
        fontFamily: "Arial, Helvetica, sans-serif",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <span style={{ fontSize: 34, fontWeight: 700 }}>RepoMeteor</span>
        <span
          style={{
            color: "#ff9b64",
            fontSize: 22,
            fontWeight: 700,
            letterSpacing: 2,
          }}
        >
          REPOSITORY SIGNAL
        </span>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 26 }}>
        <div style={{ fontSize: 62, fontWeight: 800, letterSpacing: -2 }}>
          {fullName}
        </div>
        {score ? (
          <div style={{ display: "flex", alignItems: "flex-end", gap: 34 }}>
            <span
              style={{
                color: "#ff8752",
                fontSize: 132,
                lineHeight: 0.85,
                fontWeight: 800,
              }}
            >
              {score}
            </span>
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: 10,
                paddingBottom: 6,
              }}
            >
              <span style={{ fontSize: 30 }}>RepoMeteor momentum</span>
              <span style={{ color: "#9faec1", fontSize: 23 }}>
                {delta} · {confidence}
              </span>
            </div>
          </div>
        ) : (
          <div style={{ color: "#9faec1", fontSize: 32 }}>
            Transparent momentum from aggregate snapshots
          </div>
        )}
      </div>
      <div style={{ color: "#9faec1", fontSize: 22 }}>
        Early signals, published methodology, no individual stargazer identities
      </div>
    </div>,
    size,
  );
}
