package tradeapi

import (
	"context"
	"reflect"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualsign"
)

// Disconnected trusted-local dependencies. No loader, grant, action constructor
// by request ID, HTTP/startup consumer, allocator, permit or transport.
// Expected value identities are copied; interfaces are trusted synchronous
// primitives whose results must still be freshly verified at each boundary.
type manualTrustComposition struct {
	store      *Store
	owner      Identity
	profile    manualRuntimeProfile
	timeSource manualRuntimeTimeSource
	identity   manualRuntimeIdentitySource
	nonce      manualNonceObserver
	runtime    manualRuntimeObserver
	signer     manualsign.Signer
	cipher     manualartifact.Cipher
	decryptor  manualArtifactDecryptor
	keyVersion string
	health     *manualTrustHealth
}

type manualTrustHealth struct{ unhealthy atomic.Bool }

func manualTrustUsable(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return !r.IsNil()
	}
	return true
}

// Constructor verifies configuration only. It performs no DB writes, provider
// samples, observations, clock bootstrap or signing. No ordinary caller can
// obtain this package-private bundle through API.Options or a public factory.
func newManualTrustComposition(store *Store, owner Identity, profile manualRuntimeProfile,
	source manualRuntimeTimeSource, identity manualRuntimeIdentitySource, nonce manualNonceObserver,
	runtime manualRuntimeObserver, signer manualsign.Signer, cipher manualartifact.Cipher, decryptor manualArtifactDecryptor) (result *manualTrustComposition, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = ErrInvalid
		}
	}()
	if store == nil || store.db == nil || !owner.valid() || !profile.valid() {
		return nil, ErrInvalid
	}
	for _, v := range []any{source, identity, nonce, runtime, signer, cipher, decryptor} {
		if !manualTrustUsable(v) {
			return nil, ErrInvalid
		}
	}
	version := cipher.KeyVersion()
	if signer.Address() != common.HexToAddress(owner.WalletAddress) || !validID(version) || version != decryptor.KeyVersion() || cipher.KeyVersion() != version {
		return nil, ErrInvalid
	}
	return &manualTrustComposition{store: store, owner: owner, profile: profile, timeSource: source, identity: identity, nonce: nonce, runtime: runtime, signer: signer, cipher: cipher, decryptor: decryptor, keyVersion: version, health: &manualTrustHealth{}}, nil
}

func (b *manualTrustComposition) signingWriter() *manualLocalSigningWriter {
	return &manualLocalSigningWriter{store: b.store, observer: b.nonce, signer: b.signer, cipher: b.cipher, trust: b}
}

// Do not pass this bundle into recovery. The verifier receives only its
// original narrow dependencies, never the signer, cipher or whole bundle.
func (b *manualTrustComposition) verifier() *manualRuntimeVerifier {
	return &manualRuntimeVerifier{store: b.store, profile: b.profile, timeSource: b.timeSource, identity: b.identity, decryptor: b.decryptor, observer: b.runtime}
}

func (b *manualTrustComposition) session(ctx context.Context, id Identity, scope manualAdmissionScope) (*manualPreSignTrust, error) {
	if b == nil || b.health == nil || ctx == nil || !b.profile.valid() || id != b.owner || scope != b.profile.process.scope {
		return nil, ErrInvalid
	}
	return &manualPreSignTrust{bundle: b, ctx: ctx, q: b.store.db, clock: manualRuntimeClock{source: b.timeSource, maximumError: b.profile.maximumError}}, nil
}
