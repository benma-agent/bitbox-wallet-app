// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"
	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"
	sqlitestore "github.com/BitBoxSwiss/bitboxsync-client-go/storage/sqlite"

	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
)

// runner owns one running BitBoxSync engine and its registered wallet-data collections.
type runner struct {
	// mu protects status, cancel, engine, collections, and namespaceID.
	mu sync.Mutex
	// config contains the app dependencies this runner uses for event handling.
	config Config
	// rootFingerprint identifies the keystore whose metadata this runner syncs.
	rootFingerprint string
	// identity is the BitBoxSync identity used to reopen this runner after local state repair.
	identity raw.Identity
	// keyID identifies the BitBoxSync auth identity persisted in the sync store.
	keyID string
	// namespaceID identifies the default namespace used for wallet metadata.
	namespaceID string
	// storePath is the SQLite store path used by this runner.
	storePath string
	// cancel stops the background engine and event goroutines.
	cancel context.CancelFunc
	// engine coordinates authentication, namespace state, and collection sync.
	engine *syncclient.Engine
	// collections contains the concrete wallet-data collection backends registered on engine.
	collections *runnerCollections
	// status holds the latest runtime status for this runner.
	status KeystoreStatus
}

// newRunner creates one inactive per-keystore runner record.
func newRunner(config Config, rootFingerprint string) *runner {
	return &runner{
		config:          config,
		rootFingerprint: rootFingerprint,
	}
}

// open constructs one ready-to-run BitBoxSync engine for a keystore.
func (r *runner) open(
	ctx context.Context,
	identity raw.Identity,
	rootFingerprint []byte,
	storePath string,
) error {
	publicIdentity, err := PublicIdentityFromRaw(identity)
	if err != nil {
		return err
	}
	keyID, err := publicIdentity.KeyID()
	if err != nil {
		return err
	}
	client, err := raw.New(r.config.BaseURL, r.config.HTTPClient)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.config.DataDir, 0700); err != nil {
		return errp.WithStack(err)
	}
	store, err := sqlitestore.Open(storePath)
	if err != nil {
		return err
	}
	engine, err := syncclient.Open(ctx, syncclient.Config{
		Client:       client,
		Identity:     identity,
		Store:        store,
		PollInterval: pollInterval,
		RefreshSkew:  refreshSkew,
	})
	if err != nil {
		_ = store.Close()
		return err
	}

	r.mu.Lock()
	r.identity = identity
	r.keyID = keyID
	r.storePath = storePath
	r.engine = engine
	r.status.Running = true
	r.status.LastError = ""
	r.mu.Unlock()

	namespace, err := engine.DefaultNamespace(ctx)
	r.handlePendingEvents(ctx)
	if err != nil {
		_ = r.close()
		return err
	}
	collections, err := registerRunnerCollections(namespace, r.config, rootFingerprint)
	if err != nil {
		_ = r.close()
		return err
	}
	r.mu.Lock()
	r.namespaceID = namespace.ID()
	r.collections = collections
	r.status.DefaultNamespaceID = namespace.ID()
	r.mu.Unlock()
	return nil
}

// startBackground launches the runner's event loop and sync engine loop.
func (r *runner) startBackground(rollbackHandler func(*runner)) {
	runCtx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
	go r.runEventLoop(runCtx, rollbackHandler)
	go r.runEngine(runCtx)
}

// login performs an explicit login and immediate foreground sync on this runner.
func (r *runner) login(ctx context.Context) error {
	engine, err := r.engineSnapshot()
	if err != nil {
		r.recordError(err)
		return err
	}
	if err := engine.Login(ctx); err != nil {
		r.recordError(err)
		return err
	}
	if err := engine.SyncNow(ctx); err != nil {
		r.recordError(err)
		return err
	}
	r.recordSuccess()
	return nil
}

// syncNow performs an immediate foreground sync on this runner.
func (r *runner) syncNow(ctx context.Context) error {
	engine, err := r.engineSnapshot()
	if err != nil {
		return err
	}
	if err := engine.SyncNow(ctx); err != nil {
		r.recordError(err)
		return err
	}
	r.recordSuccess()
	return nil
}

// scheduleSync wakes the background sync loop for this runner.
func (r *runner) scheduleSync() {
	if engine, err := r.engineSnapshot(); err == nil {
		engine.ScheduleSync()
	}
}

// close stops this runner and releases the sync engine resources.
func (r *runner) close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	engine := r.engine
	r.engine = nil
	r.collections = nil
	r.status.Running = false
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if engine != nil {
		return engine.Close()
	}
	return nil
}

// engineSnapshot returns this runner's active engine.
func (r *runner) engineSnapshot() (*syncclient.Engine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.engine == nil {
		return nil, errDisabled
	}
	return r.engine, nil
}

// collectionsSnapshot returns the collection backends registered on this runner.
func (r *runner) collectionsSnapshot() *runnerCollections {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.collections
}

