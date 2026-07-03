// SPDX-License-Identifier: Apache-2.0

package bitbox02

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"

	keystorePkg "github.com/BitBoxSwiss/bitbox-wallet-app/backend/keystore"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
	"github.com/BitBoxSwiss/bitbox02-api-go/api/firmware"
	"github.com/BitBoxSwiss/bitbox02-api-go/util/semver"
	"github.com/BitBoxSwiss/bitboxsync-client-go/protocol"
	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"
)

var bitBoxSyncMinFirmwareVersion = semver.NewSemVer(9, 27, 0)

type bitBoxSyncIdentity struct {
	device        *Device
	authPublicKey ed25519.PublicKey
	wrapPublicKey *ecdh.PublicKey
}

var _ raw.Identity = (*bitBoxSyncIdentity)(nil)

func newBitBoxSyncIdentity(device *Device) (*bitBoxSyncIdentity, error) {
	if !device.Version().AtLeast(bitBoxSyncMinFirmwareVersion) {
		return nil, keystorePkg.ErrFirmwareUpgradeRequired
	}
	identity, err := device.BitBoxSyncIdentity()
	if err != nil {
		return nil, err
	}
	wrapPublicKey, err := ecdh.X25519().NewPublicKey(identity.WrapPublicKey)
	if err != nil {
		return nil, errp.WithStack(err)
	}
	return &bitBoxSyncIdentity{
		device:        device,
		authPublicKey: ed25519.PublicKey(bytes.Clone(identity.AuthPublicKey)),
		wrapPublicKey: wrapPublicKey,
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

func bitBoxSyncDeviceError(err error) error {
	if firmware.IsErrorAbort(err) {
		return errp.WithStack(keystorePkg.ErrSigningAborted)
	}
	return err
}

// BitBoxSyncIdentify implements keystore.Keystore.
func (keystore *keystore) BitBoxSyncIdentify() (raw.Identity, error) {
	return newBitBoxSyncIdentity(keystore.device)
}

// Kind returns the auth-key kind of the BitBox02 BitBoxSync identity.
func (identity *bitBoxSyncIdentity) Kind() string {
	return protocol.IdentityKindKeystore
}

// AuthPublicKey returns the authentication public key.
func (identity *bitBoxSyncIdentity) AuthPublicKey() ed25519.PublicKey {
	return ed25519.PublicKey(bytes.Clone(identity.authPublicKey))
}

// WrapPublicKey returns the public key used for namespace DEK wrapping.
func (identity *bitBoxSyncIdentity) WrapPublicKey() *ecdh.PublicKey {
	return identity.wrapPublicKey
}

// SignLoginIntent signs the canonical BitBoxSync login intent on the device.
func (identity *bitBoxSyncIdentity) SignLoginIntent(ctx context.Context, challenge []byte) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	signature, err := identity.device.BitBoxSyncSignLoginIntent(challenge)
	if err != nil {
		return nil, bitBoxSyncDeviceError(err)
	}
	return signature, nil
}

// SignRefreshIntent signs the canonical BitBoxSync refresh intent on the device.
func (identity *bitBoxSyncIdentity) SignRefreshIntent(ctx context.Context, challenge []byte) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	signature, err := identity.device.BitBoxSyncSignRefreshIntent(challenge)
	if err != nil {
		return nil, bitBoxSyncDeviceError(err)
	}
	return signature, nil
}

// SignRevokeAllTokensIntent signs the revoke-all-tokens intent on the device.
func (identity *bitBoxSyncIdentity) SignRevokeAllTokensIntent(ctx context.Context, challenge []byte) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	signature, err := identity.device.BitBoxSyncSignRevokeAllTokensIntent(challenge)
	if err != nil {
		return nil, bitBoxSyncDeviceError(err)
	}
	return signature, nil
}

// SignCreateNamespaceInviteIntent signs the namespace-invite sensitive action on the device.
func (identity *bitBoxSyncIdentity) SignCreateNamespaceInviteIntent(
	ctx context.Context,
	challenge,
	namespaceID,
	inviteID,
	inviteServerSecretHash []byte,
	expiresAt int64,
	maxAccepted int,
) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if expiresAt < 0 {
		return nil, errp.New("namespace invite expiry before unix epoch")
	}
	if maxAccepted < 0 || maxAccepted > protocol.MaxAcceptedJoinRequestsPerInvite {
		return nil, errp.New("namespace invite limits are out of range")
	}
	signature, err := identity.device.BitBoxSyncSignCreateNamespaceInviteIntent(
		challenge,
		namespaceID,
		inviteID,
		inviteServerSecretHash,
		uint64(expiresAt),
		uint32(maxAccepted),
	)
	if err != nil {
		return nil, bitBoxSyncDeviceError(err)
	}
	return signature, nil
}

// SignNamespaceJoinRequestIntent signs a namespace join request on the device.
func (identity *bitBoxSyncIdentity) SignNamespaceJoinRequestIntent(
	ctx context.Context,
	namespaceID,
	inviteID []byte,
	serverOrigin string,
	expiresAt int64,
) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if expiresAt < 0 {
		return nil, errp.New("join request expiry before unix epoch")
	}
	signature, err := identity.device.BitBoxSyncSignJoinRequestIntent(namespaceID, inviteID, serverOrigin, uint64(expiresAt))
	if err != nil {
		return nil, bitBoxSyncDeviceError(err)
	}
	return signature, nil
}

// Attest returns the BitBox02 attestation proof for challenge.
func (identity *bitBoxSyncIdentity) Attest(ctx context.Context, challenge []byte) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	attestation, err := identity.device.GetAttestation(challenge)
	if err != nil {
		return nil, bitBoxSyncDeviceError(err)
	}
	out := make([]byte, 1+len(attestation))
	copy(out[1:], attestation)
	return out, nil
}

// UnwrapNamespaceDEK unwraps a namespace DEK on the device.
func (identity *bitBoxSyncIdentity) UnwrapNamespaceDEK(ctx context.Context, namespaceID []byte, wrappedDEK []byte) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	namespaceDEK, err := identity.device.BitBoxSyncUnwrapNamespaceDEK(namespaceID, wrappedDEK)
	if err != nil {
		return nil, bitBoxSyncDeviceError(err)
	}
	return namespaceDEK, nil
}
