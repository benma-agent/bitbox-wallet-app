// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts"
	accountsTypes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/types"
	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/keystore"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"
	"github.com/BitBoxSwiss/bitboxsync-client-go/protocol"
	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"
	sqlitestore "github.com/BitBoxSwiss/bitboxsync-client-go/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func TestStatusReturnsPerKeystoreSnapshot(t *testing.T) {
	service := New(Config{BaseURL: "https://sync.example"})
	service.runners = map[string]*runner{
		"01020304": testRunnerWithStatus("01020304", KeystoreStatus{
			Running:            true,
			DefaultNamespaceID: "namespace-a",
		}),
		"05060708": testRunnerWithStatus("05060708", KeystoreStatus{
			Running:            true,
			DefaultNamespaceID: "namespace-b",
		}),
	}

	status := service.Status()
	require.Equal(t, "https://sync.example", status.BaseURL)
	require.Len(t, status.Keystores, 2)
	require.Equal(t, "namespace-a", status.Keystores["01020304"].DefaultNamespaceID)
	require.Equal(t, "namespace-b", status.Keystores["05060708"].DefaultNamespaceID)

	delete(status.Keystores, "01020304")
	require.Len(t, service.Status().Keystores, 2)
}

func TestAuthStatusReturnsPerKeystoreSnapshot(t *testing.T) {
	expiresAt := time.Now().Add(28 * 24 * time.Hour).UTC()
	service := New(Config{BaseURL: "https://sync.example"})
	service.runners = map[string]*runner{
		"01020304": testRunnerWithAuthStatus("01020304", KeystoreAuthStatus{
			tokenExpiresAt:     &expiresAt,
			RefreshRecommended: true,
		}),
		"05060708": testRunnerWithAuthStatus("05060708", KeystoreAuthStatus{
			LoginRequired: true,
		}),
	}

	status := service.Status()
	require.Len(t, status.Keystores, 2)
	require.True(t, status.Keystores["01020304"].AuthStatus.RefreshRecommended)
	require.True(t, status.Keystores["05060708"].AuthStatus.LoginRequired)

	returnedDaysLeft := status.Keystores["01020304"].AuthStatus.DaysLeft
	require.NotNil(t, returnedDaysLeft)
	require.Equal(t, 28, *returnedDaysLeft)

	statusJSON, err := json.Marshal(status)
	require.NoError(t, err)
	require.Contains(t, string(statusJSON), `"daysLeft":28`)
	require.NotContains(t, string(statusJSON), "tokenExpiresAt")

	*returnedDaysLeft = 99
	require.Equal(t, 28, *service.Status().Keystores["01020304"].AuthStatus.DaysLeft)
}

func TestHandleSyncEventUpdatesAuthStatus(t *testing.T) {
	var notified []string
	service := New(Config{
		NotifyAuthStatusChanged: func(rootFingerprint string) {
			notified = append(notified, rootFingerprint)
		},
	})
	syncRunner := newRunner(service.Config, "01020304")
	service.runners = map[string]*runner{
		"01020304": syncRunner,
	}
	expiresAt := time.Now().Add(28 * 24 * time.Hour).UTC()

	syncRunner.handleSyncEvent(context.Background(), syncclient.Event{
		Type:           syncclient.EventAuthRefreshRecommended,
		TokenExpiresAt: expiresAt,
	}, nil)
	status := service.Status().Keystores["01020304"].AuthStatus
	require.False(t, status.LoginRequired)
	require.True(t, status.RefreshRecommended)
	require.NotNil(t, status.DaysLeft)
	require.Equal(t, 28, *status.DaysLeft)

	syncRunner.handleSyncEvent(context.Background(), syncclient.Event{
		Type:           syncclient.EventAuthLoginRequired,
		TokenExpiresAt: expiresAt,
	}, nil)
	status = service.Status().Keystores["01020304"].AuthStatus
	require.True(t, status.LoginRequired)
	require.False(t, status.RefreshRecommended)
	require.Nil(t, status.DaysLeft)

	newExpiresAt := expiresAt.Add(time.Hour)
	syncRunner.handleSyncEvent(context.Background(), syncclient.Event{
		Type:           syncclient.EventAuthSessionReady,
		TokenExpiresAt: newExpiresAt,
	}, nil)
	status = service.Status().Keystores["01020304"].AuthStatus
	require.False(t, status.LoginRequired)
	require.False(t, status.RefreshRecommended)
	require.Nil(t, status.DaysLeft)
	require.Equal(t, []string{"01020304", "01020304", "01020304"}, notified)
}

