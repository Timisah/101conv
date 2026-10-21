const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const dist = path.join(__dirname, 'dist');
const read = relative => fs.readFileSync(path.join(dist, relative), 'utf8');
const about = read('about/index.html');
const app = read('index.html');
const robots = read('robots.txt');
const readme = fs.readFileSync(path.join(__dirname, '..', 'README.md'), 'utf8');

assert.match(about, /^<!doctype html>/i);
assert.match(about, /<title>101conv — A private file workspace<\/title>/);
assert.match(about, /<meta name="robots" content="index,follow" \/>/);
assert.match(about, /itemtype="https:\/\/schema\.org\/SoftwareApplication"/);
assert.match(about, /itemprop="applicationCategory" content="MultimediaApplication"/);
assert.match(about, /itemprop="operatingSystem" content="Linux, Docker"/);
assert.match(about, /<a class="skip-link" href="#main-content">Skip to content<\/a>/);
assert.match(about, /id="main-content"/);
assert.match(about, /git clone https:\/\/github\.com\/Timisah\/101conv\.git/);
assert.match(about, /\.env<\/code> private/);
assert.equal((about.match(/<h1\b/g) || []).length, 1);
for (const workspace of ['Convert', 'Compress', 'Build', 'Inspect', 'Automate']) {
  assert.match(about, new RegExp(`<h3>${workspace}<\\/h3>`));
}
for (const catalogArea of ['Images', 'Audio and video', 'Documents']) {
  assert.match(about, new RegExp(`<h3>${catalogArea}<\\/h3>`));
}
assert.match(about, /https:\/\/github\.com\/Timisah\/101conv/);
assert.match(about, /href="\/login"/);
assert.doesNotMatch(about, /<(?:script|img|iframe)\b/i);

assert.match(readme, /^### Verified catalog overview$/m);
for (const catalogArea of ['| Images |', '| Audio/video |', '| Documents |']) {
  assert.match(readme, new RegExp(`^${catalogArea.replace(/[|]/g, '\\|')}`, 'm'));
}
assert.match(readme, /docs\/profiles\/catalog-format\.md/);

assert.match(app, /<meta name="robots" content="noindex,nofollow" \/>/);
assert.match(robots, /^User-agent: \*\nDisallow: \/\nAllow: \/about\nAllow: \/about\/\n?$/);

console.log('Public discovery build contract passed.');
