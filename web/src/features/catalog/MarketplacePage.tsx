import { useEffect, useMemo, useRef, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { ArrowRight, CircleAlert, LoaderCircle, PackagePlus, RefreshCw, Search, ShieldCheck } from 'lucide-react';
import { Empty, ErrorState, Loading, PageHeading } from '../../components/common';
import { explain } from '../format';
import { apiRequest } from '../session';
import {
  catalogProfileType,
  categoriesOf,
  entryKey,
  missingEngines,
  searchableText,
  statusOf,
  uniqueFormats,
} from './catalogData';
import { useCatalog } from './CatalogContext';
import { StatusTag } from './StatusTag';
import type { BatchPreview, CatalogCategory, Preview, StarterBundle } from './types';
import './catalog.css';

type Item = ReturnType<typeof useCatalog>['entries'][number];

const CATEGORY_OPTIONS: { id: CatalogCategory; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'images', label: 'Images' },
  { id: 'audio', label: 'Audio' },
  { id: 'video', label: 'Video' },
  { id: 'documents', label: 'Documents' },
  { id: 'archives', label: 'Archives' },
];

function FormatChipList({
  sources,
  targets,
  expanded,
  onToggle,
}: {
  sources: string[];
  targets: string[];
  expanded: boolean;
  onToggle: () => void;
}) {
  const limit = 3;
  const shownSources = expanded ? sources : sources.slice(0, limit);
  const shownTargets = expanded ? targets : targets.slice(0, limit);
  const hiddenCount = Math.max(0, sources.length - limit) + Math.max(0, targets.length - limit);

  return (
    <div className="format-chips">
      {shownSources.map(ext => (
        <span key={'s-' + ext}>{ext.toUpperCase()}</span>
      ))}
      {!expanded && sources.length > limit && (
        <button type="button" className="format-more-btn" onClick={onToggle}>
          +{sources.length - limit} more
        </button>
      )}
      <ArrowRight size={12} />
      {shownTargets.map(ext => (
        <span key={'t-' + ext}>{ext.toUpperCase()}</span>
      ))}
      {!expanded && targets.length > limit && (
        <button type="button" className="format-more-btn" onClick={onToggle}>
          +{targets.length - limit} more
        </button>
      )}
      {expanded && hiddenCount > 0 && (
        <button type="button" className="format-more-btn" onClick={onToggle}>
          Show fewer
        </button>
      )}
    </div>
  );
}

