// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"testing"
	"time"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts"
	accountsTypes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/types"
	coinpkg "github.com/BitBoxSwiss/bitbox-wallet-app/backend/coins/coin"
	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/config"
	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/signing"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
	"github.com/stretchr/testify/require"
)

func accountNameSetters(
	accountsProvider func() []accounts.Interface,
	rootFingerprint []byte,
) (AccountConfigNameSetter, AccountConfigNameConditionalSetter) {
	set := func(accountCode accountsTypes.Code, name string, modifiedAt time.Time) error {
		account := accountByCode(accountConfigSyncAccounts(accountsProvider(), rootFingerprint), accountCode)
		if account == nil {
			return errp.New("account not found")
		}
		if account.Config().Config.Name == name &&
			accountNameModifiedAtEqual(account.Config().Config.NameModifiedAt, modifiedAt) {
			return nil
		}
		account.Config().Config.Name = name
		account.Config().Config.NameModifiedAt = accountNameModifiedAtPtr(modifiedAt)
		return nil
	}
	setIfCurrent := func(
		accountCode accountsTypes.Code,
		current string,
		currentModifiedAt time.Time,
		currentFound bool,
		name string,
		modifiedAt time.Time,
	) (bool, error) {
		account := accountByCode(accountConfigSyncAccounts(accountsProvider(), rootFingerprint), accountCode)
		if account == nil {
			return false, errp.New("account not found")
		}
		existingFound := account.Config().Config.Name != ""
		if existingFound != currentFound {
			return false, nil
		}
		if currentFound &&
			(account.Config().Config.Name != current ||
				!accountNameModifiedAtEqual(account.Config().Config.NameModifiedAt, currentModifiedAt)) {
			return false, nil
		}
		account.Config().Config.Name = name
		account.Config().Config.NameModifiedAt = accountNameModifiedAtPtr(modifiedAt)
		return true, nil
	}
	return set, setIfCurrent
}

func accountConfigValueWithName(name string, modifiedAt time.Time) accountConfigValue {
	return accountConfigValue{
		Name: &modifiedString{
			Value:      name,
			ModifiedAt: modifiedAt,
		},
	}
}

func TestAccountConfigKeyEncoding(t *testing.T) {
	key, err := encodeAccountConfigKey("v0-test-btc-0")
	require.NoError(t, err)
	require.Equal(t, "v0-test-btc-0", key)
	accountCode, err := decodeAccountConfigKey(key)
	require.NoError(t, err)
	require.Equal(t, accountsTypes.Code("v0-test-btc-0"), accountCode)

	escapedKey, err := encodeAccountConfigKey("acct/with space%")
	require.NoError(t, err)
	require.Equal(t, "acct%2Fwith%20space%25", escapedKey)
	accountCode, err = decodeAccountConfigKey(escapedKey)
	require.NoError(t, err)
	require.Equal(t, accountsTypes.Code("acct/with space%"), accountCode)
}

func TestDecodeAccountConfigKeyRejectsInvalidKeys(t *testing.T) {
	for _, key := range []string{
		"wallet/v0-test-btc-0/config",
		"",
		"/",
		"%zz",
		"v0-test-btc-0/config",
		"v0-test-btc-0/bucket",
		"account/v0-test-btc-0/config",
		"account//config",
		"account/%zz/config",
	} {
		_, err := decodeAccountConfigKey(key)
		require.Error(t, err, key)
	}
}

