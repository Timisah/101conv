export type Repo = { id: string; name: string; url: string; kind: string; trust: string; enabled: boolean; status: string; lastRefresh?: string; lastError?: string };
export type Package = { id: string; type?: 'conversion' | 'compression'; version: string; name: string; engines?: any[]; rules?: any[]; compression?: unknown; license?: string; description?: string };
export type Catalog = { repository: Repo; engines: Package[]; profiles: Package[]; incompatibleProfiles: { id: string; version: string; reason: string }[] };
export type Dependency = { engineId: string; engineName?: string; engineLicense?: string; installedVersion?: string; action: string };
export type Preview = {
  planId: string; publisher: string; trust: string; expiresAt?: string; license?: string;
  dependencies?: Dependency[]; plannedChanges?: string[]; repository: Repo;
  // Present for an update: the installed version and a readable summary of what differs.
  installedVersion?: string; profileChanges?: string[];
};
export type InstalledProfile = { definition: { id: string; name: string; version: string; type?: 'conversion' | 'compression' }; state: { enabled: boolean } };
export type InstalledEngine = { definition: { id: string; name: string; version: string } };

export type CatalogEntry = { repoId: string; repo: Repo; kind: 'profile' | 'engine'; pkg: Package };
export type Status =
  | { kind: 'new' }
  | { kind: 'update'; installed: string; available: string }
  | { kind: 'installed'; version: string }
  | { kind: 'engine-version'; installed: string };