export function MarketplacePage({ notify }: { notify: (message: string) => void }) {
  const location = useLocation();
  const params = new URLSearchParams(location.search);
  const requested = params.get('tab');
  const focus = params.get('focus') || '';
  const tab = requested === 'compression' || requested === 'engines' ? requested : 'conversion';
  const catalog = useCatalog();
  const { repos, entries, updates, unseenNew, installedProfiles, installedEngines } = catalog;
  const [search, setSearch] = useState('');
  const [category, setCategory] = useState<CatalogCategory>('all');
  const [busy, setBusy] = useState('');
  const [previews, setPreviews] = useState<Record<string, Preview>>({});
  const [expandedFormats, setExpandedFormats] = useState<Record<string, boolean>>({});
  const [selected, setSelected] = useState<Record<string, { repoId: string; profileId: string }>>({});
  const [batchPlan, setBatchPlan] = useState<{ title: string; repoId: string; preview: BatchPreview } | null>(null);

  // Remember what was new when this page opened, then mark it seen so the
  // navigation badge clears without the banner vanishing mid-visit.
  const fresh = useRef<Set<string> | null>(null);
  useEffect(() => {
    if (catalog.loading || !entries.length || fresh.current) return;
    fresh.current = new Set(unseenNew.map(entryKey));
    catalog.markSeen();
  }, [catalog.loading, entries.length, unseenNew]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (focus) document.getElementById('catalog-' + focus)?.scrollIntoView({ block: 'center', behavior: 'smooth' });
  }, [focus, entries.length]);

  const query = search.trim().toLowerCase();
  const results = useMemo(
    () =>
      entries
        .filter(item =>
          tab === 'engines' ? item.kind === 'engine' : item.kind === 'profile' && catalogProfileType(item.pkg) === tab,
        )
        .filter(item => (category === 'all' ? true : categoriesOf(item).includes(category)))
        .filter(item => (!query ? true : searchableText(item).includes(query))),
    [entries, tab, category, query],
  );
  const incompatible =
    tab === 'engines'
      ? []
      : Object.values(catalog.catalogs)
          .flatMap(c => c.incompatibleProfiles.map(p => ({ repository: c.repository, ...p })))
          .filter(p => (p.id + ' ' + p.repository.name + ' ' + p.reason).toLowerCase().includes(query));
  const unsignedApproved = repos.some(r => r.id === '101conv-official' && r.trust === 'approved_unsigned');
  const hasBrowsable = repos.some(r => r.enabled && (r.trust === 'pinned' || r.id === '101conv-official'));

  const starterBundles = useMemo(() => {
    const out: { repoId: string; repoTrust: string; bundle: StarterBundle; installedCount: number; pendingIds: string[] }[] = [];
    for (const [repoId, cat] of Object.entries(catalog.catalogs)) {
      for (const bundle of cat.bundles || []) {
        const availableIds = bundle.profileIds.filter(pid =>
          entries.some(e => e.repoId === repoId && e.kind === 'profile' && e.pkg.id === pid),
        );
        if (!availableIds.length) continue;
        const installedCount = availableIds.filter(pid =>
          installedProfiles.some(p => p.definition.id === pid),
        ).length;
        const pendingIds = availableIds.filter(pid => {
          const entry = entries.find(e => e.repoId === repoId && e.kind === 'profile' && e.pkg.id === pid);
          if (!entry) return false;
          const st = statusOf(entry, installedProfiles, installedEngines);
          return st.kind === 'new' || st.kind === 'update';
        });
        out.push({ repoId, repoTrust: cat.repository.trust, bundle, installedCount, pendingIds });
      }
    }
    return out;
  }, [catalog.catalogs, entries, installedProfiles, installedEngines]);

  const selectedList = Object.values(selected);

  function toggleSelected(item: Item) {
    const key = entryKey(item);
    setSelected(current => {
      const next = { ...current };
      if (next[key]) delete next[key];
      else next[key] = { repoId: item.repoId, profileId: item.pkg.id };
      return next;
    });
  }

  async function review(item: Item) {
    const updating = statusOf(item, installedProfiles, installedEngines).kind === 'update';
    setBusy(entryKey(item));
    try {
      const path =
        item.kind === 'profile'
          ? `profiles/${item.pkg.id}${updating ? '/update-preview' : '/preview'}`
          : `engines/${item.pkg.id}/${item.pkg.version}/preview`;
      setPreviews(current => ({ ...current, [entryKey(item)]: undefined as unknown as Preview }));
      const plan = await apiRequest<Preview>(`/repositories/${item.repoId}/${path}`);
      setPreviews(current => ({ ...current, [entryKey(item)]: plan }));
    } catch (e) {
      notify(explain((e as Error).message));
    } finally {
      setBusy('');
    }
  }

  async function confirm(item: Item) {
    const plan = previews[entryKey(item)];
    if (!plan) return;
    const updating = statusOf(item, installedProfiles, installedEngines).kind === 'update';
    setBusy(entryKey(item));
    try {
      const path =
        item.kind === 'profile'
          ? updating
            ? `profiles/${item.pkg.id}`
            : `profiles/${item.pkg.id}/install`
          : `engines/${item.pkg.id}/${item.pkg.version}/install`;
      await apiRequest(`/repositories/${item.repoId}/${path}`, {
        method: updating ? 'PUT' : 'POST',
        body: JSON.stringify({ planId: plan.planId }),
      });
      notify(`${item.pkg.name} ${updating ? 'updated to v' + item.pkg.version : 'installed'}.`);
      setPreviews(current => {
        const next = { ...current };
        delete next[entryKey(item)];
        return next;
      });
      await catalog.refreshInstalled();
    } catch (e) {
      notify(explain((e as Error).message));
    } finally {
      setBusy('');
    }
  }

  async function reviewBatch(title: string, repoId: string, profileIds: string[]) {
    if (!profileIds.length) return;
    setBusy('batch:' + title);
    try {
      const preview = await apiRequest<BatchPreview>(`/repositories/${repoId}/batch-preview`, {
        method: 'POST',
        body: JSON.stringify({ profileIds }),
      });
      setBatchPlan({ title, repoId, preview });
    } catch (e) {
      notify(explain((e as Error).message));
    } finally {
      setBusy('');
    }
  }

  async function confirmBatch() {
    if (!batchPlan) return;
    setBusy('batch-apply');
    try {
      await apiRequest(`/repositories/${batchPlan.repoId}/batch-apply`, {
        method: 'POST',
        body: JSON.stringify({ planId: batchPlan.preview.planId }),
      });
      const count = batchPlan.preview.items.length;
      notify(`${batchPlan.title}: ${count} profile${count === 1 ? '' : 's'} applied.`);
      setBatchPlan(null);
      setSelected({});
      await catalog.refreshInstalled();
    } catch (e) {
      notify(explain((e as Error).message));
    } finally {
      setBusy('');
    }
  }

  const updateRepoId = updates[0]?.repoId || '';
  const allUpdatesSameRepo = updates.length > 0 && updates.every(u => u.repoId === updateRepoId);
  const canApplyUpdates =
    allUpdatesSameRepo &&
    (updates[0].repo.trust === 'pinned' || updates[0].repo.trust === 'approved_unsigned');

  return (
    <>
      <PageHeading
        eyebrow="REPOSITORY CATALOG"
        title="Browse the catalog"
        description="Install conversion profiles, compression profiles, and shared engines as separate catalog items."
        action={
          <button className="secondary" onClick={() => void catalog.refresh(true)}>
            <RefreshCw size={15} /> Refresh catalog
          </button>
        }
      />
      <div className="market-hero">
        <div className="market-hero-icon">
          <Search size={20} />
        </div>
        <div>
          <b>{unsignedApproved ? 'Official catalog · unsigned installs enabled' : 'Official catalog · browse only'}</b>
          <span>
            {unsignedApproved
              ? 'An administrator approved installs from GitHub without signed metadata.'
              : 'Approve unsigned installs in Administration to install the listed profiles.'}
          </span>
        </div>
        <div className="trust-indicator">
          <CircleAlert size={15} /> {unsignedApproved ? 'Unsigned approval' : 'Browse only'}
        </div>
      </div>

      {(updates.length > 0 || (fresh.current?.size || 0) > 0) && (
        <section className="catalog-news" aria-label="Updates and new profiles">
          {updates.length > 0 && (
            <div>
              <div className="catalog-news-head">
                <b>
                  {updates.length} update{updates.length === 1 ? '' : 's'} available
                </b>
                {canApplyUpdates && (
                  <button
                    type="button"
                    className="secondary small"
                    disabled={busy !== ''}
                    onClick={() =>
                      void reviewBatch(
                        'Update all compatible profiles',
                        updateRepoId,
                        updates.map(u => u.pkg.id),
                      )
                    }
                  >
                    {busy === 'batch:Update all compatible profiles' ? (
                      <LoaderCircle className="spin" size={14} />
                    ) : (
                      <ShieldCheck size={14} />
                    )}{' '}
                    Review all updates
                  </button>
                )}
              </div>
              <ul>
                {updates.map(entry => {
                  const status = statusOf(entry, installedProfiles, installedEngines);
                  const target = catalogProfileType(entry.pkg) === 'compression' ? 'compression' : 'conversion';
                  return (
                    <li key={entryKey(entry)}>
                      <NavLink
                        className="text-link"
                        to={`/marketplace?tab=${target}&focus=${encodeURIComponent(entry.pkg.id)}`}
                      >
                        {entry.pkg.name}
                        {status.kind === 'update' ? ` v${status.installed} → v${status.available}` : ''}
                      </NavLink>
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
          {(fresh.current?.size || 0) > 0 && (
            <div>
              <b>
                {fresh.current!.size} new profile{fresh.current!.size === 1 ? '' : 's'} since you last looked
              </b>
              <span>
                Marked <em>New</em> below. Nothing is installed or updated until you approve it.
              </span>
            </div>
          )}
        </section>
      )}

      {batchPlan && (
        <section className="install-plan batch-plan" aria-label={'Batch plan: ' + batchPlan.title}>
          <h4>
            {batchPlan.title} ({batchPlan.preview.items.length} profile
            {batchPlan.preview.items.length === 1 ? '' : 's'})
          </h4>
          <dl>
            <div>
              <dt>Publisher</dt>
              <dd>{batchPlan.preview.publisher}</dd>
            </div>
            <div>
              <dt>Trust</dt>
              <dd>
                {batchPlan.preview.trust === 'approved_unsigned'
                  ? 'Administrator-approved unsigned catalog; the publisher is not verified'
                  : 'Verified repository'}
              </dd>
            </div>
            {(batchPlan.preview.dependencies || []).map(dep => (
              <div key={dep.engineId + dep.action}>
                <dt>Engine</dt>
                <dd>
                  {dep.action === 'reuse installed engine'
                    ? `Reuses ${dep.engineName || dep.engineId} v${dep.installedVersion}`
                    : `Installs ${dep.engineName || dep.engineId} (shared across selected profiles)`}
                  {dep.engineLicense ? ` · ${dep.engineLicense}` : ''}
                </dd>
              </div>
            ))}
          </dl>
          <ul className="batch-plan-items">
            {batchPlan.preview.items.map(item => (
              <li key={item.profile.id}>
                <b>
                  {item.profile.name} ·{' '}
                  {item.action === 'update'
                    ? `Update v${item.installedVersion} → v${item.profile.version}`
                    : `Install v${item.profile.version}`}
                </b>
                <small>{item.license || item.profile.license || 'License not specified'}</small>
                {item.profileChanges && item.profileChanges.length > 0 && (
                  <ul className="plan-changes">
                    {item.profileChanges.map(line => (
                      <li key={line}>{line}</li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
          <p className="plan-note">
            Shared engine downloads are deduplicated. Queued jobs and pinned automations keep their existing versions.
          </p>
          <div className="plan-actions">
            <button
              type="button"
              className="primary small"
              disabled={busy === 'batch-apply'}
              onClick={() => void confirmBatch()}
            >
              {busy === 'batch-apply' ? <LoaderCircle className="spin" size={14} /> : null}
              Approve &amp; apply ({batchPlan.preview.items.length})
            </button>
            <button
              type="button"
              className="secondary small"
              disabled={busy === 'batch-apply'}
              onClick={() => setBatchPlan(null)}
            >
              Cancel
            </button>
            <small>Plan expires at {new Date(batchPlan.preview.expiresAt || Date.now() + 600000).toLocaleTimeString()}.</small>
          </div>
        </section>
      )}

      {starterBundles.length > 0 && tab !== 'engines' && (
        <section className="starter-bundles" aria-label="Starter bundles">
          <div className="starter-bundles-header">
            <h2>Starter bundles</h2>
            <span>Provision common workflows in one reviewed step.</span>
          </div>
          <div className="bundle-grid">
            {starterBundles.map(({ repoId, repoTrust, bundle, installedCount, pendingIds }) => {
              const canInstall = repoTrust === 'pinned' || repoTrust === 'approved_unsigned';
              const allInstalled = pendingIds.length === 0;
              return (
                <article className="bundle-card" key={repoId + '|' + bundle.id}>
                  <div className="bundle-card-top">
                    <h3>{bundle.name}</h3>
                    <span className="mini-badge">
                      {installedCount}/{bundle.profileIds.length} installed
                    </span>
                  </div>
                  {bundle.description && <p>{bundle.description}</p>}
                  <div className="bundle-card-actions">
                    <small>{bundle.profileIds.length} profiles</small>
                    {allInstalled ? (
                      <span className="mini-badge trusted">All installed</span>
                    ) : !canInstall ? (
                      <button type="button" className="secondary small" disabled>
                        Approval needed
                      </button>
                    ) : (
                      <button
                        type="button"
                        className="secondary small"
                        disabled={busy !== ''}
                        onClick={() => void reviewBatch(bundle.name, repoId, pendingIds)}
                      >
                        {busy === 'batch:' + bundle.name ? (
                          <LoaderCircle className="spin" size={14} />
                        ) : (
                          <PackagePlus size={14} />
                        )}{' '}
                        Review bundle ({pendingIds.length})
                      </button>
                    )}
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      )}

      {repos.some(r => r.trust !== 'pinned' && r.id !== '101conv-official') && (
        <div className="banner trust-warning">
          <ShieldCheck size={17} />
          <span>Unverified community sources are excluded from browsing. Review their trust state in Administration.</span>
          <NavLink to="/admin" className="text-link">
            Review
          </NavLink>
        </div>
      )}

      <nav className="tabbar" aria-label="Repository catalog categories">
        <NavLink
          aria-current={tab === 'conversion' ? 'page' : undefined}
          className={() => (tab === 'conversion' ? 'active' : '')}
          to="/marketplace?tab=conversion"
        >
          Conversion profiles
        </NavLink>
        <NavLink
          aria-current={tab === 'compression' ? 'page' : undefined}
          className={() => (tab === 'compression' ? 'active' : '')}
          to="/marketplace?tab=compression"
        >
          Compression profiles
        </NavLink>
        <NavLink
          aria-current={tab === 'engines' ? 'page' : undefined}
          className={() => (tab === 'engines' ? 'active' : '')}
          to="/marketplace?tab=engines"
        >
          Engines
        </NavLink>
      </nav>

      <div className="toolbar marketplace-toolbar">
        <div className="search-box">
          <Search size={17} />
          <input
            placeholder={
              tab === 'engines'
                ? 'Search engines or capabilities'
                : 'Search formats (avif, pdf, flac), capabilities, or profiles'
            }
            aria-label={'Search ' + (tab === 'engines' ? 'engines' : tab + ' profiles')}
            value={search}
            onChange={event => setSearch(event.target.value)}
          />
        </div>
        <span className="result-count">{results.length} available</span>
      </div>

      <div className="catalog-filters" role="group" aria-label="Filter by media category">
        {CATEGORY_OPTIONS.map(opt => (
          <button
            key={opt.id}
            type="button"
            className={'catalog-filter-pill' + (category === opt.id ? ' active' : '')}
            aria-pressed={category === opt.id}
            onClick={() => setCategory(opt.id)}
          >
            {opt.label}
          </button>
        ))}
      </div>

      {selectedList.length > 0 && (
        <div className="batch-bar" role="region" aria-label="Selected profiles for batch installation">
          <span>
            <b>{selectedList.length}</b> profile{selectedList.length === 1 ? '' : 's'} selected
          </span>
          <div className="batch-bar-actions">
            <button
              type="button"
              className="primary small"
              disabled={busy !== '' || new Set(selectedList.map(s => s.repoId)).size !== 1}
              title={
                new Set(selectedList.map(s => s.repoId)).size !== 1
                  ? 'Select profiles from one repository at a time'
                  : undefined
              }
              onClick={() =>
                void reviewBatch(
                  'Selected profiles',
                  selectedList[0].repoId,
                  selectedList.map(s => s.profileId),
                )
              }
            >
              {busy === 'batch:Selected profiles' ? <LoaderCircle className="spin" size={14} /> : <ShieldCheck size={14} />}{' '}
              Review selected ({selectedList.length})
            </button>
            <button type="button" className="secondary small" onClick={() => setSelected({})}>
              Clear
            </button>
          </div>
        </div>
      )}

      {catalog.error && <ErrorState message={catalog.error} retry={() => void catalog.refresh(true)} />}
      {catalog.loading ? (
        <Loading />
      ) : repos.length === 0 ? (
        <Empty icon={Search} title="No repositories configured" detail="Manage repository sources in Administration." />
      ) : !hasBrowsable ? (
        <div className="unavailable-card">
          <div className="unavailable-icon">
            <ShieldCheck size={20} />
          </div>
          <div>
            <h3>No browseable catalogs are available</h3>
            <p>
              The official catalog is public on GitHub. Refresh the page after it becomes reachable, or configure a trusted
              source for installation.
            </p>
            <div className="repo-status-row">
              {repos.map(r => (
                <span key={r.id}>
                  <i className={r.trust === 'pinned' ? 'green-dot' : 'gray-dot'} />
                  {r.name} · {r.trust === 'unavailable' ? 'Browse only; not verified' : r.trust}
                </span>
              ))}
            </div>
            <NavLink className="text-link" to="/admin">
              Review repository status <ArrowRight size={14} />
            </NavLink>
          </div>
        </div>
      ) : results.length === 0 ? (
        <Empty icon={Search} title="No matches" detail="Try a different format, category, or refresh repository metadata." />
      ) : (
        <div className="market-grid">
          {results.map(item => {
            const key = entryKey(item);
            const status = statusOf(item, installedProfiles, installedEngines);
            const plan = previews[key];
            const canInstall = item.repo.trust === 'pinned' || item.repo.trust === 'approved_unsigned';
            const unverified = item.repo.trust === 'approved_unsigned' || item.repo.trust === 'unavailable';
            const alsoInstalls = item.kind === 'profile' ? missingEngines(item.pkg, installedEngines) : [];
            const updating = status.kind === 'update';
            const formats = item.kind === 'profile' ? uniqueFormats(item.pkg) : { sources: [], targets: [] };
            const selectable = item.kind === 'profile' && canInstall && (status.kind === 'new' || status.kind === 'update');
            return (
              <article
                className={'market-card' + (focus === item.pkg.id ? ' focused' : '')}
                id={'catalog-' + item.pkg.id}
                key={item.repoId + item.kind + item.pkg.id + item.pkg.version}
              >
                <div className="market-card-top">
                  <span className="market-type">
                    {item.kind === 'engine'
                      ? 'ENGINE'
                      : item.pkg.type === 'compression'
                        ? 'COMPRESSION PROFILE'
                        : 'CONVERSION PROFILE'}
                  </span>
                  <span
                    className={'mini-badge ' + (item.repo.trust === 'pinned' ? 'trusted' : '')}
                    aria-label={unverified ? 'Administrator-approved unsigned source' : 'Verified source'}
                  >
                    {item.repo.trust === 'pinned' ? <ShieldCheck size={12} /> : <CircleAlert size={12} />}{' '}
                    {item.repo.trust === 'pinned'
                      ? item.repo.name
                      : item.repo.trust === 'approved_unsigned'
                        ? 'Admin-approved · unsigned'
                        : 'GitHub · browse only'}
                  </span>
                </div>
                <h3>{item.pkg.name}</h3>
                <div className="market-status">
                  <StatusTag status={status} />
                </div>
                <p>{item.pkg.description || item.pkg.id + ' · ' + item.kind + ' package'}</p>
                <div className="market-meta">
                  <span>v{item.pkg.version}</span>
                  <span>{item.pkg.license || 'License not specified'}</span>
                </div>
                {item.olderVersions.length > 0 && (
                  <small className="market-older">
                    Older releases in the catalog: {item.olderVersions.map(v => 'v' + v).join(', ')}
                  </small>
                )}
                {item.kind === 'profile' && (
                  <FormatChipList
                    sources={formats.sources}
                    targets={formats.targets}
                    expanded={!!expandedFormats[key]}
                    onToggle={() => setExpandedFormats(cur => ({ ...cur, [key]: !cur[key] }))}
                  />
                )}
                {alsoInstalls.length > 0 && !plan && status.kind !== 'installed' && (
                  <small className="market-older">Also installs the {alsoInstalls.join(', ')} engine.</small>
                )}
                <div className="market-card-actions">
                  {selectable && (
                    <label className="batch-select-label">
                      <input
                        type="checkbox"
                        checked={!!selected[key]}
                        onChange={() => toggleSelected(item)}
                      />
                      Select
                    </label>
                  )}
                  {status.kind === 'installed' ? null : !canInstall ? (
                    <button className="secondary small" disabled title="Approve unsigned installs in Administration">
                      <CircleAlert size={14} /> Browse only · approval needed
                    </button>
                  ) : !plan ? (
                    <button className="secondary small" disabled={busy === key} onClick={() => void review(item)}>
                      {busy === key ? <LoaderCircle className="spin" /> : <ShieldCheck size={14} />}{' '}
                      {updating ? 'Review update' : 'Review installation'}
                    </button>
                  ) : null}
                </div>
                {plan && (
                  <section
                    className="install-plan"
                    aria-label={(updating ? 'Update plan for ' : 'Installation plan for ') + item.pkg.name}
                  >
                    <h4>
                      {updating
                        ? `Update ${item.pkg.name}: v${plan.installedVersion || (status.kind === 'update' ? status.installed : '')} → v${item.pkg.version}`
                        : `Install ${item.pkg.name} v${item.pkg.version}`}
                    </h4>
                    {updating &&
                      (plan.profileChanges?.length ? (
                        <ul className="plan-changes">
                          {plan.profileChanges.map(line => (
                            <li key={line}>{line}</li>
                          ))}
                        </ul>
                      ) : (
                        <p className="plan-note">No change to supported formats or options.</p>
                      ))}
                    <dl>
                      <div>
                        <dt>Publisher</dt>
                        <dd>{plan.publisher}</dd>
                      </div>
                      <div>
                        <dt>Trust</dt>
                        <dd>
                          {plan.trust === 'approved_unsigned'
                            ? 'Administrator-approved unsigned catalog; the publisher is not verified'
                            : 'Verified repository'}
                        </dd>
                      </div>
                      <div>
                        <dt>License</dt>
                        <dd>{plan.license || item.pkg.license || 'Unspecified'}</dd>
                      </div>
                      {(plan.dependencies || []).map(dep => (
                        <div key={dep.engineId}>
                          <dt>Engine</dt>
                          <dd>
                            {dep.action === 'reuse installed engine'
                              ? `Reuses ${dep.engineName || dep.engineId} v${dep.installedVersion}`
                              : `Installs ${dep.engineName || dep.engineId}`}
                          </dd>
                        </div>
                      ))}
                    </dl>
                    {updating && <p className="plan-note">Jobs already queued keep the settings they were created with.</p>}
                    <div className="plan-actions">
                      <button className="primary small" disabled={busy === key} onClick={() => void confirm(item)}>
                        {busy === key ? <LoaderCircle className="spin" /> : null}
                        {updating ? `Update to v${item.pkg.version}` : 'Install'}
                      </button>
                      <button
                        className="secondary small"
                        disabled={busy === key}
                        onClick={() =>
                          setPreviews(current => {
                            const next = { ...current };
                            delete next[key];
                            return next;
                          })
                        }
                      >
                        Cancel
                      </button>
                      <small>Plan expires at {new Date(plan.expiresAt || Date.now() + 600000).toLocaleTimeString()}.</small>
                    </div>
                  </section>
                )}
              </article>
            );
          })}
        </div>
      )}

      {incompatible.length > 0 && (
        <section className="incompatible-section">
          <div className="section-intro">
            <div>
              <h2>Incompatible with this instance</h2>
              <p>These profiles are signed but do not match the current app version or platform.</p>
            </div>
          </div>
          <div className="market-grid">
            {incompatible.map(item => (
              <article className="market-card incompatible-card" key={item.repository.id + item.id}>
                <div className="market-card-top">
                  <span className="market-type">PROFILE</span>
                  <span className="mini-badge">Incompatible</span>
                </div>
                <h3>{item.id}</h3>
                <p>{item.reason}</p>
                <div className="market-meta">
                  <span>v{item.version}</span>
                  <span>{item.repository.name}</span>
                </div>
                <div className="unavailable-label">
                  <CircleAlert size={13} /> Cannot install on this instance
                </div>
              </article>
            ))}
          </div>
        </section>
      )}
    </>
  );
}
