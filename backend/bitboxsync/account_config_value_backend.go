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
	accountConfigCollection = "account-config-v1"

	erc20CoinCodePrefix = "eth-erc20-"
)

// accountConfigValue is the synced payload for selected account config fields.
//
// Account config sync schema:
//
// Collection: account-config-v1
//
// Each currently loaded, non-hidden single-sig account owned by the enabled
// keystore exposes one item. The value currently contains the account name.
//
//	key:   <url-path-escaped-account-code>
//	value: {"name":{"value":"Spending","modifiedAt":"2026-05-11T10:24:00Z"}}
//
// Inactive accounts are included. Hidden-unused accounts and ERC20 token
// accounts are excluded, matching the account-label export/import behavior.
type accountConfigValue struct {
	// Name stores the synced display name and edit timestamp for one account.
	Name *modifiedString `json:"name,omitempty"`
}

// modifiedString stores a synced string with the timestamp of the edit that produced it.
type modifiedString struct {
	// Value is the synced string payload.
	Value string `json:"value"`
	// ModifiedAt is the timestamp used to order conflicting edits.
	ModifiedAt time.Time `json:"modifiedAt"`
}

// AccountConfigNameSetter persists an account name with its sync edit timestamp.
type AccountConfigNameSetter func(accountsTypes.Code, string, time.Time) error

// AccountConfigNameConditionalSetter persists an account name if the current stored
// name and edit timestamp still match the expected value.
type AccountConfigNameConditionalSetter func(
	accountsTypes.Code,
	string,
	time.Time,
	bool,
	string,
	time.Time,
) (bool, error)

func encodeAccountConfigKey(accountCode accountsTypes.Code) (string, error) {
	if accountCode == "" {
		return "", errp.New("invalid account config account code")
	}
	return url.PathEscape(string(accountCode)), nil
}

func decodeAccountConfigKey(key string) (accountsTypes.Code, error) {
	if key == "" || strings.Contains(key, "/") {
		return "", errp.New("invalid account config key")
	}
	accountCode, err := url.PathUnescape(key)
	if err != nil {
		return "", errp.WithStack(err)
	}
	if accountCode == "" {
		return "", errp.New("invalid account config key")
	}
	return accountsTypes.Code(accountCode), nil
}

// accountConfigValueBackend adapts selected account config fields to a BitBoxSync value backend.
type accountConfigValueBackend struct {
	// accounts returns the currently loaded wallet accounts eligible for sync inspection.
	accounts func() []accounts.Interface
	// rootFingerprint scopes synced accounts to the enabled keystore.
	rootFingerprint []byte
	// setAccountName writes synced account names into app config.
	setAccountName AccountConfigNameSetter
	// setAccountNameIfCurrent writes account names only when optimistic concurrency matches.
	setAccountNameIfCurrent AccountConfigNameConditionalSetter
}

// newAccountConfigValueBackend constructs the account-config value backend for one keystore.
func newAccountConfigValueBackend(
	accounts func() []accounts.Interface,
	rootFingerprint []byte,
	setAccountName AccountConfigNameSetter,
	setAccountNameIfCurrent AccountConfigNameConditionalSetter,
) (*accountConfigValueBackend, error) {
	if accounts == nil {
		return nil, errp.New("account config accounts provider is required")
	}
	if setAccountName == nil {
		return nil, errp.New("account name setter is required")
	}
	if setAccountNameIfCurrent == nil {
		return nil, errp.New("conditional account name setter is required")
	}
	if len(rootFingerprint) == 0 {
		return nil, errp.New("account config root fingerprint is required")
	}
	return &accountConfigValueBackend{
		accounts:                accounts,
		rootFingerprint:         bytes.Clone(rootFingerprint),
		setAccountName:          setAccountName,
		setAccountNameIfCurrent: setAccountNameIfCurrent,
	}, nil
}

