// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"
	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"
	sqlitestore "github.com/BitBoxSwiss/bitboxsync-client-go/storage/sqlite"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts"
	accountsTypes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/types"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
	"github.com/sirupsen/logrus"
)

const (
	// Namespace long polling provides prompt remote wakeups; this interval is
	// the correctness fallback for missed notifications and unsupported servers.
	pollInterval = 5 * time.Minute
	refreshSkew  = 45 * 24 * time.Hour
)

// Status describes the current BitBoxSync state.
type Status struct {
	// BaseURL is the BitBoxSync server URL used by this service.
	BaseURL string `json:"baseURL"`
	// Keystores maps root fingerprints to their sync runtime status.
	Keystores map[string]KeystoreStatus `json:"keystores"`
}

// KeystoreStatus describes the BitBoxSync state for one keystore.
type KeystoreStatus struct {
	// Running reports whether a local sync engine is currently active.
	Running bool `json:"running"`
	// DefaultNamespaceID is the namespace used for this keystore's wallet metadata.
	DefaultNamespaceID string `json:"defaultNamespaceID,omitempty"`
	// LastSync is the time of the last successful sync pass.
	LastSync *time.Time `json:"lastSync,omitempty"`
	// LastError is the latest user-visible sync error, if any.
	LastError string `json:"lastError,omitempty"`
	// AuthStatus is the current auth/session state.
	AuthStatus KeystoreAuthStatus `json:"authStatus"`
}

// KeystoreAuthStatus describes auth/session state for one keystore.
type KeystoreAuthStatus struct {
	// DaysLeft is derived when status is read and populated on the
	// returned status copy when token expiry is known and refresh is recommended.
	DaysLeft *int `json:"daysLeft,omitempty"`
	// LoginRequired reports that the user must reconnect the keystore before sync can continue.
	LoginRequired bool `json:"loginRequired,omitempty"`
	// RefreshRecommended reports that reconnecting the keystore would refresh auth before expiry.
	RefreshRecommended bool `json:"refreshRecommended,omitempty"`
	// tokenExpiresAt stays internal; the API exposes only rounded daysLeft.
	tokenExpiresAt *time.Time
}

// Config contains dependencies for a BitBoxSync service.
type Config struct {
	// BaseURL is the BitBoxSync server URL used by new runners.
	BaseURL string
	// DataDir is the directory where the local BitBoxSync SQLite store is kept.
	DataDir string
	// HTTPClient is the HTTP client used by the raw BitBoxSync API client.
	HTTPClient *http.Client
	// Accounts returns the currently loaded wallet accounts for collection backends.
	Accounts func() []accounts.Interface
	// SetAccountName writes a synced account name without a compare-and-swap guard.
	SetAccountName AccountNameSetter
	// SetAccountNameIfCurrent writes a synced account name only if the local value matches.
	SetAccountNameIfCurrent AccountNameConditionalSetter
	// NotifyTxNotesChanged tells the app to reload transaction-note views for an account.
	NotifyTxNotesChanged func(accountsTypes.Code)
	// NotifyAuthStatusChanged tells the app that auth banner state changed for a keystore.
	NotifyAuthStatusChanged func(rootFingerprint string)
	// Log receives BitBoxSync runtime, sync, and conflict diagnostics.
	Log *logrus.Entry
}

// Service manages the BitBoxSync engine for wallet metadata.
type Service struct {
	// mu protects lifecycleLocks and runners.
	mu sync.Mutex
	// Config contains dependencies shared by all per-keystore runners.
	Config Config
	// lifecycleLocks serializes start/enable/login/disable per keystore so
	// those operations cannot open duplicate engines or close an engine while
	// another lifecycle operation is still deciding what runner state exists.
	lifecycleLocks map[string]*sync.Mutex

	// runners holds per-keystore sync runners by root fingerprint. A runner can
	// be inactive when startup discovered that the device is required before the
	// engine can be opened.
	runners map[string]*runner
}

