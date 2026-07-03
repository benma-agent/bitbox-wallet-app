// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"

	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"

	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
	"github.com/sirupsen/logrus"
)

// runnerCollections groups the concrete wallet-data collection backends for one runner.
type runnerCollections struct {
	// txNotes adapts account transaction notes to a BitBoxSync collection.
	txNotes *txNotesValueBackend
	// accountConfig adapts account configuration fields to a BitBoxSync collection.
	accountConfig *accountConfigValueBackend
}

// registerRunnerCollections registers all wallet metadata collections for a namespace.
func registerRunnerCollections(
	namespace *syncclient.Namespace,
	config Config,
	rootFingerprint []byte,
) (*runnerCollections, error) {
	txNotes, err := registerTxNotesCollection(namespace, config, rootFingerprint)
	if err != nil {
		return nil, err
	}
	accountConfig, err := registerAccountConfigCollection(namespace, config, rootFingerprint)
	if err != nil {
		return nil, err
	}
	return &runnerCollections{
		txNotes:       txNotes,
		accountConfig: accountConfig,
	}, nil
}

// registerTxNotesCollection registers transaction-note sync for a namespace.
func registerTxNotesCollection(
	namespace *syncclient.Namespace,
	config Config,
	rootFingerprint []byte,
) (*txNotesValueBackend, error) {
	if config.Accounts == nil {
		return nil, errp.New("transaction notes accounts provider is required")
	}
	valueBackend, err := newTxNotesValueBackend(config.Accounts, rootFingerprint, config.NotifyTxNotesChanged)
	if err != nil {
		return nil, err
	}
	_, err = syncclient.OpenCollection(namespace, txNotesCollection, syncclient.CollectionConfig[txNotesBucket]{
		Codec:   syncclient.JSONCodec[txNotesBucket](),
		Merge:   mergeTxNotesBucket,
		Backend: valueBackend,
	})
	if err != nil {
		return nil, err
	}
	return valueBackend, nil
}

// registerAccountConfigCollection registers account-config sync for a namespace.
func registerAccountConfigCollection(
	namespace *syncclient.Namespace,
	config Config,
	rootFingerprint []byte,
) (*accountConfigValueBackend, error) {
	if config.Accounts == nil {
		return nil, errp.New("account config accounts provider is required")
	}
	valueBackend, err := newAccountConfigValueBackend(
		config.Accounts,
		rootFingerprint,
		config.SetAccountName,
		config.SetAccountNameIfCurrent,
	)
	if err != nil {
		return nil, err
	}
	_, err = syncclient.OpenCollection(namespace, accountConfigCollection, syncclient.CollectionConfig[accountConfigValue]{
		Codec:   syncclient.JSONCodec[accountConfigValue](),
		Merge:   valueBackend.merge,
		Backend: valueBackend,
	})
	if err != nil {
		return nil, err
	}
	return valueBackend, nil
}

// flushTerminalNotifications emits deferred app notifications after a sync pass ends.
func (c *runnerCollections) flushTerminalNotifications() {
	if c == nil || c.txNotes == nil {
		return
	}
	// Remote note writes are already durable when the terminal event is
	// delivered. Flush one transaction reload per affected account now,
	// after all bucket writes from this pass have had a chance to record
	// their account code.
	c.txNotes.flushChangedAccountNotifications()
}

// logItemEvent logs collection-specific upload and download diagnostics.
func (c *runnerCollections) logItemEvent(ctx context.Context, log *logrus.Entry, event syncclient.Event) {
	if c == nil {
		return
	}
	logTxNoteSyncEvent(ctx, log, event, c.txNotes)
	logAccountConfigSyncEvent(ctx, log, event, c.accountConfig)
}

// logConflictEvent logs collection-specific conflict diagnostics.
func (c *runnerCollections) logConflictEvent(log *logrus.Entry, event syncclient.Event) {
	if c == nil || log == nil {
		return
	}
	switch event.Collection {
	case txNotesCollection:
		fields, err := txNotesItemLogFields(event)
		if err != nil {
			log.WithError(err).WithField("key", event.Key).Warn("could not decode BitBoxSync transaction note key")
			return
		}
		log.WithFields(fields).Warn("BitBoxSync transaction note conflict")
	case accountConfigCollection:
		fields, err := accountConfigItemLogFields(event)
		if err != nil {
			log.WithError(err).WithField("key", event.Key).Warn("could not decode BitBoxSync account config key")
			return
		}
		log.WithFields(fields).Warn("BitBoxSync account config conflict")
	}
}
