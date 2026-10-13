# Algebra design system

The direction is editorial minimalism: warm paper, ink black, generous space, and expressive word art. The goal is to make controlled agent spending legible before introducing implementation details. Design feasibility score: 14 (impact 5, fit 5, feasibility 5, performance 4, consistency risk 5).

## Typography and color

Manrope carries headings and interface copy. Instrument Serif adds an italic accent to the landing headlines. IBM Plex Mono distinguishes prices, capabilities, labels and integration examples. All three are self-hosted by Next's font pipeline.

`app/globals.css` contains the live application's semantic color tokens, with explicit light and dark themes. `app/algebra.css` contains the warm-paper landing tokens and dark product preview tokens. Most space follows an 8px rhythm, with hairline dividers and restrained corner radii.

The authenticated console mixes light and dark surfaces like the landing page: a warm white workspace, white summary cards and toolbars, a black sidebar, dark chart panels, black spend-pass credentials and a dark chat composer. Its direction is restrained editorial minimalism (design feasibility score: 14). Manrope and IBM Plex Mono retain continuity with the product. The scoped `console-inverse` palette keeps text, controls and chart marks readable inside dark panels; the capability breakdown and chat transcript stay light. Active navigation uses a gray inset and border; status text carries meaning alongside the neutral palette.

The overview includes native SVG spending bars and request-volume lines, plus horizontal capability bars. A 7/30-day selector and keyboard-focusable chart points reveal daily values. The charts use up to 200 recent API records, grouped by request creation date in local time, and explicitly disclose the limit when reached. Spending is USDC only; request volume includes all loaded requests. A separate, clearly labeled example-data toggle supports showcasing empty accounts. Chart interaction adds no animation or chart-library dependency.

## Brand

The mark is a pair of interlocking gates: an agent and its spending authority. It appears consistently in the landing page, app shell, authentication pages, and SVG icon. Hover brings the gates together. Provider marks use the existing published-logo/favicon component with a monogram fallback. Provider names do not imply an affiliation or endorsement.

## Motion and interaction

The hero uses a short entrance sequence and an SVG routing signal. Section reveals use IntersectionObserver and disconnect after the first reveal. The brand mark, provider cards, links and buttons respond to hover. The dashboard includes focusable chart points, period selection, provider search, budget and per-call sliders, pause/resume, request simulation, and request details. The full preview is at `/demo`.

CSS effects respect `prefers-reduced-motion`. MotionConfig applies the user's motion preference to existing Motion components. Reveals start visible without JavaScript. The mobile menu, FAQ, code tabs, sliders and request details support keyboard use.

## Data boundaries

Landing and `/demo` use explicitly labeled illustrative data. No payment API is called. The authenticated console continues to use real API data, including its budget meters. Routing, sessions, wallet access, pass creation, approvals and payment authority remain in the existing backend flows.

## Page composition

The homepage moves from the product promise to the interactive workspace, an explorable execution flow, three dark capability panels, integration examples, frequently asked questions, and a clear account-creation action. The oversized closing wordmark and interlocking routing diagram are the visual anchors. The live app uses a shared sidebar, breadcrumb header, concise page descriptions and reusable controls.