// New creates a disabled BitBoxSync service.
func New(config Config) *Service {
	return &Service{
		Config:         config,
		lifecycleLocks: map[string]*sync.Mutex{},
		runners:        map[string]*runner{},
	}
}

// Start starts metadata sync for an already enabled identity without allowing a
// device prompt.
func (s *Service) Start(ctx context.Context, identity raw.Identity, rootFingerprint []byte) (KeystoreStatus, error) {
	return s.ensureRunner(ctx, identity, rootFingerprint, promptNever)
}

// Enable starts metadata sync for identity and allows the explicit setup flow
// to prompt for the keystore if authentication or namespace setup needs it.
func (s *Service) Enable(ctx context.Context, identity raw.Identity, rootFingerprint []byte) (KeystoreStatus, error) {
	return s.ensureRunner(ctx, identity, rootFingerprint, promptMayAsk)
}

// Login refreshes an enabled keystore's BitBoxSync setup. It is the only
// service operation allowed to prompt for the keystore after initial enable.
func (s *Service) Login(ctx context.Context, identity raw.Identity, rootFingerprint []byte) (KeystoreStatus, error) {
	rootFingerprintHex, unlock, err := s.lockLifecycle(rootFingerprint)
	if err != nil {
		return KeystoreStatus{}, err
	}
	defer unlock()
	ctx = applyPromptPolicy(ctx, promptMayAsk)
	runner, err := s.snapshotRunnerByRootFingerprint(rootFingerprintHex)
	if err == nil {
		// A registered runner means namespace setup and collection registration
		// already completed, so reconnect the existing engine instead of
		// opening another one with duplicate background goroutines.
		if err := runner.login(ctx); err != nil {
			if errors.Is(err, syncclient.ErrRollback) {
				return s.repairRollbackAndSyncLocked(ctx, rootFingerprintHex, runner, rootFingerprint, promptMayAsk)
			}
			return runner.statusSnapshot(), err
		}
		return runner.statusSnapshot(), nil
	}
	if !errors.Is(err, errDisabled) {
		return KeystoreStatus{}, err
	}
	return s.ensureRunnerLocked(ctx, identity, rootFingerprint, rootFingerprintHex, promptMayAsk)
}

// Disable stops sync and forgets locally cached secrets for the identity.
//
// This clears cached namespace DEKs and access tokens but keeps namespace heads,
// item versions, merge bases, queued writes, and unresolved conflict state. It
// does not delete wallet data such as transaction notes.
func (s *Service) Disable(rootFingerprint []byte, publicIdentity *PublicIdentity) error {
	rootFingerprintHex, unlock, err := s.lockLifecycle(rootFingerprint)
	if err != nil {
		return err
	}
	defer unlock()

	s.mu.Lock()
	runner := s.runners[rootFingerprintHex]
	var keyID string
	if runner != nil {
		keyID = runner.keyIDSnapshot()
		delete(s.runners, rootFingerprintHex)
	}
	s.mu.Unlock()
	s.notifyAuthStatusChangedIfConfigured(rootFingerprintHex)

	if runner != nil {
		if err := runner.close(); err != nil {
			return err
		}
	}
	if keyID == "" && publicIdentity != nil {
		keyID, err = publicIdentity.KeyID()
		if err != nil {
			return err
		}
	}
	if keyID == "" {
		if s.Config.Log != nil {
			s.Config.Log.WithField("rootFingerprint", rootFingerprintHex).Warn("BitBoxSync disable: KeyID missing")
		}
	}
	return s.forgetIdentitySecrets(keyID)
}

