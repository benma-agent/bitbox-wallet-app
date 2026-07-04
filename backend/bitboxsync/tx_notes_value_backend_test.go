// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts"
	accountMocks "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/mocks"
	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/notes"
	accountsTypes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/types"
	coinpkg "github.com/BitBoxSwiss/bitbox-wallet-app/backend/coins/coin"
	coinMocks "github.com/BitBoxSwiss/bitbox-wallet-app/backend/coins/coin/mocks"
	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/config"
	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/signing"
	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"
	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"
	sqlitestore "github.com/BitBoxSwiss/bitboxsync-client-go/storage/sqlite"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

var (
	testRootFingerprint      = []byte{0x01, 0x02, 0x03, 0x04}
	otherTestRootFingerprint = []byte{0x05, 0x06, 0x07, 0x08}
	txNoteTestModifiedAt     = time.Date(2026, 5, 11, 10, 24, 0, 0, time.UTC)
)

func txNoteEntryForTest(note string) txNoteEntry {
	return txNoteEntry{
		Note:       note,
		ModifiedAt: txNoteTestModifiedAt,
	}
}

func txNotesBucketForTest(values map[string]string) txNotesBucket {
	out := txNotesBucket{}
	for txID, note := range values {
		out[txID] = txNoteEntryForTest(note)
	}
	return out
}

func valueFromNotes(t *testing.T, txNotes *notes.Notes, key string) txNotesBucket {
	t.Helper()
	_, bucket, err := decodeTxNoteBucketKey(key)
	require.NoError(t, err)
	return txNotesBucketFromEntries(txNotes.TransactionNoteEntries(), bucket)
}

func txIDInBucket(t *testing.T, bucket int, exclude ...string) string {
	t.Helper()
	excluded := map[string]struct{}{}
	for _, txID := range exclude {
		excluded[txID] = struct{}{}
	}
	for i := 0; i < 10_000; i++ {
		txID := fmt.Sprintf("tx-%d", i)
		if _, ok := excluded[txID]; ok {
			continue
		}
		if txNoteBucketIndex(txID) == bucket {
			return txID
		}
	}
	t.Fatalf("could not find transaction ID in bucket %d", bucket)
	return ""
}

func testXPub(t *testing.T) *hdkeychain.ExtendedKey {
	t.Helper()
	xprv, err := hdkeychain.NewMaster(make([]byte, 32), &chaincfg.TestNet3Params)
	require.NoError(t, err)
	xpub, err := xprv.Neuter()
	require.NoError(t, err)
	return xpub
}

func testKeypath(t *testing.T, keypath string) signing.AbsoluteKeypath {
	t.Helper()
	absoluteKeypath, err := signing.NewAbsoluteKeypath(keypath)
	require.NoError(t, err)
	return absoluteKeypath
}

func testBitcoinSigningConfiguration(
	t *testing.T,
	rootFingerprint []byte,
	scriptType signing.ScriptType,
	keypath string,
) *signing.Configuration {
	t.Helper()
	return signing.NewBitcoinConfiguration(
		scriptType,
		append([]byte(nil), rootFingerprint...),
		testKeypath(t, keypath),
		testXPub(t),
	)
}

func testEthereumSigningConfiguration(
	t *testing.T,
	rootFingerprint []byte,
	keypath string,
) *signing.Configuration {
	t.Helper()
	return signing.NewEthereumConfiguration(
		append([]byte(nil), rootFingerprint...),
		testKeypath(t, keypath),
		testXPub(t),
	)
}

func testSigningConfigurations(t *testing.T, rootFingerprint []byte) signing.Configurations {
	t.Helper()
	return signing.Configurations{
		testBitcoinSigningConfiguration(
			t,
			rootFingerprint,
			signing.ScriptTypeP2WPKH,
			"m/84'/1'/0'",
		),
	}
}

func mockAccount(t *testing.T, code accountsTypes.Code) (accounts.Interface, *notes.Notes) {
	t.Helper()
	return mockAccountWithConfig(t, config.Account{Code: code})
}

