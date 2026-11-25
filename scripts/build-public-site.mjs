#!/usr/bin/env node
/**
 * Catalog-driven static GitHub Pages site generator and verifier (FR-017).
 *
 * Reads the authoritative catalog at catalog/targets/catalog/index.json and
 * emits a zero-JavaScript, zero-tracker, responsive light/dark static site for
 * truthful human and AI-search discovery. No doorway pages are generated: only
 * substantive workspace guides, one catalog-backed formats reference, and a
 * self-hosting/security guide.
 *
 * Usage:
 *   node scripts/build-public-site.mjs [--out site-dist] [--base-url https://timisah.github.io/101conv]
 *   node scripts/build-public-site.mjs --verify
 */
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, '..');
const CATALOG_DIR = path.join(REPO_ROOT, 'catalog', 'targets');
const REPO_URL = 'https://github.com/Timisah/101conv';

function parseArgs(argv) {
  const opts = {
    out: path.join(REPO_ROOT, 'site-dist'),
    baseUrl: process.env.SITE_BASE_URL || '',
    verify: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--verify') {
      opts.verify = true;
    } else if (arg === '--out' && argv[i + 1]) {
      opts.out = path.resolve(argv[++i]);
    } else if (arg === '--base-url' && argv[i + 1]) {
      opts.baseUrl = argv[++i];
    }
  }
  opts.baseUrl = opts.baseUrl.replace(/\/+$/, '');
  return opts;
}

