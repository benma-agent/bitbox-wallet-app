// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"context"
	"testing"

	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"
	"github.com/stretchr/testify/require"
)

func TestPublicIdentityMatchesRawIdentity(t *testing.T) {
	identity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(identity)
	require.NoError(t, err)

	require.NoError(t, publicIdentity.Matches(identity))
}

func TestPublicIdentityRejectsMismatchingRawIdentity(t *testing.T) {
	identity, err := raw.NewDummyKeystore("identity")
	require.NoError(t, err)
	publicIdentity, err := PublicIdentityFromRaw(identity)
	require.NoError(t, err)
	otherIdentity, err := raw.NewDummyKeystore("other-identity")
	require.NoError(t, err)

	require.ErrorContains(t, publicIdentity.Matches(otherIdentity), "does not match")
}

func TestPromptAllowed(t *testing.T) {
	require.False(t, promptAllowed(nil))
	require.False(t, promptAllowed(context.Background()))
	require.True(t, promptAllowed(withPromptAllowed(context.Background())))
}
