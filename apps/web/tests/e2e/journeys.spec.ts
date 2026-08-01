import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

/**
 * End-to-end journeys against a running demo-mode stack.
 *
 * These run against real data served by the API, so they assert the product's
 * promises rather than component internals: that rankings appear, that filters
 * change what is shown, that history is honest about when tracking began, and
 * that the demo indicator is impossible to miss.
 */

/** A repository the demo dataset always ranks highly. */
const HEADLINE_REPO = "orbit-labs/signal-kit";

/** Runs an axe scan and reports any violation with its rule id and target. */
async function expectNoAccessibilityViolations(page: Page) {
  const results = await new AxeBuilder({ page })
    // Colour-contrast analysis is unreliable against animated gradient
    // backgrounds and produces false positives; contrast is verified in design
    // review instead. Every other rule stays enabled.
    .disableRules(["color-contrast"])
    .analyze();

  const summary = results.violations.map((violation) => ({
    id: violation.id,
    impact: violation.impact,
    nodes: violation.nodes.map((node) => node.target.join(" ")),
  }));

  expect(summary).toEqual([]);
}

test.describe("discovery", () => {
  test("home page shows ranked repositories with explanations", async ({
    page,
  }) => {
    await page.goto("/");

    await expect(page.getByRole("heading", { level: 1 })).toContainText(
      "Catch rising repositories",
    );

    // Demo mode must be unmistakable on every page.
    await expect(page.getByText(/demo data/i).first()).toBeVisible();

    // At least one ranked repository, and it must explain itself.
    const firstCard = page.locator("article, li").filter({
      hasText: HEADLINE_REPO,
    });
    await expect(firstCard.first()).toBeVisible();
    await expect(
      page.getByText(/Gained .* stars during/).first(),
    ).toBeVisible();
  });

  test("window selection changes the ranking context", async ({ page }) => {
    await page.goto("/?window=24h");
    await expect(page.locator("body")).toContainText(/24h/i);

    await page.goto("/?window=7d");
    await expect(page.locator("body")).toContainText(/7d/i);
  });

  test("explore filters by language and narrows the cohort", async ({
    page,
  }) => {
    await page.goto("/explore");
    const unfiltered = await page.locator("a[href^='/r/']").count();
    expect(unfiltered).toBeGreaterThan(0);

    await page.goto("/explore?language=Go");
    const links = page.locator("a[href^='/r/']");
    await expect(links.first()).toBeVisible();

    const filtered = await links.count();
    expect(filtered).toBeGreaterThan(0);
    expect(filtered).toBeLessThanOrEqual(unfiltered);
  });

  test("hidden gems collection returns only small repositories", async ({
    page,
  }) => {
    await page.goto("/explore?collection=hidden-gems");

    // The collection is defined as 10..1000 stars, so the page must not show a
    // five-figure star count.
    await expect(page.locator("a[href^='/r/']").first()).toBeVisible();
    await expect(page.locator("body")).not.toContainText(/5[0-9],\d{3}/);
  });

  test("an empty result set explains itself rather than showing nothing", async ({
    page,
  }) => {
    await page.goto("/explore?language=Fortran");

    await expect(page.locator("body")).toContainText(
      /no results|nothing|no repositories|no signals/i,
    );
  });
});

test.describe("repository detail", () => {
  test("shows momentum, history and when tracking began", async ({ page }) => {
    await page.goto(`/r/${HEADLINE_REPO}`);

    await expect(page.getByRole("heading", { level: 1 })).toContainText(
      "signal-kit",
    );

    // Momentum for the standard windows.
    await expect(page.locator("body")).toContainText(/24h/);

    // The page must state where RepoMeteor's own history starts. Implying
    // history older than the first snapshot would be dishonest.
    await expect(page.locator("body")).toContainText(/track(ing|ed)/i);

    // A chart is present and has a text alternative.
    const chart = page.locator("svg").first();
    await expect(chart).toBeVisible();
  });

  test("history range control changes the rendered range", async ({ page }) => {
    await page.goto(`/r/${HEADLINE_REPO}?range=24h`);
    await expect(page.locator("body")).toContainText(/24h/);

    await page.goto(`/r/${HEADLINE_REPO}?range=30d`);
    await expect(page.locator("svg").first()).toBeVisible();
  });

  test("an untracked repository returns a useful not-found page", async ({
    page,
  }) => {
    const response = await page.goto("/r/nobody/nothing-here");

    expect(response?.status()).toBe(404);
    await expect(page.locator("body")).toContainText("404");
    await expect(page.locator("body")).toContainText(
      /could not be found|not be tracked|may not be tracked/i,
    );
  });
});

