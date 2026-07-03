// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type {
  TBitBoxSyncKeystoreAuthStatus,
  TBitBoxSyncKeystoreStatus,
  TBitBoxSyncStatus,
} from '@/api/bitboxsync';

const bitBoxSyncApi = vi.hoisted(() => ({
  authStatus: {
    baseURL: '',
    keystores: {},
  } as TBitBoxSyncStatus,
  getBitBoxSyncAuthStatus: vi.fn(),
  loginBitBoxSync: vi.fn(),
  syncBitBoxSyncAuthStatus: vi.fn(),
}));

vi.mock('@/api/bitboxsync', () => ({
  getBitBoxSyncAuthStatus: bitBoxSyncApi.getBitBoxSyncAuthStatus,
  loginBitBoxSync: bitBoxSyncApi.loginBitBoxSync,
  syncBitBoxSyncAuthStatus: bitBoxSyncApi.syncBitBoxSyncAuthStatus,
}));

vi.mock('@/components/alert/Alert', () => ({
  alertUser: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, string | number>) => {
      const value = (input: string | number | undefined) => input === undefined ? '' : String(input);
      switch (key) {
      case 'bitBoxSync.auth.loginRequired':
        return `expired:${value(values?.name)}`;
      case 'bitBoxSync.auth.refreshRecommended.days':
        return `refresh:${value(values?.name)}:${value(values?.days)}d`;
      case 'bitBoxSync.auth.refreshRecommended.unknown':
        return `refresh:${value(values?.name)}:soon`;
      case 'bitBoxSync.auth.refreshRecommended.action':
        return 'action';
      case 'button.connect':
        return 'Connect';
      default:
        return key;
      }
    },
  }),
}));

import { BitBoxSyncSessionBanners } from './bitboxsync-session';

const bitBoxSyncStatus = (keystores: TBitBoxSyncStatus['keystores']): TBitBoxSyncStatus => ({
  baseURL: '',
  keystores,
});

const keystoreStatus = (authStatus: TBitBoxSyncKeystoreAuthStatus): TBitBoxSyncKeystoreStatus => ({
  name: 'BitBox 02',
  status: {
    running: false,
    authStatus,
  },
});

describe('BitBoxSyncSessionBanners', () => {
  beforeEach(() => {
    bitBoxSyncApi.authStatus = bitBoxSyncStatus({});
    bitBoxSyncApi.getBitBoxSyncAuthStatus.mockImplementation(() => Promise.resolve(bitBoxSyncApi.authStatus));
    bitBoxSyncApi.loginBitBoxSync.mockResolvedValue({
      success: true,
      status: { baseURL: '', keystores: {} },
    });
    bitBoxSyncApi.syncBitBoxSyncAuthStatus.mockImplementation(() => () => {});
  });

  it('shows an expired session banner with the keystore name', async () => {
    bitBoxSyncApi.authStatus = bitBoxSyncStatus({
      f23ab988: keystoreStatus({ loginRequired: true }),
    });

    render(<BitBoxSyncSessionBanners />);

    expect(await screen.findByText('expired:BitBox 02')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeInTheDocument();
  });

  it('shows a refresh banner with the keystore name and rounded days left', async () => {
    bitBoxSyncApi.authStatus = bitBoxSyncStatus({
      f23ab988: keystoreStatus({ daysLeft: 28, refreshRecommended: true }),
    });

    render(<BitBoxSyncSessionBanners />);

    expect(await screen.findByText('refresh:BitBox 02:28d action')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeInTheDocument();
  });

  it('shows a refresh banner as soon when expiry is less than two days away', async () => {
    bitBoxSyncApi.authStatus = bitBoxSyncStatus({
      f23ab988: keystoreStatus({ daysLeft: 1, refreshRecommended: true }),
    });

    render(<BitBoxSyncSessionBanners />);

    expect(await screen.findByText('refresh:BitBox 02:soon action')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeInTheDocument();
  });

  it('does not show banners for clear statuses', async () => {
    bitBoxSyncApi.authStatus = bitBoxSyncStatus({
      f23ab988: keystoreStatus({}),
    });

    const { container } = render(<BitBoxSyncSessionBanners />);

    await waitFor(() => {
      expect(bitBoxSyncApi.getBitBoxSyncAuthStatus).toHaveBeenCalled();
    });
    expect(container).toBeEmptyDOMElement();
  });

  it('connects the keystore and reloads auth status', async () => {
    bitBoxSyncApi.authStatus = bitBoxSyncStatus({
      f23ab988: keystoreStatus({ loginRequired: true }),
    });
    bitBoxSyncApi.loginBitBoxSync.mockImplementation(async () => {
      bitBoxSyncApi.authStatus = bitBoxSyncStatus({
        f23ab988: keystoreStatus({}),
      });
      return {
        success: true,
        status: { baseURL: '', keystores: {} },
      };
    });

    render(<BitBoxSyncSessionBanners />);

    await userEvent.click(await screen.findByRole('button', { name: 'Connect' }));

    expect(bitBoxSyncApi.loginBitBoxSync).toHaveBeenCalledWith('f23ab988');
    await waitFor(() => {
      expect(screen.queryByText('expired:BitBox 02')).not.toBeInTheDocument();
    });
  });
});