func TestAuthStatusTreatsExpiredRefreshRecommendationAsLoginRequired(t *testing.T) {
	service := New(Config{BaseURL: "https://sync.example"})
	expiresAt := time.Now().Add(-time.Minute).UTC()
	service.runners = map[string]*runner{
		"01020304": testRunnerWithAuthStatus("01020304", KeystoreAuthStatus{
			tokenExpiresAt:     &expiresAt,
			RefreshRecommended: true,
		}),
	}

	status := service.Status().Keystores["01020304"].AuthStatus
	require.True(t, status.LoginRequired)
	require.False(t, status.RefreshRecommended)
	require.Nil(t, status.DaysLeft)
}

func TestAuthTokenDaysLeft(t *testing.T) {
	now := time.Unix(0, 0)
	require.Equal(t, 0, authTokenDaysLeft(now, now))
	require.Equal(t, 1, authTokenDaysLeft(now, now.Add(90*time.Minute)))
	require.Equal(t, 28, authTokenDaysLeft(now, now.Add(28*24*time.Hour)))
	require.Equal(t, 29, authTokenDaysLeft(now, now.Add(28*24*time.Hour+12*time.Hour)))
}

func TestStartWaitsForKeystoreLifecycleLock(t *testing.T) {
	service := New(Config{})
	rootFingerprint := []byte{1, 2, 3, 4}
	_, unlock, err := service.lockLifecycle(rootFingerprint)
	require.NoError(t, err)

	liveIdentity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	identity := &blockingKindIdentity{
		Identity: liveIdentity,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		_, _ = service.Start(context.Background(), identity, rootFingerprint)
		close(done)
	}()

	select {
	case <-identity.entered:
		t.Fatal("start entered while keystore lifecycle lock was held")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	close(identity.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("start did not finish after keystore lifecycle lock was released")
	}
}

func TestStartSuppressesNeedsDeviceWithoutPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/auth/challenge", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(protocol.ChallengeResponse{
			Challenge: protocol.EncodeBase64(make([]byte, 32)),
		}))
	}))
	defer server.Close()

	liveIdentity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(liveIdentity)
	require.NoError(t, err)
	var connects int
	identity, err := NewCachedIdentity(
		publicIdentity,
		[]byte{1, 2, 3, 4},
		func([]byte) (keystore.Keystore, error) {
			connects++
			return nil, nil
		},
	)
	require.NoError(t, err)
	service := New(Config{
		BaseURL: server.URL,
		DataDir: t.TempDir(),
	})

	status, err := service.Start(context.Background(), identity, []byte{1, 2, 3, 4})

	require.NoError(t, err)
	require.False(t, status.Running)
	require.Empty(t, status.LastError)
	require.Equal(t, 0, connects)
	authStatus := service.Status().Keystores["01020304"].AuthStatus
	require.True(t, authStatus.LoginRequired)
}

func TestRecordErrorSuppressesNeedsDeviceStatusError(t *testing.T) {
	service := New(Config{BaseURL: "https://sync.example"})
	syncRunner := newRunner(service.Config, "01020304")
	service.runners = map[string]*runner{
		"01020304": syncRunner,
	}

	syncRunner.recordError(errp.WithStack(errNeedsDevice))

	status := service.Status().Keystores["01020304"]
	require.Empty(t, status.LastError)
}

