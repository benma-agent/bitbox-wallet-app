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
) (AccountNameSetter, AccountNameConditionalSetter) {
	set := func(accountCode accountsTypes.Code, name string, modifiedAt time.Time) error {
		account := accountByCode(accountNameSyncAccounts(accountsProvider(), rootFingerprint), accountCode)
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
		account := accountByCode(accountNameSyncAccounts(accountsProvider(), rootFingerprint), accountCode)
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

func accountNameValueWithName(name string, modifiedAt time.Time) accountNameValue {
	return accountNameValue{
		ModifiedAt: modifiedAt,
		Name:       name,
	}
}

func TestAccountNameKeyEncoding(t *testing.T) {
	key, err := encodeAccountNameKey("v0-test-btc-0")
	require.NoError(t, err)
	require.Equal(t, "v0-test-btc-0", key)
	accountCode, err := decodeAccountNameKey(key)
	require.NoError(t, err)
	require.Equal(t, accountsTypes.Code("v0-test-btc-0"), accountCode)

	escapedKey, err := encodeAccountNameKey("acct/with space%")
	require.NoError(t, err)
	require.Equal(t, "acct%2Fwith%20space%25", escapedKey)
	accountCode, err = decodeAccountNameKey(escapedKey)
	require.NoError(t, err)
	require.Equal(t, accountsTypes.Code("acct/with space%"), accountCode)
}

func TestDecodeAccountNameKeyRejectsInvalidKeys(t *testing.T) {
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
		_, err := decodeAccountNameKey(key)
		require.Error(t, err, key)
	}
}

func TestAccountNameValueBackendKeysGetSetAndSnapshot(t *testing.T) {
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
	backend, err := newAccountNameValueBackend(accountsProvider, testRootFingerprint, set, setIfCurrent)
	require.NoError(t, err)

	activeKey, err := encodeAccountNameKey("v0-test-btc-0")
	require.NoError(t, err)
	inactiveKey, err := encodeAccountNameKey("v0-test-btc-1")
	require.NoError(t, err)
	hiddenKey, err := encodeAccountNameKey("v0-test-btc-2")
	require.NoError(t, err)
	tokenKey, err := encodeAccountNameKey("v0-test-eth-0-eth-erc20-usdt")
	require.NoError(t, err)
	wrongRootKey, err := encodeAccountNameKey("v0-test-btc-3")
	require.NoError(t, err)
	emptyConfigKey, err := encodeAccountNameKey("v0-test-btc-4")
	require.NoError(t, err)
	mixedRootKey, err := encodeAccountNameKey("v0-test-btc-5")
	require.NoError(t, err)
	unknownConfigKey, err := encodeAccountNameKey("v0-test-btc-6")
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
	require.Equal(t, accountNameValueWithName("Bitcoin", time.Time{}), value)

	snapshot, err := backend.Snapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, accountNameValueWithName("Bitcoin", time.Time{}), snapshot[activeKey])
	require.Equal(t, accountNameValueWithName("Savings", time.Time{}), snapshot[inactiveKey])

	remoteModifiedAt := txNoteTestModifiedAt.Add(time.Hour)
	require.NoError(t, backend.Set(ctx, activeKey, accountNameValueWithName("Remote Bitcoin", remoteModifiedAt)))
	require.Equal(t, "Remote Bitcoin", activeAccount.Config().Config.Name)
	require.NotNil(t, activeAccount.Config().Config.NameModifiedAt)
	require.True(t, remoteModifiedAt.Equal(*activeAccount.Config().Config.NameModifiedAt))
	require.Error(t, backend.Set(ctx, wrongRootKey, accountNameValueWithName("Wrong Remote", remoteModifiedAt)))
}

func TestAccountNameValueBackendSetIfCurrentRejectsRacedWrite(t *testing.T) {
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
	backend, err := newAccountNameValueBackend(accountsProvider, testRootFingerprint, set, setIfCurrent)
	require.NoError(t, err)
	key, err := encodeAccountNameKey("v0-test-btc-0")
	require.NoError(t, err)

	remoteModifiedAt := baseModifiedAt.Add(time.Hour)
	replaced, err := backend.SetIfCurrent(ctx, key,
		accountNameValueWithName("Base", baseModifiedAt),
		true,
		accountNameValueWithName("Remote", remoteModifiedAt),
	)
	require.NoError(t, err)
	require.True(t, replaced)
	require.Equal(t, "Remote", account.Config().Config.Name)
	require.True(t, remoteModifiedAt.Equal(*account.Config().Config.NameModifiedAt))

	localModifiedAt := remoteModifiedAt.Add(time.Hour)
	account.Config().Config.Name = "Local"
	account.Config().Config.NameModifiedAt = &localModifiedAt
	replaced, err = backend.SetIfCurrent(ctx, key,
		accountNameValueWithName("Remote", remoteModifiedAt),
		true,
		accountNameValueWithName("Stale Remote", remoteModifiedAt.Add(2*time.Hour)),
	)
	require.NoError(t, err)
	require.False(t, replaced)
	require.Equal(t, "Local", account.Config().Config.Name)
}

func TestAccountNameValueBackendSetIfCurrentCreatesMissingValue(t *testing.T) {
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
	backend, err := newAccountNameValueBackend(accountsProvider, testRootFingerprint, set, setIfCurrent)
	require.NoError(t, err)
	key, err := encodeAccountNameKey("v0-test-btc-0")
	require.NoError(t, err)

	modifiedAt := txNoteTestModifiedAt
	replaced, err := backend.SetIfCurrent(ctx, key,
		accountNameValue{},
		false,
		accountNameValueWithName("Remote", modifiedAt),
	)
	require.NoError(t, err)
	require.True(t, replaced)
	require.Equal(t, "Remote", account.Config().Config.Name)
	require.True(t, modifiedAt.Equal(*account.Config().Config.NameModifiedAt))
}

func TestMergeAccountName(t *testing.T) {
	baseTime := txNoteTestModifiedAt
	localTime := baseTime.Add(time.Hour)
	remoteTime := baseTime.Add(2 * time.Hour)
	base := accountNameValueWithName("Base", baseTime)
	merged, resolved, err := mergeAccountNameValue(
		"Bitcoin",
		&base,
		accountNameValueWithName("Base", baseTime),
		accountNameValueWithName("Remote", remoteTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountNameValueWithName("Remote", remoteTime), merged)

	merged, resolved, err = mergeAccountNameValue(
		"Bitcoin",
		&base,
		accountNameValueWithName("Local", localTime),
		accountNameValueWithName("Remote", remoteTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountNameValueWithName("Remote", remoteTime), merged)

	merged, resolved, err = mergeAccountNameValue(
		"Bitcoin",
		&base,
		accountNameValueWithName("Bitcoin", remoteTime),
		accountNameValueWithName("Custom", localTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountNameValueWithName("Custom", localTime), merged)
}

func TestMergeAccountNameWithoutBase(t *testing.T) {
	localTime := txNoteTestModifiedAt
	remoteTime := localTime.Add(time.Hour)
	merged, resolved, err := mergeAccountNameValue(
		"Bitcoin",
		nil,
		accountNameValueWithName("Local", localTime),
		accountNameValueWithName("Remote", remoteTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountNameValueWithName("Remote", remoteTime), merged)

	merged, resolved, err = mergeAccountNameValue(
		"Bitcoin",
		nil,
		accountNameValueWithName("Bitcoin", remoteTime),
		accountNameValueWithName("Custom", localTime),
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, accountNameValueWithName("Custom", localTime), merged)
}
