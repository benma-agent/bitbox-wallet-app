// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"testing"

	accountMocks "github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/mocks"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
	"github.com/stretchr/testify/require"
)

func TestSetTxNoteSchedulesBitBoxSyncAfterStore(t *testing.T) {
	var scheduled bool
	handlers := &Handlers{
		account: &accountMocks.InterfaceMock{
			SetTxNoteFunc: func(txID string, note string) error {
				require.Equal(t, "tx-id", txID)
				require.Equal(t, "note", note)
				require.False(t, scheduled)
				return nil
			},
		},
		scheduleSync: func() {
			scheduled = true
		},
	}

	require.NoError(t, handlers.setTxNote("tx-id", "note"))
	require.True(t, scheduled)
}

func TestSetTxNoteDoesNotScheduleBitBoxSyncAfterStoreError(t *testing.T) {
	var scheduled bool
	handlers := &Handlers{
		account: &accountMocks.InterfaceMock{
			SetTxNoteFunc: func(string, string) error {
				return errp.New("store failed")
			},
		},
		scheduleSync: func() {
			scheduled = true
		},
	}

	require.Error(t, handlers.setTxNote("tx-id", "note"))
	require.False(t, scheduled)
}
