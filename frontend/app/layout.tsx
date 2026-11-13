import type { Metadata } from "next";
import localFont from "next/font/local";
import { ThemeSync } from "../components/theme-sync";
import { themeInitScript } from "../lib/theme";
import "./globals.css";

// Self-hosted variable fonts (OFL, see app/fonts): no network access at build or run time.
const inter = localFont({ src: "./fonts/Inter-latin-variable.woff2", variable: "--font-inter", weight: "100 900", display: "swap" });
const jetbrainsMono = localFont({ src: "./fonts/JetBrainsMono-latin-variable.woff2", variable: "--font-jetbrains-mono", weight: "100 800", display: "swap" });

export const metadata: Metadata = { title: "Holdings", description: "Your private workspace" };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  // The head script sets html[data-theme] before first paint, so React must not
  // treat the attribute difference as a hydration error.
  return <html lang="en" className={`${inter.variable} ${jetbrainsMono.variable}`} suppressHydrationWarning>
    <head><script dangerouslySetInnerHTML={{ __html: themeInitScript }} /></head>
    <body><ThemeSync />{children}</body>
  </html>;
}
