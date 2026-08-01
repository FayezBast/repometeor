import { Chalk } from "chalk";
import Table from "cli-table3";
import type { Comparison, Detail, Track, Trending } from "./types.js";

const number = new Intl.NumberFormat("en");
const plus = (value: number) =>
  `${value >= 0 ? "+" : ""}${number.format(value)}`;

// Tab, line feed, vertical tab, form feed and carriage return. These collapse
// to a single space rather than being dropped, so the words on either side stay
// separated.
function isSpacing(code: number) {
  return code >= 0x09 && code <= 0x0d;
}

// Code points a terminal acts on instead of printing. Written as ranges rather
// than as a character class so that the source itself stays free of the very
// characters it is guarding against.
function isUnsafe(code: number) {
  return (
    // C0 controls, which include ESC — the lead byte of every escape sequence.
    code <= 0x1f ||
    // DEL, and the C1 controls. U+009B is a single-character CSI that some
    // terminals honour exactly as they honour "ESC [".
    (code >= 0x7f && code <= 0x9f) ||
    // Zero-width characters, which hide content in plain sight.
    (code >= 0x200b && code <= 0x200d) ||
    // Bidirectional overrides and isolates: the "Trojan Source" trick, where
    // displayed order differs from stored order.
    (code >= 0x202a && code <= 0x202e) ||
    (code >= 0x2066 && code <= 0x2069) ||
    // Byte-order mark.
    code === 0xfeff
  );
}

/**
 * Renders a string supplied by the API as inert text.
 *
 * Every value here is printed straight to a terminal, and these values
 * originate with whoever owns the repository being described. Left alone, an
 * escape sequence in a description can erase the lines around it and repaint
 * them — enough to forge a convincing instruction to run a command. The API
 * strips these on ingest, but `--api-url` points this CLI at an arbitrary
 * server, so the terminal boundary is enforced here rather than assumed
 * upstream.
 */
export function text(value: string) {
  let out = "";
  for (const character of value) {
    const code = character.codePointAt(0) ?? 0;
    if (isSpacing(code)) {
      if (!out.endsWith(" ")) out += " ";
      continue;
    }
    if (isUnsafe(code)) continue;
    out += character;
  }
  return out.trim();
}

export function formatTrending(data: Trending, color: boolean) {
  const chalk = new Chalk({ level: color ? 1 : 0 });
  const table = new Table({
    head: [
      "#",
      "Repository",
      "Stars",
      `+${text(data.meta.window)}`,
      "Score",
      "Confidence",
    ].map((v) => chalk.dim(v)),
    colAligns: ["right", "left", "right", "right", "right", "left"],
    style: { head: [], border: [], compact: true },
    chars: {
      top: "",
      "top-mid": "",
      "top-left": "",
      "top-right": "",
      bottom: "",
      "bottom-mid": "",
      "bottom-left": "",
      "bottom-right": "",
      left: "",
      "left-mid": "",
      mid: "",
      "mid-mid": "",
      right: "",
      "right-mid": "",
      middle: "  ",
    },
  });
  for (const item of data.data) {
    table.push([
      item.momentum.rank ?? "—",
      chalk.bold(text(item.repository.fullName)),
      number.format(item.repository.stars),
      chalk.green(plus(item.momentum.starDelta)),
      chalk.yellow(item.momentum.score.toFixed(1)),
      text(item.momentum.confidenceLabel),
    ]);
  }
  return `${chalk.bold(`RepoMeteor · ${text(data.meta.collection)} · ${text(data.meta.window)}`)}\n${chalk.dim(`Updated ${text(data.meta.generatedAt)}${data.meta.demoMode ? " · DEMO DATA" : ""}`)}\n\n${table.toString()}`;
}

export function formatDetail(data: Detail, color: boolean) {
  const chalk = new Chalk({ level: color ? 1 : 0 });
  const momentum = data.momentum["24h"] ?? Object.values(data.momentum)[0];
  const language = data.repository.language
    ? text(data.repository.language)
    : "";
  const rows = [
    ["Stars", number.format(data.repository.stars)],
    ["Forks", number.format(data.repository.forks)],
    ["Language", language || "Not specified"],
    ["Tracking", `${text(data.tracking.state)} · ${text(data.tracking.tier)}`],
  ];
  if (momentum)
    rows.unshift([
      "Momentum",
      `${chalk.yellow(momentum.score.toFixed(1))} · ${plus(momentum.starDelta)} ${text(momentum.window)} · ${text(momentum.confidenceLabel)}`,
    ]);
  return `${chalk.bold(text(data.repository.fullName))}\n${text(data.repository.description)}\n\n${rows.map(([key, value]) => `${chalk.dim(key.padEnd(12))} ${value}`).join("\n")}${momentum?.reasons.length ? `\n\n${chalk.bold("Why it is rising")}\n${momentum.reasons.map((reason) => `• ${text(reason)}`).join("\n")}` : ""}`;
}

export function formatComparison(data: Comparison, color: boolean) {
  const chalk = new Chalk({ level: color ? 1 : 0 });
  const table = new Table({
    head: ["Repository", "Stars", "Growth", "Relative", "Score"].map((v) =>
      chalk.dim(v),
    ),
  });
  for (const item of data.data)
    table.push([
      text(item.repository.fullName),
      number.format(item.repository.stars),
      plus(item.summary.absoluteGrowth),
      `${(item.summary.relativeGrowth * 100).toFixed(1)}%`,
      item.momentum?.score.toFixed(1) ?? "—",
    ]);
  return `${chalk.bold(`RepoMeteor comparison · ${text(data.meta.window)}`)}\n\n${table.toString()}`;
}

export function formatTrack(data: Track, color: boolean) {
  const chalk = new Chalk({ level: color ? 1 : 0 });
  return `${chalk.green("✓")} ${chalk.bold(text(data.repository))}\n${text(data.message)}${data.webUrl ? `\n${text(data.webUrl)}` : ""}`;
}