// Close stops sync and releases resources without deleting local sync state.
func (s *Service) Close() error {
	s.mu.Lock()
	runners := s.runners
	s.runners = map[string]*runner{}
	s.mu.Unlock()

	var firstErr error
	for _, runner := range runners {
		if err := runner.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Status returns a snapshot of the BitBoxSync status.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := Status{
		BaseURL:   s.Config.BaseURL,
		Keystores: map[string]KeystoreStatus{},
	}
	for rootFingerprint, runner := range s.runners {
		status.Keystores[rootFingerprint] = runner.statusSnapshot()
	}
	return status
}

// SyncNow performs a foreground sync pass.
func (s *Service) SyncNow(ctx context.Context, rootFingerprint []byte) error {
	rootFingerprintHex, unlock, err := s.lockLifecycle(rootFingerprint)
	if err != nil {
		return err
	}
	defer unlock()
	runner, err := s.snapshotRunnerByRootFingerprint(rootFingerprintHex)
	if err != nil {
		return err
	}
	if err := runner.syncNow(ctx); err != nil {
		if errors.Is(err, syncclient.ErrRollback) {
			_, err := s.repairRollbackAndSyncLocked(ctx, rootFingerprintHex, runner, rootFingerprint, promptNever)
			return err
		}
		return err
	}
	return nil
}

// ScheduleSync asks the background sync engine to reconcile soon.
//
// This is a local wake-up only. It is intentionally a no-op when sync is
// disabled so app writes can schedule sync opportunistically without changing
// their error behavior.
func (s *Service) ScheduleSync() {
	s.mu.Lock()
	runners := make([]*runner, 0, len(s.runners))
	for _, runner := range s.runners {
		runners = append(runners, runner)
	}
	s.mu.Unlock()
	for _, runner := range runners {
		runner.scheduleSync()
	}
}

var errDisabled = errp.New("BitBoxSync is disabled")

// ensureRunner serializes one lifecycle operation and starts a runner if needed.
func (s *Service) ensureRunner(
	ctx context.Context,
	identity raw.Identity,
	rootFingerprint []byte,
	policy promptPolicy,
) (KeystoreStatus, error) {
	rootFingerprintHex, unlock, err := s.lockLifecycle(rootFingerprint)
	if err != nil {
		return KeystoreStatus{}, err
	}
	defer unlock()
	return s.ensureRunnerLocked(ctx, identity, rootFingerprint, rootFingerprintHex, policy)
}

// ensureRunnerLocked starts one runner while the caller holds the lifecycle lock.
func (s *Service) ensureRunnerLocked(
	ctx context.Context,
	identity raw.Identity,
	rootFingerprint []byte,
	rootFingerprintHex string,
	policy promptPolicy,
) (KeystoreStatus, error) {
	if existing := s.statusSnapshot(rootFingerprintHex); existing.Running {
		return existing, nil
	}
	ctx = applyPromptPolicy(ctx, policy)
	runner := s.ensureRunnerRecord(rootFingerprintHex)
	err := runner.open(ctx, identity, rootFingerprint, s.storePath())
	if err != nil {
		if errors.Is(err, syncclient.ErrRollback) {
			status, _, err := s.repairRollbackLocked(ctx, rootFingerprintHex, runner, rootFingerprint, policy)
			if isNeedsDevice(err) && policy == promptNever {
				return status, nil
			}
			return status, err
		}
		runner.recordError(err)
		if isNeedsDevice(err) && policy == promptNever {
			// Startup is intentionally non-interactive. Auth/login requirements are
			// surfaced through auth status, so a needed device prompt is not a
			// startup failure.
			runner.recordAuthLoginRequired(time.Time{})
			return runner.statusSnapshot(), nil
		}
		return runner.statusSnapshot(), err
	}

	status := runner.statusSnapshot()
	if s.Config.Log != nil {
		s.Config.Log.WithFields(logrus.Fields{
			"baseURL":         s.Config.BaseURL,
			"sqlite":          s.storePath(),
			"keyID":           runner.keyIDSnapshot(),
			"namespaceID":     status.DefaultNamespaceID,
			"rootFingerprint": rootFingerprintHex,
		}).Info("BitBoxSync enabled")
	}
	runner.startBackground(s.repairBackgroundRollback)
	return status, nil
}

// repairBackgroundRollback resets local sync metadata after a background rollback.
func (s *Service) repairBackgroundRollback(runner *runner) {
	rootFingerprintHex := runner.rootFingerprint
	rootFingerprint, err := hex.DecodeString(rootFingerprintHex)
	if err != nil {
		runner.recordError(err)
		return
	}
	lockedRootFingerprintHex, unlock, err := s.lockLifecycle(rootFingerprint)
	if err != nil {
		runner.recordError(err)
		return
	}
	defer unlock()
	if lockedRootFingerprintHex != rootFingerprintHex {
		runner.recordError(errp.New("BitBoxSync rollback repair root fingerprint mismatch"))
		return
	}
	s.mu.Lock()
	current := s.runners[rootFingerprintHex]
	s.mu.Unlock()
	if current != runner {
		return
	}
	if _, _, err := s.repairRollbackLocked(context.Background(), rootFingerprintHex, runner, rootFingerprint, promptNever); err != nil {
		if !isNeedsDevice(err) {
			runner.recordError(err)
		}
		return
	}
}

// repairRollbackAndSyncLocked repairs rollback state and retries one foreground sync.
func (s *Service) repairRollbackAndSyncLocked(
	ctx context.Context,
	rootFingerprintHex string,
	oldRunner *runner,
	rootFingerprint []byte,
	policy promptPolicy,
) (KeystoreStatus, error) {
	status, runner, err := s.repairRollbackLocked(ctx, rootFingerprintHex, oldRunner, rootFingerprint, policy)
	if err != nil {
		return status, err
	}
	if err := runner.syncNow(applyPromptPolicy(ctx, policy)); err != nil {
		return runner.statusSnapshot(), err
	}
	return runner.statusSnapshot(), nil
}

// repairRollbackLocked resets local sync metadata and reopens one runner.
func (s *Service) repairRollbackLocked(
	ctx context.Context,
	rootFingerprintHex string,
	oldRunner *runner,
	rootFingerprint []byte,
	policy promptPolicy,
) (KeystoreStatus, *runner, error) {
	identity, keyID, ok := oldRunner.reopenStateSnapshot()
	if !ok {
		return oldRunner.statusSnapshot(), nil, errp.New("BitBoxSync rollback repair is missing runner state")
	}
	s.mu.Lock()
	current := s.runners[rootFingerprintHex]
	s.mu.Unlock()
	if current != oldRunner {
		return KeystoreStatus{}, nil, errDisabled
	}

	if s.Config.Log != nil {
		s.Config.Log.WithFields(logrus.Fields{
			"keyID":           keyID,
			"rootFingerprint": rootFingerprintHex,
		}).Warn("BitBoxSync rollback detected; resetting local sync metadata")
	}
	if err := oldRunner.close(); err != nil {
		return oldRunner.statusSnapshot(), nil, err
	}
	if err := s.resetSyncState(keyID); err != nil {
		oldRunner.recordError(err)
		return oldRunner.statusSnapshot(), nil, err
	}

	runner := newRunner(s.Config, rootFingerprintHex)
	s.mu.Lock()
	if s.runners[rootFingerprintHex] != oldRunner {
		s.mu.Unlock()
		return KeystoreStatus{}, nil, errDisabled
	}
	s.runners[rootFingerprintHex] = runner
	s.mu.Unlock()

	ctx = applyPromptPolicy(ctx, policy)
	if err := runner.open(ctx, identity, rootFingerprint, s.storePath()); err != nil {
		runner.recordError(err)
		if isNeedsDevice(err) {
			runner.recordAuthLoginRequired(time.Time{})
		}
		return runner.statusSnapshot(), nil, err
	}
	status := runner.statusSnapshot()
	runner.startBackground(s.repairBackgroundRollback)
	return status, runner, nil
}

// ensureRunnerRecord returns the per-keystore runner record, creating it if needed.
func (s *Service) ensureRunnerRecord(rootFingerprintHex string) *runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	runner := s.runners[rootFingerprintHex]
	if runner == nil {
		runner = newRunner(s.Config, rootFingerprintHex)
		s.runners[rootFingerprintHex] = runner
	}
	return runner
}

