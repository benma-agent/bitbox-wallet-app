// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"maps"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts"
	accountNotes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/notes"
	accountsTypes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/types"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
)

const (
	txNotesCollection  = "tx-notes-v1"
	txNotesBucketCount = 256

	txNotesKeySegmentSeparator = "/"
)

// Transaction note sync schema:
//
// Collection: tx-notes-v1
//
// Each currently active single-sig account owned by the enabled keystore
// exposes 256 fixed bucket items:
//
//	key:   <url-path-escaped-account-code>/135
//	value: {"<internalTxID>":{"note":"<note>","modifiedAt":"2026-05-11T10:24:00Z"}}
//
// A transaction note belongs to the bucket identified by the first byte of
// SHA-256(internalTxID). Empty note strings are tombstones with their own
// modifiedAt timestamp and are kept in the normal wallet notes store so
// deletions can sync and win over older non-empty notes.
type txNotesBucket map[string]txNoteEntry

// txNoteEntry stores one synced transaction note and the edit timestamp used for conflict resolution.
type txNoteEntry struct {
	// Note is the user-entered note text, or an empty tombstone for deletion.
	Note string `json:"note"`
	// ModifiedAt is the timestamp used to order conflicting edits.
	ModifiedAt time.Time `json:"modifiedAt"`
}

func encodeTxNotesBucketKey(accountCode accountsTypes.Code, bucket int) (string, error) {
	if accountCode == "" {
		return "", errp.New("invalid transaction note account code")
	}
	if bucket < 0 || bucket >= txNotesBucketCount {
		return "", errp.New("invalid transaction notes bucket")
	}
	return strings.Join([]string{
		url.PathEscape(string(accountCode)),
		strconv.Itoa(bucket),
	}, txNotesKeySegmentSeparator), nil
}

func decodeTxNoteBucketKey(key string) (accountsTypes.Code, int, error) {
	parts := strings.Split(key, txNotesKeySegmentSeparator)
	if len(parts) != 2 {
		return "", 0, errp.New("invalid transaction note key")
	}
	accountCode, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", 0, errp.WithStack(err)
	}
	if accountCode == "" {
		return "", 0, errp.New("invalid transaction note key")
	}
	bucket, err := strconv.Atoi(parts[1])
	if err != nil || strconv.Itoa(bucket) != parts[1] || bucket < 0 || bucket >= txNotesBucketCount {
		return "", 0, errp.New("invalid transaction note bucket key")
	}
	return accountsTypes.Code(accountCode), bucket, nil
}

func txNoteBucketIndex(internalTxID string) int {
	sum := sha256.Sum256([]byte(internalTxID))
	return int(sum[0])
}

// txNotesValueBackend adapts account transaction notes to a BitBoxSync value backend.
type txNotesValueBackend struct {
	// accounts returns the currently loaded wallet accounts eligible for sync inspection.
	accounts func() []accounts.Interface
	// rootFingerprint scopes synced notes to accounts owned by the enabled keystore.
	rootFingerprint []byte
	// notifyTxNotesChanged asks the app to reload transaction data for an account.
	notifyTxNotesChanged func(accountsTypes.Code)

	// mu protects changedAccounts while sync applies remote bucket values.
	mu sync.Mutex
	// changedAccounts accumulates accounts whose notes changed while the sync
	// engine applies remote values. The service flushes it on a terminal sync
	// event so the frontend reloads once per account instead of once per note.
	changedAccounts map[accountsTypes.Code]struct{}
}

// newTxNotesValueBackend constructs the transaction-note value backend for one keystore.
func newTxNotesValueBackend(
	accounts func() []accounts.Interface,
	rootFingerprint []byte,
	notifyTxNotesChanged func(accountsTypes.Code),
) (*txNotesValueBackend, error) {
	if accounts == nil {
		return nil, errp.New("transaction notes accounts provider is required")
	}
	if len(rootFingerprint) == 0 {
		return nil, errp.New("transaction notes root fingerprint is required")
	}
	return &txNotesValueBackend{
		accounts:             accounts,
		rootFingerprint:      bytes.Clone(rootFingerprint),
		notifyTxNotesChanged: notifyTxNotesChanged,
	}, nil
}

// flushChangedAccountNotifications turns the accumulated account-code set into
// the existing account transaction reload events. It is called once the sync
// pass reports success or failure, not during individual note writes.
func (b *txNotesValueBackend) flushChangedAccountNotifications() {
	accounts := b.takeChangedAccounts()
	for _, accountCode := range accounts {
		b.notifyAccountChanged(accountCode)
	}
}

