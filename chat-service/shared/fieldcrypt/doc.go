// Package fieldcrypt encrypts content-bearing database fields at rest with
// envelope encryption: AES-256-GCM under versioned data keys, the data keys
// held in a KeyRing (an env keyring on dev, KMS-wrapped keys in production).
//
// It is a byte-compatible copy of Architecture/shared/pii (chat-service is a
// separate Go workspace and its images build with GOWORK=off from this tree,
// so it cannot import that module). The sealed blob is the same:
//
//	version uint32 big-endian (4) | nonce (12) | AES-256-GCM ciphertext || tag (16)
//
// and the GCM additional data of an unbound Seal is the scope string, exactly
// as in pii, so a pii blob opens here given the same (scope, version) key and
// the reverse. envelope.go, keyring.go and their tests are that package's
// files with the package name changed; the additions are:
//
//   - row binding (SealBound / OpenBound / Bind): additional data is
//     scope || 0x00 || binding, so a ciphertext moved to another row (another
//     conversation, message, user) fails to open instead of decrypting there;
//   - VersionedRing: master keys by version for every scope, each scope's data
//     key derived with HKDF-SHA256 so the columns never share a GCM key;
//   - KMSRing: master keys minted by a KeyWrapper (KMS) and persisted wrapped,
//     unwrapped once at boot and on a version the process has not seen;
//   - Cipher: the column codec. A sealed value is stored as Prefix followed by
//     the blob in unpadded base64url, so it fits TEXT columns and, quoted, JSONB.
//     Values written before encryption (no prefix) still read back unchanged,
//     which is what lets a deployment switch on without downtime.
//
// # Operational rules
//
//   - Never log plaintext, sealed values, keys or wrapped keys. Errors from
//     this package never contain any of them.
//   - A Cipher without a ring (Plaintext) writes plaintext; services decide at
//     boot whether that is allowed (development only) and say so in the log.
//   - Losing a key version loses every value sealed under it.
//   - GCM with random nonces is safe for about 2^32 seals per key. Each scope
//     derives its own key, but a busy deployment should still rotate (add a
//     version) at least yearly; old versions keep decrypting.
package fieldcrypt
