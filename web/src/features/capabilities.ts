export type CapabilityOption = { key: string; label: string; type: 'number' | 'select' | 'boolean'; default: string; min?: number; max?: number; step?: number; choices?: string[]; targetSizeMb?: boolean };
export type CanonicalCapability = { id: string; operation: 'convert' | 'compress'; sourceFormat: string; targetFormat: string; providerIds: string[] };
export type CanonicalProviderCapability = { capabilityId: string; options?: CapabilityOption[] };
export type CanonicalProvider = { id: string; profileId: string; version: string; name: string; description?: string; license?: string; engineId: string; engineVersion?: string; enabled: boolean; installed: boolean; available: boolean; capabilities: CanonicalProviderCapability[] };
export type CapabilityCatalog = { schemaVersion: number; capabilities: CanonicalCapability[]; providers: CanonicalProvider[] };

export const normalizeFormat = (value: string) => value.trim().toLowerCase().replace(/^\./, '');
export const profileIdFromProvider = (providerId = '') => {
  const legacy = providerId.startsWith('legacy/') ? providerId.slice('legacy/'.length) : providerId;
  const at = legacy.indexOf('@');
  return (at > 0 ? legacy.slice(0, at) : legacy).split('/')[0];
};

const versionParts = (value = '') => value.split(/[.+-]/).slice(0, 3).map(part => Number(part) || 0);
const compareVersions = (a = '', b = '') => {
  const left = versionParts(a);
  const right = versionParts(b);
  for (let i = 0; i < 3; i += 1) {
    if ((left[i] || 0) !== (right[i] || 0)) return (left[i] || 0) - (right[i] || 0);
  }
  return 0;
};

export const providerSupports = (provider: CanonicalProvider, operation: CanonicalCapability['operation'], source: string, target: string) => {
  const id = `${operation}:${normalizeFormat(source)}:${normalizeFormat(target)}`;
  return provider.capabilities.some(capability => capability.capabilityId === id);
};

export const capabilityFor = (catalog: CapabilityCatalog, operation: CanonicalCapability['operation'], source: string, target: string) =>
  catalog.capabilities.find(capability => capability.operation === operation && capability.sourceFormat === normalizeFormat(source) && capability.targetFormat === normalizeFormat(target));

export const availableProviders = (catalog: CapabilityCatalog) => catalog.providers.filter(provider => provider.enabled && provider.installed && provider.available);

export const providersFor = (catalog: CapabilityCatalog, operation: CanonicalCapability['operation']) =>
  availableProviders(catalog).filter(provider => provider.capabilities.some(capability => capability.capabilityId.startsWith(`${operation}:`)));

export const providerOrder = (left: CanonicalProvider, right: CanonicalProvider) => {
  if (left.profileId !== right.profileId) return left.profileId.localeCompare(right.profileId);
  const profileVersion = compareVersions(right.version, left.version);
  if (profileVersion) return profileVersion;
  if (left.engineId !== right.engineId) return left.engineId.localeCompare(right.engineId);
  return compareVersions(right.engineVersion, left.engineVersion);
};

export const resolveProvider = (catalog: CapabilityCatalog, operation: CanonicalCapability['operation'], sources: string[], target: string, preferredProfileId = '') => {
  const candidates = availableProviders(catalog).filter(provider => sources.length > 0 && sources.every(source => providerSupports(provider, operation, source, target)));
  const preferred = candidates.find(provider => provider.id === preferredProfileId || provider.profileId === preferredProfileId);
  return preferred || [...candidates].sort(providerOrder)[0];
};

export const outputFormats = (catalog: CapabilityCatalog, operation: CanonicalCapability['operation'], sources: string[]) => {
  const normalizedSources = sources.map(normalizeFormat);
  if (!normalizedSources.length) return [];
  const providers = availableProviders(catalog);
  return [...new Set(catalog.capabilities
    .filter(capability => capability.operation === operation && providers.some(provider => capability.providerIds.includes(provider.id)))
    .map(capability => capability.targetFormat))]
    .filter(target => normalizedSources.every(source => providers.some(provider => providerSupports(provider, operation, source, target))))
    .sort();
};

export const inputFormats = (catalog: CapabilityCatalog, operation: CanonicalCapability['operation']) =>
  [...new Set(providersFor(catalog, operation).flatMap(provider => provider.capabilities
    .filter(capability => capability.capabilityId.startsWith(`${operation}:`))
    .map(capability => capability.capabilityId.split(':')[1])))].sort();

export const optionsFor = (catalog: CapabilityCatalog, operation: CanonicalCapability['operation'], sources: string[], target: string, preferredProfileId = '') => {
  const provider = resolveProvider(catalog, operation, sources, target, preferredProfileId);
  if (!provider) return { provider: undefined, options: [] as CapabilityOption[], consistent: false };
  const schemas = sources.map(source => provider.capabilities.find(capability => capability.capabilityId === `${operation}:${normalizeFormat(source)}:${normalizeFormat(target)}`)?.options || []);
  const first = schemas[0] || [];
  const consistent = schemas.every(schema => JSON.stringify(schema) === JSON.stringify(first));
  return { provider, options: consistent ? first : [], consistent };
};

export const unavailableCapabilityMessage = (catalog: CapabilityCatalog, operation: CanonicalCapability['operation'], sources: string[]) => {
  const normalizedSources = sources.map(normalizeFormat);
  const declared = catalog.capabilities.filter(capability => capability.operation === operation && normalizedSources.every(source => catalog.capabilities.some(candidate => candidate.operation === operation && candidate.sourceFormat === source && candidate.targetFormat === capability.targetFormat)));
  if (!declared.length) return `No verified ${operation} capability is declared for ${normalizedSources.map(source => `.${source}`).join(', ')}.`;
  return `A ${operation} capability is declared for these files, but no enabled installed provider can produce a common output. An administrator can install or enable the required engine and profile.`;
};