// lockLifecycle returns the serialized lifecycle lock for a root fingerprint.
func (s *Service) lockLifecycle(rootFingerprint []byte) (string, func(), error) {
	rootFingerprintHex, err := requireRootFingerprint(rootFingerprint)
	if err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	lock := s.lifecycleLocks[rootFingerprintHex]
	if lock == nil {
		// Keep one lifecycle lock per keystore so start/enable/login/disable
		// observe each other's final runner state before deciding whether to
		// open or close an engine. Locks stay for the service lifetime; deleting
		// them is easy to race with waiters and the map is bounded by seen
		// keystores.
		lock = &sync.Mutex{}
		s.lifecycleLocks[rootFingerprintHex] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return rootFingerprintHex, lock.Unlock, nil
}

// snapshotRunnerByRootFingerprint returns the active runner for an encoded root fingerprint.
func (s *Service) snapshotRunnerByRootFingerprint(rootFingerprintHex string) (*runner, error) {
	s.mu.Lock()
	runner := s.runners[rootFingerprintHex]
	s.mu.Unlock()
	if runner == nil {
		return nil, errDisabled
	}
	if _, err := runner.engineSnapshot(); err != nil {
		return nil, err
	}
	return runner, nil
}

// statusSnapshot returns the latest status for an encoded root fingerprint.
func (s *Service) statusSnapshot(rootFingerprint string) KeystoreStatus {
	s.mu.Lock()
	runner := s.runners[rootFingerprint]
	s.mu.Unlock()
	if runner == nil {
		return KeystoreStatus{}
	}
	return runner.statusSnapshot()
}

// storePath returns the SQLite store path for all BitBoxSync runners.
func (s *Service) storePath() string {
	return filepath.Join(s.Config.DataDir, "sync.sqlite")
}

// forgetIdentitySecrets removes locally cached auth tokens and namespace DEKs for keyID.
func (s *Service) forgetIdentitySecrets(keyID string) error {
	if keyID == "" {
		return nil
	}
	if err := os.MkdirAll(s.Config.DataDir, 0700); err != nil {
		return errp.WithStack(err)
	}
	store, err := sqlitestore.Open(s.storePath())
	if err != nil {
		return err
	}
	defer store.Close()
	return store.ForgetIdentitySecrets(context.Background(), keyID)
}

// resetSyncState removes local namespace and item metadata for keyID after rollback.
func (s *Service) resetSyncState(keyID string) error {
	if keyID == "" {
		return nil
	}
	if err := os.MkdirAll(s.Config.DataDir, 0700); err != nil {
		return errp.WithStack(err)
	}
	store, err := sqlitestore.Open(s.storePath())
	if err != nil {
		return err
	}
	defer store.Close()
	return store.ResetSyncState(context.Background(), keyID)
}

// requireRootFingerprint validates and encodes a keystore root fingerprint.
func requireRootFingerprint(rootFingerprint []byte) (string, error) {
	if len(rootFingerprint) == 0 {
		return "", errp.New("BitBoxSync wallet is required")
	}
	return hex.EncodeToString(rootFingerprint), nil
}

// notifyAuthStatusChangedIfConfigured emits an auth-status notification when configured.
func (s *Service) notifyAuthStatusChangedIfConfigured(rootFingerprint string) {
	if s.Config.NotifyAuthStatusChanged != nil {
		s.Config.NotifyAuthStatusChanged(rootFingerprint)
	}
}
