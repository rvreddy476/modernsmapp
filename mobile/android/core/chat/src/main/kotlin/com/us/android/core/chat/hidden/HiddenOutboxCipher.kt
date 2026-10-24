package com.us.android.core.chat.hidden

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Seals the body of a hidden chat's queued send (24 Oct 2026).
 *
 * A message typed in an open hidden chat is a Room outbox row before it is a
 * network call. If the chats re-lock before it goes (background, a minute
 * idle), the row waits — and a waiting row must not be plain text on disk.
 * So its body is sealed with AES-GCM under a key of its OWN:
 *  - not the biometric vault's key ([HiddenUnlockVault]): that one needs a
 *    fingerprint per use, and this one must work from the outbox worker with
 *    nobody at the screen;
 *  - no user authentication, no enrolment invalidation — a background key;
 *  - destroyed on sign-out and on a hidden-chats reset, which turns any
 *    sealed bytes left in free pages or the WAL into noise.
 *
 * Callers FAIL CLOSED: a null from [seal] means the row is not written at all
 * (never written in plain text instead).
 */
interface HiddenOutboxCipher {
    /** `iv || ciphertext+tag`, Base64, bound to [aad]; null when the key cannot be made or used. */
    fun seal(plain: ByteArray, aad: ByteArray): String?

    /** The plain bytes, or null — a wrong [aad], a changed byte, a destroyed key. */
    fun open(sealed: String, aad: ByteArray): ByteArray?

    /** Forgets the key: every row sealed under it can never open again. */
    fun destroyKey()
}

/** Where the AES key comes from: the Android Keystore in the app, a software key on the JVM test path. */
interface OutboxKeySource {
    /** The key; made first when [create] and absent. Null when it cannot be had. */
    fun key(create: Boolean): SecretKey?

    fun destroy()
}

/**
 * AES-256-GCM, a fresh random 96-bit IV per seal (the provider chooses it —
 * the Keystore refuses a caller-chosen IV for a randomized key), 128-bit tag.
 * The same code runs in the app and in the tests; only the key source differs.
 */
open class AesGcmOutboxCipher(private val keys: OutboxKeySource) : HiddenOutboxCipher {

    override fun seal(plain: ByteArray, aad: ByteArray): String? = runCatching {
        val key = keys.key(create = true) ?: return null
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        cipher.updateAAD(aad)
        val body = cipher.doFinal(plain)
        val iv = cipher.iv
        check(iv != null && iv.size == IV_BYTES) { "unexpected iv" }
        java.util.Base64.getEncoder().encodeToString(iv + body)
    }.getOrNull()

    override fun open(sealed: String, aad: ByteArray): ByteArray? = runCatching {
        val raw = java.util.Base64.getDecoder().decode(sealed)
        if (raw.size <= IV_BYTES) return null
        val key = keys.key(create = false) ?: return null
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, raw, 0, IV_BYTES))
        cipher.updateAAD(aad)
        cipher.doFinal(raw, IV_BYTES, raw.size - IV_BYTES)
    }.getOrNull()

    override fun destroyKey() {
        runCatching { keys.destroy() }
    }

    private companion object {
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
        const val TAG_BITS = 128
    }
}

/** The outbox key in the Android Keystore: non-exportable, usable in the background. */
class KeystoreOutboxKeySource : OutboxKeySource {

    private val lock = Any()

    override fun key(create: Boolean): SecretKey? = synchronized(lock) {
        val store = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        if (!create) return null
        val spec = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(KEY_BITS)
            .setRandomizedEncryptionRequired(true)
            // The worker seals and opens with nobody at the screen.
            .setUserAuthenticationRequired(false)
            .build()
        KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
            .apply { init(spec) }
            .generateKey()
    }

    override fun destroy() {
        synchronized(lock) {
            KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }.deleteEntry(ALIAS)
        }
    }

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"

        /** Separate from the biometric vault's `hidden_chats_unlock_v1`. */
        const val ALIAS = "hidden_chats_outbox_v1"
        const val KEY_BITS = 256
    }
}

/** The app's outbox cipher. */
@Singleton
class KeystoreHiddenOutboxCipher @Inject constructor() : AesGcmOutboxCipher(KeystoreOutboxKeySource())
