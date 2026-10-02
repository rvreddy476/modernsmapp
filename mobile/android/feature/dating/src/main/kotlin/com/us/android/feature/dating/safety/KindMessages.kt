package com.us.android.feature.dating.safety

import com.us.android.core.common.chat.ConversationKindness
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/*
 * Kind messages (mechanic M13), for the chat of a Pulse match.
 *
 * Chat asks through [ConversationKindness] (`:core:common`), which `:app`
 * binds to [DatingConversationKindness]; chat never learns it is Pulse that
 * answers, and this module never reaches into chat.
 *
 *  - Before a send, `POST /kind-check` on the text. `kind: false` holds the
 *    send for a gentle "this might come across as unkind" with Edit and Send
 *    anyway. Any failure — 429 KIND_CHECK_RATE_LIMITED, a 404, the network —
 *    just sends: the check never stops a message.
 *  - Each text the other person sent is checked once (the answer is kept by
 *    message id). `kind: false` covers it ("Tap to read") and asks "Did this
 *    bother you?": Yes posts `bothered: true`, and the 201's `offer_report`
 *    offers the ordinary report flow for the sender; No posts `bothered: false`
 *    and uncovers it.
 *  - `404 MECHANIC_NOT_ENABLED` from any of these switches the checks off for
 *    the rest of the session, silently.
 *
 * A conversation is a Pulse chat when it belongs to one of the caller's
 * matches ([DatingRepository.matchForConversation]): chat-service says so in
 * its conversation response (`source_app` "dating" with the `match_id`), and
 * every match Dating reads is remembered too. A conversation known neither way
 * (a chat-service build without those fields) is looked up with one read of
 * the matches, at most every [LOOKUP_INTERVAL_MS]. A Momentum account outside
 * the Dating pilot (the gateway's 404) is never asked again in this process.
 * Every other conversation is left alone.
 */
@Singleton
class DatingConversationKindness internal constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val clock: () -> Long,
) : ConversationKindness {

    @Inject
    constructor(repository: DatingRepository, session: DatingSession) : this(repository, session, System::currentTimeMillis)

    private val lookup = Mutex()
    private var lastLookupAt: Long? = null

    /** Dating is closed to this account: nothing is asked again in this process. */
    @Volatile
    private var unavailable = false

    /** Received texts already judged, by message id: true covers. Bounded, oldest first out. */
    private val judged = object : LinkedHashMap<String, Boolean>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Boolean>?): Boolean = size > MAX_JUDGED
    }

    /** Chat-service names a Pulse match's chat (`source_app` "dating", `match_id`): no lookup needed then. */
    override fun conversationSource(conversationId: String, sourceApp: String?, sourceId: String?) {
        if (sourceApp == SOURCE_APP && !sourceId.isNullOrBlank()) repository.rememberConversation(conversationId, sourceId)
    }

    override suspend fun appliesTo(conversationId: String): Boolean = matchOf(conversationId) != null

    override suspend fun mightBeUnkind(conversationId: String, text: String): Boolean {
        val trimmed = text.trim()
        // The server reads up to 2000 characters; a longer text is simply sent.
        if (trimmed.isEmpty() || trimmed.codePointCount(0, trimmed.length) > MAX_TEXT || matchOf(conversationId) == null) return false
        return unkind(trimmed) ?: false
    }

    override suspend fun shouldCover(conversationId: String, messageId: String, text: String): Boolean {
        synchronized(judged) { judged[messageId] }?.let { return it }
        val trimmed = text.trim()
        if (trimmed.isEmpty() || trimmed.codePointCount(0, trimmed.length) > MAX_TEXT || matchOf(conversationId) == null) return false
        // Only an answer is kept: a failure is not a verdict.
        val cover = unkind(trimmed) ?: return false
        synchronized(judged) { judged[messageId] = cover }
        return cover
    }

    override suspend fun bothered(conversationId: String, messageId: String, bothered: Boolean): Boolean {
        val matchId = matchOf(conversationId) ?: return false
        // "No": the viewer read it and it was fine — it stays uncovered from now on.
        if (!bothered) synchronized(judged) { judged[messageId] = false }
        return when (val result = repository.bothered(matchId, bothered)) {
            is DatingResult.Success -> result.value.offerReport
            is DatingResult.Failure -> {
                switchOffIfDisabled(result.error)
                false
            }
        }
    }

    /** Sign-out: nothing judged for one account is kept for the next. */
    fun forget() {
        synchronized(judged) { judged.clear() }
        lastLookupAt = null
        unavailable = false
    }

    /** `kind`, or null when there is no answer to act on. */
    private suspend fun unkind(text: String): Boolean? = when (val result = repository.kindCheck(text)) {
        is DatingResult.Success -> result.value.kind == false
        is DatingResult.Failure -> {
            switchOffIfDisabled(result.error)
            null
        }
    }

    /** The match [conversationId] belongs to, or null for every conversation that is not a Pulse chat. */
    private suspend fun matchOf(conversationId: String): String? {
        if (conversationId.isBlank() || unavailable || session.isMechanicDisabled(MECHANIC)) return null
        repository.matchForConversation(conversationId)?.let { return it }
        return lookup.withLock {
            repository.matchForConversation(conversationId)?.let { return@withLock it }
            val now = clock()
            val last = lastLookupAt
            if (last != null && now - last < LOOKUP_INTERVAL_MS) return@withLock null
            lastLookupAt = now
            val read = repository.matches()
            if (read is DatingResult.Failure && read.error == DatingError.NotAvailable) unavailable = true
            repository.matchForConversation(conversationId)
        }
    }

    private fun switchOffIfDisabled(error: DatingError) {
        if (error.code == CODE_MECHANIC_NOT_ENABLED) session.disableMechanic(MECHANIC)
    }

    companion object {
        /** The [DatingSession.disableMechanic] key for kind messages. */
        const val MECHANIC = "kind_check"

        /** chat-service's `source_app` for a Pulse match's chat. */
        const val SOURCE_APP = "dating"
        const val MAX_TEXT = 2000
        const val LOOKUP_INTERVAL_MS = 30_000L
        private const val MAX_JUDGED = 500
        private const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
    }
}
