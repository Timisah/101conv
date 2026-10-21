# Public discoverability (P5 slice)

The P5 discoverability slice adds a static `/about/` page to the Web image.
It is deliberately separate from the authenticated React workspace so crawlers
can read accurate project information without receiving a session, guest
credential, file-processing endpoint, or management surface.

## Public contract

- `/about/` is static HTML copied from `web/public/about/` during the Vite build.
- `/about/` describes Convert, Compress, Build, Inspect, and Automate without
  inventing a supported format matrix or promising universal fidelity.
- `/login` and the GitHub repository are the only calls to action.
- `web/public/robots.txt` allows only the public information page and blocks
  the application/API paths from indexing.
- `web/index.html` is marked `noindex,nofollow`; the SPA shell is an
  authenticated workspace, not a search landing page.

The page uses semantic Schema.org microdata and responsive, dependency-free
CSS. It has no external analytics, remote fonts, third-party scripts, or
uploaded-file flow. The existing Nginx security headers therefore continue to
apply.

## Deliberate limits

The project name, logo, canonical domain, and public demo remain unresolved.
The page does not choose a new identity, add a canonical URL that could be
wrong for a self-hosted deployment, create a sitemap before a domain exists,
or enable anonymous processing. A future owner-approved brand/domain pass can
add canonical and sitemap metadata to a separately published static site.
