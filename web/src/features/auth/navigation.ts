import {
  Activity,
  AudioLines,
  BarChart3,
  Boxes,
  Clock3,
  Eye,
  FilePlus2,
  FileUp,
  FolderOpen,
  HardDrive,
  Search,
  Settings2,
  ShieldCheck,
  Workflow,
  Zap,
  type LucideIcon,
} from 'lucide-react';

export type NavEntry = [string, LucideIcon, string];
export type WorkspaceAccessKey = 'convert' | 'compress' | 'jobs';

export const WORKSPACE_LINKS: [
  string,
  LucideIcon,
  string,
  string,
  WorkspaceAccessKey,
  boolean?,
][] = [
  ['/convert', FileUp, 'Convert files', 'Convert', 'convert', true],
  ['/compress', Zap, 'Compress files', 'Compress', 'compress'],
  ['/build', FilePlus2, 'Build document', 'Build', 'convert'],
  ['/inspect', Eye, 'Inspect files', 'Inspect', 'jobs'],
  ['/jobs', Clock3, 'Jobs & history', 'Jobs', 'jobs'],
];

export const ADMIN_SECTIONS: [string, string, NavEntry[]][] = [
  [
    'AUTOMATION',
    'Automation administration',
    [
      ['/drives', FolderOpen, 'Mounted drives'],
      ['/automations', Workflow, 'Automations'],
    ],
  ],
  [
    'INSTANCE',
    'Instance administration',
    [
      ['/storage', HardDrive, 'Storage'],
      ['/operations', Activity, 'Operations'],
      ['/analytics', BarChart3, 'Analytics'],
      ['/admin', Settings2, 'Repositories & settings'],
      ['/access', ShieldCheck, 'Users & access'],
    ],
  ],
];

export const MOBILE_ADMIN_ITEMS: NavEntry[] = [
  ['/drives', FolderOpen, 'Mounted drives'],
  ['/automations', Workflow, 'Automations'],
  ['/converters?tab=conversion', Zap, 'Conversion profiles'],
  ['/converters?tab=compression', AudioLines, 'Compression profiles'],
  ['/converters?tab=engines', Boxes, 'Engines'],
  ['/profile-defaults', Settings2, 'Profile defaults'],
  ['/marketplace', Search, 'Catalog'],
  ['/storage', HardDrive, 'Storage'],
  ['/operations', Activity, 'Operations'],
  ['/analytics', BarChart3, 'Analytics'],
  ['/admin', Settings2, 'Admin'],
];

const ROUTE_LABELS: Record<string, string> = {
  '/convert': 'Convert files',
  '/compress': 'Compress files',
  '/build': 'Build document',
  '/inspect': 'Inspect files',
  '/jobs': 'Jobs & history',
  '/marketplace': 'Repository catalog',
  '/admin': 'Repositories & settings',
  '/storage': 'Storage',
  '/operations': 'Operations',
  '/analytics': 'Processing analytics',
  '/account': 'Account & API keys',
  '/profile-defaults': 'Profile defaults',
  '/drives': 'Mounted drives',
  '/automations': 'Automations',
  '/access': 'Users & access',
};

export function sectionFor(path: string): string {
  if (path === '/admin') return 'Administration';
  if (path === '/converters' || path === '/marketplace') return 'Converter setup';
  return 'Workspace';
}

export function labelFor(path: string, search = ''): string {
  const tab = new URLSearchParams(search).get('tab');
  if (path === '/converters') {
    return tab === 'engines' ? 'Engines' : tab === 'compression' ? 'Compression profiles' : 'Conversion profiles';
  }
  if (path === '/marketplace') {
    return tab === 'engines'
      ? 'Available engines'
      : tab === 'compression'
        ? 'Compression profiles'
        : 'Conversion profiles';
  }
  return ROUTE_LABELS[path] || 'Convert files';
}
