// Package pii seals personal identifiers at rest, derives salted lookup hashes
// for exact-match search, and masks identifiers for display.
//
// It is the shared form of commerce-service/internal/pii. The envelope wire
// format is byte-compatible with commerce, so a commerce row opens here given a
// KeyRing that maps the same (scope, version) to the same key:
//
//	version uint32 big-endian (4) | nonce (12) | AES-256-GCM ciphertext || tag (16)
//
// The GCM additional data is the scope string, so a value moved between scopes
// (columns, retention classes, services) fails to open instead of decrypting.
//
// # What to seal
//
// Seal with a Sealer and store the ciphertext plus its key version:
//
//   - bank account numbers (restaurant settlement, rider payout, seller payout);
//   - PAN;
//   - driving-licence number;
//   - vehicle registration number when it is tied to a rider;
//   - any government ID document number;
//   - by default, personal names and phone numbers (commerce precedent).
//
// # What to hash as well
//
// Next to the ciphertext, store a LookupHasher hash when the service needs an
// exact-match lookup or duplicate detection: vehicle registration, DL number,
// PAN, bank account. Normalise with CompactUpper or a shared/kyc normaliser.
//
// The lookup salt is a secret and is held like a key: in a secrets manager and
// never in the repository. These identifiers have low entropy, so anyone holding
// the salt can brute-force the hashes. A hash is therefore never a substitute
// for sealing, and it is never displayed or logged.
//
// # What may stay clear
//
// IFSC, the last four digits of an account, state code, city and PIN code
// (commerce precedent), and the key version. An FSSAI licence number and a
// registered business's GSTIN are displayed publicly by law. Note that a GSTIN
// embeds the entity's PAN, so storing a GSTIN in clear exposes that PAN.
//
// # Aadhaar
//
// Never store an Aadhaar number in any form: not in clear, not sealed, not
// hashed. The Aadhaar Act and UIDAI regulations restrict storage to authorised
// entities operating an Aadhaar Data Vault. A hash of 12 digits is
// brute-forceable, so hashing does not make it safe. Do not accept it in forms
// and do not log it.
//
// # Operational rules
//
//   - Never log plaintext, sealed blobs, keys or lookup salts. Errors returned by
//     this package never contain any of them; KeyRing and Normaliser
//     implementations must keep the same promise.
//   - StaticKeyRing is for development and tests. Services must refuse to start
//     with it in production; this package does not know the environment.
//   - Losing a key-ring version loses every value sealed under it.
//   - Seal refuses an empty value and Open refuses an empty blob, so a missing
//     ciphertext never reads back as "". Optional fields skip the call.
package pii
