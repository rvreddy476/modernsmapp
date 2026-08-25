package com.us.android.core.chat.lock

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * P0-7 correction-pass proofs for the chat-lock verifier derivation.
 *
 * The KDF parameters ARE the security claim: the known-answer test pins the
 * exact argon2id output for fixed inputs, so any silent change to memory,
 * iterations, parallelism, output length or the algorithm itself fails here
 * instead of weakening every stored verifier.
 */
class PinKdfTest {

    private val salt = ByteArray(16) { it.toByte() }

    @Test
    fun `argon2id parameters are the reviewed OWASP profile`() {
        assertThat(PinKdf.ARGON2_MEMORY_KIB).isEqualTo(47_104)
        assertThat(PinKdf.ARGON2_ITERATIONS).isEqualTo(2)
        assertThat(PinKdf.ARGON2_PARALLELISM).isEqualTo(1)
        assertThat(PinKdf.VERIFIER_BYTES).isEqualTo(32)
    }

    @Test
    fun `argon2id known answer is stable`() {
        val out = PinKdf.deriveArgon2id("482913", salt)
        assertThat(out.toHex()).isEqualTo(KNOWN_ANSWER_HEX)
    }

    @Test
    fun `derivation is deterministic and input sensitive`() {
        val a = PinKdf.deriveArgon2id("482913", salt)
        assertThat(PinKdf.deriveArgon2id("482913", salt)).isEqualTo(a)
        assertThat(PinKdf.deriveArgon2id("482914", salt)).isNotEqualTo(a)
        val otherSalt = ByteArray(16) { (it + 1).toByte() }
        assertThat(PinKdf.deriveArgon2id("482913", otherSalt)).isNotEqualTo(a)
    }

    @Test
    fun `legacy pbkdf2 differs from argon2id and only serves the upgrade path`() {
        val argon = PinKdf.derive(PinKdf.VERSION_ARGON2ID, "482913", salt)
        val legacy = PinKdf.derive(PinKdf.VERSION_PBKDF2, "482913", salt)
        assertThat(argon).isNotEqualTo(legacy)
        assertThat(legacy).hasLength(PinKdf.VERIFIER_BYTES)
    }

    @Test
    fun `unknown verifier version is refused`() {
        try {
            PinKdf.derive(99, "482913", salt)
            throw AssertionError("unknown version must throw")
        } catch (expected: IllegalArgumentException) {
            // refused, as required
        }
    }

    @Test
    fun `constant time comparison compares correctly`() {
        val a = byteArrayOf(1, 2, 3)
        assertThat(PinKdf.constantTimeEquals(a, byteArrayOf(1, 2, 3))).isTrue()
        assertThat(PinKdf.constantTimeEquals(a, byteArrayOf(1, 2, 4))).isFalse()
        assertThat(PinKdf.constantTimeEquals(a, byteArrayOf(1, 2))).isFalse()
    }

    @Test
    fun `chat surfaces carry FLAG_SECURE exactly while the lock is enabled`() {
        // 0x2000 is WindowManager.LayoutParams.FLAG_SECURE; pinned as a
        // literal because this module is deliberately Android-free.
        assertThat(chatLockSecureWindowFlags(lockEnabled = true)).isEqualTo(0x00002000)
        assertThat(chatLockSecureWindowFlags(lockEnabled = false)).isEqualTo(0)
    }

    private fun ByteArray.toHex() = joinToString("") { "%02x".format(it) }

    private companion object {
        // Generated once from this exact parameter set (argon2id, m=47104 KiB,
        // t=2, p=1, 32-byte output) and pinned; a parameter or algorithm
        // change breaks this expectation on purpose.
        const val KNOWN_ANSWER_HEX =
            "e220d9079e013ee1b6cc99d4a5c0517112dbef9486088b27e3c0c627b529f259"
    }
}
