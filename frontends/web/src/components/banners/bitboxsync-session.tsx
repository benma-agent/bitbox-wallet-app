// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  getBitBoxSyncAuthStatus,
  loginBitBoxSync,
  syncBitBoxSyncAuthStatus,
} from '@/api/bitboxsync';
import { alertUser } from '@/components/alert/Alert';
import { Button } from '@/components/forms';
import { SessionStatus } from '@/components/status/status-session';
import { useSync } from '@/hooks/api';
import styles from './bitboxsync-session.module.css';
import type { TBitBoxSyncKeystoreStatus, TBitBoxSyncStatus } from '@/api/bitboxsync';

type KeystoreAuthBanner = {
  rootFingerprint: string;
  status: TBitBoxSyncKeystoreStatus;
};

export const BitBoxSyncSessionBanners = () => {
  const { t } = useTranslation();
  const liveStatus = useSync(getBitBoxSyncAuthStatus, syncBitBoxSyncAuthStatus);
  const [bitBoxSyncStatus, setBitBoxSyncStatus] = useState<TBitBoxSyncStatus>();
  const [busyRootFingerprint, setBusyRootFingerprint] = useState<string>();

  useEffect(() => {
    setBitBoxSyncStatus(liveStatus);
  }, [liveStatus]);

  const connect = async (rootFingerprint: string) => {
    try {
      setBusyRootFingerprint(rootFingerprint);
      const result = await loginBitBoxSync(rootFingerprint);
      if (!result.success) {
        if (result.errorCode !== 'userAbort') {
          alertUser(result.errorMessage || t('genericError'));
        }
        return;
      }
      setBitBoxSyncStatus(await getBitBoxSyncAuthStatus());
    } catch (err) {
      console.error(err);
      alertUser(t('genericError'));
    } finally {
      setBusyRootFingerprint(undefined);
    }
  };

  const banners: KeystoreAuthBanner[] = Object
    .entries(bitBoxSyncStatus?.keystores || {})
    .filter(([, keystoreStatus]) => (
      keystoreStatus.status.authStatus.loginRequired || keystoreStatus.status.authStatus.refreshRecommended
    ))
    .map(([rootFingerprint, status]) => ({ rootFingerprint, status }));

  if (banners.length === 0) {
    return null;
  }

  return (
    <>
      {banners.map(({ rootFingerprint, status }) => {
        const { authStatus: keystoreAuthStatus } = status.status;
        const loginRequired = Boolean(
          keystoreAuthStatus.loginRequired
          || (keystoreAuthStatus.refreshRecommended && keystoreAuthStatus.daysLeft === 0),
        );
        const message = loginRequired
          ? t('bitBoxSync.auth.loginRequired', { name: status.name })
          : keystoreAuthStatus.daysLeft !== undefined && keystoreAuthStatus.daysLeft >= 2
            ? `${t('bitBoxSync.auth.refreshRecommended.days', {
              days: keystoreAuthStatus.daysLeft,
              name: status.name,
            })} ${t('bitBoxSync.auth.refreshRecommended.action')}`
            : `${t('bitBoxSync.auth.refreshRecommended.unknown', {
              name: status.name,
            })} ${t('bitBoxSync.auth.refreshRecommended.action')}`;
        return (
          <SessionStatus
            key={rootFingerprint}
            type={loginRequired ? 'error' : 'warning'}
            dismissibleKey="">
            <div className={styles.content}>
              <span>{message}</span>
              <Button
                secondary
                disabled={busyRootFingerprint === rootFingerprint}
                onClick={() => void connect(rootFingerprint)}>
                {t('button.connect')}
              </Button>
            </div>
          </SessionStatus>
        );
      })}
    </>
  );
};