// accountConfigSyncAccounts returns the loaded accounts whose account config
// should participate in sync. Unlike transaction notes, inactive accounts are
// included so config changes survive account deactivation across devices.
// Accounts not solely owned by rootFingerprint are omitted.
func accountConfigSyncAccounts(
	accountList []accounts.Interface,
	rootFingerprint []byte,
) []accounts.Interface {
	filtered := make([]accounts.Interface, 0, len(accountList))
	for _, account := range accountList {
		if account == nil {
			continue
		}
		accountConfig := account.Config()
		if accountConfig == nil || accountConfig.Config == nil {
			continue
		}
		config := accountConfig.Config
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

// Keys implements syncclient.ValueBackend[accountConfigValue].
func (b *accountConfigValueBackend) Keys(context.Context) ([]string, error) {
	var keys []string
	for _, account := range b.syncAccounts() {
		key, err := encodeAccountConfigKey(account.Config().Config.Code)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// Get implements syncclient.ValueBackend[accountConfigValue].
func (b *accountConfigValueBackend) Get(_ context.Context, key string) (accountConfigValue, error) {
	accountCode, err := decodeAccountConfigKey(key)
	if err != nil {
		return accountConfigValue{}, err
	}
	account := b.account(accountCode)
	if account == nil {
		return accountConfigValue{}, syncclient.ErrNotFound
	}
	value := accountConfigValueFromConfig(account)
	if value.Name == nil {
		return accountConfigValue{}, syncclient.ErrNotFound
	}
	return value, nil
}

// Snapshot implements syncclient.ValueBackend[accountConfigValue].
func (b *accountConfigValueBackend) Snapshot(context.Context) (map[string]accountConfigValue, error) {
	snapshot := map[string]accountConfigValue{}
	for _, account := range b.syncAccounts() {
		config := account.Config().Config
		if config.Name == "" {
			continue
		}
		key, err := encodeAccountConfigKey(config.Code)
		if err != nil {
			return nil, err
		}
		snapshot[key] = accountConfigValueFromConfig(account)
	}
	return snapshot, nil
}

// Set implements syncclient.ValueBackend[accountConfigValue].
func (b *accountConfigValueBackend) Set(_ context.Context, key string, value accountConfigValue) error {
	accountCode, err := decodeAccountConfigKey(key)
	if err != nil {
		return err
	}
	if err := validateAccountConfigValue(value); err != nil {
		return err
	}
	if b.account(accountCode) == nil {
		return errp.New("account config account is not loaded")
	}
	return b.setAccountName(accountCode, value.Name.Value, value.Name.ModifiedAt)
}

// SetIfCurrent implements syncclient.ConditionalValueBackend[accountConfigValue].
func (b *accountConfigValueBackend) SetIfCurrent(
	_ context.Context,
	key string,
	current accountConfigValue,
	currentFound bool,
	value accountConfigValue,
) (bool, error) {
	accountCode, err := decodeAccountConfigKey(key)
	if err != nil {
		return false, err
	}
	if err := validateAccountConfigValue(value); err != nil {
		return false, err
	}
	if currentFound {
		if err := validateAccountConfigValue(current); err != nil {
			return false, err
		}
	}
	if b.account(accountCode) == nil {
		return false, errp.New("account config account is not loaded")
	}
	var currentName string
	var currentModifiedAt time.Time
	if currentFound {
		currentName = current.Name.Value
		currentModifiedAt = current.Name.ModifiedAt
	}
	return b.setAccountNameIfCurrent(
		accountCode,
		currentName,
		currentModifiedAt,
		currentFound,
		value.Name.Value,
		value.Name.ModifiedAt,
	)
}

// account returns the loaded syncable account for accountCode.
func (b *accountConfigValueBackend) account(accountCode accountsTypes.Code) accounts.Interface {
	return accountByCode(b.syncAccounts(), accountCode)
}

// syncAccounts returns the current filtered account list for this backend's keystore.
func (b *accountConfigValueBackend) syncAccounts() []accounts.Interface {
	return accountConfigSyncAccounts(b.accounts(), b.rootFingerprint)
}

func validateAccountConfigValue(value accountConfigValue) error {
	if value.Name == nil {
		return errp.New("account config name is required")
	}
	if value.Name.Value == "" {
		return errp.New("account name cannot be empty")
	}
	return nil
}

func accountConfigValueFromConfig(account accounts.Interface) accountConfigValue {
	config := account.Config().Config
	value := accountConfigValue{}
	if config.Name == "" {
		return value
	}
	value.Name = &modifiedString{Value: config.Name}
	if config.NameModifiedAt != nil {
		value.Name.ModifiedAt = config.NameModifiedAt.UTC()
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

// merge resolves one account-config item conflict using account-name merge rules.
func (b *accountConfigValueBackend) merge(
	key string,
	base *accountConfigValue,
	local accountConfigValue,
	remote accountConfigValue,
) (accountConfigValue, bool, error) {
	accountCode, err := decodeAccountConfigKey(key)
	if err != nil {
		return accountConfigValue{}, false, err
	}
	defaultName := ""
	if account := b.account(accountCode); account != nil {
		if name, ok := accounts.DefaultAccountName(account); ok {
			defaultName = name
		}
	}
	return mergeAccountConfig(defaultName, base, local, remote)
}

func mergeAccountConfig(
	defaultName string,
	base *accountConfigValue,
	local,
	remote accountConfigValue,
) (accountConfigValue, bool, error) {
	if base != nil {
		if err := validateAccountConfigValue(*base); err != nil {
			return accountConfigValue{}, false, err
		}
	}
	if err := validateAccountConfigValue(local); err != nil {
		return accountConfigValue{}, false, err
	}
	if err := validateAccountConfigValue(remote); err != nil {
		return accountConfigValue{}, false, err
	}

	var baseName *modifiedString
	if base != nil {
		baseName = base.Name
	}
	name, resolved, err := mergeAccountName(defaultName, baseName, *local.Name, *remote.Name)
	if err != nil || !resolved {
		return accountConfigValue{}, resolved, err
	}
	return accountConfigValue{Name: &name}, true, nil
}

// mergeAccountName resolves account-name conflicts. Generated default names
// are low-information values, so a deliberate custom name wins over a default
// name before the last-modified timestamp is considered.
func mergeAccountName(
	defaultName string,
	base *modifiedString,
	local,
	remote modifiedString,
) (modifiedString, bool, error) {
	if base == nil {
		return preferredAccountNameValue(defaultName, local, remote), true, nil
	}

	localChanged := !sameModifiedString(local, *base)
	remoteChanged := !sameModifiedString(remote, *base)
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

func preferredAccountNameValue(defaultName string, local, remote modifiedString) modifiedString {
	if defaultName != "" {
		localDefault := local.Value == defaultName
		remoteDefault := remote.Value == defaultName
		switch {
		case localDefault && !remoteDefault:
			return remote
		case remoteDefault && !localDefault:
			return local
		}
	}
	return latestAccountNameValue(local, remote)
}

func latestAccountNameValue(local, remote modifiedString) modifiedString {
	switch {
	case remote.ModifiedAt.After(local.ModifiedAt):
		return remote
	case local.ModifiedAt.After(remote.ModifiedAt):
		return local
	case remote.Value > local.Value:
		// Equal timestamps do not identify a latest edit. Use the name as a
		// deterministic tie-breaker so independent merges converge.
		return remote
	default:
		return local
	}
}

func sameModifiedString(a, b modifiedString) bool {
	return a.Value == b.Value && a.ModifiedAt.Equal(b.ModifiedAt)
}
