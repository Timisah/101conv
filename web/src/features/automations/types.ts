export type DriveView = {
  id: string;
  name: string;
  path: string;
  access: 'read' | 'read_write';
  enabled: boolean;
  accessible: boolean;
  writable: boolean;
  freeBytes?: number;
  healthError?: string;
  automationCount: number;
};

export type AutomationCondition = {
  operator: string;
  value?: string;
  bytes?: number;
  children?: AutomationCondition[];
};

export type AutomationGraphNode = {
  id: string;
  blockId: string;
  version: string;
  kind: 'trigger' | 'condition' | 'action' | 'output';
  extensionId?: string;
  extensionVersion?: string;
  config?: Record<string, unknown>;
};

export type AutomationGraph = {
  nodes: AutomationGraphNode[];
  edges: { from: string; to: string; port?: string }[];
};

export type AutomationView = {
  id: string;
  name: string;
  enabled: boolean;
  status: string;
  diagnostic?: string;
  profileNotice?: string;
  revision: number;
  definition: {
    trigger: {
      type: string;
      inputDriveId: string;
      intervalSeconds: number;
      stabilitySeconds: number;
      maxConcurrent: number;
      processExistingOnNextScan: boolean;
    };
    filter: {
      extensions: string[];
      nameGlob: string;
      minBytes: number;
      maxBytes: number;
    };
    condition?: AutomationCondition;
    action: {
      mode: 'convert' | 'compress';
      profileId: string;
      profileVersion: string;
      targetFormat: string;
      options?: Record<string, string>;
      outputDriveId: string;
      outputBehavior: string;
    };
    graph?: AutomationGraph;
  };
  createdAt: string;
  lastExecution?: string;
};

export type BlueprintView = {
  id: string;
  version: string;
  name: string;
  description: string;
  mode: 'convert' | 'compress';
};

export type AutomationRunView = {
  id: string;
  fileName: string;
  state: string;
  jobId: string;
  outputName: string;
  error?: string;
  startedAt: string;
};

export type ExtensionView = {
  manifest: {
    id: string;
    name: string;
    publisher: string;
    version: string;
    extensionApiRange?: { minimum?: string; maximumExclusive?: string };
    blocks?: { id: string; version: string; kind: string; name: string }[];
  };
  compatible: boolean;
  active: boolean;
  status: string;
  diagnostic?: string;
  installedAt: string;
  updatedAt: string;
};

export type AutomationDryRunFile = {
  name: string;
  size: number;
  matches: boolean;
  reason?: string;
};

export type AutomationDryRunPreview = {
  truncated?: boolean;
  files?: AutomationDryRunFile[];
};

export function readCondition(condition?: AutomationCondition) {
  let root = condition;
  let logic: 'and' | 'or' | 'not' = 'and';
  if (root?.operator === 'not') {
    logic = 'not';
    root = root.children?.[0];
  } else if (root?.operator === 'or' || root?.operator === 'and') {
    logic = root.operator;
  }
  const leaves =
    root && (root.operator === 'and' || root.operator === 'or') ? root.children || [] : root ? [root] : [];
  const exts = leaves.filter(item => item.operator === 'extension').map(item => item.value || '');
  const glob = leaves.find(item => item.operator === 'name_glob')?.value || '';
  const min = leaves.find(item => item.operator === 'min_size')?.bytes || 0;
  const max = leaves.find(item => item.operator === 'max_size')?.bytes || 0;
  return {
    logic,
    extensions: exts.join(', '),
    glob,
    min: min ? String(Math.round(min / 1048576)) : '',
    max: max ? String(Math.round(max / 1048576)) : '',
  };
}
