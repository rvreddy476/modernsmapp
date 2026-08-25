package com.us.android.core.chat.lock

import android.content.Context
import android.content.SharedPreferences
import android.util.Base64
import android.util.Log
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.security.SecureRandom
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The LOCAL chat lock (directive §3.6, CH-LB-6).
 *
 * ## WHAT THIS IS AND IS NOT
 *
 * An APPLICATION lock over the chat surfaces — not message encryption. The
 * PIN never leaves the device, is never logged, and is never used as key
 * material: what is stored is a random salt plus a versioned verifier.
 *
 * ## VERIFIER (P0-7 correction)
 *
 * Verifiers are argon2id ([PinKdf.VERSION_ARGON2ID]) — a reviewed
 * memory-hard KDF, as the directive requires — and live in Keystore-wrapped
 * [EncryptedSharedPreferences], so an extracted data directory yields
 * ciphertext without the hardware key. Verifiers written by the earlier
 * PBKDF2 build are verified once with the legacy KDF and rewritten as
 * argon2id on the first successful unlock.
 *
 * If the encrypted store cannot be opened (Keystore corruption), the lock
 * FAILS LOCKED: chat stays gated and the only exit is [resetForgotten],
 * which wipes cached chat data. A backdoor around an unreadable verifier
 * would be a backdoor around the lock.
 *
 * ## LOCK TIMING
 *
 * Process start with the lock enabled starts LOCKED — process death, reboot
 * and logout can never resurrect an unlocked state. Backgrounding stamps the
 * time; foregrounding past the configured interval re-locks. While the lock
 * is enabled, chat surfaces additionally carry FLAG_SECURE (see
 * [chatLockSecureWindowFlags]) so the recents snapshot never retains message
 * text.
 *
 * ## THROTTLING
 *
 * Wrong attempts are counted; at [MAX_ATTEMPTS] the verifier refuses for a
 * doubling lockout window. The refusal happens BEFORE the KDF runs, so a
 * throttled attempt costs the same regardless of the guess (no timing
 * distinction between throttled-right and throttled-wrong).
 *
 * ## FORGOTTEN PIN
 *
 * There is no backdoor. Reset = wipe: the reset screen states that clearing
 * the lock also clears this device's cached chat data, and the caller passes
 * a wipe callback that does exactly that.
 */
