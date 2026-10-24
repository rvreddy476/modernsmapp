package com.us.android.core.chat.hidden

import com.us.android.core.common.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/** Why the tokens went. */
enum class HiddenDropReason { Idle, Background, SignOut, Expired, Server, Lock, Changed }

/** What the screens may know: whether the list and the chats are open — never the tokens. */
data class HiddenTokenSnapshot(
    val revealed: Boolean = false,
    val opened: Boolean = false,
    /** Why the last drop happened, until the next token arrives. */
    val lastDrop: HiddenDropReason? = null,
)

/**
 * Hidden chats (contract 24 Oct 2026): the reveal and open tokens, IN MEMORY.
 *
 * A correct secret key returns a `reveal_token` (the hidden list); the chat
 * password returns an `open_token` (reading and sending in a hidden chat).
 * Both live only in this object: never DataStore, SharedPreferences, Room, a
 * Bundle, a saved-state handle, a URL that is logged, or a log line. Process
 * death starts locked.
 *
 * The client re-locks sooner than the server's 15-minute sliding expiry:
 *  - the app goes to the background (ProcessLifecycleOwner, [com.us.android.core.chat.hidden.HiddenChats.onAppBackgrounded]),
 *  - one minute without activity (the timer here),
 *  - sign-out,
 *  - the server refuses a token (404 on the list, 403 CHAT_LOCKED, a refused room).
 * Dropping the reveal token drops the open token with it.
 *
 * The store also remembers which conversation ids are hidden for this user
 * (learned from the hidden list, from Hide chat, from a `locked` row or a
 * 403 CHAT_LOCKED), so the normal list, badges and pickers can leave them
 * out. That set survives a re-lock — it is what keeps a hidden chat out of
 * the normal surfaces — and is cleared only on sign-out.
 */
