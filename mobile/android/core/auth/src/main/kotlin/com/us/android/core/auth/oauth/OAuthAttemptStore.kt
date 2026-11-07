package com.us.android.core.auth.oauth

import android.content.Context
import android.content.SharedPreferences
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import dagger.hilt.android.qualifiers.ApplicationContext
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The one Google sign-in attempt in flight: its `state` and PKCE verifier.
 *
 * Kept outside the process so that the browser hop surviving a process death
 * (Android reclaims a backgrounded app freely while the consent page is up)
 * still ends in a sign-in rather than an unconditional state mismatch. The
 * verifier is a short-lived secret, so the stored copy is encrypted and
 * [GoogleAuthorizationFlow] honours it for [GoogleAuthorizationFlow.ATTEMPT_TTL_MILLIS] only.
 */
data class PendingOAuthAttempt(
    val state: String,
    val codeVerifier: String,
    val createdAtMillis: Long,
)

interface OAuthAttemptStore {
    fun save(attempt: PendingOAuthAttempt)

    /** The stored attempt, or null if there is none or it cannot be read back. */
    fun load(): PendingOAuthAttempt?

    fun clear()
}

/** Process memory only: for tests, and for a host that wants no persistence. */
class InMemoryOAuthAttemptStore : OAuthAttemptStore {
    @Volatile
    private var attempt: PendingOAuthAttempt? = null

    override fun save(attempt: PendingOAuthAttempt) {
        this.attempt = attempt
    }

    override fun load(): PendingOAuthAttempt? = attempt

    override fun clear() {
        attempt = null
    }
}

/**
 * [OAuthAttemptStore] in private SharedPreferences, the value encrypted with
 * AES-256-GCM under its own Android Keystore key — the same primitive
 * [com.us.android.core.auth.KeystoreTokenStore] uses for the refresh token,
 * under a separate alias so clearing one never touches the other.
 */
@Singleton
class KeystoreOAuthAttemptStore @Inject constructor(
    @ApplicationContext private val context: Context,
) : OAuthAttemptStore {

    private val prefs: SharedPreferences by lazy {
        context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
    }

    override fun save(attempt: PendingOAuthAttempt) {
        val plain = listOf(attempt.state, attempt.codeVerifier, attempt.createdAtMillis.toString())
            .joinToString(SEPARATOR)
        // commit, not apply: the browser opens right after this returns, and
        // the process may die before an asynchronous write lands.
        prefs.edit().putString(KEY_ATTEMPT, encrypt(plain)).commit()
    }

    override fun load(): PendingOAuthAttempt? {
        val stored = prefs.getString(KEY_ATTEMPT, null) ?: return null
        val parts = runCatching { decrypt(stored) }.getOrNull()?.split(SEPARATOR)
        val createdAt = parts?.getOrNull(2)?.toLongOrNull()
        if (parts == null || parts.size != FIELD_COUNT || createdAt == null) {
            // Unreadable (key invalidated, corrupt value): an attempt nobody
            // can finish. Drop it; the person simply taps the button again.
            clear()
            return null
        }
        return PendingOAuthAttempt(state = parts[0], codeVerifier = parts[1], createdAtMillis = createdAt)
    }

    override fun clear() {
        prefs.edit().remove(KEY_ATTEMPT).commit()
    }

    private fun secretKey(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getEntry(KEY_ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(KEY_SIZE_BITS)
                .build(),
        )
        return generator.generateKey()
    }

    private fun encrypt(plain: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, secretKey())
        val combined = cipher.iv + cipher.doFinal(plain.toByteArray(Charsets.UTF_8))
        return Base64.encodeToString(combined, Base64.NO_WRAP)
    }

    private fun decrypt(stored: String): String {
        val combined = Base64.decode(stored, Base64.NO_WRAP)
        val iv = combined.copyOfRange(0, GCM_IV_LENGTH)
        val cipherText = combined.copyOfRange(GCM_IV_LENGTH, combined.size)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(GCM_TAG_LENGTH_BITS, iv))
        return String(cipher.doFinal(cipherText), Charsets.UTF_8)
    }

    private companion object {
        const val PREFS_NAME = "us_oauth_attempt"
        const val KEY_ATTEMPT = "google_attempt"

        // Neither the state nor the verifier (both base64url) can contain it.
        const val SEPARATOR = "|"
        const val FIELD_COUNT = 3

        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "us_oauth_attempt_key"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val KEY_SIZE_BITS = 256
        const val GCM_IV_LENGTH = 12
        const val GCM_TAG_LENGTH_BITS = 128
    }
}
