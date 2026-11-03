import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = { title: "Holdings", description: "Your private workspace" };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="en"><body>{children}</body></html>;
}
