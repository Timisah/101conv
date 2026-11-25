import assert from 'node:assert/strict';
import test from 'node:test';
import { maxFileQuotaText, quotaText } from './src/features/admin/quotas.ts';
import {
  ADMIN_SECTIONS,
  MOBILE_ADMIN_ITEMS,
  WORKSPACE_LINKS,
  labelFor,
  sectionFor,
} from './src/features/auth/navigation.ts';
import { readCondition } from './src/features/automations/types.ts';
import {
  automaticProfileLabel,
  ext,
  idempotencyKey,
  supportsMapping,
} from './src/features/converters/types.ts';
import { mergeDataDefaults } from './src/features/useData.ts';
import { categoriesOf, searchableText, uniqueFormats } from './src/features/catalog/catalogData.ts';
import {
  batchCollagePlan,
  fileKindFor,
  isBrowserPreviewableImage,
  previewDescriptorFor,
} from './src/features/previews/filePreview.ts';

test('navigation metadata preserves route labels, sections, and mobile shortcuts', () => {
  assert.deepEqual(
    WORKSPACE_LINKS.map(([, , , shortLabel]) => shortLabel),
    ['Convert', 'Compress', 'Build', 'Inspect', 'Jobs'],
  );
  assert.equal(ADMIN_SECTIONS.length, 2);
  assert.ok(MOBILE_ADMIN_ITEMS.some(([to, , label]) => to === '/marketplace' && label === 'Catalog'));
  assert.equal(sectionFor('/admin'), 'Administration');
  assert.equal(sectionFor('/converters'), 'Converter setup');
  assert.equal(sectionFor('/marketplace'), 'Converter setup');
  assert.equal(sectionFor('/storage'), 'Workspace');
  assert.equal(labelFor('/converters', '?tab=engines'), 'Engines');
  assert.equal(labelFor('/converters', '?tab=compression'), 'Compression profiles');
  assert.equal(labelFor('/converters', ''), 'Conversion profiles');
  assert.equal(labelFor('/marketplace', '?tab=engines'), 'Available engines');
  assert.equal(labelFor('/access'), 'Users & access');
  assert.equal(labelFor('/unknown-route'), 'Convert files');
});

test('converter helpers normalize extensions, match rules, and summarize preferences', () => {
  assert.equal(ext('Archive.Photo.PNG'), 'png');
  assert.equal(ext('no-extension'), '');

  const profile = {
    definition: {
      id: 'img-compress',
      type: 'compression',
      name: 'Image compressor',
      version: '1.2.0',
      engines: [],
      rules: [
        {
          engineId: 'ffmpeg',
          executable: 'ffmpeg',
          sources: ['.PNG', 'jpg'],
          targets: ['webp'],
          arguments: [],
          compressionArguments: ['--q'],
        },
        {
          engineId: 'ffmpeg',
          executable: 'ffmpeg',
          sources: ['bmp'],
          targets: ['png'],
          arguments: [],
        },
      ],
    },
    state: { enabled: true },
  };
  assert.equal(supportsMapping(profile, 'png', 'webp', true), true);
  assert.equal(supportsMapping(profile, 'bmp', 'png', true), false);
  assert.equal(supportsMapping(profile, 'bmp', 'png', false), true);

  const preferences = [
    {
      mode: 'convert',
      sourceFormat: 'png',
      targetFormat: 'webp',
      effectiveProfileId: 'p1',
      effectiveSource: 'personal preference',
      globalAvailable: true,
      userAvailable: true,
      alternatives: [{ id: 'p1', name: 'Fast WebP', version: '1.0.0' }],
    },
    {
      mode: 'convert',
      sourceFormat: 'jpg',
      targetFormat: 'webp',
      effectiveProfileId: 'p2',
      effectiveSource: 'administrator default',
      globalAvailable: true,
      userAvailable: true,
      alternatives: [{ id: 'p2', name: 'JPEG WebP', version: '1.0.0' }],
    },
  ];
  assert.equal(automaticProfileLabel([], 'webp', preferences), 'Automatic · use saved preferences');
  assert.equal(
    automaticProfileLabel(['png'], 'webp', preferences),
    'Automatic · Fast WebP (personal preference)',
  );
  assert.equal(
    automaticProfileLabel(['png', 'jpg'], 'webp', preferences),
    'Automatic · resolve each format mapping',
  );
  assert.equal(automaticProfileLabel(['gif'], 'webp', preferences), 'Automatic · no compatible profile');
  assert.match(idempotencyKey(), /^[0-9a-f-]{32,36}$/i);
});

test('quota formatters describe inherited, unlimited, byte, and count limits', () => {
  assert.equal(maxFileQuotaText({ mode: 'unlimited' }, 1073741824), '1.00 GB instance maximum');
  assert.equal(maxFileQuotaText({ mode: 'inherit' }, 1073741824), '1.00 GB instance maximum');
  assert.equal(maxFileQuotaText({ mode: 'limited', value: 10 * 1024 * 1024 }, 1073741824), '10.0 MB');
  assert.equal(quotaText({ mode: 'unlimited' }, 0), 'Unlimited');
  assert.equal(quotaText({ mode: 'inherit' }, 0), 'Inherited');
  assert.equal(quotaText({ mode: 'limited', value: 25 }, 0), '25');
  assert.equal(quotaText({ mode: 'limited', value: 50 * 1024 * 1024 }, 0), '50.0 MB');
});

