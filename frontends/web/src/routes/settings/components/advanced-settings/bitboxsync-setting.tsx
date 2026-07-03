// SPDX-License-Identifier: Apache-2.0

import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  disableBitBoxSync,
  enableBitBoxSync,
  getBitBoxSyncStatus,
  syncBitBoxSync,
} from '@/api/bitboxsync';
import { alertUser } from '@/components/alert/Alert';
import { Button } from '@/components/forms';
import { ConnectedKeystore } from '@/components/keystore/connected-keystore';
import { useLoad } from '@/hooks/api';
import { getAccountsByKeystore } from '@/routes/account/utils';
import { SettingsItem } from '@/routes/settings/components/settingsItem/settingsItem';
import styles from './bitboxsync-setting.module.css';
import type { TAccount, TKeystore } from '@/api/account';
import type { TBitBoxSyncKeystoreStatus, TBitBoxSyncResponse, TBitBoxSyncStatus } from '@/api/bitboxsync';
import type { TAccountsByKeystore } from '@/routes/account/utils';

type TProps = {
  accounts: TAccount[];
};

type TKeystoreRowProps = {
  accountsByKeystore: TAccountsByKeystore[];
  busy: boolean;
  keystore: TKeystore;
  onDisable: () => void;
  onEnable: (keystore: TKeystore) => void;
  onSync: () => void;
  statusLoaded: boolean;
  status?: TBitBoxSyncKeystoreStatus;
};

const KeystoreRow = ({
  accountsByKeystore,
  busy,
  keystore,
  onDisable,
  onEnable,
  onSync,
  statusLoaded,
  status,
}: TKeystoreRowProps) => {
  const { t } = useTranslation();
  const enabled = Boolean(status);
  const actionsDisabled = busy || !statusLoaded;

  return (
    <div className={styles.keystoreRow}>
      <ConnectedKeystore
        accountsByKeystore={accountsByKeystore}
        className={styles.keystore}
        keystore={keystore}
      />
      <span className={styles.state}>
        {enabled
          ? t('generic.enabled_true')
          : t('generic.enabled_false')}
      </span>
      <div className={styles.actions}>
        {enabled ? (
          <>
            <Button
              secondary
              disabled={actionsDisabled}
              onClick={onSync}>
              {t('newSettings.sync.bitboxSync.sync')}
            </Button>
            <Button
              secondary
              disabled={actionsDisabled}
              onClick={onDisable}>
              {t('generic.disable')}
            </Button>
          </>
        ) : (
          <Button
            secondary
            disabled={actionsDisabled}
            onClick={() => onEnable(keystore)}>
            {t('generic.enable')}
          </Button>
        )}
      </div>
    </div>
  );
};

export const BitBoxSyncSetting = ({ accounts }: TProps) => {
  const { t } = useTranslation();
  const loadedStatus = useLoad(getBitBoxSyncStatus);
  const accountsByKeystore = useMemo(() => getAccountsByKeystore(accounts), [accounts]);
  const [status, setStatus] = useState<TBitBoxSyncStatus>();
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setStatus(loadedStatus);
  }, [loadedStatus]);

  const runAction = async (action: () => Promise<TBitBoxSyncResponse | undefined>) => {
    try {
      setBusy(true);
      const result = await action();
      if (!result) {
        return;
      }
      setStatus(result.status);
      if (!result.success) {
        if (result.errorCode === 'userAbort') {
          return;
        }
        alertUser(result.errorMessage || t('genericError'));
      }
    } catch (err) {
      console.error(err);
      alertUser(t('genericError'));
    } finally {
      setBusy(false);
    }
  };

  const enableForKeystore = (keystore: TKeystore) => {
    void runAction(() => enableBitBoxSync(keystore.rootFingerprint));
  };

  const keystoreStatuses = status ? Object.values(status.keystores) : [];
  const secondaryText = (() => {
    if (!status) {
      return t('loading');
    }
    const lastError = keystoreStatuses.find(keystoreStatus => keystoreStatus.status.lastError)?.status.lastError;
    if (lastError) {
      return t('generic.lastError', { error: lastError });
    }
    const lastSync = keystoreStatuses
      .map(keystoreStatus => keystoreStatus.status.lastSync)
      .filter((value): value is string => Boolean(value))
      .sort()
      .pop();
    if (lastSync) {
      return t('newSettings.sync.bitboxSync.lastSync', {
        time: new Date(lastSync).toLocaleString(),
      });
    }
    return t('newSettings.sync.bitboxSync.description', {
      url: status.baseURL,
    });
  })();

  return (
    <SettingsItem
      collapseOnSmall
      settingName={t('newSettings.sync.bitboxSync.title')}
      secondaryText={secondaryText}
      displayedValue={accountsByKeystore.length === 0
        ? t('generic.enabled_false')
        : undefined}
      extraComponent={accountsByKeystore.length === 0 ? undefined : (
        <div className={styles.keystores}>
          {accountsByKeystore.map(({ keystore }) => (
            <KeystoreRow
              key={keystore.rootFingerprint}
              accountsByKeystore={accountsByKeystore}
              busy={busy}
              keystore={keystore}
              onDisable={() => void runAction(() => disableBitBoxSync(keystore.rootFingerprint))}
              onEnable={enableForKeystore}
              onSync={() => void runAction(() => syncBitBoxSync(keystore.rootFingerprint))}
              statusLoaded={Boolean(status)}
              status={status?.keystores[keystore.rootFingerprint]}
            />
          ))}
        </div>
      )}
    />
  );
};