func TestAccountConfigValueBackendKeysGetSetAndSnapshot(t *testing.T) {
	ctx := context.Background()
	activeAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:     "v0-test-btc-0",
		CoinCode: coinpkg.CodeBTC,
		Name:     "Bitcoin",
	})
	inactiveAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:     "v0-test-btc-1",
		CoinCode: coinpkg.CodeBTC,
		Name:     "Savings",
		Inactive: true,
	})
	hiddenAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:                "v0-test-btc-2",
		CoinCode:            coinpkg.CodeBTC,
		Name:                "Hidden",
		HiddenBecauseUnused: true,
	})
	tokenAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:     "v0-test-eth-0-eth-erc20-usdt",
		CoinCode: coinpkg.Code("eth-erc20-usdt"),
		Name:     "Tether USD",
	})
	wrongRootAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:     "v0-test-btc-3",
		CoinCode: coinpkg.CodeBTC,
		Name:     "Wrong Root",
		SigningConfigurations: testSigningConfigurations(
			t,
			otherTestRootFingerprint,
		),
	})
	emptyConfigAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:                  "v0-test-btc-4",
		CoinCode:              coinpkg.CodeBTC,
		Name:                  "Empty Config",
		SigningConfigurations: signing.Configurations{},
	})
	mixedRootAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:     "v0-test-btc-5",
		CoinCode: coinpkg.CodeBTC,
		Name:     "Mixed Root",
		SigningConfigurations: signing.Configurations{
			testBitcoinSigningConfiguration(
				t,
				testRootFingerprint,
				signing.ScriptTypeP2WPKH,
				"m/84'/1'/0'",
			),
			testBitcoinSigningConfiguration(
				t,
				otherTestRootFingerprint,
				signing.ScriptTypeP2TR,
				"m/86'/1'/0'",
			),
		},
	})
	unknownConfigAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:                  "v0-test-btc-6",
		CoinCode:              coinpkg.CodeBTC,
		Name:                  "Unknown Config",
		SigningConfigurations: signing.Configurations{&signing.Configuration{}},
	})
	accountList := []accounts.Interface{
		activeAccount,
		inactiveAccount,
		hiddenAccount,
		tokenAccount,
		wrongRootAccount,
		emptyConfigAccount,
		mixedRootAccount,
		unknownConfigAccount,
	}
	accountsProvider := func() []accounts.Interface {
		return accountList
	}
	set, setIfCurrent := accountNameSetters(accountsProvider, testRootFingerprint)
	backend, err := newAccountConfigValueBackend(accountsProvider, testRootFingerprint, set, setIfCurrent)
	require.NoError(t, err)

	activeKey, err := encodeAccountConfigKey("v0-test-btc-0")
	require.NoError(t, err)
	inactiveKey, err := encodeAccountConfigKey("v0-test-btc-1")
	require.NoError(t, err)
	hiddenKey, err := encodeAccountConfigKey("v0-test-btc-2")
	require.NoError(t, err)
	tokenKey, err := encodeAccountConfigKey("v0-test-eth-0-eth-erc20-usdt")
	require.NoError(t, err)
	wrongRootKey, err := encodeAccountConfigKey("v0-test-btc-3")
	require.NoError(t, err)
	emptyConfigKey, err := encodeAccountConfigKey("v0-test-btc-4")
	require.NoError(t, err)
	mixedRootKey, err := encodeAccountConfigKey("v0-test-btc-5")
	require.NoError(t, err)
	unknownConfigKey, err := encodeAccountConfigKey("v0-test-btc-6")
	require.NoError(t, err)

	keys, err := backend.Keys(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{activeKey, inactiveKey}, keys)
	require.NotContains(t, keys, hiddenKey)
	require.NotContains(t, keys, tokenKey)
	require.NotContains(t, keys, wrongRootKey)
	require.NotContains(t, keys, emptyConfigKey)
	require.NotContains(t, keys, mixedRootKey)
	require.NotContains(t, keys, unknownConfigKey)

	value, err := backend.Get(ctx, activeKey)
	require.NoError(t, err)
	require.Equal(t, accountConfigValueWithName("Bitcoin", time.Time{}), value)

	snapshot, err := backend.Snapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, accountConfigValueWithName("Bitcoin", time.Time{}), snapshot[activeKey])
	require.Equal(t, accountConfigValueWithName("Savings", time.Time{}), snapshot[inactiveKey])

	remoteModifiedAt := txNoteTestModifiedAt.Add(time.Hour)
	require.NoError(t, backend.Set(ctx, activeKey, accountConfigValueWithName("Remote Bitcoin", remoteModifiedAt)))
	require.Equal(t, "Remote Bitcoin", activeAccount.Config().Config.Name)
	require.NotNil(t, activeAccount.Config().Config.NameModifiedAt)
	require.True(t, remoteModifiedAt.Equal(*activeAccount.Config().Config.NameModifiedAt))
	require.Error(t, backend.Set(ctx, wrongRootKey, accountConfigValueWithName("Wrong Remote", remoteModifiedAt)))
}

