package com.us.android.core.chat.hidden

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.toDomain
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.model.SessionState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject
import javax.inject.Provider
import javax.inject.Singleton

/**
 * The conversations a NORMAL surface may show: never a hidden one, whether
 * the row itself says so (`locked`) or this process learned it.
 */
fun List<Conversation>.withoutHiddenChats(hiddenIds: Set<String>): List<Conversation> =
    filterNot { it.locked || it.id in hiddenIds }

/** The identity step-up's answer. */
sealed interface StepUpOutcome {
    data class Ok(val token: String) : StepUpOutcome
    data object WrongCode : StepUpOutcome
    data object Replayed : StepUpOutcome
    data object NoTwoFactor : StepUpOutcome
    data class RateLimited(val retryAfterSec: Long?) : StepUpOutcome
    data object Failed : StepUpOutcome
}

/** Maps a refused `POST /v1/auth/step-up` (identity's codes: INVALID_OTP, OTP_REPLAYED, MFA_NOT_ENROLLED, RATE_LIMITED). */
@Suppress("MagicNumber")
fun stepUpOutcomeOf(status: Int, body: String?, retryAfterHeader: String?, nowMs: Long): StepUpOutcome {
    val code = errorCodeOf(body)
    return when {
        status == 429 -> StepUpOutcome.RateLimited(
            (hiddenErrorOf(status, body, retryAfterHeader, nowMs) as? HiddenError.Locked)?.retryAfterSec,
        )
        code == "MFA_NOT_ENROLLED" -> StepUpOutcome.NoTwoFactor
        code == "OTP_REPLAYED" -> StepUpOutcome.Replayed
        status == 401 || status == 400 -> StepUpOutcome.WrongCode
        else -> StepUpOutcome.Failed
    }
}

/**
 * Hidden chats for the app (contract pinned 24 Oct 2026; server 0d5e09cfb).
 *
 * Owns the calls and the in-memory hidden list; the tokens themselves are
 * [HiddenChatTokens]'. Every re-lock — background, a minute idle, the server
 * refusing a token, a secret changed — empties the list, tells the server
 * (`POST /hidden/lock`) and purges whatever of the hidden chats this device
 * cached while they were open.
 */
