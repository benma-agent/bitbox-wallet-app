// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"bytes"
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts"
	accountsTypes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/types"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
)

const (
	accountNameCollection = "account-name-v1"

	erc20CoinCodePrefix = "eth-erc20-"
)

// accountNameValue is the synced payload for account names.
//
// Account name sync schema:
//
// Collection: account-name-v1
//
// Each currently loaded, non-hidden single-sig account owned by the enabled
// keystore exposes one item. The value currently contains the account name.
//
//	key:   <url-path-escaped-account-code>
//	value: fixed binary accountNameValue
//
// Inactive accounts are included. Hidden-unused accounts and ERC20 token
// accounts are excluded, matching the account-label export/import behavior.
type accountNameValue struct {
	// ModifiedAt is the timestamp used to order conflicting account name edits.
	//
	// Note: do not evolve this fixed binary schema with additional fields. Add
	// new collections or items instead when syncing additional account metadata.
	ModifiedAt time.Time
	// Name stores the synced display name for one account.
	Name string
}

// AccountNameSetter persists an account name with its sync edit timestamp.
type AccountNameSetter func(accountsTypes.Code, string, time.Time) error

// AccountNameConditionalSetter persists an account name if the current stored
// name and edit timestamp still match the expected value.
type AccountNameConditionalSetter func(
	accountsTypes.Code,
	string,
	time.Time,
	bool,
	string,
	time.Time,
) (bool, error)

func encodeAccountNameKey(accountCode accountsTypes.Code) (string, error) {
	if accountCode == "" {
		return "", errp.New("invalid account name account code")
	}
	return url.PathEscape(string(accountCode)), nil
}

func decodeAccountNameKey(key string) (accountsTypes.Code, error) {
	if key == "" || strings.Contains(key, "/") {
		return "", errp.New("invalid account name key")
	}
	accountCode, err := url.PathUnescape(key)
	if err != nil {
		return "", errp.WithStack(err)
	}
	if accountCode == "" {
		return "", errp.New("invalid account name key")
	}
	return accountsTypes.Code(accountCode), nil
}

// accountNameValueBackend adapts account names to a BitBoxSync value backend.
type accountNameValueBackend struct {
	// accounts returns the currently loaded wallet accounts eligible for sync inspection.
	accounts func() []accounts.Interface
	// rootFingerprint scopes synced accounts to the enabled keystore.
	rootFingerprint []byte
	// setAccountName writes synced account names into app config.
	setAccountName AccountNameSetter
	// setAccountNameIfCurrent writes account names only when optimistic concurrency matches.
	setAccountNameIfCurrent AccountNameConditionalSetter
}

// newAccountNameValueBackend constructs the account-name value backend for one keystore.
func newAccountNameValueBackend(
	accounts func() []accounts.Interface,
	rootFingerprint []byte,
	setAccountName AccountNameSetter,
	setAccountNameIfCurrent AccountNameConditionalSetter,
) (*accountNameValueBackend, error) {
	if accounts == nil {
		return nil, errp.New("account name accounts provider is required")
	}
	if setAccountName == nil {
		return nil, errp.New("account name setter is required")
	}
	if setAccountNameIfCurrent == nil {
		return nil, errp.New("conditional account name setter is required")
	}
	if len(rootFingerprint) == 0 {
		return nil, errp.New("account name root fingerprint is required")
	}
	return &accountNameValueBackend{
		accounts:                accounts,
		rootFingerprint:         bytes.Clone(rootFingerprint),
		setAccountName:          setAccountName,
		setAccountNameIfCurrent: setAccountNameIfCurrent,
	}, nil
}

