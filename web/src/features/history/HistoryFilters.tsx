import { useEffect, useState } from 'react';
import { ChevronDown, Filter, Search, X } from 'lucide-react';
import type { HistoryFilters as Filters } from './types';

export type OwnerOption = { id: string; name: string };

function Select({ label, ariaLabel, value, onChange, children }: { label: string; ariaLabel?: string; value: string; onChange: (value: string) => void; children: React.ReactNode }) {
  return (
    <label className="filter-field">
      <span>{label}</span>
      <div className="select-wrap">
        <select aria-label={ariaLabel} value={value} onChange={event => onChange(event.target.value)}>{children}</select>
        <ChevronDown size={14} />
      </div>
    </label>
  );
}

// Search is always visible; the other filters collapse behind one button on
// phones and are shown inline on wider screens.
export function HistoryFilters({ filters, activeCount, owners, onChange, onReset }: { filters: Filters; activeCount: number; owners?: OwnerOption[]; onChange: (patch: Partial<Filters>) => void; onReset: () => void }) {
  const [open, setOpen] = useState(false);
  // Typing is debounced so the server is queried once the user pauses.
  const [text, setText] = useState(filters.q);
  useEffect(() => setText(filters.q), [filters.q]);
  useEffect(() => {
    if (text === filters.q) return;
    const timer = setTimeout(() => onChange({ q: text }), 300);
    return () => clearTimeout(timer);
  }, [text, filters.q, onChange]);
  const panelFilters = activeCount - (filters.q ? 1 : 0);
  return (
    <form className="history-filters" role="search" aria-label="Filter jobs and history" onSubmit={event => event.preventDefault()}>
      <div className="history-search-row">
        <div className="search-box">
          <Search size={17} />
          <input aria-label="Search jobs" type="search" placeholder="Search files, formats, IDs" value={text} maxLength={120} onChange={event => setText(event.target.value)} />
          {text && <button type="button" className="icon-btn quiet clear-search" aria-label="Clear search" onClick={() => { setText(''); onChange({ q: '' }); }}><X size={15} /></button>}
        </div>
        <button type="button" className="secondary filter-toggle" aria-expanded={open} aria-controls="history-filter-panel" onClick={() => setOpen(value => !value)}>
          <Filter size={15} /> Filters{panelFilters > 0 && <span className="count-badge" aria-label={`${panelFilters} active`}>{panelFilters}</span>}
        </button>
      </div>
      <div id="history-filter-panel" className={'history-filter-panel' + (open ? ' open' : '')}>
        <Select label="Operation" value={filters.mode} onChange={mode => onChange({ mode: mode as Filters['mode'] })}>
          <option value="">All operations</option><option value="convert">Convert</option><option value="compress">Compress</option>
        </Select>
        <Select label="Type" value={filters.type} onChange={type => onChange({ type: type as Filters['type'], ...(type === 'job' && filters.status === 'partial' ? { status: '' } : {}) })}>
          <option value="">All types</option><option value="job">Single files</option><option value="batch">Batches</option>
        </Select>
        <Select label="Status" ariaLabel="Filter jobs by status" value={filters.status} onChange={status => onChange({ status })}>
          <option value="">All statuses</option>
          {['queued', 'running', 'succeeded', 'failed', 'cancelled'].map(state => <option key={state} value={state}>{state.charAt(0).toUpperCase() + state.slice(1)}</option>)}
          {filters.type !== 'job' && <option value="partial">Partial (batches)</option>}
        </Select>
        <Select label="Time" value={filters.range} onChange={range => onChange({ range: range as Filters['range'] })}>
          <option value="">Any time</option><option value="24h">Last 24 hours</option><option value="7d">Last 7 days</option><option value="30d">Last 30 days</option>
        </Select>
        {owners && (
          <Select label="Owner" value={filters.owner} onChange={owner => onChange({ owner })}>
            <option value="">All owners</option>
            {owners.map(owner => <option key={owner.id} value={owner.id}>{owner.name}</option>)}
          </Select>
        )}
        {activeCount > 0 && <button type="button" className="text-btn reset-filters" onClick={onReset}>Reset filters</button>}
      </div>
    </form>
  );
}
