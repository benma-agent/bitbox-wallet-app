// SPDX-License-Identifier: Apache-2.0

import { subscribeEndpoint } from './subscribe';
import { apiGet, apiPost } from '@/utils/request';

export type TBitBoxSyncStatus = {
  baseURL: string;
  keystores: {
    [rootFingerprint: string]: TBitBoxSyncKeystoreStatus;
  };
};

export type TBitBoxSyncKeystoreStatus = {
  name: string;
  status: TBitBoxSyncKeystoreRuntimeStatus;
};

export type TBitBoxSyncKeystoreRuntimeStatus = {
  running: boolean;
  defaultNamespaceID?: string;
  lastSync?: string;
  lastError?: string;
  authStatus: TBitBoxSyncKeystoreAuthStatus;
};

export type TBitBoxSyncKeystoreAuthStatus = {
  daysLeft?: number;
  loginRequired?: boolean;
  refreshRecommended?: boolean;
};

export type TBitBoxSyncResponse = {
  success: true;
  status: TBitBoxSyncStatus;
} | {
  success: false;
  errorCode?: string;
  errorMessage?: string;
  status: TBitBoxSyncStatus;
};

export const getBitBoxSyncStatus = (): Promise<TBitBoxSyncStatus> => {
  return apiGet('bitboxsync/status');
};

export const getBitBoxSyncAuthStatus = (): Promise<TBitBoxSyncStatus> => {
  return apiGet('bitboxsync/auth-status');
};

export const syncBitBoxSyncAuthStatus = (
  cb: (status: TBitBoxSyncStatus) => void,
) => {
  return subscribeEndpoint('bitboxsync/auth-status', cb);
};

export const enableBitBoxSync = (rootFingerprint: string): Promise<TBitBoxSyncResponse> => {
  return apiPost('bitboxsync/enable', { rootFingerprint });
};

export const loginBitBoxSync = (rootFingerprint: string): Promise<TBitBoxSyncResponse> => {
  return apiPost('bitboxsync/login', { rootFingerprint });
};

export const syncBitBoxSync = (rootFingerprint: string): Promise<TBitBoxSyncResponse> => {
  return apiPost('bitboxsync/sync', { rootFingerprint });
};

export const disableBitBoxSync = (rootFingerprint: string): Promise<TBitBoxSyncResponse> => {
  return apiPost('bitboxsync/disable', { rootFingerprint });
};
