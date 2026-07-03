// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/hex"

	"github.com/BitBoxSwiss/bitboxsync-client-go/protocol"
	"github.com/BitBoxSwiss/bitboxsync-client-go/raw"

	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
)

// errNeedsDevice is returned when BitBoxSync needs the keystore to sign or
// unwrap data, but the current operation is not allowed to prompt the user.
const errNeedsDevice errp.ErrorCode = "bitBoxSyncNeedsDevice"

func isNeedsDevice(err error) bool {
	return errp.Cause(err) == errNeedsDevice
}

// PublicIdentity contains the non-secret public identity material needed to
// construct a BitBoxSync engine without connecting the keystore.
type PublicIdentity struct {
	// Kind is the BitBoxSync identity kind, such as a keystore-backed identity.
	Kind string
	// AuthPublicKey is the Ed25519 key used to identify and authenticate the keystore.
	AuthPublicKey ed25519.PublicKey
	// WrapPublicKey is the X25519 key used to wrap namespace data-encryption keys.
	WrapPublicKey []byte
}

// PublicIdentityFromRaw extracts and validates the public identity material.
func PublicIdentityFromRaw(identity raw.Identity) (PublicIdentity, error) {
	if identity == nil {
		return PublicIdentity{}, errp.New("BitBoxSync identity is required")
	}
	publicIdentity := PublicIdentity{
		Kind:          identity.Kind(),
		AuthPublicKey: cloneAuthPublicKey(identity.AuthPublicKey()),
	}
	if wrapPublicKey := identity.WrapPublicKey(); wrapPublicKey != nil {
		publicIdentity.WrapPublicKey = bytes.Clone(wrapPublicKey.Bytes())
	}
	if err := publicIdentity.Validate(); err != nil {
		return PublicIdentity{}, err
	}
	return publicIdentity, nil
}

// Validate checks whether the public identity material is structurally usable.
func (identity PublicIdentity) Validate() error {
	if identity.Kind == "" {
		return errp.New("BitBoxSync identity kind is required")
	}
	if len(identity.AuthPublicKey) != ed25519.PublicKeySize {
		return errp.New("BitBoxSync identity auth public key is invalid")
	}
	if len(identity.WrapPublicKey) != 32 {
		return errp.New("BitBoxSync identity wrap public key is invalid")
	}
	if _, err := ecdh.X25519().NewPublicKey(identity.WrapPublicKey); err != nil {
		return errp.WithStack(err)
	}
	return nil
}

// KeyID derives the canonical BitBoxSync key ID from the cached auth public key.
func (identity PublicIdentity) KeyID() (string, error) {
	if err := identity.Validate(); err != nil {
		return "", err
	}
	keyID := protocol.KeyIDFromAuthPublicKey(identity.AuthPublicKey)
	return hex.EncodeToString(keyID[:]), nil
}

// Matches checks that rawIdentity has the same public identity material.
func (identity PublicIdentity) Matches(rawIdentity raw.Identity) error {
	other, err := PublicIdentityFromRaw(rawIdentity)
	if err != nil {
		return err
	}
	if identity.Kind != other.Kind ||
		!bytes.Equal(identity.AuthPublicKey, other.AuthPublicKey) ||
		!bytes.Equal(identity.WrapPublicKey, other.WrapPublicKey) {
		return errp.New("connected BitBoxSync identity does not match cached identity")
	}
	return nil
}

func cloneAuthPublicKey(authPublicKey ed25519.PublicKey) ed25519.PublicKey {
	if authPublicKey == nil {
		return nil
	}
	return ed25519.PublicKey(bytes.Clone(authPublicKey))
}

// promptAllowedKey marks contexts whose operation is allowed to prompt the user.
type promptAllowedKey struct{}

// promptPolicy describes whether a sync operation may connect and prompt the keystore.
type promptPolicy int

const (
	// promptNever forbids device prompts for background or startup sync work.
	promptNever promptPolicy = iota
	// promptMayAsk permits device prompts during explicit user-triggered flows.
	promptMayAsk
)

// applyPromptPolicy attaches prompt permission to ctx when the operation allows it.
func applyPromptPolicy(ctx context.Context, policy promptPolicy) context.Context {
	if policy == promptMayAsk {
		return withPromptAllowed(ctx)
	}
	return ctx
}

// withPromptAllowed returns a context that permits keystore prompts in identity methods.
func withPromptAllowed(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, promptAllowedKey{}, true)
}

// promptAllowed reports whether identity methods may connect and prompt the keystore.
func promptAllowed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(promptAllowedKey{}).(bool)
	return allowed
}
