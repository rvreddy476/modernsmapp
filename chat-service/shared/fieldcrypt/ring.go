package fieldcrypt

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// hkdfInfoPrefix separates this derivation from any other use of the same
// master key material.
const hkdfInfoPrefix = "atpost/chat-fieldcrypt/v1|"

// VersionedRing holds master keys by version for every scope. The active
// version is the highest one. DataKey derives the scope's key from the
// version's master key with HKDF-SHA256 (info = prefix || scope), so two
// columns never encrypt under the same GCM key even though one secret is
// configured. The zero value holds no keys and fails closed.
type VersionedRing struct {
	mu      sync.RWMutex
	masters map[uint32][]byte
	active  uint32
	derived map[derivedKeyID][]byte
}

type derivedKeyID struct {
	scope   Scope
	version uint32
}

var _ KeyRing = (*VersionedRing)(nil)

// NewVersionedRing builds a ring from master keys by version. It refuses
// version 0, a key that is not 32 bytes and an all-zero key. Keys are copied.
func NewVersionedRing(masters map[uint32][]byte) (*VersionedRing, error) {
	r := &VersionedRing{}
	for v, k := range masters {
		if err := r.add(v, k); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// add installs a master key. A version already present with the same bytes
// is a no-op (a reload); with different bytes it is refused.
func (r *VersionedRing) add(version uint32, key []byte) error {
	if version == 0 {
		return fmt.Errorf("%w: version 0 is reserved", ErrInvalidKeyVersion)
	}
	if err := validateKey(key); err != nil {
		return fmt.Errorf("fieldcrypt: key version %d: %w", version, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.masters[version]; ok {
		if string(existing) == string(key) {
			return nil
		}
		return fmt.Errorf("%w: version %d is already in the ring with a different key", ErrInvalidKeyVersion, version)
	}
	if r.masters == nil {
		r.masters = map[uint32][]byte{}
		r.derived = map[derivedKeyID][]byte{}
	}
	r.masters[version] = append([]byte(nil), key...)
	if version > r.active {
		r.active = version
	}
	return nil
}

// has reports whether the ring holds a version.
func (r *VersionedRing) has(version uint32) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.masters[version]
	return ok
}

// Versions lists the versions held, ascending.
func (r *VersionedRing) Versions() []uint32 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]uint32, 0, len(r.masters))
	for v := range r.masters {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ActiveVersion implements KeyRing. Every valid scope shares the active
// version.
func (r *VersionedRing) ActiveVersion(_ context.Context, scope Scope) (uint32, error) {
	if r == nil {
		return 0, ErrScopeNotConfigured
	}
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.active == 0 {
		return 0, ErrScopeNotConfigured
	}
	return r.active, nil
}

// DataKey implements KeyRing: the scope's key for exactly this version.
func (r *VersionedRing) DataKey(_ context.Context, scope Scope, version uint32) ([]byte, error) {
	if r == nil || version == 0 {
		return nil, ErrUnknownKeyVersion
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	id := derivedKeyID{scope, version}
	r.mu.RLock()
	if k, ok := r.derived[id]; ok {
		r.mu.RUnlock()
		return append([]byte(nil), k...), nil
	}
	master, ok := r.masters[version]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownKeyVersion
	}
	k, err := hkdf.Key(sha256.New, master, nil, hkdfInfoPrefix+string(scope), DataKeySize)
	if err != nil {
		return nil, ErrBadDataKey
	}
	r.mu.Lock()
	r.derived[id] = k
	r.mu.Unlock()
	return append([]byte(nil), k...), nil
}

// ParseKeyList parses the env keyring format "v1:<key>,v2:<key>": versioned
// 32-byte keys in base64 (standard or URL-safe, padded or not) or 64 hex
// characters. Whitespace around entries is ignored. It refuses an empty
// list, a malformed entry, version 0, a duplicate version and a bad key.
// Errors name the entry position only and never echo key material.
func ParseKeyList(spec string) (*VersionedRing, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("%w: the key list is empty", ErrBadDataKey)
	}
	masters := map[uint32][]byte{}
	for i, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		name, raw, ok := strings.Cut(entry, ":")
		if !ok || len(name) < 2 || (name[0] != 'v' && name[0] != 'V') {
			return nil, fmt.Errorf("%w: entry %d is not v<version>:<key>", ErrInvalidKeyVersion, i+1)
		}
		n, err := strconv.ParseUint(name[1:], 10, 32)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%w: entry %d has no valid version (1 or more)", ErrInvalidKeyVersion, i+1)
		}
		if _, dup := masters[uint32(n)]; dup {
			return nil, fmt.Errorf("%w: version %d appears twice", ErrInvalidKeyVersion, n)
		}
		key, err := DecodeKey(raw)
		if err != nil {
			return nil, fmt.Errorf("entry %d (version %d): %w", i+1, n, err)
		}
		masters[uint32(n)] = key
	}
	return NewVersionedRing(masters)
}

// EncodeKeyList renders a ring spec for ParseKeyList (tests and tooling).
func EncodeKeyList(masters map[uint32][]byte) string {
	versions := make([]uint32, 0, len(masters))
	for v := range masters {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	parts := make([]string, 0, len(versions))
	for _, v := range versions {
		parts = append(parts, "v"+strconv.FormatUint(uint64(v), 10)+":"+base64.StdEncoding.EncodeToString(masters[v]))
	}
	return strings.Join(parts, ",")
}
