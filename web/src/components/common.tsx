import type { ReactNode } from 'react';
import { CircleAlert, File, LoaderCircle } from 'lucide-react';
import { pretty } from '../features/format';

export function PageHeading({title,description,action}:{eyebrow?:string;title:string;description:string;action?:ReactNode}){return <div className="page-heading"><div><h1>{title}</h1><p className="page-description">{description}</p></div>{action}</div>}
export function StatePill({state}:{state:string}){return <span className={'state-pill '+state.toLowerCase()}><i/>{pretty(state)}</span>}
export function Empty({icon:Icon=File, title, detail, action,children}:{icon?:any;title:string;detail:string;action?:ReactNode;children?:ReactNode}){return <div className="empty-state"><span className="empty-icon"><Icon size={22}/></span><h3>{title}</h3><p>{detail}</p>{action||children}</div>}

// A pill with explicit wording and a semantic tone, for statuses that are not job states.
export type Tone = 'ok' | 'warn' | 'bad' | 'info' | 'muted';
const toneClass: Record<Tone, string> = { ok: 'succeeded', warn: 'queued', bad: 'failed', info: 'running', muted: 'cancelled' };
export function StatusPill({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className={'state-pill ' + toneClass[tone]}><i />{children}</span>;
}
export function ErrorState({ message, retry }: { message: string; retry: () => void }) {
  return <div className="banner error"><CircleAlert size={18} /><span>{message}</span><button className="text-btn" onClick={retry}>Retry</button></div>;
}
export function Loading() { return <div className="loading-row"><LoaderCircle className="spin" /> Loading…</div>; }
