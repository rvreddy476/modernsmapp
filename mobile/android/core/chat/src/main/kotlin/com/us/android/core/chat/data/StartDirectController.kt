package com.us.android.core.chat.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult

/** What a "start chat" attempt produced. */
sealed interface StartDirectResult {
    /** The conversation to open. Existing or newly created — the server decides. */
    data class Opened(val conversation: Conversation) : StartDirectResult

    /**
     * The server refused on policy grounds.
     *
     * Distinct from [Failed] because it is NOT retryable: the answer will be
     * the same until the target changes their privacy setting or the
     * relationship changes. A UI that offers "try again" here is offering a
     * button that cannot work.
     */
    data class NotAllowed(val error: AppError) : StartDirectResult

    /** Something transient. Retrying is reasonable and reuses the same key. */
    data class Failed(val error: AppError) : StartDirectResult
}

/**
 * Opens a direct conversation with one person.
 *
 * ## ONE INTENT, ONE KEY
 *
 * The key is minted once per target and reused for every retry, exactly as
 * [ThreadController] does for sending. Tapping "Message" is one intent no
 * matter how many times the network drops it; minting a key per call would
 * make a lost response create a second conversation with the same person, and
 * the user's history would silently split across two threads with no way to
 * merge them.
 *
 * The key is cleared only on success, so:
 *
 *  - retry after a timeout replays the original creation and returns the SAME
 *    conversation;
 *  - starting a chat with a DIFFERENT person is a different intent and gets a
 *    different key.
 *
 * ## THE CLIENT DOES NOT DECIDE ELIGIBILITY
 *
 * There is no local check for "can I message this person". graph-service owns
 * that decision, re-evaluates it on every attempt, and a client-side copy would
 * be a second policy implementation that drifts — and one that is wrong in the
 * dangerous direction the moment someone tightens their settings.
 *
 * Plain class, not a ViewModel: the same behaviour is wanted from a profile
 * screen, a search result and a group member row.
 */
class StartDirectController(private val repository: ChatRepository) {

    private var pendingKey: String? = null
    private var pendingUserId: String? = null

    /** True while a request is in flight, so a surface can disable its button. */
    var inFlight: Boolean = false
        private set

    suspend fun open(otherUserId: String): StartDirectResult {
        val key = pendingKey?.takeIf { pendingUserId == otherUserId }
            ?: ChatRepository.newIdempotencyKey().also {
                pendingKey = it
                pendingUserId = otherUserId
            }

        inFlight = true
        val result = try {
            repository.createDirect(otherUserId, key)
        } finally {
            inFlight = false
        }

        return when (result) {
            is AppResult.Success -> {
                // Only a success retires the intent. Clearing it on failure is
                // what would turn a retry into a second conversation.
                pendingKey = null
                pendingUserId = null
                StartDirectResult.Opened(result.data)
            }

            is AppResult.Failure ->
                if (result.error.isMessagingNotAllowed()) {
                    StartDirectResult.NotAllowed(result.error)
                } else {
                    StartDirectResult.Failed(result.error)
                }
        }
    }

    /**
     * Recognises the policy denial.
     *
     * Matched on the server's error CODE, not its message — the rule
     * [AppError] states in its own KDoc. The message is written for a human
     * and will be reworded; `MESSAGING_NOT_ALLOWED` is the contract
     * (message-service returns it for `ErrMessagingNotAllowed`). Matching on
     * prose is how a copy edit turns a clear refusal into an endless retry
     * loop.
     */
    private fun AppError.isMessagingNotAllowed(): Boolean =
        this is AppError.Forbidden && code.equals(MESSAGING_NOT_ALLOWED, ignoreCase = true)

    private companion object {
        const val MESSAGING_NOT_ALLOWED = "MESSAGING_NOT_ALLOWED"
    }
}
