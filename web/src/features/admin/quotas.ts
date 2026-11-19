import { bytes } from '../format.ts';

export type QuotaLimit = {
  mode: 'limited' | 'unlimited' | 'inherit';
  value?: number;
};

export type UserQuotas = {
  maxFileBytes: QuotaLimit;
  monthlyJobs: QuotaLimit;
  monthlyBytes: QuotaLimit;
};

export type AccessUser = {
  id: string;
  username: string;
  role: string;
  roleId: string;
  enabled: boolean;
  canConvert: boolean;
  canReadJobs: boolean;
  maxFileBytes: number;
  monthlyJobs: number;
  monthlyBytes: number;
  createdAt: string;
  groups: string[];
  groupIds: string[];
  permissions: Record<string, boolean>;
  permissionOrigins: Record<string, { source: string; effect: boolean }[]>;
  quotas: UserQuotas;
  usage?: { uploads: number; inputBytes: number; jobs: number };
};

export type AccessGroup = {
  id: string;
  name: string;
  description: string;
  roleId?: string;
  permissions: Record<string, boolean>;
  quotas: UserQuotas;
  memberCount: number;
};

export type AccessRole = {
  id: string;
  name: string;
  description: string;
  permissions: Record<string, boolean>;
  builtIn: boolean;
};

export function maxFileQuotaText(quota: QuotaLimit | undefined, effective: number): string {
  if (!quota) return bytes(effective);
  if (quota.mode === 'unlimited' || quota.mode === 'inherit') {
    return `${bytes(effective)} instance maximum`;
  }
  return quota.value ? bytes(quota.value) : bytes(effective);
}

export function quotaText(quota: QuotaLimit | undefined, legacy: number): string {
  if (!quota) return legacy ? String(legacy) : 'Unlimited';
  if (quota.mode === 'unlimited') return 'Unlimited';
  if (quota.mode === 'inherit') return 'Inherited';
  if (quota.value === undefined) return '—';
  return quota.value > 1024 * 1024 ? bytes(quota.value) : String(quota.value);
}
