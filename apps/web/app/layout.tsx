import type { Metadata, Viewport } from "next";
import { SiteFooter } from "@/components/SiteFooter";
import { SiteHeader } from "@/components/SiteHeader";
import "./globals.css";

const webURL = process.env.WEB_PUBLIC_URL ?? "http://localhost:3000";

export const metadata: Metadata = {
  metadataBase: new URL(webURL),
  title: {
    default: "RepoMeteor · Rising open-source repositories",
    template: "%s · RepoMeteor",
  },
  description:
    "Catch rising repositories before they hit orbit with transparent open-source momentum rankings.",
  applicationName: "RepoMeteor",
  icons: { icon: "/logo.svg" },
  openGraph: {
    type: "website",
    siteName: "RepoMeteor",
    title: "RepoMeteor · Catch rising repositories before they hit orbit",
    description:
      "Transparent momentum rankings built from aggregate GitHub repository snapshots.",
  },
  twitter: { card: "summary_large_image" },
};

export const viewport: Viewport = {
  colorScheme: "dark light",
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#090c12" },
    { media: "(prefers-color-scheme: light)", color: "#f5f4ef" },
  ],
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>
        <a className="skip-link" href="#main-content">
          Skip to content
        </a>
        <SiteHeader />
        <main id="main-content">{children}</main>
        <SiteFooter />
      </body>
    </html>
  );
}