func mockAccountWithConfig(t *testing.T, accountConfig config.Account) (accounts.Interface, *notes.Notes) {
	t.Helper()
	txNotes, err := notes.LoadNotes(filepath.Join(t.TempDir(), "notes.json"))
	require.NoError(t, err)
	accountConfigCopy := accountConfig
	if accountConfigCopy.SigningConfigurations == nil {
		accountConfigCopy.SigningConfigurations = testSigningConfigurations(t, testRootFingerprint)
	}
	cfg := &accounts.AccountConfig{
		Config: &accountConfigCopy,
	}
	return &accountMocks.InterfaceMock{
		ConfigFunc: func() *accounts.AccountConfig {
			return cfg
		},
		CoinFunc: func() coinpkg.Coin {
			return &coinMocks.CoinMock{
				NameFunc: func() string {
					return testCoinName(accountConfigCopy.CoinCode)
				},
			}
		},
		NotesFunc: func() *notes.Notes {
			return txNotes
		},
		SetTxNoteFunc: func(txID string, note string) error {
			_, err := txNotes.SetTxNote(txID, note)
			return err
		},
		TxNoteFunc: func(txID string) string {
			return txNotes.TxNote(txID)
		},
		TransactionsFunc: func() (accounts.OrderedTransactions, error) {
			return accounts.NewOrderedTransactions([]*accounts.TransactionData{
				{InternalID: "bb-without-local-note"},
			}), nil
		},
	}, txNotes
}

func testCoinName(code coinpkg.Code) string {
	switch code {
	case coinpkg.CodeLTC, coinpkg.CodeTLTC:
		return "Litecoin"
	case coinpkg.CodeETH, coinpkg.CodeSEPETH:
		return "Ethereum"
	default:
		return "Bitcoin"
	}
}

func malformedAccount(accountConfig *accounts.AccountConfig, txNotes *notes.Notes) accounts.Interface {
	return &accountMocks.InterfaceMock{
		ConfigFunc: func() *accounts.AccountConfig {
			return accountConfig
		},
		NotesFunc: func() *notes.Notes {
			return txNotes
		},
	}
}

func TestDisableKeepsLocalSyncStore(t *testing.T) {
	dataDir := t.TempDir()
	service := New(Config{DataDir: dataDir, Log: logrus.NewEntry(logrus.New())})
	for _, path := range []string{
		service.storePath(),
		service.storePath() + "-wal",
		service.storePath() + "-shm",
	} {
		require.NoError(t, os.WriteFile(path, []byte("sync state"), 0600))
	}

	require.NoError(t, service.Disable([]byte{1, 2, 3, 4}, nil))
	_, err := os.Stat(service.storePath())
	require.NoError(t, err)
}

func TestDisableForgetsIdentitySecrets(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	service := New(Config{DataDir: dataDir})
	rootFingerprint := []byte{1, 2, 3, 4}
	rawIdentity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(rawIdentity)
	require.NoError(t, err)
	keyID, err := publicIdentity.KeyID()
	require.NoError(t, err)
	namespaceID := "namespace"

	store, err := sqlitestore.Open(service.storePath())
	require.NoError(t, err)
	require.NoError(t, store.SaveIdentity(ctx, syncclient.IdentityState{
		KeyID:              keyID,
		Kind:               "keystore",
		AccessToken:        "access-token",
		TokenExpiry:        time.Now().Add(time.Hour).UTC(),
		DefaultNamespaceID: namespaceID,
	}))
	require.NoError(t, store.SaveNamespace(ctx, syncclient.NamespaceState{
		KeyID:           keyID,
		NamespaceID:     namespaceID,
		Kind:            "default",
		NamespaceHead:   42,
		ActiveScopeHash: "scope",
		DEK:             []byte("namespace-dek"),
	}))
	require.NoError(t, store.SaveItem(ctx, syncclient.ItemState{
		KeyID:       keyID,
		NamespaceID: namespaceID,
		Collection:  "collection",
		Key:         "key",
		ItemID:      "item-id",
		Version:     7,
		BaseVersion: 7,
		BaseValue:   []byte("base-value"),
	}))
	require.NoError(t, store.Close())

	require.NoError(t, service.Disable(rootFingerprint, &publicIdentity))

	store, err = sqlitestore.Open(service.storePath())
	require.NoError(t, err)
	defer store.Close()
	identity, err := store.LoadIdentity(ctx, keyID)
	require.NoError(t, err)
	require.Empty(t, identity.AccessToken)
	require.True(t, identity.TokenExpiry.IsZero())
	require.Equal(t, namespaceID, identity.DefaultNamespaceID)
	namespace, err := store.GetNamespace(ctx, keyID, namespaceID)
	require.NoError(t, err)
	require.Empty(t, namespace.DEK)
	require.Equal(t, uint64(42), namespace.NamespaceHead)
	item, err := store.GetItemByLogicalKey(ctx, keyID, namespaceID, "collection", "key")
	require.NoError(t, err)
	require.Equal(t, []byte("base-value"), item.BaseValue)
	require.Equal(t, uint64(7), item.Version)
}