func TestSyncNowRepairsRollbackAndRetries(t *testing.T) {
	ctx := context.Background()
	fixture := newRollbackRepairFixture(t, ctx)
	fixture.openStaleRunner(t, ctx)

	// The first foreground sync sees that the server no longer reports the
	// cached default namespace and therefore gets ErrRollback from the client
	// engine. Service.SyncNow must treat that as a self-healing condition: close
	// the stale runner, clear only local namespace/item sync metadata, reopen
	// with the preserved bearer token, and retry the requested foreground sync
	// once. The caller should observe a successful SyncNow, not a rollback error
	// and not a half-repaired disabled runner.
	require.NoError(t, fixture.service.SyncNow(ctx, fixture.rootFingerprint))
	fixture.requireRepaired(t, ctx)
}

func TestBackgroundRunRepairsRollbackAndResumes(t *testing.T) {
	ctx := context.Background()
	fixture := newRollbackRepairFixture(t, ctx)
	staleRunner := fixture.openStaleRunner(t, ctx)

	// Engine.Run does not return normal sync-pass failures to the service. It
	// emits EventSyncFailed and continues running. This test covers that
	// background path explicitly: the old runner's initial Run sync observes the
	// server rollback, the event loop invokes service rollback repair, and the
	// replacement runner's background Run performs a successful sync without a
	// foreground SyncNow call.
	staleRunner.startBackground(fixture.service.repairBackgroundRollback)

	require.Eventually(t, func() bool {
		status := fixture.service.Status().Keystores[fixture.rootFingerprintHex]
		return status.Running &&
			status.LastSync != nil &&
			status.LastError == "" &&
			status.DefaultNamespaceID != "" &&
			status.DefaultNamespaceID != fixture.oldNamespaceID
	}, time.Second, 10*time.Millisecond)
	fixture.requireRepaired(t, ctx)
}

type rollbackRepairFixture struct {
	service            *Service
	identity           raw.Identity
	keyID              string
	rootFingerprint    []byte
	rootFingerprintHex string
	oldNamespaceID     string
	oldItemID          string

	serverMu           sync.Mutex
	repairedNamespace  string
	listNamespaceCalls int
}

func newRollbackRepairFixture(t *testing.T, ctx context.Context) *rollbackRepairFixture {
	t.Helper()

	identity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(identity)
	require.NoError(t, err)
	keyID, err := publicIdentity.KeyID()
	require.NoError(t, err)

	fixture := &rollbackRepairFixture{
		identity:           identity,
		keyID:              keyID,
		rootFingerprint:    []byte{1, 2, 3, 4},
		rootFingerprintHex: "01020304",
		oldNamespaceID:     strings.Repeat("ab", protocol.NamespaceIDLength*2),
		oldItemID:          strings.Repeat("cd", protocol.ItemIDLength*2),
	}
	server := httptest.NewServer(http.HandlerFunc(fixture.handleSyncServerRequest(t)))
	t.Cleanup(server.Close)

	fixture.service = New(Config{
		BaseURL: server.URL,
		DataDir: t.TempDir(),
		Accounts: func() []accounts.Interface {
			return nil
		},
		SetAccountName: func(accountsTypes.Code, string, time.Time) error {
			return nil
		},
		SetAccountNameIfCurrent: func(accountsTypes.Code, string, time.Time, bool, string, time.Time) (bool, error) {
			return true, nil
		},
	})
	t.Cleanup(func() { _ = fixture.service.Close() })

	store, err := sqlitestore.Open(fixture.service.storePath())
	require.NoError(t, err)
	require.NoError(t, store.SaveIdentity(ctx, syncclient.IdentityState{
		KeyID:              keyID,
		Kind:               protocol.IdentityKindKeystore,
		AccessToken:        "access-token",
		TokenExpiry:        time.Now().Add(time.Hour).UTC(),
		DefaultNamespaceID: fixture.oldNamespaceID,
	}))
	require.NoError(t, store.SaveNamespace(ctx, syncclient.NamespaceState{
		KeyID:         keyID,
		NamespaceID:   fixture.oldNamespaceID,
		Kind:          protocol.NamespaceKindDefault,
		NamespaceHead: 7,
		DEK:           []byte("old namespace dek"),
	}))
	require.NoError(t, store.SaveItem(ctx, syncclient.ItemState{
		KeyID:       keyID,
		NamespaceID: fixture.oldNamespaceID,
		Collection:  "collection",
		Key:         "key",
		ItemID:      fixture.oldItemID,
		Version:     3,
		BaseVersion: 3,
		BaseValue:   []byte("base-value"),
	}))
	require.NoError(t, store.Close())

	return fixture
}

