import { useEffect, useMemo, useRef, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { ArrowRight, CircleAlert, LoaderCircle, RefreshCw, Search, ShieldCheck } from 'lucide-react';
import { Empty, ErrorState, Loading, PageHeading } from '../../components/common';
import { explain } from '../format';
import { apiRequest } from '../session';
import { catalogProfileType, entryKey, missingEngines, statusOf } from './catalogData';
import { useCatalog } from './CatalogContext';
import { StatusTag } from './StatusTag';
import type { Preview } from './types';
import './catalog.css';

type Item = ReturnType<typeof useCatalog>['entries'][number];

export function MarketplacePage({ notify }: { notify: (message: string) => void }) {
  const location = useLocation();
  const params = new URLSearchParams(location.search);
  const requested = params.get('tab');
  const focus = params.get('focus') || '';
  const tab = requested === 'compression' || requested === 'engines' ? requested : 'conversion';
  const catalog = useCatalog();
  const { repos, entries, updates, unseenNew, installedProfiles, installedEngines } = catalog;
  const [search, setSearch] = useState('');
  const [busy, setBusy] = useState('');
  const [previews, setPreviews] = useState<Record<string, Preview>>({});

  // Remember what was new when this page opened, then mark it seen so the
  // navigation badge clears without the banner vanishing mid-visit.
  const fresh = useRef<Set<string> | null>(null);
  useEffect(() => {
    if (catalog.loading || !entries.length || fresh.current) return;
    fresh.current = new Set(unseenNew.map(entryKey));
    catalog.markSeen();
  }, [catalog.loading, entries.length, unseenNew]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { if (focus) document.getElementById('catalog-' + focus)?.scrollIntoView({ block: 'center', behavior: 'smooth' }); }, [focus, entries.length]);

  const results = useMemo(() => entries
    .filter(item => tab === 'engines' ? item.kind === 'engine' : item.kind === 'profile' && catalogProfileType(item.pkg) === tab)
    .filter(item => (item.pkg.name + ' ' + item.pkg.id + ' ' + item.repo.name).toLowerCase().includes(search.toLowerCase())), [entries, tab, search]);
  const incompatible = tab === 'engines' ? [] : Object.values(catalog.catalogs)
    .flatMap(c => c.incompatibleProfiles.map(p => ({ repository: c.repository, ...p })))
    .filter(p => (p.id + ' ' + p.repository.name + ' ' + p.reason).toLowerCase().includes(search.toLowerCase()));
  const unsignedApproved = repos.some(r => r.id === '101conv-official' && r.trust === 'approved_unsigned');
  const hasBrowsable = repos.some(r => r.enabled && (r.trust === 'pinned' || r.id === '101conv-official'));

  async function review(item: Item) {
    const updating = statusOf(item, installedProfiles, installedEngines).kind === 'update';
    setBusy(entryKey(item));
    try {
      const path = item.kind === 'profile' ? `profiles/${item.pkg.id}${updating ? '/update-preview' : '/preview'}` : `engines/${item.pkg.id}/${item.pkg.version}/preview`;
      setPreviews(current => ({ ...current, [entryKey(item)]: undefined as unknown as Preview }));
      const plan = await apiRequest<Preview>(`/repositories/${item.repoId}/${path}`);
      setPreviews(current => ({ ...current, [entryKey(item)]: plan }));
    } catch (e) { notify(explain((e as Error).message)); } finally { setBusy(''); }
  }

  async function confirm(item: Item) {
    const plan = previews[entryKey(item)];
    if (!plan) return;
    const updating = statusOf(item, installedProfiles, installedEngines).kind === 'update';
    setBusy(entryKey(item));
    try {
      const path = item.kind === 'profile' ? (updating ? `profiles/${item.pkg.id}` : `profiles/${item.pkg.id}/install`) : `engines/${item.pkg.id}/${item.pkg.version}/install`;
      await apiRequest(`/repositories/${item.repoId}/${path}`, { method: updating ? 'PUT' : 'POST', body: JSON.stringify({ planId: plan.planId }) });
      notify(`${item.pkg.name} ${updating ? 'updated to v' + item.pkg.version : 'installed'}.`);
      setPreviews(current => { const next = { ...current }; delete next[entryKey(item)]; return next; });
      await catalog.refreshInstalled();
    } catch (e) { notify(explain((e as Error).message)); } finally { setBusy(''); }
  }

  return (
    <>
      <PageHeading eyebrow="REPOSITORY CATALOG" title="Browse the catalog" description="Install conversion profiles, compression profiles, and shared engines as separate catalog items." action={<button className="secondary" onClick={() => void catalog.refresh(true)}><RefreshCw size={15} /> Refresh catalog</button>} />
      <div className="market-hero">
        <div className="market-hero-icon"><Search size={20} /></div>
        <div>
          <b>{unsignedApproved ? 'Official catalog · unsigned installs enabled' : 'Official catalog · browse only'}</b>
          <span>{unsignedApproved ? 'An administrator approved installs from GitHub without signed metadata.' : 'Approve unsigned installs in Administration to install the listed profiles.'}</span>
        </div>
        <div className="trust-indicator"><CircleAlert size={15} /> {unsignedApproved ? 'Unsigned approval' : 'Browse only'}</div>
      </div>
      {(updates.length > 0 || (fresh.current?.size || 0) > 0) && (
        <section className="catalog-news" aria-label="Updates and new profiles">
          {updates.length > 0 && <div><b>{updates.length} update{updates.length === 1 ? '' : 's'} available</b><ul>{updates.map(entry => {
            const status = statusOf(entry, installedProfiles, installedEngines);
            const target = catalogProfileType(entry.pkg) === 'compression' ? 'compression' : 'conversion';
            return <li key={entryKey(entry)}><NavLink className="text-link" to={`/marketplace?tab=${target}&focus=${encodeURIComponent(entry.pkg.id)}`}>{entry.pkg.name}{status.kind === 'update' ? ` v${status.installed} → v${status.available}` : ''}</NavLink></li>;
          })}</ul></div>}
          {(fresh.current?.size || 0) > 0 && <div><b>{fresh.current!.size} new profile{fresh.current!.size === 1 ? '' : 's'} since you last looked</b><span>Marked <em>New</em> below. Nothing is installed or updated until you approve it.</span></div>}
        </section>
      )}
      {repos.some(r => r.trust !== 'pinned' && r.id !== '101conv-official') && <div className="banner trust-warning"><ShieldCheck size={17} /><span>Unverified community sources are excluded from browsing. Review their trust state in Administration.</span><NavLink to="/admin" className="text-link">Review</NavLink></div>}
      <nav className="tabbar" aria-label="Repository catalog categories">
        <NavLink aria-current={tab === 'conversion' ? 'page' : undefined} className={() => tab === 'conversion' ? 'active' : ''} to="/marketplace?tab=conversion">Conversion profiles</NavLink>
        <NavLink aria-current={tab === 'compression' ? 'page' : undefined} className={() => tab === 'compression' ? 'active' : ''} to="/marketplace?tab=compression">Compression profiles</NavLink>
        <NavLink aria-current={tab === 'engines' ? 'page' : undefined} className={() => tab === 'engines' ? 'active' : ''} to="/marketplace?tab=engines">Engines</NavLink>
      </nav>
      <div className="toolbar marketplace-toolbar">
        <div className="search-box"><Search size={17} /><input placeholder={tab === 'engines' ? 'Search engines' : 'Search ' + tab + ' profiles'} aria-label={'Search ' + (tab === 'engines' ? 'engines' : tab + ' profiles')} value={search} onChange={event => setSearch(event.target.value)} /></div>
        <span className="result-count">{results.length} available</span>
      </div>
      {catalog.error && <ErrorState message={catalog.error} retry={() => void catalog.refresh(true)} />}
      {catalog.loading ? <Loading /> : repos.length === 0 ? <Empty icon={Search} title="No repositories configured" detail="Manage repository sources in Administration." /> : !hasBrowsable ? (
        <div className="unavailable-card">
          <div className="unavailable-icon"><ShieldCheck size={20} /></div>
          <div>
            <h3>No browseable catalogs are available</h3>
            <p>The official catalog is public on GitHub. Refresh the page after it becomes reachable, or configure a trusted source for installation.</p>
            <div className="repo-status-row">{repos.map(r => <span key={r.id}><i className={r.trust === 'pinned' ? 'green-dot' : 'gray-dot'} />{r.name} · {r.trust === 'unavailable' ? 'Browse only; not verified' : r.trust}</span>)}</div>
            <NavLink className="text-link" to="/admin">Review repository status <ArrowRight size={14} /></NavLink>
          </div>
        </div>
      ) : results.length === 0 ? <Empty icon={Search} title="No matches" detail="Try a different term or refresh repository metadata." /> : (
        <div className="market-grid">{results.map(item => {
          const key = entryKey(item);
          const status = statusOf(item, installedProfiles, installedEngines);
          const plan = previews[key];
          const canInstall = item.repo.trust === 'pinned' || item.repo.trust === 'approved_unsigned';
          const unverified = item.repo.trust === 'approved_unsigned' || item.repo.trust === 'unavailable';
          const alsoInstalls = item.kind === 'profile' ? missingEngines(item.pkg, installedEngines) : [];
          const updating = status.kind === 'update';
          return (
            <article className={'market-card' + (focus === item.pkg.id ? ' focused' : '')} id={'catalog-' + item.pkg.id} key={item.repoId + item.kind + item.pkg.id + item.pkg.version}>
              <div className="market-card-top">
                <span className="market-type">{item.kind === 'engine' ? 'ENGINE' : item.pkg.type === 'compression' ? 'COMPRESSION PROFILE' : 'CONVERSION PROFILE'}</span>
                <span className={'mini-badge ' + (item.repo.trust === 'pinned' ? 'trusted' : '')} aria-label={unverified ? 'Administrator-approved unsigned source' : 'Verified source'}>{item.repo.trust === 'pinned' ? <ShieldCheck size={12} /> : <CircleAlert size={12} />} {item.repo.trust === 'pinned' ? item.repo.name : item.repo.trust === 'approved_unsigned' ? 'Admin-approved · unsigned' : 'GitHub · browse only'}</span>
              </div>
              <h3>{item.pkg.name}</h3>
              <div className="market-status"><StatusTag status={status} /></div>
              <p>{item.pkg.description || item.pkg.id + ' · ' + item.kind + ' package'}</p>
              <div className="market-meta"><span>v{item.pkg.version}</span><span>{item.pkg.license || 'License not specified'}</span></div>
              {item.olderVersions.length > 0 && <small className="market-older">Older releases in the catalog: {item.olderVersions.map(v => 'v' + v).join(', ')}</small>}
              {item.kind === 'profile' && <div className="format-chips">{(item.pkg.rules || []).flatMap((r: any) => r.sources || []).slice(0, 3).map((x: string, i: number) => <span key={'s' + i}>{x.toUpperCase()}</span>)}<ArrowRight size={12} />{(item.pkg.rules || []).flatMap((r: any) => r.targets || []).slice(0, 3).map((x: string, i: number) => <span key={'t' + i}>{x.toUpperCase()}</span>)}</div>}
              {alsoInstalls.length > 0 && !plan && status.kind !== 'installed' && <small className="market-older">Also installs the {alsoInstalls.join(', ')} engine.</small>}
              <div className="market-card-actions">
                {status.kind === 'installed' ? null
                  : !canInstall ? <button className="secondary small" disabled title="Approve unsigned installs in Administration"><CircleAlert size={14} /> Browse only · approval needed</button>
                  : !plan ? <button className="secondary small" disabled={busy === key} onClick={() => void review(item)}>{busy === key ? <LoaderCircle className="spin" /> : <ShieldCheck size={14} />} {updating ? 'Review update' : 'Review installation'}</button>
                  : null}
              </div>
              {plan && (
                <section className="install-plan" aria-label={(updating ? 'Update plan for ' : 'Installation plan for ') + item.pkg.name}>
                  <h4>{updating ? `Update ${item.pkg.name}: v${plan.installedVersion || (status.kind === 'update' ? status.installed : '')} → v${item.pkg.version}` : `Install ${item.pkg.name} v${item.pkg.version}`}</h4>
                  {updating && (plan.profileChanges?.length ? <ul className="plan-changes">{plan.profileChanges.map(line => <li key={line}>{line}</li>)}</ul> : <p className="plan-note">No change to supported formats or options.</p>)}
                  <dl>
                    <div><dt>Publisher</dt><dd>{plan.publisher}</dd></div>
                    <div><dt>Trust</dt><dd>{plan.trust === 'approved_unsigned' ? 'Administrator-approved unsigned catalog; the publisher is not verified' : 'Verified repository'}</dd></div>
                    <div><dt>License</dt><dd>{plan.license || item.pkg.license || 'Unspecified'}</dd></div>
                    {(plan.dependencies || []).map(dep => <div key={dep.engineId}><dt>Engine</dt><dd>{dep.action === 'reuse installed engine' ? `Reuses ${dep.engineName || dep.engineId} v${dep.installedVersion}` : `Installs ${dep.engineName || dep.engineId}`}</dd></div>)}
                  </dl>
                  {updating && <p className="plan-note">Jobs already queued keep the settings they were created with.</p>}
                  <div className="plan-actions">
                    <button className="primary small" disabled={busy === key} onClick={() => void confirm(item)}>{busy === key ? <LoaderCircle className="spin" /> : null}{updating ? `Update to v${item.pkg.version}` : 'Install'}</button>
                    <button className="secondary small" disabled={busy === key} onClick={() => setPreviews(current => { const next = { ...current }; delete next[key]; return next; })}>Cancel</button>
                    <small>Plan expires at {new Date(plan.expiresAt || Date.now() + 600000).toLocaleTimeString()}.</small>
                  </div>
                </section>
              )}
            </article>
          );
        })}</div>
      )}
      {incompatible.length > 0 && (
        <section className="incompatible-section">
          <div className="section-intro"><div><h2>Incompatible with this instance</h2><p>These profiles are signed but do not match the current app version or platform.</p></div></div>
          <div className="market-grid">{incompatible.map(item => (
            <article className="market-card incompatible-card" key={item.repository.id + item.id}>
              <div className="market-card-top"><span className="market-type">PROFILE</span><span className="mini-badge">Incompatible</span></div>
              <h3>{item.id}</h3>
              <p>{item.reason}</p>
              <div className="market-meta"><span>v{item.version}</span><span>{item.repository.name}</span></div>
              <div className="unavailable-label"><CircleAlert size={13} /> Cannot install on this instance</div>
            </article>
          ))}</div>
        </section>
      )}
    </>
  );
}