func TestCloseKeepsLocalSyncStore(t *testing.T) {
	dataDir := t.TempDir()
	service := New(Config{DataDir: dataDir})
	require.NoError(t, os.WriteFile(service.storePath(), []byte("sync state"), 0600))

	require.NoError(t, service.Close())
	_, err := os.Stat(service.storePath())
	require.NoError(t, err)
}

func TestTxNoteKeyEncoding(t *testing.T) {
	bucketKey, err := encodeTxNotesBucketKey("v0-test-btc-0", 135)
	require.NoError(t, err)
	require.Equal(t, "v0-test-btc-0/135", bucketKey)
	accountCode, bucket, err := decodeTxNoteBucketKey(bucketKey)
	require.NoError(t, err)
	require.Equal(t, accountsTypes.Code("v0-test-btc-0"), accountCode)
	require.Equal(t, 135, bucket)

	escapedBucketKey, err := encodeTxNotesBucketKey("acct/with space%", 255)
	require.NoError(t, err)
	require.Equal(t, "acct%2Fwith%20space%25/255", escapedBucketKey)
	accountCode, bucket, err = decodeTxNoteBucketKey(escapedBucketKey)
	require.NoError(t, err)
	require.Equal(t, accountsTypes.Code("acct/with space%"), accountCode)
	require.Equal(t, 255, bucket)
}

func TestDecodeTxNoteKeyRejectsInvalidKeys(t *testing.T) {
	for _, key := range []string{
		"wallet/v0-test-btc-0/index",
		"/0",
		"%zz/0",
		"v0-test-btc-0/index",
		"v0-test-btc-0",
		"v0-test-btc-0/-1",
		"v0-test-btc-0/256",
		"v0-test-btc-0/abc",
		"v0-test-btc-0/001",
		"v0-test-btc-0/bucket/1",
		"v0-test-btc-0/index/extra",
		"account/v0-test-btc-0/1",
		"account//index",
		"account/%zz/index",
	} {
		_, _, err := decodeTxNoteBucketKey(key)
		require.Error(t, err, key)
	}
}

func TestTxNoteBucketIndexUsesSHA256(t *testing.T) {
	require.Equal(t, 0x96, txNoteBucketIndex("aa"))
	require.Equal(t, 0x67, txNoteBucketIndex("0xaa"))
	require.Equal(t, 0x4a, txNoteBucketIndex("zz"))
}

func TestTxNotesValueBackendKeysGetPutAndQueueEmptyValue(t *testing.T) {
	ctx := context.Background()
	account, txNotes := mockAccount(t, "v0-test-btc-0")
	_, err := txNotes.SetTxNote("aa-local", "local note")
	require.NoError(t, err)

	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, nil)
	require.NoError(t, err)

	// The transaction-note model exposes all 256 bucket keys for each active
	// account, regardless of how many notes currently exist.
	localBucketKey, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("aa-local"))
	require.NoError(t, err)
	keys, err := backend.Keys(ctx)
	require.NoError(t, err)
	require.Len(t, keys, txNotesBucketCount)
	require.Contains(t, keys, localBucketKey)

	value, err := backend.Get(ctx, localBucketKey)
	require.NoError(t, err)
	require.Equal(t, "local note", value["aa-local"].Note)
	require.False(t, value["aa-local"].ModifiedAt.IsZero())

	// Downloading a remote bucket updates the normal wallet notes store.
	remoteBucketKey, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("cc-remote"))
	require.NoError(t, err)
	require.NoError(t, backend.Set(ctx, remoteBucketKey, txNotesBucketForTest(map[string]string{"cc-remote": "remote note"})))
	require.Equal(t, "remote note", txNotes.TxNote("cc-remote"))
	require.True(t, txNoteTestModifiedAt.Equal(txNotes.TransactionNoteEntries()["cc-remote"].Metadata.ModifiedAt))

	// Empty note strings are meaningful tombstones, so they must remain present
	// in the bucket value backed by the normal notes store.
	_, err = txNotes.SetTxNote("aa-local", "")
	require.NoError(t, err)
	rememberedKey, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("aa-local"))
	require.NoError(t, err)
	require.Equal(t, localBucketKey, rememberedKey)
	value, err = backend.Get(ctx, localBucketKey)
	require.NoError(t, err)
	require.Equal(t, "", value["aa-local"].Note)
	require.Contains(t, keys, remoteBucketKey)
}

