// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"

	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/keystore"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
)

// ConnectKeystore prompts the user to connect the keystore identified by
// rootFingerprint if it is not already available.
type ConnectKeystore func(rootFingerprint []byte) (keystore.Keystore, error)

// cachedIdentity serves public identity data from config and prompts only for private operations.
type cachedIdentity struct {
	// publicIdentity is the cached public key material used without connecting the keystore.
	publicIdentity PublicIdentity
	// rootFingerprint identifies the keystore to connect when a private operation is allowed.
	rootFingerprint []byte
	// connectKeystore obtains the live keystore for signing, attestation, and unwrap operations.
	connectKeystore ConnectKeystore
}

var _ raw.Identity = (*cachedIdentity)(nil)

// NewCachedIdentity creates an identity backed by cached public identity
// material. Public key methods are served from the cache; signing and unwrap
// operations only prompt through connectKeystore when the context explicitly
// allows prompting.
func NewCachedIdentity(
	publicIdentity PublicIdentity,
	rootFingerprint []byte,
	connectKeystore ConnectKeystore,
) (raw.Identity, error) {
	if err := publicIdentity.Validate(); err != nil {
		return nil, err
	}
	if len(rootFingerprint) == 0 {
		return nil, errp.New("BitBoxSync wallet is required")
	}
	if connectKeystore == nil {
		return nil, errp.New("BitBoxSync keystore connector is required")
	}
	return &cachedIdentity{
		publicIdentity:  publicIdentity,
		rootFingerprint: bytes.Clone(rootFingerprint),
		connectKeystore: connectKeystore,
	}, nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// Kind implements raw.Identity.
func (identity *cachedIdentity) Kind() string {
	return identity.publicIdentity.Kind
}

// AuthPublicKey implements raw.Identity.
func (identity *cachedIdentity) AuthPublicKey() ed25519.PublicKey {
	return ed25519.PublicKey(bytes.Clone(identity.publicIdentity.AuthPublicKey))
}

// WrapPublicKey implements raw.Identity.
func (identity *cachedIdentity) WrapPublicKey() *ecdh.PublicKey {
	wrapPublicKey, err := ecdh.X25519().NewPublicKey(identity.publicIdentity.WrapPublicKey)
	if err != nil {
		panic(err)
	}
	return wrapPublicKey
}

// liveIdentity connects and validates the keystore when the context allows prompting.
func (identity *cachedIdentity) liveIdentity(ctx context.Context) (raw.Identity, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if !promptAllowed(ctx) {
		return nil, errp.WithStack(errNeedsDevice)
	}
	keystore, err := identity.connectKeystore(identity.rootFingerprint)
	if err != nil {
		return nil, err
	}
	if keystore == nil {
		return nil, errp.New("BitBoxSync keystore is required")
	}
	liveIdentity, err := keystore.BitBoxSyncIdentify()
	if err != nil {
		return nil, err
	}
	if err := identity.publicIdentity.Matches(liveIdentity); err != nil {
		return nil, err
	}
	return liveIdentity, nil
}

// SignLoginIntent implements raw.Identity.
func (identity *cachedIdentity) SignLoginIntent(ctx context.Context, challenge []byte) ([]byte, error) {
	liveIdentity, err := identity.liveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return liveIdentity.SignLoginIntent(ctx, challenge)
}

// SignRefreshIntent implements raw.Identity.
func (identity *cachedIdentity) SignRefreshIntent(ctx context.Context, challenge []byte) ([]byte, error) {
	liveIdentity, err := identity.liveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return liveIdentity.SignRefreshIntent(ctx, challenge)
}

// SignRevokeAllTokensIntent implements raw.Identity.
func (identity *cachedIdentity) SignRevokeAllTokensIntent(ctx context.Context, challenge []byte) ([]byte, error) {
	liveIdentity, err := identity.liveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return liveIdentity.SignRevokeAllTokensIntent(ctx, challenge)
}

// SignCreateNamespaceInviteIntent implements raw.Identity.
func (identity *cachedIdentity) SignCreateNamespaceInviteIntent(
	ctx context.Context,
	challenge,
	namespaceID,
	inviteID,
	inviteServerSecretHash []byte,
	expiresAt int64,
	maxAccepted int,
) ([]byte, error) {
	liveIdentity, err := identity.liveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return liveIdentity.SignCreateNamespaceInviteIntent(
		ctx,
		challenge,
		namespaceID,
		inviteID,
		inviteServerSecretHash,
		expiresAt,
		maxAccepted,
	)
}

// SignNamespaceJoinRequestIntent implements raw.Identity.
func (identity *cachedIdentity) SignNamespaceJoinRequestIntent(
	ctx context.Context,
	namespaceID,
	inviteID []byte,
	serverOrigin string,
	expiresAt int64,
) ([]byte, error) {
	liveIdentity, err := identity.liveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return liveIdentity.SignNamespaceJoinRequestIntent(ctx, namespaceID, inviteID, serverOrigin, expiresAt)
}

// Attest implements raw.Identity.
func (identity *cachedIdentity) Attest(ctx context.Context, challenge []byte) ([]byte, error) {
	liveIdentity, err := identity.liveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return liveIdentity.Attest(ctx, challenge)
}

// UnwrapNamespaceDEK implements raw.Identity.
func (identity *cachedIdentity) UnwrapNamespaceDEK(ctx context.Context, namespaceID []byte, wrappedDEK []byte) ([]byte, error) {
	liveIdentity, err := identity.liveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return liveIdentity.UnwrapNamespaceDEK(ctx, namespaceID, wrappedDEK)
}
