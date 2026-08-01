import { Chalk } from "chalk";
import Table from "cli-table3";
import type { Comparison, Detail, Track, Trending } from "./types.js";

const number = new Intl.NumberFormat("en");
const plus = (value: number) =>
  `${value >= 0 ? "+" : ""}${number.format(value)}`;

export function formatTrending(data: Trending, color: boolean) {
  const chalk = new Chalk({ level: color ? 1 : 0 });
  const table = new Table({
    head: [
      "#",
      "Repository",
      "Stars",
      `+${data.meta.window}`,
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
      chalk.bold(item.repository.fullName),
      number.format(item.repository.stars),
      chalk.green(plus(item.momentum.starDelta)),
      chalk.yellow(item.momentum.score.toFixed(1)),
      item.momentum.confidenceLabel,
    ]);
  }
  return `${chalk.bold(`RepoMeteor · ${data.meta.collection} · ${data.meta.window}`)}\n${chalk.dim(`Updated ${data.meta.generatedAt}${data.meta.demoMode ? " · DEMO DATA" : ""}`)}\n\n${table.toString()}`;
}

export function formatDetail(data: Detail, color: boolean) {
  const chalk = new Chalk({ level: color ? 1 : 0 });
  const momentum = data.momentum["24h"] ?? Object.values(data.momentum)[0];
  const rows = [
    ["Stars", number.format(data.repository.stars)],
    ["Forks", number.format(data.repository.forks)],
    ["Language", data.repository.language ?? "Not specified"],
    ["Tracking", `${data.tracking.state} · ${data.tracking.tier}`],
  ];
  if (momentum)
    rows.unshift([
      "Momentum",
      `${chalk.yellow(momentum.score.toFixed(1))} · ${plus(momentum.starDelta)} ${momentum.window} · ${momentum.confidenceLabel}`,
    ]);
  return `${chalk.bold(data.repository.fullName)}\n${data.repository.description}\n\n${rows.map(([key, value]) => `${chalk.dim(key.padEnd(12))} ${value}`).join("\n")}${momentum?.reasons.length ? `\n\n${chalk.bold("Why it is rising")}\n${momentum.reasons.map((reason) => `• ${reason}`).join("\n")}` : ""}`;
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
      item.repository.fullName,
      number.format(item.repository.stars),
      plus(item.summary.absoluteGrowth),
      `${(item.summary.relativeGrowth * 100).toFixed(1)}%`,
      item.momentum?.score.toFixed(1) ?? "—",
    ]);
  return `${chalk.bold(`RepoMeteor comparison · ${data.meta.window}`)}\n\n${table.toString()}`;
}

export function formatTrack(data: Track, color: boolean) {
  const chalk = new Chalk({ level: color ? 1 : 0 });
  return `${chalk.green("✓")} ${chalk.bold(data.repository)}\n${data.message}${data.webUrl ? `\n${data.webUrl}` : ""}`;
}