@Singleton
class ChatLockManager @Inject constructor(
    @ApplicationContext private val context: Context,
) {

    /** How long chat may sit in the background before re-locking. */
    @Suppress("MagicNumber") // Each entry names its own duration; a constant per value adds nothing.
    enum class LockInterval(val millis: Long, val label: String) {
        Immediately(0L, "Immediately"),
        OneMinute(60_000L, "After 1 minute"),
        FiveMinutes(300_000L, "After 5 minutes"),
        ThirtyMinutes(1_800_000L, "After 30 minutes"),
    }

    // Null when the Keystore-backed store cannot be opened — every read then
    // reports the fail-LOCKED posture and only resetForgotten() recovers.
    private val prefs: SharedPreferences? = openSecurePrefs(context)

    private val _locked = MutableStateFlow(isEnabled)

    /** True while the chat surfaces must show the lock screen. */
    val locked: StateFlow<Boolean> = _locked.asStateFlow()

    private var backgroundedAtMillis: Long = 0L

    val isEnabled: Boolean
        get() = prefs?.getBoolean(KEY_ENABLED, false) ?: true // corrupted store fails locked

    var interval: LockInterval
        get() = LockInterval.entries.firstOrNull { it.name == prefs?.getString(KEY_INTERVAL, null) }
            ?: LockInterval.Immediately
        set(value) {
            prefs?.edit()?.putString(KEY_INTERVAL, value.name)?.apply()
        }

    val biometricEnabled: Boolean
        get() = prefs?.getBoolean(KEY_BIOMETRIC, true) ?: false

    fun setBiometricEnabled(enabled: Boolean) {
        prefs?.edit()?.putBoolean(KEY_BIOMETRIC, enabled)?.apply()
    }

    /** Millis until PIN attempts are accepted again; 0 when not throttled. */
    fun lockoutRemainingMillis(): Long =
        ((prefs?.getLong(KEY_LOCKOUT_UNTIL, 0L) ?: 0L) - System.currentTimeMillis()).coerceAtLeast(0L)

    /** Enables the lock with a fresh PIN. */
    fun enable(pin: String) {
        val store = prefs ?: return
        require(pin.length >= MIN_PIN_LENGTH) { "PIN too short" }
        val salt = ByteArray(SALT_BYTES).also { SecureRandom().nextBytes(it) }
        val hash = PinKdf.deriveArgon2id(pin, salt)
        store.edit()
            .putBoolean(KEY_ENABLED, true)
            .putInt(KEY_KDF_VERSION, PinKdf.VERSION_ARGON2ID)
            .putString(KEY_SALT, Base64.encodeToString(salt, Base64.NO_WRAP))
            .putString(KEY_VERIFIER, Base64.encodeToString(hash, Base64.NO_WRAP))
            .putInt(KEY_ATTEMPTS, 0)
            .putLong(KEY_LOCKOUT_UNTIL, 0L)
            .apply()
        _locked.value = false // just set it up; they are present
    }

    /** Disables the lock; requires the current PIN. */
    fun disable(pin: String): Boolean {
        if (!verifyThrottled(pin)) return false
        prefs?.edit()
            ?.putBoolean(KEY_ENABLED, false)
            ?.remove(KEY_SALT)
            ?.remove(KEY_VERIFIER)
            ?.remove(KEY_KDF_VERSION)
            ?.putInt(KEY_ATTEMPTS, 0)
            ?.putLong(KEY_LOCKOUT_UNTIL, 0L)
            ?.apply()
        _locked.value = false
        return true
    }

    /**
     * The no-backdoor reset: clears the lock AND asks the caller to wipe the
     * device's cached chat data via [wipe]. The UI states both consequences
     * before calling this. Also the ONLY recovery from a corrupted secure
     * store.
     */
    suspend fun resetForgotten(wipe: suspend () -> Unit) {
        wipe()
        prefs?.edit()?.clear()?.apply()
        if (prefs == null) {
            // The store never opened; drop the files so the next process
            // start recreates a fresh, working store (still disabled).
            context.deleteSharedPreferences(PREFS_NAME_SECURE)
        }
        _locked.value = false
    }

    fun unlockWithPin(pin: String): Boolean {
        if (!verifyThrottled(pin)) return false
        _locked.value = false
        return true
    }

    /** Called by the UI after a SUCCESSFUL BiometricPrompt authentication. */
    fun unlockWithDeviceAuth() {
        prefs?.edit()?.putInt(KEY_ATTEMPTS, 0)?.putLong(KEY_LOCKOUT_UNTIL, 0L)?.apply()
        _locked.value = false
    }

    fun lockNow() {
        if (isEnabled) _locked.value = true
    }

    /** App went to background: stamp the time. */
    fun onAppBackgrounded() {
        backgroundedAtMillis = System.currentTimeMillis()
        if (isEnabled && interval == LockInterval.Immediately) {
            _locked.value = true
        }
    }

    /** App returned: re-lock when the interval has passed. */
    fun onAppForegrounded() {
        if (!isEnabled || backgroundedAtMillis == 0L) return
        if (System.currentTimeMillis() - backgroundedAtMillis >= interval.millis) {
            _locked.value = true
        }
    }

    /** Logout: the next account must not inherit this account's lock. */
    fun clearForLogout() {
        prefs?.edit()?.clear()?.apply()
        _locked.value = false
    }

    private fun verifyThrottled(pin: String): Boolean {
        val store = prefs ?: return false // corrupted store: nothing verifies
        // Throttle check FIRST — a locked-out attempt never runs the KDF, so
        // its timing carries no information about the guess.
        if (lockoutRemainingMillis() > 0) return false
        val salt = store.getString(KEY_SALT, null)?.let { Base64.decode(it, Base64.NO_WRAP) }
        val stored = store.getString(KEY_VERIFIER, null)?.let { Base64.decode(it, Base64.NO_WRAP) }
        if (salt == null || stored == null) return false
        val version = store.getInt(KEY_KDF_VERSION, PinKdf.VERSION_PBKDF2)

        val candidate = PinKdf.derive(version, pin, salt)
        val matches = PinKdf.constantTimeEquals(candidate, stored)
        if (matches) {
            val editor = store.edit().putInt(KEY_ATTEMPTS, 0).putLong(KEY_LOCKOUT_UNTIL, 0L)
            if (version != PinKdf.VERSION_ARGON2ID) {
                // Legacy PBKDF2 verifier: upgrade in place now that the PIN
                // is in hand. Fresh salt — never reuse across schemes.
                val newSalt = ByteArray(SALT_BYTES).also { SecureRandom().nextBytes(it) }
                editor
                    .putInt(KEY_KDF_VERSION, PinKdf.VERSION_ARGON2ID)
                    .putString(KEY_SALT, Base64.encodeToString(newSalt, Base64.NO_WRAP))
                    .putString(
                        KEY_VERIFIER,
                        Base64.encodeToString(PinKdf.deriveArgon2id(pin, newSalt), Base64.NO_WRAP),
                    )
            }
            editor.apply()
        } else {
            val attempts = store.getInt(KEY_ATTEMPTS, 0) + 1
            val editor = store.edit().putInt(KEY_ATTEMPTS, attempts)
            if (attempts >= MAX_ATTEMPTS) {
                // Doubling window: 30s, 60s, 120s… capped at 30 minutes.
                val exponent = (attempts - MAX_ATTEMPTS).coerceAtMost(LOCKOUT_MAX_EXPONENT)
                val window = BASE_LOCKOUT_MILLIS shl exponent
                editor.putLong(KEY_LOCKOUT_UNTIL, System.currentTimeMillis() + window)
            }
            editor.apply()
        }
        return matches
    }

    private fun openSecurePrefs(context: Context): SharedPreferences? = try {
        val masterKey = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        val secure = EncryptedSharedPreferences.create(
            context,
            PREFS_NAME_SECURE,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
        migrateLegacyPlainPrefs(context, secure)
        secure
    } catch (@Suppress("TooGenericExceptionCaught") e: Exception) {
        // Deliberately broad: EncryptedSharedPreferences surfaces Keystore
        // corruption as several unrelated exception types, and every one of
        // them must land on the same posture — fail LOCKED, never open:
        // isEnabled reports true and nothing verifies until resetForgotten()
        // wipes and recreates.
        Log.e(TAG, "chat-lock secure store unavailable — failing locked", e)
        null
    }

    /**
     * One-time migration from the pre-correction plain SharedPreferences
     * file. Values (including a PBKDF2 verifier, upgraded on next unlock)
     * move into the encrypted store; the plain file is then cleared so no
     * verifier remains outside the Keystore boundary.
     */
    private fun migrateLegacyPlainPrefs(context: Context, secure: SharedPreferences) {
        val legacy = context.getSharedPreferences(PREFS_NAME_LEGACY, Context.MODE_PRIVATE)
        if (!legacy.contains(KEY_ENABLED) && !legacy.contains(KEY_VERIFIER)) return
        val editor = secure.edit()
        legacy.all.forEach { (key, value) ->
            when (value) {
                is Boolean -> editor.putBoolean(key, value)
                is Int -> editor.putInt(key, value)
                is Long -> editor.putLong(key, value)
                is String -> editor.putString(key, value)
            }
        }
        if (legacy.contains(KEY_VERIFIER) && !legacy.contains(KEY_KDF_VERSION)) {
            editor.putInt(KEY_KDF_VERSION, PinKdf.VERSION_PBKDF2)
        }
        editor.apply()
        legacy.edit().clear().apply()
    }

    companion object {
        private const val TAG = "ChatLockManager"
        private const val PREFS_NAME_LEGACY = "chat_lock"
        private const val PREFS_NAME_SECURE = "chat_lock_secure"
        private const val KEY_ENABLED = "enabled"
        private const val KEY_INTERVAL = "interval"
        private const val KEY_BIOMETRIC = "biometric"
        private const val KEY_SALT = "salt"
        private const val KEY_VERIFIER = "verifier"
        private const val KEY_KDF_VERSION = "kdf_version"
        private const val KEY_ATTEMPTS = "attempts"
        private const val KEY_LOCKOUT_UNTIL = "lockout_until"

        private const val SALT_BYTES = 16

        private const val MAX_ATTEMPTS = 5
        private const val BASE_LOCKOUT_MILLIS = 30_000L
        private const val LOCKOUT_MAX_EXPONENT = 6

        /**
         * P0-7: a 4-digit verifier is offline-guessable even under a
         * memory-hard KDF; 6 is the floor the review accepted alongside
         * Keystore-wrapped storage.
         */
        const val MIN_PIN_LENGTH = 6
    }
}
