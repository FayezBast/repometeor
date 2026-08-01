import { chromium } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const baseURL = process.env.WEB_TEST_URL ?? "http://127.0.0.1:3000";
const outputDir = fileURLToPath(new URL("../docs/assets/", import.meta.url));

await mkdir(outputDir, { recursive: true });

const browser = await chromium.launch();
try {
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
    colorScheme: "dark",
    deviceScaleFactor: 1,
    locale: "en-US",
    timezoneId: "UTC",
    reducedMotion: "reduce",
  });

  await page.goto(baseURL, { waitUntil: "networkidle" });
  await page.getByRole("heading", { level: 1 }).waitFor();
  await page
    .getByText(/demo data/i)
    .first()
    .waitFor();
  await page.screenshot({ path: `${outputDir}/dashboard.png`, fullPage: true });

  await page.goto(`${baseURL}/r/orbit-labs/signal-kit`, {
    waitUntil: "networkidle",
  });
  await page.getByRole("heading", { level: 1 }).waitFor();
  await page
    .getByText(/demo data/i)
    .first()
    .waitFor();
  await page.screenshot({
    path: `${outputDir}/repository.png`,
    fullPage: true,
  });
} finally {
  await browser.close();
}

process.stdout.write(`Saved deterministic demo screenshots to ${outputDir}\n`);
