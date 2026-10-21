export type Job = {id:string;profileId:string;profileVersion:string;sourceFormat:string;targetFormat:string;mode?:'convert'|'compress';state:string;progress:number;attempt:number;maxAttempts:number;error?:string;inputSize?:number;outputSize?:number;savedBytes?:number;savingsPercent?:number;originalState?:string;resultState?:string;createdAt:string;updatedAt:string};
export type BatchAttempt={number:number;state:string;startedAt:string;finishedAt?:string;error?:string};
export type BatchItem={index:number;uploadId:string;fileName:string;jobId?:string;state:string;error?:string;errorCode?:string;job?:Job;attempts?:BatchAttempt[]};
export type Batch={id:string;mode:'convert'|'compress';state:string;total:number;queued:number;running:number;succeeded:number;failed:number;cancelled:number;createdAt:string;items:BatchItem[]};

export type EntryType = 'job' | 'batch';
export type HistoryTotals = { inputBytes: number; outputBytes: number; savedBytes?: number };
// One row in the unified history: a standalone job or a batch parent. Batch
// member jobs are only reachable by expanding their batch.
export type HistoryEntry = {
  type: EntryType;
  createdAt: string;
  fileName?: string;
  ownerId?: string;
  ownerName?: string;
  job?: Job;
  batch?: Batch;
  totals?: HistoryTotals;
};
export type HistoryPageData = { entries: HistoryEntry[]; total: number; limit: number; offset: number };

export type HistoryFilters = {
  q: string;
  mode: '' | 'convert' | 'compress';
  type: '' | EntryType;
  status: string;
  range: '' | '24h' | '7d' | '30d';
  owner: string;
};
export const emptyFilters: HistoryFilters = { q: '', mode: '', type: '', status: '', range: '', owner: '' };
