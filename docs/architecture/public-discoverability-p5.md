# Public discoverability (P5 slice)

The P5 discoverability slice adds a static `/about/` page to the Web image.
It is deliberately separate from the authenticated React workspace so crawlers
can read accurate project information without receiving a session, guest
credential, file-processing endpoint, or management surface.

## Public contract

- `/about/` is static HTML copied from `web/public/about/` during the Vite build.
- `/about/` describes Convert, Compress, Build, Inspect, and Automate without
  inventing a supported format matrix or promising universal fidelity.
- `/about/` includes a compact summary of the currently verified image,
  media, and document catalog paths, with links to the authoritative repository
  documentation and a deployment/profile availability caveat.
- `/about/` includes the repository's Docker quick-start command and secret
  handling reminders; it does not collect credentials or start a deployment.
- `/llms.txt` is a concise, public, machine-readable project index for crawler
  and assistant discovery. It contains only repository documentation links,
  verified capability summaries, and explicit limitations.
- `/login` and the GitHub repository are the only calls to action.
- `web/public/robots.txt` allows only the public information page and blocks
  the application/API paths from indexing, while explicitly allowing the
  public page and `/llms.txt`.
- `web/index.html` is marked `noindex,nofollow`; the SPA shell is an
  authenticated workspace, not a search landing page.

The page uses semantic Schema.org microdata and responsive, dependency-free
CSS. It has no external analytics, remote fonts, third-party scripts, or
uploaded-file flow. The existing Nginx security headers therefore continue to
apply.

The repository README carries the same verified catalog scope in a compact
matrix, so the GitHub landing path and the deployed `/about/` page do not make
different capability claims. It includes a generated screenshot of the public
page so the GitHub landing path is useful before a deployment is available.

`web/public-smoke.cjs` runs after the production build in the frontend CI job.
It checks that the public page is copied to `dist/about/`, keeps the
authenticated SPA shell no-indexed, retains the restricted robots policy, and
still exposes the five workspace descriptions, quick-start command, Schema.org
item, GitHub link, and sign-in route without introducing remote assets. It also
checks that the README retains the matching catalog matrix and screenshot, and
that `llms.txt` remains credential-free. The page has a keyboard skip link and
visible focus styling for the public navigation.

## Deliberate limits

The project name, logo, canonical domain, and public demo remain unresolved.
The page does not choose a new identity, add a canonical URL that could be
wrong for a self-hosted deployment, create a sitemap before a domain exists,
or enable anonymous processing. A future owner-approved brand/domain pass can
add canonical and sitemap metadata to a separately published static site.