// keyIDSnapshot returns the BitBoxSync auth identity key ID known to this runner.
func (r *runner) keyIDSnapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keyID
}

// reopenStateSnapshot returns the identity state needed to reopen this runner.
func (r *runner) reopenStateSnapshot() (raw.Identity, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.identity == nil || r.keyID == "" {
		return nil, "", false
	}
	return r.identity, r.keyID, true
}

// runEngine drives the sync engine until the runner is closed.
func (r *runner) runEngine(ctx context.Context) {
	engine, err := r.engineSnapshot()
	if err != nil {
		return
	}
	if err := engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, syncclient.ErrClosed) {
		r.recordError(err)
	}
}

// statusSnapshot returns the externally visible runtime status for this runner.
func (r *runner) statusSnapshot() KeystoreStatus {
	r.mu.Lock()
	status := r.status
	r.mu.Unlock()
	status.AuthStatus = authStatusSnapshot(status.AuthStatus, time.Now())
	return status
}

// authStatusSnapshot returns externally visible auth status.
func authStatusSnapshot(status KeystoreAuthStatus, now time.Time) KeystoreAuthStatus {
	snapshot := KeystoreAuthStatus{
		LoginRequired:      status.LoginRequired,
		RefreshRecommended: status.RefreshRecommended,
	}
	if status.tokenExpiresAt == nil {
		return snapshot
	}
	daysLeft := authTokenDaysLeft(now, *status.tokenExpiresAt)
	if daysLeft == 0 && snapshot.RefreshRecommended {
		snapshot.LoginRequired = true
		snapshot.RefreshRecommended = false
		return snapshot
	}
	if snapshot.RefreshRecommended {
		snapshot.DaysLeft = &daysLeft
	}
	return snapshot
}

// recordSuccess updates runtime status after a successful sync operation.
func (r *runner) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.status.Running = r.engine != nil
	r.status.LastSync = &now
	r.status.LastError = ""
}

// recordError updates runtime status after a failed sync operation.
func (r *runner) recordError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	r.status.Running = r.engine != nil
	if isNeedsDevice(err) {
		// Background sync is not allowed to prompt for a keystore. Auth/session
		// prompts are delivered via auth status, so this expected condition is
		// neither a user-visible sync error nor something to warn-log.
		r.mu.Unlock()
		return
	}
	r.status.LastError = err.Error()
	r.mu.Unlock()
	if r.config.Log != nil {
		r.config.Log.WithError(err).WithField("rootFingerprint", r.rootFingerprint).Warn("BitBoxSync error")
	}
}

// recordAuthLoginRequired marks auth as requiring a user reconnect.
func (r *runner) recordAuthLoginRequired(tokenExpiresAt time.Time) {
	r.updateAuthStatus(func(status *KeystoreAuthStatus) {
		status.LoginRequired = true
		status.RefreshRecommended = false
		setTokenExpiresAt(status, tokenExpiresAt)
	})
}

// recordAuthRefreshRecommended marks auth as valid but nearing expiry.
func (r *runner) recordAuthRefreshRecommended(tokenExpiresAt time.Time) {
	r.updateAuthStatus(func(status *KeystoreAuthStatus) {
		status.LoginRequired = false
		status.RefreshRecommended = true
		setTokenExpiresAt(status, tokenExpiresAt)
	})
}

// recordAuthSessionReady marks auth as ready and clears reconnect prompts.
func (r *runner) recordAuthSessionReady(tokenExpiresAt time.Time) {
	r.updateAuthStatus(func(status *KeystoreAuthStatus) {
		status.LoginRequired = false
		status.RefreshRecommended = false
		setTokenExpiresAt(status, tokenExpiresAt)
	})
}

// updateAuthStatus updates this runner's auth status and emits the configured notification.
func (r *runner) updateAuthStatus(update func(*KeystoreAuthStatus)) {
	r.mu.Lock()
	status := r.status.AuthStatus
	update(&status)
	r.status.AuthStatus = status
	r.mu.Unlock()
	if r.config.NotifyAuthStatusChanged != nil {
		r.config.NotifyAuthStatusChanged(r.rootFingerprint)
	}
}

// setTokenExpiresAt stores token expiry internally and clears derived days-left state.
func setTokenExpiresAt(status *KeystoreAuthStatus, tokenExpiresAt time.Time) {
	status.DaysLeft = nil
	status.tokenExpiresAt = nil
	if tokenExpiresAt.IsZero() {
		return
	}
	expiresAt := tokenExpiresAt
	status.tokenExpiresAt = &expiresAt
}

// authTokenDaysLeft returns the rounded number of days before a token expires.
func authTokenDaysLeft(now, tokenExpiresAt time.Time) int {
	remaining := tokenExpiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	if remaining < 2*24*time.Hour {
		return 1
	}
	return int((remaining + 12*time.Hour) / (24 * time.Hour))
}