func TestTxNotesValueBackendSetIfCurrentRejectsRacedBucketWrite(t *testing.T) {
	ctx := context.Background()
	account, txNotes := mockAccount(t, "v0-test-btc-0")
	_, err := txNotes.SetTxNote("aa-local", "base note")
	require.NoError(t, err)

	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, nil)
	require.NoError(t, err)
	key, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("aa-local"))
	require.NoError(t, err)

	current := valueFromNotes(t, txNotes, key)
	replaced, err := backend.SetIfCurrent(
		ctx,
		key,
		current,
		true,
		txNotesBucketForTest(map[string]string{"aa-local": "remote note"}),
	)
	require.NoError(t, err)
	require.True(t, replaced)
	require.Equal(t, "remote note", txNotes.TxNote("aa-local"))

	_, err = txNotes.SetTxNote("aa-local", "local note")
	require.NoError(t, err)
	replaced, err = backend.SetIfCurrent(
		ctx,
		key,
		txNotesBucketForTest(map[string]string{"aa-local": "remote note"}),
		true,
		txNotesBucketForTest(map[string]string{"aa-local": "stale remote note"}),
	)
	require.NoError(t, err)
	require.False(t, replaced)
	require.Equal(t, "local note", txNotes.TxNote("aa-local"))
}

func TestTxNotesValueBackendSetReplacesWholeBucket(t *testing.T) {
	ctx := context.Background()
	account, txNotes := mockAccount(t, "v0-test-btc-0")
	txID := "aa-local"
	bucket := txNoteBucketIndex(txID)
	staleTxID := txIDInBucket(t, bucket, txID)
	_, err := txNotes.SetTxNote(txID, "local note")
	require.NoError(t, err)
	_, err = txNotes.SetTxNote(staleTxID, "stale note")
	require.NoError(t, err)

	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, nil)
	require.NoError(t, err)
	key, err := encodeTxNotesBucketKey("v0-test-btc-0", bucket)
	require.NoError(t, err)

	require.NoError(t, backend.Set(ctx, key, txNotesBucket{
		txID: txNoteEntryForTest("remote note"),
	}))

	entries := txNotes.TransactionNoteEntries()
	require.Equal(t, "remote note", entries[txID].Note)
	require.NotContains(t, entries, staleTxID)
}

func TestTxNotesValueBackendSetIfCurrentCanReplaceWithEmptyBucket(t *testing.T) {
	ctx := context.Background()
	account, txNotes := mockAccount(t, "v0-test-btc-0")
	txID := "aa-local"
	_, err := txNotes.SetTxNote(txID, "local note")
	require.NoError(t, err)

	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, nil)
	require.NoError(t, err)
	key, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex(txID))
	require.NoError(t, err)
	current := valueFromNotes(t, txNotes, key)

	replaced, err := backend.SetIfCurrent(ctx, key, current, true, txNotesBucket{})
	require.NoError(t, err)
	require.True(t, replaced)
	require.NotContains(t, txNotes.TransactionNoteEntries(), txID)
}

func TestTxNotesValueBackendBatchesChangedAccountNotifications(t *testing.T) {
	ctx := context.Background()
	firstAccount, firstNotes := mockAccount(t, "v0-test-btc-0")
	secondAccount, secondNotes := mockAccount(t, "v0-test-btc-1")
	var notified []accountsTypes.Code

	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{firstAccount, secondAccount}
	}, testRootFingerprint, func(accountCode accountsTypes.Code) {
		notified = append(notified, accountCode)
	})
	require.NoError(t, err)

	firstKey, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("aa-first"))
	require.NoError(t, err)
	firstOtherKey, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("bb-first"))
	require.NoError(t, err)
	secondKey, err := encodeTxNotesBucketKey("v0-test-btc-1", txNoteBucketIndex("cc-second"))
	require.NoError(t, err)

	require.NoError(t, backend.Set(ctx, secondKey, txNotesBucketForTest(map[string]string{"cc-second": "second note"})))
	require.NoError(t, backend.Set(ctx, firstKey, txNotesBucketForTest(map[string]string{"aa-first": "first note"})))
	require.NoError(t, backend.Set(ctx, firstOtherKey, txNotesBucketForTest(map[string]string{"bb-first": "other first note"})))
	require.Empty(t, notified)
	require.Equal(t, "first note", firstNotes.TxNote("aa-first"))
	require.Equal(t, "other first note", firstNotes.TxNote("bb-first"))
	require.Equal(t, "second note", secondNotes.TxNote("cc-second"))

	backend.flushChangedAccountNotifications()
	require.ElementsMatch(t, []accountsTypes.Code{"v0-test-btc-0", "v0-test-btc-1"}, notified)
}