func (fixture *rollbackRepairFixture) handleSyncServerRequest(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		fixture.serverMu.Lock()
		defer fixture.serverMu.Unlock()

		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/namespaces/default":
			var req protocol.EnsureDefaultNamespaceRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			fixture.repairedNamespace = req.ProposedNamespaceID
			writeTestJSON(t, w, protocol.EnsureDefaultNamespaceResponse{
				NamespaceID: fixture.repairedNamespace,
				Kind:        protocol.NamespaceKindDefault,
				WrappedDEK:  req.WrappedDEK,
				Created:     true,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/namespaces/mine":
			fixture.listNamespaceCalls++
			if fixture.listNamespaceCalls == 1 {
				writeTestJSON(t, w, protocol.ListNamespacesResponse{})
				return
			}
			require.NotEmpty(t, fixture.repairedNamespace)
			writeTestJSON(t, w, protocol.ListNamespacesResponse{
				Namespaces: []protocol.NamespaceSummary{{
					NamespaceID:   fixture.repairedNamespace,
					Kind:          protocol.NamespaceKindDefault,
					NamespaceHead: 0,
				}},
			})
		case r.Method == http.MethodGet &&
			strings.HasPrefix(r.URL.Path, "/v1/namespaces/") &&
			strings.HasSuffix(r.URL.Path, "/items"):
			require.Contains(t, r.URL.Path, fixture.repairedNamespace)
			writeTestJSON(t, w, protocol.GetNamespaceItemsResponse{
				NamespaceID:   fixture.repairedNamespace,
				NamespaceHead: 0,
				Items:         map[string]protocol.NamespaceItemVersion{},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/namespaces/watch":
			writeTestJSON(t, w, protocol.WatchNamespacesResponse{TimedOut: true})
		default:
			http.NotFound(w, r)
		}
	}
}

func (fixture *rollbackRepairFixture) openStaleRunner(t *testing.T, ctx context.Context) *runner {
	t.Helper()
	staleRunner := newRunner(fixture.service.Config, fixture.rootFingerprintHex)
	require.NoError(t, staleRunner.open(ctx, fixture.identity, fixture.rootFingerprint, fixture.service.storePath()))
	fixture.service.mu.Lock()
	fixture.service.runners[fixture.rootFingerprintHex] = staleRunner
	fixture.service.mu.Unlock()
	return staleRunner
}

func (fixture *rollbackRepairFixture) requireRepaired(t *testing.T, ctx context.Context) {
	t.Helper()
	status := fixture.service.Status().Keystores[fixture.rootFingerprintHex]
	require.True(t, status.Running)
	require.Empty(t, status.LastError)
	require.NotNil(t, status.LastSync)
	require.NotEqual(t, fixture.oldNamespaceID, status.DefaultNamespaceID)
	require.NotEmpty(t, status.DefaultNamespaceID)
	require.NoError(t, fixture.service.Close())

	store, err := sqlitestore.Open(fixture.service.storePath())
	require.NoError(t, err)
	defer store.Close()
	identityState, err := store.LoadIdentity(ctx, fixture.keyID)
	require.NoError(t, err)
	require.Equal(t, "access-token", identityState.AccessToken)
	require.Equal(t, status.DefaultNamespaceID, identityState.DefaultNamespaceID)
	_, err = store.GetNamespace(ctx, fixture.keyID, fixture.oldNamespaceID)
	require.ErrorIs(t, err, syncclient.ErrNotFound)
	_, err = store.GetItemByID(ctx, fixture.keyID, fixture.oldNamespaceID, fixture.oldItemID)
	require.ErrorIs(t, err, syncclient.ErrNotFound)

	fixture.serverMu.Lock()
	defer fixture.serverMu.Unlock()
	require.GreaterOrEqual(t, fixture.listNamespaceCalls, 2)
}

func testRunnerWithStatus(rootFingerprint string, status KeystoreStatus) *runner {
	runner := newRunner(Config{}, rootFingerprint)
	runner.status = status
	return runner
}

func testRunnerWithAuthStatus(rootFingerprint string, status KeystoreAuthStatus) *runner {
	runner := newRunner(Config{}, rootFingerprint)
	runner.status.AuthStatus = status
	return runner
}

// blockingKindIdentity blocks Kind until the test releases it.
type blockingKindIdentity struct {
	// Identity is the wrapped identity whose methods are otherwise used unchanged.
	raw.Identity

	// entered is closed when Kind has started.
	entered chan struct{}

	// enteredOnce ensures entered is closed at most once.
	enteredOnce sync.Once

	// release lets the test unblock Kind.
	release chan struct{}
}

// Kind implements raw.Identity.
func (identity *blockingKindIdentity) Kind() string {
	identity.enteredOnce.Do(func() { close(identity.entered) })
	<-identity.release
	return identity.Identity.Kind()
}

func TestDisableUsesRunningKeystoreKeyID(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	service := New(Config{DataDir: dataDir})
	rootFingerprint := []byte{1, 2, 3, 4}
	identity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(identity)
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
		KeyID:       keyID,
		NamespaceID: namespaceID,
		Kind:        "default",
		DEK:         []byte("namespace-dek"),
	}))
	require.NoError(t, store.Close())

	service.runners = map[string]*runner{
		"01020304": testRunnerWithStatus("01020304", KeystoreStatus{
			Running: true,
		}),
	}
	service.runners["01020304"].keyID = keyID
	require.NoError(t, service.Disable(rootFingerprint, nil))

	store, err = sqlitestore.Open(service.storePath())
	require.NoError(t, err)
	defer store.Close()
	identityState, err := store.LoadIdentity(ctx, keyID)
	require.NoError(t, err)
	require.Empty(t, identityState.AccessToken)
	namespace, err := store.GetNamespace(ctx, keyID, namespaceID)
	require.NoError(t, err)
	require.Empty(t, namespace.DEK)
	require.Empty(t, service.Status().Keystores)
}

func TestDisableDerivesKeyIDFromCachedPublicIdentity(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	service := New(Config{DataDir: dataDir})
	rootFingerprint := []byte{1, 2, 3, 4}
	identity, err := raw.NewDummyKeystore("cached-identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(identity)
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
		KeyID:       keyID,
		NamespaceID: namespaceID,
		Kind:        "default",
		DEK:         []byte("namespace-dek"),
	}))
	require.NoError(t, store.Close())

	require.NoError(t, service.Disable(rootFingerprint, &publicIdentity))

	store, err = sqlitestore.Open(service.storePath())
	require.NoError(t, err)
	defer store.Close()
	identityState, err := store.LoadIdentity(ctx, keyID)
	require.NoError(t, err)
	require.Empty(t, identityState.AccessToken)
	namespace, err := store.GetNamespace(ctx, keyID, namespaceID)
	require.NoError(t, err)
	require.Empty(t, namespace.DEK)
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
