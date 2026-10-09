"use client";

// Replaces the root layout when it crashes, so no global CSS or fonts load:
// styles are inline, in the app's palette, following the OS theme.
export default function GlobalError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return (
    <html lang="en">
      <body>
        <title>Something went wrong — Algebra</title>
        <style>{`
          :root { --bg:#f7f8fc; --fg:#0b1020; --muted:#5b6478; --primary:#4f46e5; --on-primary:#eef0ff; }
          @media (prefers-color-scheme: dark) { :root { --bg:#0b0f1a; --fg:#e8eaf3; --muted:#98a1b8; --primary:#818cf8; --on-primary:#1d2142; } }
          body { margin:0; min-height:100vh; display:grid; place-items:center; background:var(--bg); color:var(--fg);
                 font-family: ui-sans-serif, system-ui, sans-serif; }
          main { max-width: 26rem; padding: 1.5rem; }
          h1 { font-size: 1.5rem; margin: 0 0 .5rem; letter-spacing: -.01em; }
          p { color: var(--muted); line-height: 1.6; margin: 0; }
          code { font-size: .75rem; }
          button { margin-top: 1.5rem; height: 2.5rem; padding: 0 1rem; border: 0; border-radius: .75rem;
                   background: var(--primary); color: var(--on-primary); font: inherit; font-weight: 500; cursor: pointer; }
        `}</style>
        <main role="alert">
          <h1>Algebra hit a problem</h1>
          <p>Nothing you approved was changed. Try again in a moment.</p>
          {error.digest && (
            <p style={{ marginTop: ".75rem" }}>
              <code>Reference: {error.digest}</code>
            </p>
          )}
          <button type="button" onClick={() => retry()}>
            Try again
          </button>
        </main>
      </body>
    </html>
  );
}
