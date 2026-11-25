import { Component, Fragment, useEffect, useState, type ErrorInfo, type ReactNode } from 'react';
import { NavLink, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import {
  Boxes, ChevronDown, CircleAlert, Ellipsis, LogOut,
  Search, Settings2, ShieldCheck, X, Zap,
} from 'lucide-react';
import { ThemePicker } from './components/ThemePicker';
import { AnalyticsPage } from './features/admin/AnalyticsPage';
import { OperationsPage } from './features/admin/OperationsPage';
import { MarketplacePage } from './features/catalog/MarketplacePage';
import { HistoryPage } from './features/history/HistoryPage';
import { AccountPage } from './features/account/AccountPage';
import { InstanceSettingsPage } from './features/admin/InstanceSettingsPage';
import { UserAdministrationPage } from './features/admin/UserAdministrationPage';
import { AuthGate } from './features/auth/AuthGate';
import {
  ADMIN_SECTIONS, MOBILE_ADMIN_ITEMS, WORKSPACE_LINKS, labelFor, sectionFor,
} from './features/auth/navigation';
import { useAppSession } from './features/auth/useAppSession';
import { AutomationAdministration } from './features/automations/AutomationAdministration';
import { CatalogProvider, useCatalog } from './features/catalog/CatalogContext';
import { CompressPage } from './features/compress/CompressPage';
import { ConvertPage } from './features/convert/ConvertPage';
import { ConvertersPage } from './features/converters/ConvertersPage';
import { ProfileDefaultsPage } from './features/converters/ProfileDefaultsPage';
import { DocumentCreator } from './features/DocumentCreator';
import { Inspector } from './features/Inspector';
import { StoragePage } from './features/storage/StoragePage';

const guard = (allowed: boolean, view: ReactNode, fallback = '/convert') =>
  allowed ? view : <Navigate to={fallback} replace />;

function App() {
  const app = useAppSession();
  const { session, coreInfo, updateStatus, refreshUpdate, toast, setToast, logout } = app;
  const [mobileMoreOpen, setMobileMoreOpen] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    setMobileMoreOpen(false);
    document.title = labelFor(location.pathname, location.search) + ' · 101conv';
  }, [location.pathname, location.search]);

  useEffect(() => {
    if (!mobileMoreOpen) return;
    const close = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMobileMoreOpen(false);
    };
    window.addEventListener('keydown', close);
    return () => window.removeEventListener('keydown', close);
  }, [mobileMoreOpen]);

  const isAdmin = session.role === 'admin';
  const canConvert = isAdmin || Boolean(session.canConvert || session.permissions?.['conversion.use']);
  const canCompress = isAdmin || Boolean(session.canCompress || session.permissions?.['compression.use']);
  const canReadJobs = isAdmin || Boolean(session.canReadJobs || session.permissions?.['jobs.read']);
  const canManageUsers = isAdmin || Boolean(session.permissions?.['users.manage']);
  const access = { convert: canConvert, compress: canCompress, jobs: canReadJobs };
  const converterTab = new URLSearchParams(location.search).get('tab');
  const converterClass = (engines: boolean) => () =>
    location.pathname === '/converters' && (converterTab === 'engines') === engines ? 'active' : '';
  const guestFallback = session.guest ? '/convert' : '/account';
  const convertFallback = session.guest ? (session.canCompress ? '/compress' : '/jobs') : '/account';
  const compressFallback = session.guest ? (session.canConvert ? '/convert' : '/jobs') : '/account';
  const guestHours = session.guestResultExpirationHours || 24;

  return (
    <AuthGate app={app}>
      <CatalogProvider enabled={isAdmin}>
        <div className="app-shell">
          <a className="skip-link" href="#main-content">Skip to content</a>
          <aside className="sidebar">
            <a className="brand" href="/convert"><span className="brand-mark">101</span><span>101conv</span></a>
            <div className="nav-label">WORKSPACE</div>
            <nav aria-label="Primary navigation">
              {WORKSPACE_LINKS.map(([to, Icon, label, , key, dot]) => access[key] && (
                <NavLink key={to} to={to}><Icon /><span>{label}</span>{dot && <span className="nav-dot" />}</NavLink>
              ))}
            </nav>
            {isAdmin && (
              <>
                <div className="nav-label nav-label-spaced">CONVERTER SETUP</div>
                <nav aria-label="Converter navigation">
                  <NavLink to="/converters?tab=conversion" className={converterClass(false)}><Zap /><span>Profiles</span></NavLink>
                  <NavLink to="/converters?tab=engines" className={converterClass(true)}><Boxes /><span>Engines</span></NavLink>
                  <NavLink to="/profile-defaults"><Settings2 /><span>Profile defaults</span></NavLink>
                  <NavLink to="/marketplace"><Search /><span>Repository catalog</span><CatalogNavBadge /></NavLink>
                </nav>
                {ADMIN_SECTIONS.map(([heading, ariaLabel, items]) => (
                  <Fragment key={heading}>
                    <div className="nav-label nav-label-spaced">{heading}</div>
                    <nav aria-label={ariaLabel}>
                      {items.map(([to, Icon, label]) => (
                        <NavLink key={to} to={to}><Icon /><span>{label}</span></NavLink>
                      ))}
                    </nav>
                  </Fragment>
                ))}
              </>
            )}
            {!session.guest && (
              <nav aria-label="Account navigation">
                <NavLink to="/account"><ShieldCheck /><span>Account & API keys</span></NavLink>
              </nav>
            )}
            <div className="sidebar-bottom">
              <button className="user-button" onClick={() => void logout()}>
                <span className="avatar">{session.username?.[0]?.toUpperCase() || 'U'}</span>
                <span className="user-meta">
                  <b>{session.guest ? 'Guest mode' : session.username || 'User'}</b>
                  <small>
                    {session.guest
                      ? `Results expire after ${guestHours} hours · Sign in`
                      : isAdmin ? 'Administrator' : 'Local account'}
                  </small>
                </span>
                <LogOut size={16} />
              </button>
            </div>
          </aside>

          <main className="main-area">
            <header className="topbar">
              <div className="mobile-brand"><span className="brand-mark">101</span> 101conv</div>
              <div className="breadcrumbs">
                <span>{sectionFor(location.pathname)}</span>
                <ChevronDown size={13} />
                <strong>{labelFor(location.pathname, location.search)}</strong>
              </div>
              <div className="top-actions">
                <span className="system-status">
                  <i className={coreInfo.ready === true ? 'online' : coreInfo.ready === false ? 'offline' : 'checking'} />
                  {coreInfo.ready === true ? 'Core ready' : coreInfo.ready === false ? 'Core unavailable' : 'Checking Core…'}
                </span>
                {coreInfo.version && (isAdmin
                  ? <NavLink className="version-badge" to="/admin" title="Open version and update status">v{coreInfo.version}</NavLink>
                  : <span className="version-badge" title={`Version ${coreInfo.version}`}>v{coreInfo.version}</span>)}
                {isAdmin && updateStatus?.updateAvailable && (
                  <NavLink className="update-indicator" to="/admin">Update available</NavLink>
                )}
                <ThemePicker compact />
                {session.guest ? (
                  <>
                    <span className="guest-mode-indicator">Guest · {guestHours}h results</span>
                    <button className="guest-signin-button" onClick={async () => { await logout(); navigate('/login'); }}>Sign in</button>
                  </>
                ) : (
                  <button className="mobile-logout icon-btn" aria-label="Sign out" onClick={() => void logout()}><LogOut size={18} /></button>
                )}
              </div>
            </header>

            <section className="page-wrap" id="main-content" tabIndex={-1}>
              <PageErrorBoundary key={location.pathname}>
                <Routes>
                  <Route path="/" element={<Navigate to="/convert" replace />} />
                  <Route path="/login" element={<Navigate to="/convert" replace />} />
                  <Route path="/convert" element={guard(canConvert, <ConvertPage notify={setToast} session={session} />, convertFallback)} />
                  <Route path="/compress" element={guard(canCompress, <CompressPage notify={setToast} session={session} />, compressFallback)} />
                  <Route path="/build" element={guard(canConvert, <DocumentCreator notify={setToast} session={session} />, guestFallback)} />
                  <Route path="/inspect" element={guard(canReadJobs, <Inspector notify={setToast} session={session} />, guestFallback)} />
                  <Route path="/jobs" element={guard(canReadJobs, <HistoryPage notify={setToast} session={session} />, guestFallback)} />
                  <Route path="/account" element={guard(!session.guest, <AccountPage notify={setToast} session={session} />)} />
                  <Route path="/converters" element={guard(isAdmin, <ConvertersPage notify={setToast} />)} />
                  <Route path="/profile-defaults" element={guard(isAdmin, <ProfileDefaultsPage notify={setToast} />)} />
                  <Route path="/marketplace" element={guard(isAdmin, <MarketplacePage notify={setToast} />)} />
                  <Route path="/admin" element={guard(isAdmin, <InstanceSettingsPage notify={setToast} update={updateStatus} onRefreshUpdate={refreshUpdate} />)} />
                  <Route path="/storage" element={guard(isAdmin, <StoragePage notify={setToast} />)} />
                  <Route path="/operations" element={guard(isAdmin, <OperationsPage />)} />
                  <Route path="/analytics" element={guard(isAdmin, <AnalyticsPage />)} />
                  <Route path="/drives" element={guard(isAdmin, <AutomationAdministration view="drives" notify={setToast} />)} />
                  <Route path="/automations" element={guard(isAdmin, <AutomationAdministration view="automations" notify={setToast} />)} />
                  <Route path="/access" element={guard(canManageUsers, <UserAdministrationPage notify={setToast} />)} />
                  <Route path="*" element={<Navigate to="/convert" replace />} />
                </Routes>
              </PageErrorBoundary>
            </section>
          </main>

          <nav className={'mobile-nav' + (session.guest ? ' guest' : '')} aria-label="Mobile navigation">
            {WORKSPACE_LINKS.map(([to, Icon, , shortLabel, key]) => access[key] && (
              <NavLink key={to} to={to}><Icon /><span>{shortLabel}</span></NavLink>
            ))}
            <div className="mobile-more-wrap">
              <button
                className={`mobile-more-button${mobileMoreOpen ? ' active' : ''}`}
                aria-label="More navigation options" aria-haspopup="menu" aria-expanded={mobileMoreOpen}
                onClick={() => setMobileMoreOpen(open => !open)}
              >
                <Ellipsis /><span>More</span>
              </button>
              {mobileMoreOpen && (
                <div className="mobile-more-menu" role="menu" aria-label="More navigation">
                  {isAdmin && MOBILE_ADMIN_ITEMS.map(([to, Icon, label]) => (
                    <NavLink key={to} role="menuitem" to={to}><Icon /><span>{label}</span></NavLink>
                  ))}
                  {canManageUsers && <NavLink role="menuitem" to="/access"><ShieldCheck /><span>Users & access</span></NavLink>}
                  {!session.guest && <NavLink role="menuitem" to="/account"><ShieldCheck /><span>Account & API keys</span></NavLink>}
                </div>
              )}
            </div>
          </nav>

          {toast && (
            <div className="toast" role="status">
              <CircleAlert size={17} />{toast}
              <button onClick={() => setToast('')} aria-label="Dismiss"><X size={16} /></button>
            </div>
          )}
        </div>
      </CatalogProvider>
    </AuthGate>
  );
}

class PageErrorBoundary extends Component<{ children: ReactNode }, { hasError: boolean }> {
  state = { hasError: false };
  static getDerivedStateFromError() { return { hasError: true }; }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Page rendering failed:', error, info.componentStack);
  }
  render() {
    if (!this.state.hasError) return this.props.children;
    return (
      <div className="empty-state" role="alert">
        <span className="empty-icon"><CircleAlert size={22} /></span>
        <h2>This page couldn’t load</h2>
        <p>A page error interrupted rendering. Reload the page and try again.</p>
        <button className="secondary" onClick={() => window.location.reload()}>Reload page</button>
      </div>
    );
  }
}

function CatalogNavBadge() {
  const { actionableCount, updates, unseenNew } = useCatalog();
  if (!actionableCount) return null;
  const srText = `${updates.length} update${updates.length === 1 ? '' : 's'} available, ${unseenNew.length} new profile${unseenNew.length === 1 ? '' : 's'}`;
  return (
    <span className="nav-count" title={`${updates.length} update${updates.length === 1 ? '' : 's'}, ${unseenNew.length} new`}>
      {actionableCount}<span className="sr-only"> {srText}</span>
    </span>
  );
}

export default App;
