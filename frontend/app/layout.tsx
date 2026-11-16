import type { Metadata, Viewport } from "next";
import localFont from "next/font/local";
import { ThemeSync } from "../components/theme-sync";
import { THEME_COLORS } from "../lib/pwa-colors";
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
};

// viewport-fit=cover lets standalone content reach the screen edges; the header and tab bar pad with env(safe-area-inset-*).
export const viewport: Viewport = { width: "device-width", initialScale: 1, viewportFit: "cover" };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  // The head script sets html[data-theme] before first paint, so React must not
  // treat the attribute difference as a hydration error.
  return <html lang="en" className={`${inter.variable} ${jetbrainsMono.variable}`} suppressHydrationWarning>
    <head>
      {/* The head script and ThemeSync keep this in step with the active theme (see lib/theme.ts). */}
      <meta name="theme-color" content={THEME_COLORS.light} suppressHydrationWarning />
      <script dangerouslySetInnerHTML={{ __html: themeInitScript }} />
    </head>
    <body><ThemeSync />{children}</body>
  </html>;
}
