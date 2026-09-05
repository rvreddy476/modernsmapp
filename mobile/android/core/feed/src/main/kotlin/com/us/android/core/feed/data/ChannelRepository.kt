package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.Channel
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What the client knows about the viewer's OWN channel. Three answers and a
 * "not yet": the server has one, the server said there is none (a 404
 * `NO_CHANNEL` — the ordinary case for a new account, not an error), the
 * lookup failed for some other reason, or nobody has asked.
 */
sealed interface ChannelState {
    data object Unknown : ChannelState
    data object None : ChannelState
    data class Present(val channel: Channel) : ChannelState
    data class Failed(val message: String) : ChannelState

    val channelOrNull: Channel? get() = (this as? Present)?.channel
}

/** Why a create was refused, by the server's code — the form points at the field. */
sealed interface ChannelCreateError {
    data object HandleTaken : ChannelCreateError
    data object ChannelExists : ChannelCreateError
    data class InvalidName(val message: String) : ChannelCreateError
    data class InvalidHandle(val message: String) : ChannelCreateError
    data class InvalidAbout(val message: String) : ChannelCreateError
    data class Other(val message: String) : ChannelCreateError
}

/**
 * The viewer's channel, cached for the process (Tube, 2026-09-05).
 *
 * One read per process unless something changes it: the Create → Video
 * gate, the You page, the channels strip's "You" bubble and the publish
 * pipeline's `CHANNEL_REQUIRED` recovery all ask the same question, and a
 * network call per asker would be the N+1 the FollowGraph already solved
 * for follows. A create or an edit writes the answer back here, so every
 * reader sees it at once.
 */
@Singleton
class ChannelRepository @Inject constructor(
    private val api: ChannelApi,
    private val errorMapper: ErrorMapper,
) {
    private val _own = MutableStateFlow<ChannelState>(ChannelState.Unknown)

    /** The viewer's own channel as last known. [ensureLoaded] fills it in. */
    val own: StateFlow<ChannelState> = _own.asStateFlow()

    private val loading = Mutex()

    /** Reads the channel once; later callers get the cached answer. A failure is retried on the next call. */
    suspend fun ensureLoaded(): ChannelState {
        val known = _own.value
        if (known is ChannelState.Present || known is ChannelState.None) return known
        return refresh()
    }

    /** Asks the server again whatever is cached — pull-to-refresh, or after a failure. */
    suspend fun refresh(): ChannelState = loading.withLock {
        val state = when (val result = apiCall(errorMapper) { api.me() }) {
            is AppResult.Success -> ChannelState.Present(result.data.toDomain())
            is AppResult.Failure -> when (result.error) {
                // 404 NO_CHANNEL: a real answer, not a failure.
                is AppError.NotFound -> ChannelState.None
                else -> ChannelState.Failed(loadMessage(result.error))
            }
        }
        _own.value = state
        state
    }

    /** Creates the viewer's channel; on success it is the cached channel from then on. */
    suspend fun create(name: String, handle: String, about: String): AppResult<Channel> {
        val request = CreateChannelRequest(
            name = name.trim(),
            handle = ChannelHandle.normalize(handle),
            about = about.trim().ifBlank { null },
        )
        return apiCall(errorMapper) { api.create(request) }
            .map { it.toDomain() }
            .also { result -> if (result is AppResult.Success) _own.value = ChannelState.Present(result.data) }
    }

    /** Edits the viewer's channel; blank fields are left as they are. */
    suspend fun update(name: String?, handle: String?, about: String?): AppResult<Channel> {
        val request = UpdateChannelRequest(
            name = name?.trim()?.takeIf { it.isNotBlank() },
            handle = handle?.let(ChannelHandle::normalize)?.takeIf { it.isNotBlank() },
            about = about?.trim(),
        )
        return apiCall(errorMapper) { api.update(request) }
            .map { it.toDomain() }
            .also { result -> if (result is AppResult.Success) _own.value = ChannelState.Present(result.data) }
    }

    /** Someone's channel by handle or user id. A 404 is "no channel", as [AppError.NotFound]. */
    suspend fun channel(key: String): AppResult<Channel> =
        apiCall(errorMapper) { api.get(key.removePrefix("@")) }.map { it.toDomain() }

    /**
     * Whether [handle] is free, and the server's alternative when it is
     * not. Null when the check could not be made — the form then lets the
     * create itself be the check rather than blocking on a blip.
     */
    suspend fun handleAvailable(handle: String): HandleAvailability? =
        when (val result = apiCall(errorMapper) { api.handleAvailable(ChannelHandle.normalize(handle)) }) {
            is AppResult.Success -> HandleAvailability(
                available = result.data.available,
                suggestion = result.data.suggestion.takeIf { it.isNotBlank() },
            )
            is AppResult.Failure -> null
        }

    private fun loadMessage(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        else -> "We couldn't check your channel."
    }

    private fun ChannelDto.toDomain() = Channel(
        userId = userId,
        name = name,
        handle = handle,
        about = about,
        avatarMediaId = avatarMediaId?.takeIf { it.isNotBlank() },
        avatarUrl = avatarUrl?.takeIf { it.isNotBlank() },
        videoCount = videoCount,
        createdAt = createdAt,
        updatedAt = updatedAt,
    )

    companion object {
        const val CODE_CHANNEL_EXISTS = "CHANNEL_EXISTS"
        const val CODE_HANDLE_TAKEN = "HANDLE_TAKEN"
        const val CODE_INVALID_NAME = "INVALID_NAME"
        const val CODE_INVALID_HANDLE = "INVALID_HANDLE"
        const val CODE_INVALID_ABOUT = "INVALID_ABOUT"

        /** The post-service refusal of a long video from a user without a channel. */
        const val CODE_CHANNEL_REQUIRED = "CHANNEL_REQUIRED"

        /**
         * The server's refusal of a create, as the field it points at. Codes
         * only, never messages (the app's rule): a 409 and a 400 both arrive
         * as [AppError.Unknown] carrying the code, since neither is one of the
         * platform-wide codes the mapper types.
         */
        fun createError(error: AppError): ChannelCreateError = when (error.contractCode()) {
            CODE_HANDLE_TAKEN -> ChannelCreateError.HandleTaken
            CODE_CHANNEL_EXISTS -> ChannelCreateError.ChannelExists
            CODE_INVALID_NAME -> ChannelCreateError.InvalidName("That name isn't allowed.")
            CODE_INVALID_HANDLE -> ChannelCreateError.InvalidHandle("That handle isn't allowed.")
            CODE_INVALID_ABOUT -> ChannelCreateError.InvalidAbout("That description isn't allowed.")
            else -> ChannelCreateError.Other(otherMessage(error))
        }

        /** The server's contract code, wherever the mapper put it. */
        private fun AppError.contractCode(): String? = when (this) {
            is AppError.Unknown -> code
            is AppError.Server -> code
            is AppError.Forbidden -> code
            else -> null
        }

        private fun otherMessage(error: AppError): String = when (error) {
            is AppError.NoNetwork -> "You're offline. Check your connection and try again."
            is AppError.Timeout -> "That took too long. Try again."
            is AppError.InvalidRequest -> error.message.ifBlank { "Check the fields and try again." }
            else -> "We couldn't create your channel. Try again."
        }

        /** Whether a post refusal means "make a channel first". */
        fun requiresChannel(error: AppError): Boolean =
            error is AppError.Forbidden && error.code == CODE_CHANNEL_REQUIRED
    }
}

data class HandleAvailability(val available: Boolean, val suggestion: String?)