function compareSemver(a, b) {
  const pa = String(a).split(/[.-]/).map(n => Number(n) || 0);
  const pb = String(b).split(/[.-]/).map(n => Number(n) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const diff = (pa[i] || 0) - (pb[i] || 0);
    if (diff !== 0) return diff;
  }
  return 0;
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function loadCatalogData() {
  const indexPath = path.join(CATALOG_DIR, 'catalog', 'index.json');
  const index = JSON.parse(fs.readFileSync(indexPath, 'utf8'));
  const latestById = new Map();

  for (const entry of index.profiles || []) {
    const prev = latestById.get(entry.id);
    if (!prev || compareSemver(entry.version, prev.version) > 0) {
      latestById.set(entry.id, entry);
    }
  }

  const profiles = [];
  for (const entry of latestById.values()) {
    const manifestPath = path.join(CATALOG_DIR, entry.manifestTarget);
    const raw = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
    const p = raw.profile || raw;
    const sources = new Set();
    const targets = new Set();
    for (const rule of p.rules || []) {
      for (const s of rule.sources || []) sources.add(s.toUpperCase());
      for (const t of rule.targets || []) targets.add(t.toUpperCase());
    }
    profiles.push({
      id: p.id,
      version: p.version,
      name: p.name,
      type: p.type || 'conversion',
      description: p.description || '',
      sources: [...sources].sort(),
      targets: [...targets].sort(),
      engines: (p.engines || []).map(e => e.engineId),
    });
  }
  profiles.sort((a, b) => a.name.localeCompare(b.name));

  return {
    engines: index.engines || [],
    profiles,
  };
}

const SHARED_CSS = `
:root { color-scheme: light; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #1c302b; background: #f5f7f8; }
* { box-sizing: border-box; }
body { margin: 0; line-height: 1.6; }
a { color: inherit; }
:focus-visible { outline: 3px solid #f0a83a; outline-offset: 4px; }
.skip-link { position: absolute; z-index: 2; top: 10px; left: 14px; padding: 8px 12px; border-radius: 7px; color: #fff; background: #1c302b; transform: translateY(-150%); transition: transform .15s ease; }
.skip-link:focus { transform: translateY(0); }
.shell { width: min(1120px, calc(100% - 36px)); margin: 0 auto; }
.topbar { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 16px; padding: 20px 0; border-bottom: 1px solid #e2e9e5; }
.brand { display: inline-flex; align-items: center; gap: 10px; font-weight: 750; font-size: 19px; text-decoration: none; letter-spacing: -.04em; }
.mark { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 10px; color: #fff; background: #24745d; font-size: 13px; letter-spacing: -.08em; }
.nav { display: flex; flex-wrap: wrap; align-items: center; gap: 14px; font-size: 13px; color: #51635c; }
.nav a { text-decoration: none; padding: 4px 6px; border-radius: 6px; }
.nav a:hover, .nav a[aria-current="page"] { color: #24745d; font-weight: 700; }
.button { display: inline-flex; align-items: center; justify-content: center; min-height: 40px; padding: 8px 15px; border: 1px solid #24745d; border-radius: 8px; color: #fff; background: #24745d; font-size: 13px; font-weight: 700; text-decoration: none; }
.button:hover { background: #195640; }
.eyebrow { margin: 0 0 10px; color: #24745d; font-size: 11px; font-weight: 800; letter-spacing: .15em; text-transform: uppercase; }
h1 { margin: 0; font-size: clamp(32px, 5vw, 54px); line-height: 1.06; letter-spacing: -.05em; }
h2 { margin: 0 0 12px; font-size: clamp(22px, 3.2vw, 32px); line-height: 1.16; letter-spacing: -.035em; }
h3 { margin: 0 0 8px; font-size: 17px; }
.lead { max-width: 720px; margin: 16px 0 0; color: #51635c; font-size: 17px; }
.hero { padding: 48px 0 36px; }
.section { padding: 24px 0 44px; }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; }
.card { padding: 20px; border: 1px solid #e2e9e5; border-radius: 13px; background: #fff; }
.card p { margin: 8px 0 0; color: #51635c; font-size: 13px; }
.card a.more { display: inline-block; margin-top: 12px; color: #24745d; font-size: 12px; font-weight: 700; text-decoration: none; }
.card a.more:hover { text-decoration: underline; }
.table-wrap { overflow-x: auto; border: 1px solid #e2e9e5; border-radius: 12px; background: #fff; }
table { width: 100%; border-collapse: collapse; font-size: 13px; }
th, td { text-align: left; padding: 12px 14px; border-bottom: 1px solid #e2e9e5; vertical-align: top; }
th { font-size: 12px; text-transform: uppercase; letter-spacing: .06em; color: #51635c; background: #f8faf9; }
tr:last-child td { border-bottom: 0; }
pre { margin: 12px 0 0; padding: 16px; overflow: auto; border-radius: 10px; color: #e7f0eb; background: #1c302b; font: 500 13px/1.6 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.note { margin-top: 18px; padding: 14px 16px; border-left: 3px solid #24745d; background: #eaf4ee; color: #364b44; font-size: 13px; }
.pill { display: inline-block; padding: 2px 7px; margin: 2px 4px 2px 0; border-radius: 999px; border: 1px solid #d7e2dc; background: #f3f7f5; font-size: 11px; font-weight: 600; }
.footer { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 16px; padding: 22px 0 36px; border-top: 1px solid #e2e9e5; color: #5f706a; font-size: 12px; }
.footer-links { display: flex; flex-wrap: wrap; gap: 16px; }
@media (prefers-color-scheme: dark) {
  :root { color-scheme: dark; color: #e7f0eb; background: #111b18; }
  .topbar, .footer, .card, .table-wrap, th, td { border-color: #304338; }
  .card, .table-wrap { background: #192620; }
  th { background: #1f3028; color: #b2c3b9; }
  .lead, .nav, .card p, .footer { color: #b2c3b9; }
  .pill { background: #21342b; border-color: #365043; color: #e7f0eb; }
  .note { background: #21342b; color: #cfe0d6; }
}
`;

const NAV_ITEMS = [
  { href: '/', label: 'Overview' },
  { href: '/convert/', label: 'Convert' },
  { href: '/compress/', label: 'Compress' },
  { href: '/build/', label: 'Build' },
  { href: '/inspect/', label: 'Inspect' },
  { href: '/automate/', label: 'Automate' },
  { href: '/formats/', label: 'Formats' },
  { href: '/self-host/', label: 'Self-host' },
];

function relLink(route, target, baseUrl) {
  if (baseUrl) {
    return `${baseUrl}${target}`;
  }
  const depth = route === '/' ? 0 : route.replace(/^\/|\/$/g, '').split('/').length;
  const prefix = depth === 0 ? '.' : Array(depth).fill('..').join('/');
  return target === '/' ? `${prefix}/index.html` : `${prefix}${target}index.html`;
}

function renderPage({ route, title, description, schemaType = 'WebPage', body, baseUrl }) {
  const canonical = baseUrl ? `<link rel="canonical" href="${escapeHtml(baseUrl + route)}" />\n    ` : '';
  const navHtml = NAV_ITEMS.map(item => {
    const current = item.href === route ? ' aria-current="page"' : '';
    return `<a href="${escapeHtml(relLink(route, item.href, baseUrl))}"${current}>${escapeHtml(item.label)}</a>`;
  }).join('');

  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="robots" content="index,follow" />
    <meta name="description" content="${escapeHtml(description)}" />
    <meta property="og:type" content="website" />
    <meta property="og:title" content="${escapeHtml(title)}" />
    <meta property="og:description" content="${escapeHtml(description)}" />
    ${canonical}<link rel="alternate" type="text/plain" href="${escapeHtml(baseUrl ? `${baseUrl}/llms.txt` : relLink(route, '/', baseUrl).replace(/index\.html$/, 'llms.txt'))}" title="Project summary for search and assistant tools" />
    <title>${escapeHtml(title)}</title>
    <style>${SHARED_CSS}</style>
  </head>
  <body>
    <div class="shell" itemscope itemtype="https://schema.org/${escapeHtml(schemaType)}">
      <a class="skip-link" href="#main-content">Skip to content</a>
      <header class="topbar">
        <a class="brand" href="${escapeHtml(relLink(route, '/', baseUrl))}"><span class="mark" aria-hidden="true">101</span><span itemprop="name">101conv</span></a>
        <nav class="nav" aria-label="Primary navigation">
          ${navHtml}
          <a class="button" href="${REPO_URL}">GitHub</a>
        </nav>
      </header>
      <main id="main-content">
        ${body}
      </main>
      <footer class="footer">
        <p>101conv is a provisional project name for an open-source, self-hosted file workspace. Zero trackers or third-party scripts.</p>
        <nav class="footer-links" aria-label="Footer navigation">
          <a href="${REPO_URL}">Source code</a>
          <a href="${REPO_URL}/blob/main/docs/PROJECT_STATUS.md">Verified status</a>
          <a href="${REPO_URL}/blob/main/docs/profiles/catalog-format.md">Catalog provenance</a>
        </nav>
      </footer>
    </div>
  </body>
</html>
`;
}

function buildPages(catalog, baseUrl) {
  const conversionProfiles = catalog.profiles.filter(p => p.type === 'conversion');
  const compressionProfiles = catalog.profiles.filter(p => p.type === 'compression');

  const profileRows = list =>
    list
      .map(
        p => `<tr>
          <td><strong>${escapeHtml(p.name)}</strong><br /><code>${escapeHtml(p.id)}</code> v${escapeHtml(p.version)}</td>
          <td>${p.sources.map(f => `<span class="pill">${escapeHtml(f)}</span>`).join('')}</td>
          <td>${p.targets.map(f => `<span class="pill">${escapeHtml(f)}</span>`).join('')}</td>
          <td>${escapeHtml(p.description)}</td>
        </tr>`,
      )
      .join('\n');

  const pages = [
    {
      route: '/',
      title: '101conv — Private, self-hosted file workspace',
      description:
        'Convert, compress, assemble documents, inspect metadata, and automate folders in a self-hosted Docker workspace you control.',
      schemaType: 'SoftwareApplication',
      body: `
        <meta itemprop="applicationCategory" content="MultimediaApplication" />
        <meta itemprop="operatingSystem" content="Linux, Docker" />
        <section class="hero">
          <p class="eyebrow">Open-source self-hosted file workspace</p>
          <h1>Convert, compress, build, inspect, and automate files privately.</h1>
          <p class="lead" itemprop="description">101conv runs on your own Docker or Portainer host with PostgreSQL durable jobs, an isolated non-root Worker, and versioned, verified converter catalogs.</p>
          <p style="margin-top:20px;display:flex;flex-wrap:wrap;gap:12px;">
            <a class="button" href="${escapeHtml(relLink('/', '/self-host/', baseUrl))}">Self-host with Docker</a>
            <a class="button" style="background:transparent;color:#24745d;" href="${escapeHtml(relLink('/', '/formats/', baseUrl))}">Browse verified formats (${catalog.profiles.length} profiles)</a>
          </p>
        </section>
        <section class="section">
          <h2>Five focused workspace experiences</h2>
          <div class="grid">
            <article class="card"><h3>01 · Convert</h3><p>Verified image, audio, video, Office-to-PDF, and PDF-to-image/text conversions through pinned engines.</p><a class="more" href="${escapeHtml(relLink('/', '/convert/', baseUrl))}">Convert guide →</a></article>
            <article class="card"><h3>02 · Compress</h3><p>Quality goals, per-file and combined batch size budgets, independent bitrate/resolution controls, and HDR preservation checks.</p><a class="more" href="${escapeHtml(relLink('/', '/compress/', baseUrl))}">Compress guide →</a></article>
            <article class="card"><h3>03 · Build</h3><p>Non-destructive visual Document Creator with multi-page PDF expansion, native PDF vector/JPEG preservation, and 8 export formats.</p><a class="more" href="${escapeHtml(relLink('/', '/build/', baseUrl))}">Build guide →</a></article>
            <article class="card"><h3>04 · Inspect</h3><p>Ephemeral, bounded metadata scan for images, audio/video streams, HDR signals, and PDF properties without storing uploads.</p><a class="more" href="${escapeHtml(relLink('/', '/inspect/', baseUrl))}">Inspect guide →</a></article>
            <article class="card"><h3>05 · Automate</h3><p>Mounted-folder blueprints, typed block flow, metadata conditions, immutable revisions, and safe non-overwrite publication.</p><a class="more" href="${escapeHtml(relLink('/', '/automate/', baseUrl))}">Automate guide →</a></article>
          </div>
        </section>`,
    },
    {
      route: '/convert/',
      title: 'Convert files privately — 101conv',
      description:
        'Convert images, audio, video, Office documents, and PDFs using versioned, verified engines inside an isolated self-hosted Worker.',
      body: `
        <section class="hero">
          <p class="eyebrow">Workspace 01 · Convert</p>
          <h1>Deterministic file conversion without arbitrary flags.</h1>
          <p class="lead">Choose compatible outputs resolved from installed capabilities. Every job freezes its exact engine version, profile manifest, and validated options before execution.</p>
        </section>
        <section class="section">
          <h2>Verified official conversion profiles (${conversionProfiles.length})</h2>
          <div class="table-wrap"><table><thead><tr><th>Profile</th><th>Sources</th><th>Targets</th><th>Scope &amp; limits</th></tr></thead><tbody>${profileRows(conversionProfiles)}</tbody></table></div>
          <p class="note">An installation only exposes the engines and profiles its administrator chooses to install. See <a href="${REPO_URL}/blob/main/docs/profiles/catalog-format.md">catalog provenance</a>.</p>
        </section>`,
    },
    {
      route: '/compress/',
      title: 'Compress images and video with honest budgets — 101conv',
      description:
        'Set quality goals, optional per-file or combined batch size limits, independent resolution/bitrate controls, and HDR-aware validation.',
      body: `
        <section class="hero">
          <p class="eyebrow">Workspace 02 · Compress</p>
          <h1>Bounded compression with verified output savings.</h1>
          <p class="lead">Keep original pixel dimensions by default, optionally set a per-file or combined batch size limit, and inspect real before/after byte savings.</p>
        </section>
        <section class="section">
          <h2>Verified official compression profiles (${compressionProfiles.length})</h2>
          <div class="table-wrap"><table><thead><tr><th>Profile</th><th>Sources</th><th>Targets</th><th>Scope &amp; limits</th></tr></thead><tbody>${profileRows(compressionProfiles)}</tbody></table></div>
          <p class="note">HDR10+/Dolby Vision dynamic metadata preservation is explicitly rejected until verified per codec; 101conv never silently downgrades HDR to SDR without user intent.</p>
        </section>`,
    },
    {
      route: '/build/',
      title: 'Visual Document Creator (Build) — 101conv',
      description:
        'Assemble images, PDFs, and Office documents into an ordered, non-destructive project and export PDF, CBZ, ZIP, multipage TIFF, DOCX, PPTX, ODT, or ODP.',
      body: `
        <section class="hero">
          <p class="eyebrow">Workspace 03 · Build / Document Creator</p>
          <h1>Assemble pages non-destructively without needless resampling.</h1>
          <p class="lead">Upload images, multi-page PDFs, and supported Office files, reorder or rotate individual pages with drag-and-drop or keyboard controls, and export one coherent document.</p>
        </section>
        <section class="section">
          <h2>Supported export formats and honest fidelity</h2>
          <div class="table-wrap"><table>
            <thead><tr><th>Format</th><th>Fidelity &amp; content model</th></tr></thead>
            <tbody>
              <tr><td><strong>PDF</strong></td><td>Preserves native PDF vector text/streams via object-closure assembly and embeds original JPEG bytes verbatim (zero recompression) plus lossless PNG.</td></tr>
              <tr><td><strong>CBZ &amp; Ordered Images ZIP</strong></td><td>Lossless page packaging with ordered filenames; ideal for image sequences and comic archives.</td></tr>
              <tr><td><strong>Multipage TIFF</strong></td><td>Uncompressed/lossless raster multipage container for archival scanning workflows.</td></tr>
              <tr><td><strong>DOCX, PPTX, ODT, ODP</strong></td><td>Page-image document export (one page per page/slide). Clearly disclosed as non-OCR paginated output, not editable text reconstruction.</td></tr>
            </tbody>
          </table></div>
        </section>`,
    },
    {
      route: '/inspect/',
      title: 'File Inspector & metadata privacy — 101conv',
      description:
        'Run an ephemeral, bounded metadata scan for images, audio/video streams, HDR properties, EXIF/ICC, and PDF properties without storing uploads.',
      body: `
        <section class="hero">
          <p class="eyebrow">Workspace 04 · Inspect</p>
          <h1>Inspect files without retaining uploads.</h1>
          <p class="lead">The Inspector scans an ephemeral copy through bounded parsers (native EXIF/ICC/XMP/IPTC, ffprobe, and PDF inspection) and deletes the temporary file before returning the report.</p>
        </section>
        <section class="section">
          <div class="grid">
            <article class="card"><h3>Ephemeral by design</h3><p>Scanned files are never added to conversion storage and never exposed to other users.</p></article>
            <article class="card"><h3>Explicit metadata removal</h3><p>Inspection never mutates files. Stripping metadata is a separate, explicitly invoked job that produces a new result while keeping the original intact.</p></article>
            <article class="card"><h3>Shared across workspaces</h3><p>The same bounded analysis powers Convert/Compress source hints and metadata-driven Automation conditions.</p></article>
          </div>
        </section>`,
    },
    {
      route: '/automate/',
      title: 'Safe mounted-folder Automations — 101conv',
      description:
        'Automate folder conversions with typed blocks, metadata conditions, stable-file checks, immutable revisions, and non-overwrite publication.',
      body: `
        <section class="hero">
          <p class="eyebrow">Workspace 05 · Automate</p>
          <h1>Repeatable folder workflows with safe publication.</h1>
          <p class="lead">Register host-mounted folders in Core (never directly in the Worker), compose conditions on filename, size, or media metadata (resolution, codec, HDR, audio), and publish results atomically without overwriting originals.</p>
        </section>
        <section class="section">
          <div class="grid">
            <article class="card"><h3>Immutable revisions</h3><p>Every saved automation revision pins its profile version; updates never silently break active rules.</p></article>
            <article class="card"><h3>Bounded polling</h3><p>Checks file stability before queuing, enforces per-scan entry and byte caps, and deduplicates processed files.</p></article>
            <article class="card"><h3>Declarative extensions</h3><p>Versioned extension manifests are compatibility-checked before activation without executing untrusted plugin code.</p></article>
          </div>
        </section>`,
    },
    {
      route: '/formats/',
      title: 'Verified supported formats & engines — 101conv',
      description:
        'Authoritative catalog of verified official engines and conversion/compression profiles shipped in 101conv.',
      body: `
        <section class="hero">
          <p class="eyebrow">Authoritative catalog inventory</p>
          <h1>Verified engines and profiles.</h1>
          <p class="lead">Generated directly from <code>catalog/targets/catalog/index.json</code>. Deployments expose only the engines and profiles an administrator installs.</p>
        </section>
        <section class="section">
          <h2>Pinned engines (${catalog.engines.length})</h2>
          <div class="table-wrap"><table><thead><tr><th>Engine</th><th>ID</th><th>Pinned version</th></tr></thead><tbody>
            ${catalog.engines.map(e => `<tr><td><strong>${escapeHtml(e.name)}</strong></td><td><code>${escapeHtml(e.id)}</code></td><td><code>${escapeHtml(e.version)}</code></td></tr>`).join('\n')}
          </tbody></table></div>
        </section>
        <section class="section">
          <h2>Latest official profiles (${catalog.profiles.length})</h2>
          <div class="table-wrap"><table><thead><tr><th>Profile</th><th>Sources</th><th>Targets</th><th>Description</th></tr></thead><tbody>${profileRows(catalog.profiles)}</tbody></table></div>
        </section>`,
    },
    {
      route: '/self-host/',
      title: 'Self-host 101conv with Docker or Portainer',
      description:
        'Install 101conv on a Linux Docker host or Portainer stack with PostgreSQL durable jobs, non-root Worker isolation, and backup/restore verification.',
      body: `
        <section class="hero">
          <p class="eyebrow">Self-hosting &amp; security</p>
          <h1>Run 101conv on your own hardware.</h1>
          <p class="lead">Deploy with Docker Compose or Portainer Git stacks. Keep database credentials, files, and processing inside your own network.</p>
        </section>
        <section class="section">
          <h2>Docker quick start</h2>
          <pre><code>git clone https://github.com/Timisah/101conv.git
cd 101conv
./scripts/install.sh</code></pre>
          <div class="grid" style="margin-top:20px;">
            <article class="card"><h3>Worker isolation</h3><p>Non-root UID 65532, read-only root filesystem, <code>cap_drop: [ALL]</code>, internal database network, no Docker socket, and no arbitrary CLI arguments.</p></article>
            <article class="card"><h3>Private by default</h3><p>The authenticated SPA is marked <code>noindex,nofollow</code>. Optional isolated public demo mode (<code>compose.demo.yaml</code>) enforces strict caps and an overload kill switch.</p></article>
            <article class="card"><h3>Backup &amp; restore</h3><p>Includes documented stopped-writer backup and isolated restore rehearsal verifying PostgreSQL state and post-restore conversions.</p></article>
          </div>
        </section>`,
    },
  ];

  return pages;
}

function generateSite({ out, baseUrl }) {
  const catalog = loadCatalogData();
  const pages = buildPages(catalog, baseUrl);

  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });

  for (const page of pages) {
    const dir = page.route === '/' ? out : path.join(out, page.route.replace(/^\/|\/$/g, ''));
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, 'index.html'), renderPage({ ...page, baseUrl }), 'utf8');
  }

  const robotsLines = ['User-agent: *', 'Allow: /'];
  if (baseUrl) {
    robotsLines.push(`Sitemap: ${baseUrl}/sitemap.xml`);
  }
  fs.writeFileSync(path.join(out, 'robots.txt'), `${robotsLines.join('\n')}\n`, 'utf8');

  const llms = [
    '# 101conv',
    '',
    '> 101conv is an open-source, private, self-hosted file workspace for converting, compressing, building documents, inspecting metadata, and automating folders. The name is provisional.',
    '',
    '## Pages',
    ...pages.map(p => `- ${p.title}: ${baseUrl ? baseUrl + p.route : p.route}`),
    '',
    '## Verified catalog summary',
    ...catalog.profiles.map(p => `- ${p.name} (${p.id} v${p.version}): ${p.sources.join(', ')} -> ${p.targets.join(', ')}`),
    '',
    '## Repository',
    `- GitHub: ${REPO_URL}`,
    `- Verified status: ${REPO_URL}/blob/main/docs/PROJECT_STATUS.md`,
    '',
  ].join('\n');
  fs.writeFileSync(path.join(out, 'llms.txt'), llms, 'utf8');

  if (baseUrl) {
    const urls = pages
      .map(p => `  <url><loc>${escapeHtml(baseUrl + p.route)}</loc></url>`)
      .join('\n');
    const sitemap = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`;
    fs.writeFileSync(path.join(out, 'sitemap.xml'), sitemap, 'utf8');
  }

  return { catalog, pages };
}

