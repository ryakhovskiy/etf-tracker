import type { Metadata, Viewport } from "next";
import localFont from "next/font/local";
import { PwaSync } from "../components/pwa-sync";
import { ThemeSync } from "../components/theme-sync";
import { themeInitScript } from "../lib/theme";
import "./globals.css";

// Self-hosted variable fonts (OFL, see app/fonts): no network access at build or run time.
const inter = localFont({ src: "./fonts/Inter-latin-variable.woff2", variable: "--font-inter", weight: "100 900", display: "swap" });
const jetbrainsMono = localFont({ src: "./fonts/JetBrainsMono-latin-variable.woff2", variable: "--font-jetbrains-mono", weight: "100 800", display: "swap" });

export const metadata: Metadata = {
  title: "Holdings",
  description: "Your private workspace",
  applicationName: "Holdings",
  icons: {
    icon: [{ url: "/favicon.svg", type: "image/svg+xml" }, { url: "/favicon-32.png", sizes: "32x32", type: "image/png" }],
    apple: [{ url: "/icons/apple-touch-icon.png", sizes: "180x180", type: "image/png" }],
  },
  // iOS home-screen app: opens standalone with this title; the default bar style keeps the clock readable in every theme.
  appleWebApp: { capable: true, title: "Holdings", statusBarStyle: "default" },
  // Older iOS reads the legacy tag (Next only emits the generic mobile-web-app-capable).
  other: { "apple-mobile-web-app-capable": "yes" },
};

// viewport-fit=cover lets standalone content reach the screen edges; the header and tab bar pad with env(safe-area-inset-*).
export const viewport: Viewport = { width: "device-width", initialScale: 1, viewportFit: "cover" };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  // The head script sets html[data-theme] before first paint, so React must not
  // treat the attribute difference as a hydration error.
  return <html lang="en" className={`${inter.variable} ${jetbrainsMono.variable}`} suppressHydrationWarning>
    <head>
      {/* <meta name="theme-color"> is created and kept in step by the head script and applyTheme (lib/theme.ts), not by React: a React-rendered copy would be duplicated on hydration once the script changed its colour. */}
      <script dangerouslySetInnerHTML={{ __html: themeInitScript }} />
    </head>
    <body><ThemeSync /><PwaSync />{children}</body>
  </html>;
}
