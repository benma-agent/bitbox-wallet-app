// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"testing"

	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"
	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/keystore"
	keystoreMocks "github.com/BitBoxSwiss/bitbox-wallet-app/backend/keystore/mocks"
	"github.com/stretchr/testify/require"
)

func TestCachedIdentityNeedsDeviceWithoutPrompt(t *testing.T) {
	liveIdentity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(liveIdentity)
	require.NoError(t, err)

	connects := 0
	identity, err := NewCachedIdentity(
		publicIdentity,
		[]byte{1, 2, 3, 4},
		func([]byte) (keystore.Keystore, error) {
			connects++
			return nil, nil
		},
	)
	require.NoError(t, err)

	_, err = identity.SignLoginIntent(context.Background(), make([]byte, 32))
	require.True(t, isNeedsDevice(err))
	require.Equal(t, 0, connects)
}

func TestCachedIdentityConnectsWhenPromptAllowed(t *testing.T) {
	liveIdentity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(liveIdentity)
	require.NoError(t, err)

	connects := 0
	identity, err := NewCachedIdentity(
		publicIdentity,
		[]byte{1, 2, 3, 4},
		func([]byte) (keystore.Keystore, error) {
			connects++
			return &keystoreMocks.KeystoreMock{
				BitBoxSyncIdentifyFunc: func() (raw.Identity, error) {
					return liveIdentity, nil
				},
			}, nil
		},
	)
	require.NoError(t, err)

	signature, err := identity.SignLoginIntent(withPromptAllowed(context.Background()), make([]byte, 32))
	require.NoError(t, err)
	require.NotEmpty(t, signature)
	require.Equal(t, 1, connects)
}
