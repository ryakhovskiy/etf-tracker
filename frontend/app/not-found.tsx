import Link from "next/link";
import { AppHeader } from "../components/app-header";

// Without this, an unknown URL would show Next's bare 404 with no way back (an installed app has no browser back button).
export default function NotFound() {
  return <div className="app-shell">
    <AppHeader />
    <main className="page" id="main">
      <div className="portfolio-empty tile">
        <p className="eyebrow">404</p>
        <h1>Page not found</h1>
        <p className="muted">This page does not exist or has moved.</p>
        <Link href="/" className="button primary">Back to Holdings</Link>
      </div>
    </main>
  </div>;
}