// accountNameSyncAccounts returns the loaded accounts whose account name
// should participate in sync. Unlike transaction notes, inactive accounts are
// included so name changes survive account deactivation across devices.
// Accounts not solely owned by rootFingerprint are omitted.
func accountNameSyncAccounts(
	accountList []accounts.Interface,
	rootFingerprint []byte,
) []accounts.Interface {
	filtered := make([]accounts.Interface, 0, len(accountList))
	for _, account := range accountList {
		if account == nil {
			continue
		}
		accountName := account.Config()
		if accountName == nil || accountName.Config == nil {
			continue
		}
		config := accountName.Config
		if config.HiddenBecauseUnused || strings.HasPrefix(string(config.CoinCode), erc20CoinCodePrefix) {
			continue
		}
		if !config.SigningConfigurations.SolelyOwnedByRootFingerprint(rootFingerprint) {
			continue
		}
		filtered = append(filtered, account)
	}
	return filtered
}

// Keys implements syncclient.ValueBackend[accountNameValue].
func (b *accountNameValueBackend) Keys(context.Context) ([]string, error) {
	var keys []string
	for _, account := range b.syncAccounts() {
		key, err := encodeAccountNameKey(account.Config().Config.Code)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// Get implements syncclient.ValueBackend[accountNameValue].
func (b *accountNameValueBackend) Get(_ context.Context, key string) (accountNameValue, error) {
	accountCode, err := decodeAccountNameKey(key)
	if err != nil {
		return accountNameValue{}, err
	}
	account := b.account(accountCode)
	if account == nil {
		return accountNameValue{}, syncclient.ErrNotFound
	}
	value := accountNameValueFromConfig(account)
	if value.Name == "" {
		return accountNameValue{}, syncclient.ErrNotFound
	}
	return value, nil
}

// Snapshot implements syncclient.ValueBackend[accountNameValue].
func (b *accountNameValueBackend) Snapshot(context.Context) (map[string]accountNameValue, error) {
	snapshot := map[string]accountNameValue{}
	for _, account := range b.syncAccounts() {
		config := account.Config().Config
		if config.Name == "" {
			continue
		}
		key, err := encodeAccountNameKey(config.Code)
		if err != nil {
			return nil, err
		}
		snapshot[key] = accountNameValueFromConfig(account)
	}
	return snapshot, nil
}

// Set implements syncclient.ValueBackend[accountNameValue].
func (b *accountNameValueBackend) Set(_ context.Context, key string, value accountNameValue) error {
	accountCode, err := decodeAccountNameKey(key)
	if err != nil {
		return err
	}
	if err := validateAccountNameValue(value); err != nil {
		return err
	}
	if b.account(accountCode) == nil {
		return errp.New("account is not loaded")
	}
	return b.setAccountName(accountCode, value.Name, value.ModifiedAt)
}

// SetIfCurrent implements syncclient.ConditionalValueBackend[accountNameValue].
func (b *accountNameValueBackend) SetIfCurrent(
	_ context.Context,
	key string,
	current accountNameValue,
	currentFound bool,
	value accountNameValue,
) (bool, error) {
	accountCode, err := decodeAccountNameKey(key)
	if err != nil {
		return false, err
	}
	if err := validateAccountNameValue(value); err != nil {
		return false, err
	}
	if currentFound {
		if err := validateAccountNameValue(current); err != nil {
			return false, err
		}
	}
	if b.account(accountCode) == nil {
		return false, errp.New("account is not loaded")
	}
	var currentName string
	var currentModifiedAt time.Time
	if currentFound {
		currentName = current.Name
		currentModifiedAt = current.ModifiedAt
	}
	return b.setAccountNameIfCurrent(
		accountCode,
		currentName,
		currentModifiedAt,
		currentFound,
		value.Name,
		value.ModifiedAt,
	)
}

// account returns the loaded syncable account for accountCode.
func (b *accountNameValueBackend) account(accountCode accountsTypes.Code) accounts.Interface {
	return accountByCode(b.syncAccounts(), accountCode)
}

// syncAccounts returns the current filtered account list for this backend's keystore.
func (b *accountNameValueBackend) syncAccounts() []accounts.Interface {
	return accountNameSyncAccounts(b.accounts(), b.rootFingerprint)
}

func validateAccountNameValue(value accountNameValue) error {
	if value.Name == "" {
		return errp.New("account name cannot be empty")
	}
	return nil
}

func accountNameValueFromConfig(account accounts.Interface) accountNameValue {
	config := account.Config().Config
	value := accountNameValue{}
	if config.Name == "" {
		return value
	}
	value.Name = config.Name
	if config.NameModifiedAt != nil {
		value.ModifiedAt = config.NameModifiedAt.UTC()
	}
	return value
}

func accountNameModifiedAtPtr(modifiedAt time.Time) *time.Time {
	if modifiedAt.IsZero() {
		return nil
	}
	return new(modifiedAt.UTC())
}

func accountNameModifiedAtEqual(stored *time.Time, value time.Time) bool {
	if stored == nil {
		return value.IsZero()
	}
	return stored.Equal(value)
}

// merge resolves one account-name item conflict using account-name merge rules.
func (b *accountNameValueBackend) merge(
	key string,
	base *accountNameValue,
	local accountNameValue,
	remote accountNameValue,
) (accountNameValue, bool, error) {
	accountCode, err := decodeAccountNameKey(key)
	if err != nil {
		return accountNameValue{}, false, err
	}
	defaultName := ""
	if account := b.account(accountCode); account != nil {
		if name, ok := accounts.DefaultAccountName(account); ok {
			defaultName = name
		}
	}
	return mergeAccountNameValue(defaultName, base, local, remote)
}

func mergeAccountNameValue(
	defaultName string,
	base *accountNameValue,
	local,
	remote accountNameValue,
) (accountNameValue, bool, error) {
	if base != nil {
		if err := validateAccountNameValue(*base); err != nil {
			return accountNameValue{}, false, err
		}
	}
	if err := validateAccountNameValue(local); err != nil {
		return accountNameValue{}, false, err
	}
	if err := validateAccountNameValue(remote); err != nil {
		return accountNameValue{}, false, err
	}

	name, resolved, err := mergeAccountName(defaultName, base, local, remote)
	if err != nil || !resolved {
		return accountNameValue{}, resolved, err
	}
	return name, true, nil
}

// mergeAccountName resolves account-name conflicts. Generated default names
// are low-information values, so a deliberate custom name wins over a default
// name before the last-modified timestamp is considered.
func mergeAccountName(
	defaultName string,
	base *accountNameValue,
	local,
	remote accountNameValue,
) (accountNameValue, bool, error) {
	if base == nil {
		return preferredAccountNameValue(defaultName, local, remote), true, nil
	}

	localChanged := !sameAccountNameValue(local, *base)
	remoteChanged := !sameAccountNameValue(remote, *base)
	switch {
	case localChanged && remoteChanged:
		return preferredAccountNameValue(defaultName, local, remote), true, nil
	case localChanged:
		return local, true, nil
	case remoteChanged:
		return remote, true, nil
	default:
		return local, true, nil
	}
}

func preferredAccountNameValue(defaultName string, local, remote accountNameValue) accountNameValue {
	if defaultName != "" {
		localDefault := local.Name == defaultName
		remoteDefault := remote.Name == defaultName
		switch {
		case localDefault && !remoteDefault:
			return remote
		case remoteDefault && !localDefault:
			return local
		}
	}
	return latestAccountNameValue(local, remote)
}

func latestAccountNameValue(local, remote accountNameValue) accountNameValue {
	switch {
	case remote.ModifiedAt.After(local.ModifiedAt):
		return remote
	case local.ModifiedAt.After(remote.ModifiedAt):
		return local
	case remote.Name > local.Name:
		// Equal timestamps do not identify a latest edit. Use the name as a
		// deterministic tie-breaker so independent merges converge.
		return remote
	default:
		return local
	}
}

func sameAccountNameValue(a, b accountNameValue) bool {
	return a.Name == b.Name && a.ModifiedAt.Equal(b.ModifiedAt)
}