@Singleton
@Suppress("TooManyFunctions") // One function per contract route, plus the re-lock rules.
class HiddenChats @Inject constructor(
    private val api: HiddenChatsApi,
    val tokens: HiddenChatTokens,
    private val vault: HiddenUnlockVault,
    // Providers: ChatStore and the session are both heavier than this class
    // and only needed once something happens.
    private val store: Provider<ChatStore>,
    private val session: Provider<SessionStateProvider>,
    @ApplicationScope private val scope: CoroutineScope,
) {

    private val _settings = MutableStateFlow<HiddenSettings?>(null)

    /** The account's settings; null until read (or after sign-out). */
    val settings: StateFlow<HiddenSettings?> = _settings.asStateFlow()

    private val _rows = MutableStateFlow<List<Conversation>?>(null)

    /** The hidden list while revealed; null when locked. Memory only. */
    val rows: StateFlow<List<Conversation>?> = _rows.asStateFlow()

    /** The last key the search box tried, so the same text is never sent twice in a row. */
    @Volatile private var lastSearchTry: String? = null

    init {
        tokens.addRelockListener(::onRelocked)
    }

    val viewerId: String
        get() = (session.get().sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    // ── Settings ────────────────────────────────────────────────────────

    suspend fun loadSettings(): HiddenResult<HiddenSettings> =
        when (val r = hiddenCall { api.settings() }) {
            is HiddenResult.Ok -> HiddenResult.Ok((r.value ?: HiddenSettingsDto()).toSettings()).also { _settings.value = it.value }
            is HiddenResult.Err -> r
        }

    /** First set: both secrets, no current password. */
    suspend fun setUp(secretKey: String, chatPassword: String, strict: Boolean? = null): HiddenResult<HiddenSettings> =
        saveSettings(settingsBody(secretKey = secretKey, chatPassword = chatPassword, strict = strict))

    /**
     * Change either secret (or strict mode) with the current chat password.
     * A changed secret logs every device's hidden tokens out server-side, so
     * this device re-locks too; a changed password also drops the sealed copy.
     */
    suspend fun change(
        currentChatPassword: String,
        newSecretKey: String? = null,
        newChatPassword: String? = null,
        strict: Boolean? = null,
    ): HiddenResult<HiddenSettings> {
        val result = saveSettings(
            settingsBody(
                secretKey = newSecretKey,
                chatPassword = newChatPassword,
                currentChatPassword = currentChatPassword,
                strict = strict,
            ),
        )
        if (result is HiddenResult.Ok) {
            if (!newChatPassword.isNullOrEmpty()) vault.forget(viewerId)
            if (!newSecretKey.isNullOrEmpty() || !newChatPassword.isNullOrEmpty()) tokens.drop(HiddenDropReason.Changed)
        }
        return result
    }

    private suspend fun saveSettings(body: kotlinx.serialization.json.JsonObject): HiddenResult<HiddenSettings> =
        when (val r = hiddenCall { api.updateSettings(body) }) {
            is HiddenResult.Ok -> {
                val saved = r.value?.toSettings() ?: HiddenSettings(configured = true, strict = _settings.value?.strict == true)
                _settings.value = saved
                HiddenResult.Ok(saved)
            }
            is HiddenResult.Err -> r
        }

    /** identity's in-session step-up with a 2-step code; the token is the caller's, for this one reset. */
    suspend fun stepUp(otp: String): StepUpOutcome = try {
        val response = api.stepUp(UUID.randomUUID().toString(), stepUpBody(otp))
        if (response.isSuccessful) {
            response.body()?.data?.accessToken?.trim()?.takeIf { it.isNotEmpty() }
                ?.let { StepUpOutcome.Ok(it) } ?: StepUpOutcome.Failed
        } else {
            stepUpOutcomeOf(
                response.code(),
                runCatching { response.errorBody()?.string() }.getOrNull(),
                response.headers()["Retry-After"],
                System.currentTimeMillis(),
            )
        }
    } catch (e: kotlinx.coroutines.CancellationException) {
        throw e
    } catch (@Suppress("TooGenericExceptionCaught") e: Exception) {
        StepUpOutcome.Failed
    }

    /**
     * Forgot: new secrets after re-auth. [stepUpToken] null means the bearer
     * itself is a fresh sign-in (≤ 5 minutes). Whatever was unlocked belonged
     * to the old secrets; in strict mode the hidden chats are gone for me.
     */
    suspend fun reset(secretKey: String, chatPassword: String, stepUpToken: String?, strict: Boolean? = null): HiddenResult<Unit> =
        when (val r = hiddenCall { api.reset(stepUpToken?.takeIf { it.isNotBlank() }, resetBody(secretKey, chatPassword, strict)) }) {
            is HiddenResult.Ok -> {
                vault.forget(viewerId)
                tokens.drop(HiddenDropReason.Changed)
                // The old secrets' chats (deleted outright in strict mode):
                // nothing queued for them may wait on this device.
                store.get().dropHiddenOutbox()
                _settings.value = HiddenSettings(configured = true, strict = strict ?: (_settings.value?.strict == true))
                HiddenResult.Ok(Unit)
            }
            is HiddenResult.Err -> r
        }

    // ── Reveal and the hidden list ───────────────────────────────────────

    /**
     * Text SUBMITTED from chat search. Tried as a key only when this account
     * has hidden chats, only when it is key-shaped, and never the same text
     * twice in a row — every wrong key counts toward the lockout. Any refusal
     * (a wrong key, a lockout, offline) answers false and the search carries
     * on as an ordinary search: nothing on screen hints that hidden chats
     * exist. True when the list is now revealed.
     */
    suspend fun revealFromSearch(text: String): Boolean {
        val key = text.trim()
        if (_settings.value?.configured != true || !couldBeSecretKey(key)) return false
        if (lastSearchTry == key) return false
        lastSearchTry = key
        val r = hiddenCall { api.reveal(revealBody(key)) }
        val token = (r as? HiddenResult.Ok)?.value?.revealToken?.trim()?.takeIf { it.isNotEmpty() } ?: return false
        lastSearchTry = null
        tokens.setReveal(token, r.value?.expiresAt)
        refreshList()
        return true
    }

    /** The hidden list, whole. A refused reveal token re-locks. */
    suspend fun refreshList(): HiddenResult<List<Conversation>> {
        val all = mutableListOf<ConversationDto>()
        var cursor: String? = null
        for (page in 0 until MAX_LIST_PAGES) {
            val reveal = tokens.reveal() ?: return HiddenResult.Err(HiddenError.NotFound).also { _rows.value = null }
            when (val r = hiddenCall { api.conversations(reveal, cursor = cursor) }) {
                is HiddenResult.Err -> {
                    if (r.error == HiddenError.NotFound) tokens.drop(HiddenDropReason.Server)
                    return r
                }
                is HiddenResult.Ok -> {
                    tokens.slide(isOpen = false, expiresAtHeader = r.headers[REVEAL_EXPIRES_HEADER])
                    all += r.value.orEmpty()
                    cursor = r.nextCursor
                }
            }
            if (cursor == null) break
        }
        val rows = all.filter { it.id.isNotBlank() }.map { it.toDomain().copy(locked = true) }
        tokens.markLocked(rows.map { it.id })
        // A row queued in plain text for one of these (before this build, or
        // before this process knew) is sealed as soon as the id is known.
        store.get().protectHiddenOutbox()
        _rows.value = rows
        return HiddenResult.Ok(rows)
    }

    // ── Open ─────────────────────────────────────────────────────────────

    /** The chat password: opens every hidden chat for this unlock session. */
    suspend fun open(chatPassword: String): HiddenResult<Unit> {
        val reveal = tokens.reveal() ?: return HiddenResult.Err(HiddenError.NotFound)
        return when (val r = hiddenCall { api.open(reveal, openBody(chatPassword)) }) {
            is HiddenResult.Ok -> {
                val token = r.value?.openToken?.trim().orEmpty()
                if (token.isEmpty()) {
                    HiddenResult.Err(HiddenError.Unknown(status = 200, code = null))
                } else {
                    tokens.setOpen(token, r.value?.expiresAt)
                    // What waited while the chats were locked goes now.
                    store.get().resumeHiddenSends()
                    HiddenResult.Ok(Unit)
                }
            }
            is HiddenResult.Err -> {
                // The reveal token is gone server-side: the list re-locks too.
                if (r.error == HiddenError.NotFound) tokens.drop(HiddenDropReason.Server)
                r
            }
        }
    }

    /**
     * Opened with the password unsealed after a biometric. A refusal saying
     * the password is wrong means the sealed copy is stale (changed on
     * another device): forget it, the person types it once more.
     */
    suspend fun openWithSealedPassword(password: String): HiddenResult<Unit> {
        val result = open(password)
        if (result is HiddenResult.Err && result.error is HiddenError.WrongPassword) vault.forget(viewerId)
        return result
    }

    val unlockVault: HiddenUnlockVault get() = vault

    // ── Hide / unhide ────────────────────────────────────────────────────

    /** Hide a chat for me. [HiddenError.NotConfigured] means: set the secrets up first. */
    suspend fun hide(conversationId: String): HiddenResult<Unit> =
        when (val r = hiddenCall { api.hide(conversationId) }) {
            is HiddenResult.Ok -> {
                tokens.markLocked(listOf(conversationId))
                store.get().purgeHiddenLocally(listOf(conversationId))
                if (_rows.value != null) refreshList()
                HiddenResult.Ok(Unit)
            }
            is HiddenResult.Err -> {
                if (r.error == HiddenError.NotConfigured) _settings.value = HiddenSettings(configured = false, strict = false)
                r
            }
        }

    /** Unhide from inside the open hidden chat: it returns to the normal list. */
    suspend fun unhide(conversationId: String): HiddenResult<Unit> {
        val open = tokens.open() ?: return HiddenResult.Err(HiddenError.ChatLocked)
        return when (val r = hiddenCall { api.unhide(conversationId, open) }) {
            is HiddenResult.Ok -> {
                tokens.unmarkLocked(conversationId)
                _rows.value = _rows.value?.filterNot { it.id == conversationId }
                // Its sealed sends need the open token, which is held now.
                store.get().resumeHiddenSends()
                scope.launch { store.get().syncInbox() }
                HiddenResult.Ok(Unit)
            }
            is HiddenResult.Err -> r
        }
    }

    // ── Re-lock ──────────────────────────────────────────────────────────

    /** User activity on a hidden surface: the 1-minute idle clock starts again. */
    fun touch() = tokens.touch()

    /** "Lock" now (the list's lock button, leaving the hidden surfaces). */
    fun lockNow() = tokens.drop(HiddenDropReason.Lock)

    /** ProcessLifecycleOwner onStop: hidden chats never survive the app leaving the screen. */
    fun onAppBackgrounded() = tokens.drop(HiddenDropReason.Background)

    /** Sign-out: tokens, the hidden-id set, the list, the settings and every sealed password go. */
    fun forgetForSignOut() {
        tokens.forgetAll()
        _rows.value = null
        _settings.value = null
        lastSearchTry = null
        vault.forget(null)
    }

    private fun onRelocked(reason: HiddenDropReason) {
        _rows.value = null
        if (reason == HiddenDropReason.SignOut) return
        val hidden = tokens.lockedIds.value.toList()
        scope.launch {
            // Best effort: the server's own expiry ends the tokens anyway.
            runCatching { api.lock() }
            if (hidden.isNotEmpty()) store.get().purgeHiddenLocally(hidden)
        }
    }

    private companion object {
        /** A bound, not a target: ten pages of 100 hidden chats. */
        const val MAX_LIST_PAGES = 10
    }
}