// recordAccountChanged defers frontend notification for sync-applied note
// writes. A remote bucket can contain many notes, and one sync pass can apply
// many buckets, so notifying here directly would make the frontend reload
// repeatedly while the pass is still in progress.
func (b *txNotesValueBackend) recordAccountChanged(accountCode accountsTypes.Code) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.changedAccounts == nil {
		b.changedAccounts = map[accountsTypes.Code]struct{}{}
	}
	b.changedAccounts[accountCode] = struct{}{}
}

// takeChangedAccounts drains the set of accounts that need one transaction-list
// reload after sync-applied note writes. The order is intentionally unspecified:
// each account is independent and only needs one reload.
func (b *txNotesValueBackend) takeChangedAccounts() []accountsTypes.Code {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.changedAccounts) == 0 {
		return nil
	}
	accountCodes := slices.Collect(maps.Keys(b.changedAccounts))
	b.changedAccounts = nil
	return accountCodes
}

// notifyAccountChanged emits a transaction-note change notification if configured.
func (b *txNotesValueBackend) notifyAccountChanged(accountCode accountsTypes.Code) {
	if b.notifyTxNotesChanged != nil {
		b.notifyTxNotesChanged(accountCode)
	}
}

// txNotesSyncAccounts returns the loaded accounts whose transaction notes
// should participate in sync. Inactive, hidden-unused, and accounts not solely
// owned by rootFingerprint are omitted here.
func txNotesSyncAccounts(
	accountList []accounts.Interface,
	rootFingerprint []byte,
) []accounts.Interface {
	filtered := make([]accounts.Interface, 0, len(accountList))
	for _, account := range accountList {
		if account == nil {
			continue
		}
		accountConfig := account.Config()
		if accountConfig == nil || accountConfig.Config == nil || account.Notes() == nil {
			continue
		}
		config := accountConfig.Config
		if config.Inactive || config.HiddenBecauseUnused {
			continue
		}
		if !config.SigningConfigurations.SolelyOwnedByRootFingerprint(rootFingerprint) {
			continue
		}
		filtered = append(filtered, account)
	}
	return filtered
}

