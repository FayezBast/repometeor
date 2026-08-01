import type { MetadataRoute } from "next";

export default function sitemap(): MetadataRoute.Sitemap {
  const base = process.env.WEB_PUBLIC_URL ?? "http://localhost:3000";
  return [
    "",
    "/explore",
    "/compare",
    "/methodology",
    "/docs/api",
    "/about",
    "/submit",
  ].map((path) => ({
    url: `${base}${path}`,
    changeFrequency: path === "" || path === "/explore" ? "hourly" : "weekly",
  }));
}