open class HiddenTokenStore(
    private val now: () -> Long,
    /** Runs the idle timer; null in tests that drive [checkIdle] themselves. */
    private val timerScope: CoroutineScope?,
    private val idleMs: Long = HIDDEN_IDLE_MS,
) {

    private class Held(val token: String, var expiresAt: Long, val ttl: Long)

    private val lock = Any()
    private var reveal: Held? = null
    private var open: Held? = null
    private var lastActivity: Long = now()
    private var timer: Job? = null

    private val _snapshot = MutableStateFlow(HiddenTokenSnapshot())
    val snapshot: StateFlow<HiddenTokenSnapshot> = _snapshot.asStateFlow()

    private val _lockedIds = MutableStateFlow<Set<String>>(emptySet())

    /** Conversation ids hidden for this user, as far as this process knows. */
    val lockedIds: StateFlow<Set<String>> = _lockedIds.asStateFlow()

    private val relockListeners = java.util.concurrent.CopyOnWriteArrayList<(HiddenDropReason) -> Unit>()

    /**
     * Called once per RE-LOCK — a drop that ended a held reveal token —
     * synchronously, after the snapshot changed. A listener, not the
     * snapshot flow: a flow conflates, and a reveal followed fast by a drop
     * could read as "never unlocked" and skip the clean-up.
     */
    fun addRelockListener(listener: (HiddenDropReason) -> Unit) {
        relockListeners += listener
    }

    fun setReveal(token: String, expiresAt: String?) = set(token, expiresAt, isOpen = false)

    fun setOpen(token: String, expiresAt: String?) = set(token, expiresAt, isOpen = true)

    private fun set(token: String, expiresAt: String?, isOpen: Boolean) {
        val t = token.trim()
        if (t.isEmpty()) return
        synchronized(lock) {
            val at = now()
            val exp = expiryMillis(expiresAt, at)
            val held = Held(t, exp, (exp - at).coerceAtLeast(1))
            if (isOpen) open = held else reveal = held
            lastActivity = at
            publish(lastDrop = null)
        }
        arm()
    }

    /** The reveal token, or null (absent or past its expiry). */
    fun reveal(): String? = live(isOpen = false)

    /** The open token, or null (absent or past its expiry). */
    fun open(): String? = live(isOpen = true)

    private fun live(isOpen: Boolean): String? {
        val expired: Boolean
        synchronized(lock) {
            val held = (if (isOpen) open else reveal) ?: return null
            expired = now() >= held.expiresAt
            if (!expired) return held.token
        }
        drop(HiddenDropReason.Expired, openOnly = isOpen)
        return null
    }

    /** User activity: the idle clock starts again. */
    fun touch() {
        synchronized(lock) { lastActivity = now() }
    }

    /**
     * A call with this token succeeded, so the server slid its expiry: follow
     * it ([expiresAtHeader] when the response named one, else a full window).
     */
    fun slide(isOpen: Boolean, expiresAtHeader: String? = null) {
        synchronized(lock) {
            val held = (if (isOpen) open else reveal) ?: return
            val at = now()
            val next = expiresAtHeader?.takeIf { it.isNotBlank() }?.let { expiryMillis(it, at, held.ttl) } ?: (at + held.ttl)
            held.expiresAt = maxOf(held.expiresAt, next)
        }
    }

    /** Drops the open token, or both. Idempotent: dropping nothing announces nothing. */
    fun drop(reason: HiddenDropReason, openOnly: Boolean = false) {
        val relocked: Boolean
        synchronized(lock) {
            val hadAny = reveal != null || open != null
            relocked = !openOnly && hadAny
            if (openOnly) {
                if (open == null) return
                open = null
            } else {
                reveal = null
                open = null
            }
            if (reveal == null && open == null) disarm()
            if (hadAny) publish(lastDrop = reason)
        }
        if (relocked) relockListeners.forEach { listener -> runCatching { listener(reason) } }
    }

    fun markLocked(ids: Collection<String>) {
        val clean = ids.filter { it.isNotBlank() }
        if (clean.isEmpty()) return
        synchronized(lock) { _lockedIds.value = _lockedIds.value + clean }
    }

    fun unmarkLocked(id: String) {
        synchronized(lock) { _lockedIds.value = _lockedIds.value - id }
    }

    fun isLocked(id: String?): Boolean = !id.isNullOrBlank() && id in _lockedIds.value

    /** Sign-out: both tokens and the hidden-id set go. */
    fun forgetAll() {
        synchronized(lock) { _lockedIds.value = emptySet() }
        drop(HiddenDropReason.SignOut)
    }

    /** The idle rule, on demand: drops everything once [idleMs] passed without [touch]. */
    fun checkIdle(): Boolean {
        val idle = synchronized(lock) {
            (reveal != null || open != null) && now() - lastActivity >= idleMs
        }
        if (idle) drop(HiddenDropReason.Idle)
        return idle
    }

    private fun publish(lastDrop: HiddenDropReason?) {
        _snapshot.value = HiddenTokenSnapshot(revealed = reveal != null, opened = open != null, lastDrop = lastDrop)
    }

    /** One timer at a time, aimed at lastActivity + idle; it re-aims itself while activity moves on. */
    private fun arm() {
        val scope = timerScope ?: return
        synchronized(lock) {
            if (timer?.isActive == true) return
            timer = scope.launch {
                while (true) {
                    val wait = synchronized(lock) {
                        if (reveal == null && open == null) return@launch
                        (lastActivity + idleMs - now()).coerceAtLeast(0)
                    }
                    delay(wait.coerceAtLeast(MIN_TICK_MS))
                    if (checkIdle()) return@launch
                }
            }
        }
    }

    private fun disarm() {
        timer?.cancel()
        timer = null
    }

    private companion object {
        const val MIN_TICK_MS = 250L
    }
}

/** The app's one store: the clock is the wall clock, the timer runs on the application scope. */
@Singleton
class HiddenChatTokens @Inject constructor(
    @ApplicationScope scope: CoroutineScope,
) : HiddenTokenStore(now = System::currentTimeMillis, timerScope = scope)