// Keys implements syncclient.ValueBackend[txNotesBucket].
func (b *txNotesValueBackend) Keys(context.Context) ([]string, error) {
	var keys []string
	for _, account := range b.syncAccounts() {
		accountCode := account.Config().Config.Code
		for bucket := 0; bucket < txNotesBucketCount; bucket++ {
			key, err := encodeTxNotesBucketKey(accountCode, bucket)
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// Get implements syncclient.ValueBackend[txNotesBucket].
func (b *txNotesValueBackend) Get(_ context.Context, key string) (txNotesBucket, error) {
	accountCode, bucket, err := decodeTxNoteBucketKey(key)
	if err != nil {
		return nil, err
	}
	notes := b.bucketNotes(accountCode, bucket)
	if len(notes) == 0 {
		return nil, syncclient.ErrNotFound
	}
	return notes, nil
}

// Snapshot implements syncclient.ValueBackend[txNotesBucket].
func (b *txNotesValueBackend) Snapshot(context.Context) (map[string]txNotesBucket, error) {
	snapshot := map[string]txNotesBucket{}
	for _, account := range b.syncAccounts() {
		accountCode := account.Config().Config.Code
		for txID, entry := range account.Notes().TransactionNoteEntries() {
			bucket := txNoteBucketIndex(txID)
			key, err := encodeTxNotesBucketKey(accountCode, bucket)
			if err != nil {
				return nil, err
			}
			notes := snapshot[key]
			if notes == nil {
				notes = txNotesBucket{}
				snapshot[key] = notes
			}
			notes[txID] = txNoteEntryFromAccount(entry)
		}
	}
	return snapshot, nil
}

// Set implements syncclient.ValueBackend[txNotesBucket].
func (b *txNotesValueBackend) Set(_ context.Context, key string, value txNotesBucket) error {
	accountCode, bucket, err := decodeTxNoteBucketKey(key)
	if err != nil {
		return err
	}
	if err := validateTxNotesBucket(bucket, value); err != nil {
		return err
	}
	account := b.account(accountCode)
	if account == nil {
		return errp.New("transaction notes account is not loaded")
	}
	changed, err := storeAccountTxNotesBucket(account, bucket, value)
	if err != nil {
		return err
	}
	if changed {
		b.recordAccountChanged(accountCode)
	}
	return nil
}

// SetIfCurrent implements syncclient.ConditionalValueBackend[txNotesBucket].
func (b *txNotesValueBackend) SetIfCurrent(_ context.Context, key string, current txNotesBucket, currentFound bool, value txNotesBucket) (bool, error) {
	accountCode, bucket, err := decodeTxNoteBucketKey(key)
	if err != nil {
		return false, err
	}
	if err := validateTxNotesBucket(bucket, value); err != nil {
		return false, err
	}
	account := b.account(accountCode)
	if account == nil || account.Notes() == nil {
		return false, errp.New("transaction notes account is not loaded")
	}

	matched := false
	changed, err := account.Notes().UpdateTransactionNoteEntries(func(allNotes map[string]accountNotes.TransactionNoteEntry) (bool, error) {
		existing := txNotesBucketFromEntries(allNotes, bucket)
		existingFound := len(existing) > 0
		if existingFound != currentFound || (currentFound && !sameTxNotesBucket(existing, current)) {
			return false, nil
		}
		matched = true
		return replaceTxNotesBucketEntries(allNotes, bucket, value), nil
	})
	if err != nil {
		return false, err
	}
	if matched && changed {
		b.recordAccountChanged(accountCode)
	}
	return matched, nil
}

// bucketNotes returns the current local notes for one account bucket.
func (b *txNotesValueBackend) bucketNotes(accountCode accountsTypes.Code, bucket int) txNotesBucket {
	account := b.account(accountCode)
	if account == nil || account.Notes() == nil {
		return nil
	}
	return txNotesBucketFromEntries(account.Notes().TransactionNoteEntries(), bucket)
}

// account returns the loaded syncable account for accountCode.
func (b *txNotesValueBackend) account(accountCode accountsTypes.Code) accounts.Interface {
	return accountByCode(b.syncAccounts(), accountCode)
}

// syncAccounts returns the current filtered account list for this backend's keystore.
func (b *txNotesValueBackend) syncAccounts() []accounts.Interface {
	return txNotesSyncAccounts(b.accounts(), b.rootFingerprint)
}

func accountByCode(accountList []accounts.Interface, accountCode accountsTypes.Code) accounts.Interface {
	for _, account := range accountList {
		if account == nil {
			continue
		}
		accountConfig := account.Config()
		if accountConfig == nil || accountConfig.Config == nil {
			continue
		}
		if accountConfig.Config.Code == accountCode {
			return account
		}
	}
	return nil
}

func storeAccountTxNotesBucket(account accounts.Interface, bucket int, value txNotesBucket) (bool, error) {
	if account == nil {
		return false, errp.New("transaction notes account is not loaded")
	}
	if account.Notes() == nil {
		return false, errp.New("transaction notes account is not loaded")
	}
	// Write directly to the notes store rather than account.SetTxNote(): that
	// method emits an immediate account transaction reload, which would reload
	// the frontend once per remotely imported note.
	return account.Notes().UpdateTransactionNoteEntries(func(allNotes map[string]accountNotes.TransactionNoteEntry) (bool, error) {
		return replaceTxNotesBucketEntries(allNotes, bucket, value), nil
	})
}

func validateTxNotesBucket(bucket int, value txNotesBucket) error {
	for txID, entry := range value {
		if txNoteBucketIndex(txID) != bucket {
			return errp.New("transaction note does not belong to bucket")
		}
		if len(entry.Note) > accountNotes.MaxNoteLen {
			return errp.Newf("Length of note must be smaller than %d. Got %d", accountNotes.MaxNoteLen, len(entry.Note))
		}
	}
	return nil
}

func txNotesBucketFromEntries(notes map[string]accountNotes.TransactionNoteEntry, bucket int) txNotesBucket {
	bucketNotes := txNotesBucket{}
	for txID, entry := range notes {
		if txNoteBucketIndex(txID) == bucket {
			bucketNotes[txID] = txNoteEntryFromAccount(entry)
		}
	}
	return bucketNotes
}

func replaceTxNotesBucketEntries(
	allNotes map[string]accountNotes.TransactionNoteEntry,
	bucket int,
	value txNotesBucket,
) bool {
	// Collection Set/SetIfCurrent replace the whole item value. Mirror that
	// contract even though the wallet notes file stores all buckets together.
	changed := false
	for txID := range allNotes {
		if txNoteBucketIndex(txID) != bucket {
			continue
		}
		if _, ok := value[txID]; !ok {
			delete(allNotes, txID)
			changed = true
		}
	}
	for txID, entry := range value {
		accountEntry := accountEntryFromTxNote(entry)
		existingEntry, ok := allNotes[txID]
		if !ok || !sameAccountNoteEntry(existingEntry, accountEntry) {
			allNotes[txID] = accountEntry
			changed = true
		}
	}
	return changed
}

// mergeTxNotesBucket merges note buckets. Without a common base, it combines
// both buckets and keeps the newest note for same-transaction collisions. With
// a base, a note is copied from remote when only remote changed, copied from
// local when only local changed, and the newest edit wins if both sides changed
// the same note. Empty strings are explicit tombstones, not absent values.
func mergeTxNotesBucket(_ string, base *txNotesBucket, local, remote txNotesBucket) (txNotesBucket, bool, error) {
	if base == nil {
		out := txNotesBucket{}
		maps.Copy(out, local)
		for key, remoteValue := range remote {
			if localValue, ok := local[key]; ok {
				out[key] = latestTxNoteEntry(localValue, remoteValue)
				continue
			}
			out[key] = remoteValue
		}
		return out, true, nil
	}

	out := txNotesBucket{}
	keys := map[string]struct{}{}
	for key := range *base {
		keys[key] = struct{}{}
	}
	for key := range local {
		keys[key] = struct{}{}
	}
	for key := range remote {
		keys[key] = struct{}{}
	}
	for key := range keys {
		baseValue, baseOK := (*base)[key]
		localValue, localOK := local[key]
		remoteValue, remoteOK := remote[key]
		localChanged := !sameTxNoteEntryValue(baseValue, baseOK, localValue, localOK)
		remoteChanged := !sameTxNoteEntryValue(baseValue, baseOK, remoteValue, remoteOK)
		switch {
		case localChanged && remoteChanged && localOK && remoteOK:
			out[key] = latestTxNoteEntry(localValue, remoteValue)
		case localChanged:
			if localOK {
				out[key] = localValue
			}
		case remoteChanged:
			if remoteOK {
				out[key] = remoteValue
			}
		case localOK:
			out[key] = localValue
		case remoteOK:
			out[key] = remoteValue
		}
	}
	return out, true, nil
}

func txNoteEntryFromAccount(entry accountNotes.TransactionNoteEntry) txNoteEntry {
	return txNoteEntry{
		Note:       entry.Note,
		ModifiedAt: entry.Metadata.ModifiedAt.UTC(),
	}
}

func accountEntryFromTxNote(entry txNoteEntry) accountNotes.TransactionNoteEntry {
	return accountNotes.TransactionNoteEntry{
		Note: entry.Note,
		Metadata: accountNotes.TransactionMetadata{
			ModifiedAt: entry.ModifiedAt.UTC(),
		},
	}
}

func latestTxNoteEntry(local, remote txNoteEntry) txNoteEntry {
	switch {
	case remote.ModifiedAt.After(local.ModifiedAt):
		return remote
	case local.ModifiedAt.After(remote.ModifiedAt):
		return local
	case remote.Note > local.Note:
		// Equal timestamps do not identify a latest edit. Use the note text as a
		// deterministic tie-breaker so independent merges converge.
		return remote
	default:
		return local
	}
}

func sameTxNotesBucket(a, b txNotesBucket) bool {
	if len(a) != len(b) {
		return false
	}
	for key, aValue := range a {
		bValue, ok := b[key]
		if !ok || !sameTxNoteEntry(aValue, bValue) {
			return false
		}
	}
	return true
}

func sameTxNoteEntryValue(a txNoteEntry, aOK bool, b txNoteEntry, bOK bool) bool {
	return aOK == bOK && (!aOK || sameTxNoteEntry(a, b))
}

func sameTxNoteEntry(a, b txNoteEntry) bool {
	return a.Note == b.Note && a.ModifiedAt.Equal(b.ModifiedAt)
}

func sameAccountNoteEntry(a, b accountNotes.TransactionNoteEntry) bool {
	return a.Note == b.Note && a.Metadata.ModifiedAt.Equal(b.Metadata.ModifiedAt)
}
