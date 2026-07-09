// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"errors"
	"sort"

	syncclient "github.com/BitBoxSwiss/bitboxsync-client-go/bitboxsync"

	accountsTypes "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/types"
	"github.com/sirupsen/logrus"
)

// runEventLoop consumes engine events until the runner is stopped.
func (r *runner) runEventLoop(ctx context.Context, rollbackHandler func(*runner)) {
	engine, err := r.engineSnapshot()
	if err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-engine.Events():
			if !ok {
				return
			}
			r.handleSyncEvent(ctx, event, rollbackHandler)
		}
	}
}

// handlePendingEvents handles already-buffered engine events without blocking.
func (r *runner) handlePendingEvents(ctx context.Context) {
	engine, err := r.engineSnapshot()
	if err != nil {
		return
	}
	for {
		select {
		case event, ok := <-engine.Events():
			if !ok {
				return
			}
			r.handleSyncEvent(ctx, event, nil)
		default:
			return
		}
	}
}

// handleSyncEvent applies one BitBoxSync engine event to runner status and collection hooks.
func (r *runner) handleSyncEvent(ctx context.Context, event syncclient.Event, rollbackHandler func(*runner)) {
	collections := r.collectionsSnapshot()
	switch event.Type {
	case syncclient.EventAuthLoginRequired:
		r.recordAuthLoginRequired(event.TokenExpiresAt)
	case syncclient.EventAuthRefreshRecommended:
		r.recordAuthRefreshRecommended(event.TokenExpiresAt)
	case syncclient.EventAuthSessionReady:
		r.recordAuthSessionReady(event.TokenExpiresAt)
	case syncclient.EventSyncFinished:
		r.recordSuccess()
		collections.flushTerminalNotifications()
	case syncclient.EventSyncFailed:
		if errors.Is(event.Err, syncclient.ErrRollback) && rollbackHandler != nil {
			// A rollback failure can still happen after applying some remote
			// writes. Flush pending reloads before the old runner is replaced.
			collections.flushTerminalNotifications()
			rollbackHandler(r)
			return
		}
		r.recordError(event.Err)
		// A sync pass can fail after applying some remote note writes. Those
		// writes still need to become visible in the frontend, so flush the
		// pending account reloads on failure as well.
		collections.flushTerminalNotifications()
	case syncclient.EventItemDownloaded, syncclient.EventItemUploaded:
		collections.logItemEvent(ctx, r.config.Log, event)
	case syncclient.EventConflictDetected:
		collections.logConflictEvent(r.config.Log, event)
	case syncclient.EventUnknownRemoteItem:
		if r.config.Log != nil {
			r.config.Log.WithFields(logrus.Fields{
				"namespaceID": event.NamespaceID,
				"itemID":      event.ItemID,
			}).Warn("BitBoxSync remote item is unknown locally")
		}
	}
}

// logTxNoteSyncEvent logs transaction-note item uploads and downloads.
func logTxNoteSyncEvent(ctx context.Context, log *logrus.Entry, event syncclient.Event, valueBackend *txNotesValueBackend) {
	if event.Collection != txNotesCollection || log == nil || valueBackend == nil {
		return
	}
	accountCode, bucket, err := decodeTxNoteBucketKey(event.Key)
	if err != nil {
		log.WithError(err).WithField("key", event.Key).Warn("could not decode BitBoxSync transaction note key")
		return
	}
	value, err := valueBackend.Get(ctx, event.Key)
	if err != nil {
		log.WithError(err).WithField("key", event.Key).Warn("could not load BitBoxSync transaction note bucket for sync log")
		return
	}
	txIDs := make([]string, 0, len(value))
	for txID := range value {
		txIDs = append(txIDs, txID)
	}
	sort.Strings(txIDs)
	switch event.Type {
	case syncclient.EventItemDownloaded:
		for _, txID := range txIDs {
			log.WithFields(txNoteLogFields(event, accountCode, bucket, txID, value[txID])).
				Info("BitBoxSync transaction note downloaded")
		}
	case syncclient.EventItemUploaded:
		for _, txID := range txIDs {
			log.WithFields(txNoteLogFields(event, accountCode, bucket, txID, value[txID])).
				Info("BitBoxSync transaction note uploaded")
		}
	}
}

// logAccountNameSyncEvent logs account-name item uploads and downloads.
func logAccountNameSyncEvent(ctx context.Context, log *logrus.Entry, event syncclient.Event, valueBackend *accountNameValueBackend) {
	if event.Collection != accountNameCollection || log == nil || valueBackend == nil {
		return
	}
	accountCode, err := decodeAccountNameKey(event.Key)
	if err != nil {
		log.WithError(err).WithField("key", event.Key).Warn("could not decode BitBoxSync account name key")
		return
	}
	value, err := valueBackend.Get(ctx, event.Key)
	if err != nil {
		log.WithError(err).WithField("key", event.Key).Warn("could not load BitBoxSync account name for sync log")
		return
	}
	switch event.Type {
	case syncclient.EventItemDownloaded:
		log.WithFields(accountNameLogFields(event, accountCode, value)).
			Info("BitBoxSync account name downloaded")
	case syncclient.EventItemUploaded:
		log.WithFields(accountNameLogFields(event, accountCode, value)).
			Info("BitBoxSync account name uploaded")
	}
}

// txNotesItemLogFields builds conflict log fields for a transaction-note item.
func txNotesItemLogFields(event syncclient.Event) (logrus.Fields, error) {
	accountCode, bucket, err := decodeTxNoteBucketKey(event.Key)
	if err != nil {
		return nil, err
	}
	return logrus.Fields{
		"accountCode": accountCode,
		"kind":        "bucket",
		"bucket":      bucket,
		"namespaceID": event.NamespaceID,
		"itemID":      event.ItemID,
	}, nil
}

// accountNameItemLogFields builds conflict log fields for an account-name item.
func accountNameItemLogFields(event syncclient.Event) (logrus.Fields, error) {
	accountCode, err := decodeAccountNameKey(event.Key)
	if err != nil {
		return nil, err
	}
	return logrus.Fields{
		"accountCode": accountCode,
		"kind":        "name",
		"namespaceID": event.NamespaceID,
		"itemID":      event.ItemID,
	}, nil
}

// txNoteLogFields builds upload/download log fields for a single transaction note.
func txNoteLogFields(event syncclient.Event, accountCode accountsTypes.Code, bucket int, internalTxID string, entry txNoteEntry) logrus.Fields {
	return logrus.Fields{
		"accountCode":  accountCode,
		"bucket":       bucket,
		"internalTxID": internalTxID,
		"note":         entry.Note,
		"noteBytes":    len(entry.Note),
		"deleted":      entry.Note == "",
		"modifiedAt":   entry.ModifiedAt,
		"namespaceID":  event.NamespaceID,
		"itemID":       event.ItemID,
	}
}

// accountNameLogFields builds upload/download log fields for one account name value.
func accountNameLogFields(event syncclient.Event, accountCode accountsTypes.Code, value accountNameValue) logrus.Fields {
	fields := logrus.Fields{
		"accountCode": accountCode,
		"namespaceID": event.NamespaceID,
		"itemID":      event.ItemID,
	}
	if value.Name != "" {
		fields["name"] = value.Name
		fields["nameBytes"] = len(value.Name)
		fields["modifiedAt"] = value.ModifiedAt
	}
	return fields
}
