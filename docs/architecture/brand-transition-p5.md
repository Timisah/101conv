# Brand Identity & Non-Breaking Transition Plan (FR-016)

**Status:** Owner decision pending (`DECISIONS.md` A-010, A-024).  
**Current provisional identifier:** `101conv`.

Per `AGENTS.md` and `DECISIONS.md`, no AI agent may unilaterally select a public brand name or rename repositories, packages, volumes, or API routes. This document provides the structured decision rubric, visual asset specification, and non-breaking technical migration plan so the owner can approve a final identity cleanly without risking existing deployments.

## 1. Selection rubric

The final name must satisfy all five criteria before approval:

1. **Distinctiveness & trademark safety:** No active conflict with existing file-conversion SaaS, open-source media tools, or similarly named converter businesses (the primary reason for retiring `101conv`).
2. **Full-workspace representation:** Reflects **Convert, Compress, Build, Inspect, and Automate** — not merely file-extension swapping.
3. **Domain & GitHub availability:** Matching `.org` or `.dev` (or `.app`/`.io`) domain and clean GitHub organization/repository slug available without squatting risk.
4. **Short, pronounceable, typographically clean:** Readable in a 36 px mobile top bar, CLI binary name, and favicon mark.
5. **Neutral & privacy-forward tone:** Suitable for homelabs, individuals, and small teams.

## 2. Visual asset specification (required upon selection)

Once the owner selects the final name and mark:

- **Icon mark (SVG):** geometric, legible at `16×16` favicon and `36×36` app header, monochrome-capable.
- **Wordmark (SVG):** light-theme (`#1c302b` on `#f5f7f8`) and dark-theme (`#e7f0eb` on `#111b18`) variants.
- **Web & mobile manifest assets:** `favicon.ico`, `favicon.svg`, `apple-touch-icon.png` (180×180), `icon-192.png`, `icon-512.png`, and `og-card.png` (1200×630) for GitHub/Pages sharing.

## 3. Non-breaking technical migration plan

Existing Portainer stacks, PostgreSQL databases, mounted drives, presets, automation revisions, and API clients must survive a brand rollout with **zero manual data migration**:

| Surface | Policy during & after brand transition |
| --- | --- |
| **UI display name, titles, `/about/`, static site, README** | Update immediately in the approved branding PR. |
| **Docker volumes (`postgres-data`, `uploads`, `results`) & `DATA_PATH`/`GO_DATA_PATH`** | **Keep unchanged** in `compose.yaml`. Never rename volume keys or `/var/lib/101conv` paths for cosmetics. |
| **PostgreSQL tables (`conv_*`) & DB defaults (`101conv`, `101conv_worker`)** | **Keep unchanged** so upgrades attach cleanly to existing volumes. |
| **HTTP API routes (`/api/v1/*`) & headers (`X-101conv-Proxy`, `X-101conv-Request`)** | **Keep unchanged** in v1; if new header aliases are introduced, accept both old and new indefinitely in v1. |
| **Session cookies (`101conv_admin`, `101conv_guest`)** | **Keep unchanged** (or dual-read legacy cookie names during a deprecation window) so active sessions do not break silently. |
| **Go module path & binary name** | Retain `github.com/timisah/101conv` until a deliberate major release; GitHub automatically redirects repo URLs when renamed. |
| **Catalog profile & engine IDs** | **Never rename** existing published IDs (`ffmpeg-linux-amd64`, etc.) or official catalog URLs; historical releases remain immutable. |
| **GitHub Pages & old URLs** | Publish canonical tags and `sitemap.xml` under the chosen domain via `.github/workflows/pages.yml` (`base_url` input) and keep GitHub repository redirects active. |

## 4. Owner sign-off checklist

- [ ] Candidate names checked against trademark databases (USPTO/EUIPO), GitHub org/repo availability, and domain registrars.
- [ ] Final brand name and SVG logo/favicon assets approved by project owner.
- [ ] `DECISIONS.md` A-010 and A-024 updated from `Open` to `Accepted` with the approved name.
- [ ] Dedicated non-breaking UI/docs branding PR opened and verified against existing restore rehearsal and browser smoke tests.
