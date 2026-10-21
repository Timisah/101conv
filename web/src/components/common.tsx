import type { ReactNode } from 'react';
import { File } from 'lucide-react';
import { pretty } from '../features/format';

export function PageHeading({title,description,action}:{eyebrow?:string;title:string;description:string;action?:ReactNode}){return <div className="page-heading"><div><h1>{title}</h1><p className="page-description">{description}</p></div>{action}</div>}
export function StatePill({state}:{state:string}){return <span className={'state-pill '+state.toLowerCase()}><i/>{pretty(state)}</span>}
export function Empty({icon:Icon=File, title, detail, action,children}:{icon?:any;title:string;detail:string;action?:ReactNode;children?:ReactNode}){return <div className="empty-state"><span className="empty-icon"><Icon size={22}/></span><h3>{title}</h3><p>{detail}</p>{action||children}</div>}