test('automation condition reader unpacks nested NOT/AND/OR trees cleanly', () => {
  assert.deepEqual(readCondition(undefined), {
    logic: 'and',
    extensions: '',
    glob: '',
    min: '',
    max: '',
  });
  assert.deepEqual(
    readCondition({
      operator: 'not',
      children: [
        {
          operator: 'and',
          children: [
            { operator: 'extension', value: 'jpg' },
            { operator: 'extension', value: 'png' },
            { operator: 'name_glob', value: 'IMG_*' },
            { operator: 'min_size', bytes: 2 * 1048576 },
            { operator: 'max_size', bytes: 25 * 1048576 },
          ],
        },
      ],
    }),
    {
      logic: 'not',
      extensions: 'jpg, png',
      glob: 'IMG_*',
      min: '2',
      max: '25',
    },
  );
});

test('mergeDataDefaults preserves initial defaults when API responses omit or null top-level keys', () => {
  const initial = { groups: [], roles: [{ id: 'converter' }], count: 0 };
  assert.deepEqual(mergeDataDefaults(initial, {}), initial);
  assert.deepEqual(mergeDataDefaults(initial, { groups: null, roles: undefined, count: 3 }), {
    groups: [],
    roles: [{ id: 'converter' }],
    count: 3,
  });
  assert.deepEqual(mergeDataDefaults(initial, { groups: [{ id: 'g1' }] }), {
    groups: [{ id: 'g1' }],
    roles: [{ id: 'converter' }],
    count: 0,
  });
});

test('catalog helpers index formats/capabilities, infer categories, and deduplicate format chips', () => {
  const entry = {
    repoId: '101conv-official',
    repo: { id: '101conv-official', name: 'Official Catalog', url: '', kind: 'official', trust: 'pinned', enabled: true, status: 'ok' },
    kind: 'profile',
    pkg: {
      id: 'ffmpeg-image-linux-amd64',
      name: 'FFmpeg Image Conversion',
      version: '1.2.0',
      description: 'Convert raster photos cleanly.',
      engines: [{ engineId: 'ffmpeg', capabilities: ['image.convert'] }],
      rules: [
        { sources: ['jpg', 'png', 'webp', 'avif'], targets: ['webp', 'avif', 'png', 'jpg'] },
        { sources: ['png', 'gif'], targets: ['webp'] },
      ],
    },
  };
  assert.deepEqual(uniqueFormats(entry.pkg), {
    sources: ['jpg', 'png', 'webp', 'avif', 'gif'],
    targets: ['webp', 'avif', 'png', 'jpg'],
  });
  const text = searchableText(entry);
  assert.ok(text.includes('avif') && text.includes('image.convert') && text.includes('raster photos'));
  assert.deepEqual(categoriesOf(entry), ['images']);
});

test('batch collage plans never emit a fake +0 tile and compute +N accurately', () => {
  const mk = n => Array.from({ length: n }, (_, i) => ({ index: i }));
  assert.deepEqual(batchCollagePlan(mk(1), 1), { tiles: [{ index: 0 }], overflowCount: 0 });
  assert.deepEqual(batchCollagePlan(mk(2), 2), { tiles: [{ index: 0 }, { index: 1 }], overflowCount: 0 });
  assert.deepEqual(batchCollagePlan(mk(3), 3), { tiles: [{ index: 0 }, { index: 1 }, { index: 2 }], overflowCount: 0 });
  assert.deepEqual(batchCollagePlan(mk(4), 4), { tiles: [{ index: 0 }, { index: 1 }, { index: 2 }], overflowCount: 1 });
  assert.deepEqual(batchCollagePlan(mk(3), 12), { tiles: [{ index: 0 }, { index: 1 }, { index: 2 }], overflowCount: 9 });
});

test('preview descriptors distinguish output vs original and respect expiry and safe formats', () => {
  assert.equal(isBrowserPreviewableImage('photo.jpg', 'image/jpeg'), true);
  assert.equal(isBrowserPreviewableImage('vector.svg', 'image/svg+xml'), false);
  assert.equal(isBrowserPreviewableImage('report.pdf', 'application/pdf'), false);
  assert.equal(fileKindFor('report.pdf'), 'document');
  assert.equal(fileKindFor('clip.mp4'), 'video');

  const succeeded = previewDescriptorFor({
    jobId: 'job-1',
    sourceFormat: 'png',
    targetFormat: 'jpg',
    state: 'succeeded',
    originalState: 'available',
    resultState: 'available',
  });
  assert.equal(succeeded.badge, 'Output');
  assert.equal(succeeded.url, '/api/v1/jobs/job-1/thumbnail?kind=result');

  const queued = previewDescriptorFor({
    jobId: 'job-2',
    sourceFormat: 'png',
    targetFormat: 'jpg',
    state: 'queued',
    originalState: 'available',
  });
  assert.equal(queued.badge, 'Original');
  assert.equal(queued.url, '/api/v1/jobs/job-2/thumbnail?kind=original');

  const expired = previewDescriptorFor({
    jobId: 'job-3',
    sourceFormat: 'png',
    targetFormat: 'jpg',
    state: 'succeeded',
    originalState: 'expired',
    resultState: 'expired',
  });
  assert.equal(expired.url, null);
});

