package com.us.android.core.chat.lock

import org.bouncycastle.crypto.generators.Argon2BytesGenerator
import org.bouncycastle.crypto.params.Argon2Parameters
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.PBEKeySpec

/**
 * The chat-lock PIN verifier derivation (P0-7 correction).
 *
 * Pure JVM on purpose: no Android imports, so the derivation — the part of
 * the lock whose parameters ARE the security claim — is provable in plain
 * unit tests, and a mutation of any parameter fails a test instead of
 * silently weakening the verifier.
 *
 * Current scheme ([VERSION_ARGON2ID]): Argon2id via Bouncy Castle — a
 * reviewed, memory-hard KDF as the directive requires. Parameters follow the
 * OWASP password-storage recommendation (46 MiB, t=2, p=1); memory-hardness
 * is what prices out the GPU offline-guessing attack the independent review
 * demonstrated against a PBKDF2 verifier of a short PIN.
 *
 * [VERSION_PBKDF2] survives ONLY to verify pre-existing verifiers once, after
 * which [ChatLockManager] rewrites them as argon2id.
 */
object PinKdf {

    const val VERSION_PBKDF2 = 1
    const val VERSION_ARGON2ID = 2

    const val VERIFIER_BYTES = 32

    // OWASP Argon2id recommendation: m=47104 KiB (46 MiB), t=2 later revised
    // pairings allowed; p=1. Changing any of these changes every stored
    // verifier's meaning — the KDF unit test pins them.
    const val ARGON2_MEMORY_KIB = 47_104
    const val ARGON2_ITERATIONS = 2
    const val ARGON2_PARALLELISM = 1

    /** Legacy PBKDF2 iteration count — verification of old verifiers only. */
    const val PBKDF2_ITERATIONS = 310_000

    fun derive(version: Int, pin: String, salt: ByteArray): ByteArray = when (version) {
        VERSION_ARGON2ID -> deriveArgon2id(pin, salt)
        VERSION_PBKDF2 -> deriveLegacyPbkdf2(pin, salt)
        else -> throw IllegalArgumentException("unknown verifier version $version")
    }

    fun deriveArgon2id(pin: String, salt: ByteArray): ByteArray {
        val params = Argon2Parameters.Builder(Argon2Parameters.ARGON2_id)
            .withSalt(salt)
            .withMemoryAsKB(ARGON2_MEMORY_KIB)
            .withIterations(ARGON2_ITERATIONS)
            .withParallelism(ARGON2_PARALLELISM)
            .build()
        val generator = Argon2BytesGenerator()
        generator.init(params)
        val out = ByteArray(VERIFIER_BYTES)
        val pinBytes = pin.toByteArray(Charsets.UTF_8)
        try {
            generator.generateBytes(pinBytes, out)
        } finally {
            pinBytes.fill(0)
        }
        return out
    }

    fun deriveLegacyPbkdf2(pin: String, salt: ByteArray): ByteArray {
        val spec = PBEKeySpec(pin.toCharArray(), salt, PBKDF2_ITERATIONS, VERIFIER_BYTES * Byte.SIZE_BITS)
        return try {
            SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded
        } finally {
            spec.clearPassword()
        }
    }

    /** Constant-time comparison; unequal lengths short-circuit by design. */
    fun constantTimeEquals(a: ByteArray, b: ByteArray): Boolean {
        if (a.size != b.size) return false
        var diff = 0
        for (i in a.indices) diff = diff or (a[i].toInt() xor b[i].toInt())
        return diff == 0
    }
}

/**
 * The window flags every chat surface must carry while the chat lock is
 * enabled (P0-7): FLAG_SECURE keeps message text out of the recents/task
 * switcher snapshot and blocks screen capture. Pure function so the policy —
 * enabled means secure, disabled means untouched — is unit-testable; the
 * Compose gate applies the returned mask to the activity window.
 *
 * The literal is [android.view.WindowManager.LayoutParams.FLAG_SECURE]; this
 * module stays Android-free so the value is pinned here and in the test.
 */
const val WINDOW_FLAG_SECURE = 0x00002000

fun chatLockSecureWindowFlags(lockEnabled: Boolean): Int =
    if (lockEnabled) WINDOW_FLAG_SECURE else 0
