package com.us.android.core.chat.hidden

import android.content.Context
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import android.util.Base64
import dagger.hilt.android.qualifiers.ApplicationContext
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Fingerprint or face instead of the chat password, on THIS device, after the
 * password was entered once here (contract 24 Oct 2026).
 *
 * The server has no device-unlock route: opening a hidden chat always means
 * `POST /hidden/open {chat_password}`. So what this device keeps is the chat
 * password itself, sealed under an Android Keystore AES-GCM key that
 *  - never leaves the secure hardware,
 *  - can be used only right after a STRONG biometric (per use, no time window),
 *  - is destroyed by the platform when a fingerprint or face is enrolled or
 *    removed ([KeyPermanentlyInvalidatedException] → the sealed copy is
 *    discarded and the password is asked for again).
 * The ciphertext sits in app-private preferences; without the hardware key
 * it is noise. It is dropped on sign-out, on a password change or reset, and
 * the first time the server says the sealed password is wrong.
 *
 * Every cipher comes back for a BiometricPrompt `CryptoObject`; nothing here
 * decrypts without one the prompt has authorised.
 */
interface HiddenUnlockVault {
    /** True when a sealed password for [userId] exists. */
    fun has(userId: String): Boolean

    /** A cipher to seal with, for the prompt; null when the key cannot be made or used. */
    fun sealCipher(): Cipher?

    /** Seals [password] for [userId] with an AUTHORISED [cipher] from [sealCipher]. */
    fun seal(userId: String, cipher: Cipher, password: String): Boolean

    /** A cipher to open with, for the prompt; null (and the copy forgotten) when the key is gone. */
    fun openCipher(userId: String): Cipher?

    /** The password, with an AUTHORISED [cipher] from [openCipher]; null on any failure. */
    fun open(userId: String, cipher: Cipher): String?

    /** Forgets [userId]'s sealed password, or every one when null. */
    fun forget(userId: String?)
}

@Singleton
class KeystoreHiddenUnlockVault @Inject constructor(
    @ApplicationContext context: Context,
) : HiddenUnlockVault {

    private val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    override fun has(userId: String): Boolean =
        userId.isNotBlank() && prefs.contains(slot(userId) + CT) && prefs.contains(slot(userId) + IV)

    override fun sealCipher(): Cipher? = runCatching {
        Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.ENCRYPT_MODE, key(create = true)) }
    }.recoverCatching { e ->
        // The enrolment changed since the key was made: start a fresh key.
        if (e is KeyPermanentlyInvalidatedException) {
            deleteKey()
            Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.ENCRYPT_MODE, key(create = true)) }
        } else {
            throw e
        }
    }.getOrNull()

    override fun seal(userId: String, cipher: Cipher, password: String): Boolean {
        if (userId.isBlank() || password.isEmpty()) return false
        return runCatching {
            val sealed = cipher.doFinal(password.toByteArray(Charsets.UTF_8))
            prefs.edit()
                .putString(slot(userId) + CT, Base64.encodeToString(sealed, Base64.NO_WRAP))
                .putString(slot(userId) + IV, Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
                .apply()
            true
        }.getOrDefault(false)
    }

    override fun openCipher(userId: String): Cipher? {
        if (!has(userId)) return null
        val iv = prefs.getString(slot(userId) + IV, null)?.let { Base64.decode(it, Base64.NO_WRAP) } ?: return null
        val key = runCatching { key(create = false) }.getOrNull()
        if (key == null) {
            forget(userId)
            return null
        }
        return try {
            Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(GCM_TAG_BITS, iv)) }
        } catch (invalidated: KeyPermanentlyInvalidatedException) {
            // A fingerprint or face was added or removed: this copy can never open again.
            forget(null)
            deleteKey()
            null
        } catch (@Suppress("TooGenericExceptionCaught") other: Exception) {
            null
        }
    }

    override fun open(userId: String, cipher: Cipher): String? = runCatching {
        val sealed = prefs.getString(slot(userId) + CT, null)?.let { Base64.decode(it, Base64.NO_WRAP) } ?: return null
        String(cipher.doFinal(sealed), Charsets.UTF_8)
    }.getOrNull()

    override fun forget(userId: String?) {
        if (userId == null) {
            prefs.edit().clear().apply()
        } else {
            prefs.edit().remove(slot(userId) + CT).remove(slot(userId) + IV).apply()
        }
    }

    private fun key(create: Boolean): SecretKey {
        val store = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        check(create) { "no unlock key" }
        val spec = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(KEY_BITS)
            .setUserAuthenticationRequired(true)
            .setInvalidatedByBiometricEnrollment(true)
            .apply {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    // Per use, strong biometric only — never the device PIN.
                    setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                } else {
                    @Suppress("DEPRECATION")
                    setUserAuthenticationValidityDurationSeconds(-1)
                }
            }
            .build()
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
            .apply { init(spec) }
            .generateKey()
    }

    private fun deleteKey() {
        runCatching { KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }.deleteEntry(ALIAS) }
    }

    /** The user's slot: a hash, so the file names no account. */
    private fun slot(userId: String): String =
        MessageDigest.getInstance("SHA-256").digest(userId.toByteArray(Charsets.UTF_8))
            .joinToString("") { "%02x".format(it) }
            .take(SLOT_CHARS)

    private companion object {
        const val PREFS = "hidden_chats_unlock"
        const val ALIAS = "hidden_chats_unlock_v1"
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val KEY_BITS = 256
        const val GCM_TAG_BITS = 128
        const val SLOT_CHARS = 32
        const val CT = ".ct"
        const val IV = ".iv"
    }
}
