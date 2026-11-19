import { normalizeFormat, type CapabilityOption } from '../capabilities.ts';

export type Engine = {
  definition: {
    id: string;
    name: string;
    version: string;
    description?: string;
    capabilities?: string[];
    license?: string;
  };
  installation: {
    managed: boolean;
    root?: string;
    executablePaths: Record<string, string>;
    repositoryId?: string;
  };
};

export type CompressionOption = CapabilityOption;

export type Preset = {
  id: string;
  name: string;
  description?: string;
  mode: 'convert' | 'compress';
  sourceFormat: string;
  targetFormat: string;
  providerId?: string;
  profileId?: string;
  profileVersion?: string;
  options?: Record<string, string>;
  shared: boolean;
  default: boolean;
  compatible: boolean;
  compatibilityMessage?: string;
  createdAt: string;
  updatedAt: string;
};

export type Profile = {
  definition: {
    id: string;
    type?: 'conversion' | 'compression';
    name: string;
    version: string;
    description?: string;
    license?: string;
    engines: {
      engineId: string;
      versions: { minimum?: string; maximumExclusive?: string };
      capabilities: string[];
    }[];
    rules: {
      engineId: string;
      executable: string;
      sources: string[];
      targets: string[];
      arguments: string[];
      options?: CompressionOption[];
      compressionArguments?: string[];
      streamSelection?: string;
      preserveMetadata?: boolean;
      hdrPolicy?: string;
      dynamicHDRPolicy?: string;
    }[];
    compression?: {
      options: CompressionOption[];
      targetQualityOption?: string;
    };
  };
  state: {
    enabled: boolean;
    active?: boolean;
    repositoryId?: string;
    resolvedEngines?: Record<string, string>;
  };
};

export type ProfileChoice = {
  id: string;
  name: string;
  version: string;
};

export type PreferenceMapping = {
  mode: 'convert' | 'compress';
  sourceFormat: string;
  targetFormat: string;
  globalProfileId?: string;
  userProfileId?: string;
  effectiveProfileId?: string;
  effectiveSource: string;
  globalAvailable: boolean;
  userAvailable: boolean;
  alternatives: ProfileChoice[];
};

export const ext = (name: string): string =>
  name.includes('.') ? normalizeFormat(name.split('.').pop()!) : '';

export const supportsMapping = (
  profile: Profile,
  source: string,
  target?: string,
  compression = false,
): boolean =>
  profile.definition.rules.some(
    rule =>
      (!compression || rule.compressionArguments?.length) &&
      rule.sources.some(item => item.toLowerCase().replace(/^\./, '') === source) &&
      (!target || rule.targets.some(item => item.toLowerCase().replace(/^\./, '') === target)),
  );

export function automaticProfileLabel(
  sources: string[],
  target: string,
  preferences: PreferenceMapping[],
): string {
  if (!sources.length || !target) return 'Automatic · use saved preferences';
  const mappings = Array.from(new Set(sources)).map(source =>
    preferences.find(row => row.sourceFormat === source && row.targetFormat === target),
  );
  const ids = Array.from(new Set(mappings.map(row => row?.effectiveProfileId || '')));
  if (ids.length > 1) return 'Automatic · resolve each format mapping';
  const row = mappings[0];
  if (!row?.effectiveProfileId) return 'Automatic · no compatible profile';
  const choice = row.alternatives.find(item => item.id === row.effectiveProfileId);
  return `Automatic · ${choice?.name || row.effectiveProfileId} (${row.effectiveSource})`;
}

export const idempotencyKey = (): string =>
  globalThis.crypto?.randomUUID
    ? globalThis.crypto.randomUUID()
    : Array.from(globalThis.crypto.getRandomValues(new Uint8Array(16)), byte =>
        byte.toString(16).padStart(2, '0'),
      ).join('');