func TestTxNotesValueBackendSetDoesNotCallAccountSetTxNote(t *testing.T) {
	ctx := context.Background()
	account, txNotes := mockAccount(t, "v0-test-btc-0")
	mock := account.(*accountMocks.InterfaceMock)
	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, nil)
	require.NoError(t, err)
	key, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("aa-remote"))
	require.NoError(t, err)

	require.NoError(t, backend.Set(ctx, key, txNotesBucketForTest(map[string]string{"aa-remote": "remote note"})))

	require.Equal(t, "remote note", txNotes.TxNote("aa-remote"))
	require.Empty(t, mock.SetTxNoteCalls())
}

func TestTxNotesValueBackendSetIfCurrentDoesNotNotifyUnchangedBucket(t *testing.T) {
	ctx := context.Background()
	account, txNotes := mockAccount(t, "v0-test-btc-0")
	_, err := txNotes.SetTxNote("aa-local", "base note")
	require.NoError(t, err)
	var notified []accountsTypes.Code
	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, func(accountCode accountsTypes.Code) {
		notified = append(notified, accountCode)
	})
	require.NoError(t, err)
	key, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("aa-local"))
	require.NoError(t, err)

	replaced, err := backend.SetIfCurrent(
		ctx,
		key,
		valueFromNotes(t, txNotes, key),
		true,
		valueFromNotes(t, txNotes, key),
	)
	require.NoError(t, err)
	require.True(t, replaced)
	backend.flushChangedAccountNotifications()

	require.Empty(t, notified)
}

func TestServiceSyncEventsFlushTxNoteNotifications(t *testing.T) {
	ctx := context.Background()
	account, _ := mockAccount(t, "v0-test-btc-0")
	var notified []accountsTypes.Code
	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, func(accountCode accountsTypes.Code) {
		notified = append(notified, accountCode)
	})
	require.NoError(t, err)
	runner := newRunner(Config{}, "01020304")
	runner.collections = &runnerCollections{txNotes: backend}
	key, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex("aa-remote"))
	require.NoError(t, err)

	require.NoError(t, backend.Set(ctx, key, txNotesBucketForTest(map[string]string{"aa-remote": "remote note"})))
	require.Empty(t, notified)

	runner.handleSyncEvent(ctx, syncclient.Event{Type: syncclient.EventSyncFinished}, nil)
	require.Equal(t, []accountsTypes.Code{"v0-test-btc-0"}, notified)
}

func TestTxNotesValueBackendSnapshotBuildsBucketsInOnePass(t *testing.T) {
	ctx := context.Background()
	account, txNotes := mockAccount(t, "v0-test-btc-0")
	_, err := txNotes.SetTxNote("aa-local", "local note")
	require.NoError(t, err)
	_, err = txNotes.SetTxNote("cc-local", "second note")
	require.NoError(t, err)
	_, err = txNotes.SetTxNote("deleted", "deleted note")
	require.NoError(t, err)
	_, err = txNotes.SetTxNote("deleted", "")
	require.NoError(t, err)

	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{account}
	}, testRootFingerprint, nil)
	require.NoError(t, err)

	snapshot, err := backend.Snapshot(ctx)
	require.NoError(t, err)
	for txID, note := range map[string]string{
		"aa-local": "local note",
		"cc-local": "second note",
		"deleted":  "",
	} {
		key, err := encodeTxNotesBucketKey("v0-test-btc-0", txNoteBucketIndex(txID))
		require.NoError(t, err)
		require.Equal(t, note, snapshot[key][txID].Note)
		require.False(t, snapshot[key][txID].ModifiedAt.IsZero())
	}
}