function verifySite() {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), '101conv-site-'));
  const baseUrl = 'https://timisah.github.io/101conv';
  try {
    const { catalog, pages } = generateSite({ out: tmpDir, baseUrl });
    const sitemap = fs.readFileSync(path.join(tmpDir, 'sitemap.xml'), 'utf8');
    const robots = fs.readFileSync(path.join(tmpDir, 'robots.txt'), 'utf8');
    const llms = fs.readFileSync(path.join(tmpDir, 'llms.txt'), 'utf8');

    assert.match(robots, /Sitemap: https:\/\/timisah\.github\.io\/101conv\/sitemap\.xml/);
    assert.doesNotMatch(llms, /ENGINE_ADMIN_TOKEN|POSTGRES_PASSWORD|bearer\s+token/i);

    for (const page of pages) {
      const file = page.route === '/'
        ? path.join(tmpDir, 'index.html')
        : path.join(tmpDir, page.route.replace(/^\/|\/$/g, ''), 'index.html');
      const html = fs.readFileSync(file, 'utf8');
      assert.match(html, /^<!doctype html>/i, `${page.route} missing doctype`);
      assert.equal((html.match(/<h1\b/g) || []).length, 1, `${page.route} must have one <h1>`);
      assert.match(html, /<a class="skip-link" href="#main-content">Skip to content<\/a>/);
      assert.match(html, new RegExp(`<link rel="canonical" href="${baseUrl.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}${page.route}" />`));
      assert.doesNotMatch(html, /<(?:script|iframe)\b/i, `${page.route} must not include scripts or iframes`);
      assert.ok(sitemap.includes(`<loc>${baseUrl}${page.route}</loc>`), `sitemap missing ${page.route}`);
    }

    const formatsHtml = fs.readFileSync(path.join(tmpDir, 'formats', 'index.html'), 'utf8');
    for (const profile of catalog.profiles) {
      assert.ok(formatsHtml.includes(profile.id), `/formats/ missing profile ${profile.id}`);
    }
    console.log(`Static GitHub Pages site verification passed (${pages.length} pages, ${catalog.profiles.length} catalog profiles).`);
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

const opts = parseArgs(process.argv.slice(2));
if (opts.verify) {
  verifySite();
} else {
  const { pages, catalog } = generateSite(opts);
  console.log(`Generated ${pages.length} static pages (${catalog.profiles.length} profiles) into ${opts.out}`);
}
