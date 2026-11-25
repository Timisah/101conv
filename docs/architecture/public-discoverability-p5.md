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

## Static GitHub Pages candidate

`scripts/build-public-site.mjs` generates a zero-JavaScript, zero-tracker static site directly from the authoritative `catalog/targets/catalog/index.json` and target profile manifests. It emits substantive pages for each of the five workspaces (`/convert/`, `/compress/`, `/build/`, `/inspect/`, `/automate/`), one catalog-generated `/formats/` inventory, and a `/self-host/` security/deployment guide — never doorway pages.

- When `--base-url` / `SITE_BASE_URL` is provided (such as in `.github/workflows/pages.yml`), every page receives a matching `<link rel="canonical">` and `/sitemap.xml` + `/robots.txt` + `/llms.txt` are generated together.
- When built without a base URL, canonicals and sitemap are omitted so self-hosted or preview outputs never emit wrong domains.
- CI executes `node scripts/build-public-site.mjs --verify` on every push and pull request.

## Deliberate limits

The project name, logo, and custom domain remain owner-controlled decisions (`docs/architecture/brand-transition-p5.md`, `DECISIONS.md` A-010/A-024). The self-hosted `/about/` page never injects a hardcoded canonical URL or enables anonymous processing. An optional isolated public demo stack (`compose.demo.yaml`) is documented separately in `docs/security/public-demo-p5.md` and remains disabled by default (`DEMO_MODE=false`).