func TestTxNotesAccountProviderFiltersInactiveAndHiddenAccounts(t *testing.T) {
	ctx := context.Background()
	activeAccount, _ := mockAccount(t, "v0-test-btc-0")
	inactiveAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:     "v0-test-btc-1",
		Inactive: true,
	})
	hiddenAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:                "v0-test-btc-2",
		HiddenBecauseUnused: true,
	})
	wrongRootAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:                  "v0-test-btc-3",
		SigningConfigurations: testSigningConfigurations(t, otherTestRootFingerprint),
	})
	emptyConfigAccount, _ := mockAccountWithConfig(t, config.Account{
		Code:                  "v0-test-btc-4",
		SigningConfigurations: signing.Configurations{},
	})
	mixedRootAccount, _ := mockAccountWithConfig(t, config.Account{
		Code: "v0-test-btc-5",
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
		SigningConfigurations: signing.Configurations{&signing.Configuration{}},
	})

	backend, err := newTxNotesValueBackend(func() []accounts.Interface {
		return []accounts.Interface{
			nil,
			activeAccount,
			inactiveAccount,
			hiddenAccount,
			wrongRootAccount,
			emptyConfigAccount,
			mixedRootAccount,
			unknownConfigAccount,
			malformedAccount(nil, nil),
			malformedAccount(&accounts.AccountConfig{Config: &config.Account{Code: "v0-test-btc-7"}}, nil),
		}
	}, testRootFingerprint, nil)
	require.NoError(t, err)

	// Transaction-note sync is scoped to active runtime accounts. Inactive
	// accounts are still useful to future account-metadata sync, but they must
	// not contribute the 256 note bucket keys while disabled.
	keys, err := backend.Keys(ctx)
	require.NoError(t, err)
	require.Len(t, keys, txNotesBucketCount)
	require.Contains(t, keys, "v0-test-btc-0/0")
	require.NotContains(t, keys, "v0-test-btc-1/0")
	require.NotContains(t, keys, "v0-test-btc-2/0")
	require.NotContains(t, keys, "v0-test-btc-3/0")
	require.NotContains(t, keys, "v0-test-btc-4/0")
	require.NotContains(t, keys, "v0-test-btc-5/0")
	require.NotContains(t, keys, "v0-test-btc-6/0")
	require.NotContains(t, keys, "v0-test-btc-7/0")
}

func TestMergeTxNotesBucket(t *testing.T) {
	// Bucket values merge per transaction note: non-conflicting local and remote
	// edits are combined, and simultaneous note edits prefer the newest value.
	baseTime := txNoteTestModifiedAt
	localTime := baseTime.Add(time.Hour)
	remoteTime := baseTime.Add(2 * time.Hour)
	base := txNotesBucket{
		"same":     {Note: "base", ModifiedAt: baseTime},
		"remote":   {Note: "base", ModifiedAt: baseTime},
		"local":    {Note: "base", ModifiedAt: baseTime},
		"conflict": {Note: "base", ModifiedAt: baseTime},
	}
	merged, resolved, err := mergeTxNotesBucket(
		"test-key",
		&base,
		txNotesBucket{
			"same":     {Note: "base", ModifiedAt: baseTime},
			"remote":   {Note: "base", ModifiedAt: baseTime},
			"local":    {Note: "local", ModifiedAt: localTime},
			"conflict": {Note: "local", ModifiedAt: localTime},
		},
		txNotesBucket{
			"same":     {Note: "base", ModifiedAt: baseTime},
			"remote":   {Note: "remote", ModifiedAt: remoteTime},
			"local":    {Note: "base", ModifiedAt: baseTime},
			"conflict": {Note: "remote", ModifiedAt: remoteTime},
		},
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, txNotesBucket{
		"same":     {Note: "base", ModifiedAt: baseTime},
		"remote":   {Note: "remote", ModifiedAt: remoteTime},
		"local":    {Note: "local", ModifiedAt: localTime},
		"conflict": {Note: "remote", ModifiedAt: remoteTime},
	}, merged)
}

func TestMergeTxNotesBucketWithoutBase(t *testing.T) {
	localTime := txNoteTestModifiedAt
	remoteTime := txNoteTestModifiedAt.Add(time.Hour)
	merged, resolved, err := mergeTxNotesBucket(
		"test-key",
		nil,
		txNotesBucket{
			"local": {Note: "local note", ModifiedAt: localTime},
			"same":  {Note: "local note", ModifiedAt: localTime},
		},
		txNotesBucket{
			"remote": {Note: "remote note", ModifiedAt: remoteTime},
			"same":   {Note: "remote note", ModifiedAt: remoteTime},
		},
	)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, txNotesBucket{
		"local":  {Note: "local note", ModifiedAt: localTime},
		"remote": {Note: "remote note", ModifiedAt: remoteTime},
		"same":   {Note: "remote note", ModifiedAt: remoteTime},
	}, merged)
}
