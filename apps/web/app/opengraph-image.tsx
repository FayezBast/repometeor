import { ImageResponse } from "next/og";

export const alt =
  "RepoMeteor — Catch rising repositories before they hit orbit";
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

export default function OpenGraphImage() {
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
          "radial-gradient(circle at 78% 28%, #3b1f24 0, #111722 34%, #090c12 72%)",
        fontFamily: "Arial, Helvetica, sans-serif",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 24 }}>
        {/*
          The mark is drawn rather than typed. A decorative glyph such as "✦"
          is absent from the default font, so Satori would try to download one
          at render time — a network call that fails in an offline build or a
          sandboxed CI runner and leaves the character blank. An inline SVG has
          no font dependency and matches public/logo.svg.
        */}
        <div
          style={{
            width: 72,
            height: 72,
            borderRadius: 36,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: "#ff6b32",
          }}
        >
          <svg width="44" height="44" viewBox="0 0 64 64">
            <path
              d="M8 51c15-3 24-11 31-24"
              fill="none"
              stroke="#090c12"
              strokeWidth="7"
              strokeLinecap="round"
            />
            <path
              d="m48 8 3.6 8.1L60 20l-8.4 3.9L48 32l-3.6-8.1L36 20l8.4-3.9L48 8Z"
              fill="#fff3d1"
              stroke="#090c12"
              strokeWidth="3"
              strokeLinejoin="round"
            />
          </svg>
        </div>
        <span style={{ fontSize: 38, fontWeight: 700, letterSpacing: -1 }}>
          RepoMeteor
        </span>
      </div>
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 24,
          maxWidth: 980,
        }}
      >
        <div
          style={{
            color: "#ff9b64",
            fontSize: 24,
            fontWeight: 700,
            letterSpacing: 3,
          }}
        >
          OPEN-SOURCE MOMENTUM RADAR
        </div>
        <div
          style={{
            fontSize: 72,
            lineHeight: 1.02,
            fontWeight: 800,
            letterSpacing: -3,
          }}
        >
          Catch rising repositories before they hit orbit.
        </div>
        <div style={{ color: "#b8c3d2", fontSize: 28 }}>
          Aggregate snapshots · transparent scoring · no individual stargazer
          tracking
        </div>
      </div>
    </div>,
    size,
  );
}