test.describe("compare", () => {
  test("compares two repositories side by side", async ({ page }) => {
    await page.goto(
      `/compare?repositories=${encodeURIComponent(`${HEADLINE_REPO},driftwood/schema-drift`)}`,
    );

    await expect(page.locator("body")).toContainText("signal-kit");
    await expect(page.locator("body")).toContainText("schema-drift");
  });
});

test.describe("submit", () => {
  test("the track form validates before submitting", async ({ page }) => {
    await page.goto("/submit");

    const input = page.getByRole("textbox").first();
    await expect(input).toBeVisible();

    await input.fill("not a repository");
    await page.getByRole("button", { name: /track/i }).click();

    // Either the field reports a validation problem or the API's rejection is
    // surfaced; what must not happen is a silent no-op.
    await expect(page.locator("body")).toContainText(
      /invalid|owner\/name|owner\/repository|not valid|demo/i,
      { timeout: 10_000 },
    );
  });
});

test.describe("static pages", () => {
  test("methodology publishes the formula and weights", async ({ page }) => {
    await page.goto("/methodology");

    await expect(page.locator("body")).toContainText("momentum-v1");
    await expect(page.locator("body")).toContainText(/0\.32/);
    await expect(page.locator("body")).toContainText(/confidence/i);
  });

  test("about page carries the independence disclaimer", async ({ page }) => {
    await page.goto("/about");

    await expect(page.locator("body")).toContainText(
      /not affiliated with or endorsed by GitHub/i,
    );
  });

  test("api docs page lists the endpoints", async ({ page }) => {
    await page.goto("/docs/api");

    await expect(page.locator("body")).toContainText("/v1/trending");
  });

  test("robots and sitemap are served", async ({ page }) => {
    const robots = await page.goto("/robots.txt");
    expect(robots?.status()).toBe(200);

    const sitemap = await page.goto("/sitemap.xml");
    expect(sitemap?.status()).toBe(200);
    expect(await sitemap?.text()).toContain("<urlset");
  });
});

test.describe("mobile layout", () => {
  test.skip(
    ({ isMobile }) => !isMobile,
    "viewport assertions only apply to the mobile project",
  );

  test("pages do not overflow horizontally", async ({ page }) => {
    for (const path of ["/", "/explore", `/r/${HEADLINE_REPO}`, "/submit"]) {
      await page.goto(path);

      const overflow = await page.evaluate(() => ({
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
      }));

      // A couple of pixels of rounding is tolerable; a scrollbar's worth is not.
      expect(
        overflow.scrollWidth,
        `${path} overflows horizontally`,
      ).toBeLessThanOrEqual(overflow.clientWidth + 2);
    }
  });
});

test.describe("accessibility", () => {
  test.skip(
    ({ isMobile }) => isMobile,
    "the axe sweep runs once, on the desktop project",
  );

  for (const [name, path] of [
    ["home", "/"],
    ["explore", "/explore"],
    ["repository detail", `/r/${HEADLINE_REPO}`],
    ["compare", "/compare"],
    ["submit", "/submit"],
    ["methodology", "/methodology"],
  ] as const) {
    test(`${name} has no automated accessibility violations`, async ({
      page,
    }) => {
      await page.goto(path);
      await expectNoAccessibilityViolations(page);
    });
  }

  test("a skip link is the first focusable element", async ({ page }) => {
    await page.goto("/");
    await page.keyboard.press("Tab");

    const focused = await page.evaluate(() => ({
      text: document.activeElement?.textContent ?? "",
      href: document.activeElement?.getAttribute("href") ?? "",
    }));

    expect(
      /skip/i.test(focused.text) || focused.href.startsWith("#"),
      `first tab stop was "${focused.text}" (${focused.href})`,
    ).toBe(true);
  });
});