func TestAccountConfigValueBackendSetIfCurrentRejectsRacedWrite(t *testing.T) {
	ctx := context.Background()
	baseModifiedAt := txNoteTestModifiedAt
	account, _ := mockAccountWithConfig(t, config.Account{
		Code:           "v0-test-btc-0",
		CoinCode:       coinpkg.CodeBTC,
		Name:           "Base",
		NameModifiedAt: &baseModifiedAt,
	})
	accountList := []accounts.Interface{account}
	accountsProvider := func() []accounts.Interface {
		return accountList
	}
	set, setIfCurrent := accountNameSetters(accountsProvider, testRootFingerprint)
	backend, err := newAccountConfigValueBackend(accountsProvider, testRootFingerprint, set, setIfCurrent)
	require.NoError(t, err)
	key, err := encodeAccountConfigKey("v0-test-btc-0")
	require.NoError(t, err)

	remoteModifiedAt := baseModifiedAt.Add(time.Hour)
	replaced, err := backend.SetIfCurrent(ctx, key,
		accountConfigValueWithName("Base", baseModifiedAt),
		true,
		accountConfigValueWithName("Remote", remoteModifiedAt),
	)
	require.NoError(t, err)
	require.True(t, replaced)
	require.Equal(t, "Remote", account.Config().Config.Name)
	require.True(t, remoteModifiedAt.Equal(*account.Config().Config.NameModifiedAt))

	localModifiedAt := remoteModifiedAt.Add(time.Hour)
	account.Config().Config.Name = "Local"
	account.Config().Config.NameModifiedAt = &localModifiedAt
	replaced, err = backend.SetIfCurrent(ctx, key,
		accountConfigValueWithName("Remote", remoteModifiedAt),
		true,
		accountConfigValueWithName("Stale Remote", remoteModifiedAt.Add(2*time.Hour)),
	)
	require.NoError(t, err)
	require.False(t, replaced)
	require.Equal(t, "Local", account.Config().Config.Name)
}

func TestAccountConfigValueBackendSetIfCurrentCreatesMissingValue(t *testing.T) {
	ctx := context.Background()
	account, _ := mockAccountWithConfig(t, config.Account{
		Code:     "v0-test-btc-0",
		CoinCode: coinpkg.CodeBTC,
	})
	accountList := []accounts.Interface{account}
	accountsProvider := func() []accounts.Interface {
		return accountList
	}
	set, setIfCurrent := accountNameSetters(accountsProvider, testRootFingerprint)
	backend, err := newAccountConfigValueBackend(accountsProvider, testRootFingerprint, set, setIfCurrent)
	require.NoError(t, err)
	key, err := encodeAccountConfigKey("v0-test-btc-0")
	require.NoError(t, err)

	modifiedAt := txNoteTestModifiedAt
	replaced, err := backend.SetIfCurrent(ctx, key,
		accountConfigValue{},
		false,
		accountConfigValueWithName("Remote", modifiedAt),
	)
	require.NoError(t, err)
	require.True(t, replaced)
	require.Equal(t, "Remote", account.Config().Config.Name)
	require.True(t, modifiedAt.Equal(*account.Config().Config.NameModifiedAt))
}

func TestMergeAccountConfig(t *testing.T) {
	baseTime := txNoteTestModifiedAt
	localTime := baseTime.Add(time.Hour)
	remoteTime := baseTime.Add(2 * time.Hour)
	base := accountConfigValueWithName("Base", baseTime)
	merged, resolved, err := mergeAccountConfig(
		"Bitcoin",
		&base,
		accountConfigValueWithName("Base", baseTime),
		accountConfigValueWithName("Remote", remoteTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountConfigValueWithName("Remote", remoteTime), merged)

	merged, resolved, err = mergeAccountConfig(
		"Bitcoin",
		&base,
		accountConfigValueWithName("Local", localTime),
		accountConfigValueWithName("Remote", remoteTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountConfigValueWithName("Remote", remoteTime), merged)

	merged, resolved, err = mergeAccountConfig(
		"Bitcoin",
		&base,
		accountConfigValueWithName("Bitcoin", remoteTime),
		accountConfigValueWithName("Custom", localTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountConfigValueWithName("Custom", localTime), merged)
}

func TestMergeAccountConfigWithoutBase(t *testing.T) {
	localTime := txNoteTestModifiedAt
	remoteTime := localTime.Add(time.Hour)
	merged, resolved, err := mergeAccountConfig(
		"Bitcoin",
		nil,
		accountConfigValueWithName("Local", localTime),
		accountConfigValueWithName("Remote", remoteTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountConfigValueWithName("Remote", remoteTime), merged)

	merged, resolved, err = mergeAccountConfig(
		"Bitcoin",
		nil,
		accountConfigValueWithName("Bitcoin", remoteTime),
		accountConfigValueWithName("Custom", localTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountConfigValueWithName("Custom", localTime), merged)
}
