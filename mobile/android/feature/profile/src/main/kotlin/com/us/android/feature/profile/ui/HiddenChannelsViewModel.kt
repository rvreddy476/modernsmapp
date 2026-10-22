package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.feed.data.HiddenAuthorsSource
import com.us.android.core.feed.data.HiddenChannel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Everything the Hidden channels page draws. */
data class HiddenChannelsUiState(
    val loading: Boolean = true,
    val error: String? = null,
    val refreshing: Boolean = false,
    val channels: List<HiddenChannel> = emptyList(),
) {
    val isEmpty: Boolean get() = !loading && error == null && channels.isEmpty()
}

/**
 * Settings › Hidden channels (web parity, 2026-10-22, `/settings/hidden-
 * channels`): the channels the viewer asked not to be recommended ("Don't
 * recommend this channel" on a reel or a video), each with "Show again".
 * The row goes at once and comes back where it was if the server refuses.
 */
@HiltViewModel
class HiddenChannelsViewModel @Inject constructor(
    private val source: HiddenAuthorsSource,
) : ViewModel() {

    private val _state = MutableStateFlow(HiddenChannelsUiState())
    val state: StateFlow<HiddenChannelsUiState> = _state.asStateFlow()

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val inFlight = mutableSetOf<String>()

    init {
        refresh()
    }

    fun refresh() {
        val hadRows = _state.value.channels.isNotEmpty()
        _state.update { it.copy(refreshing = hadRows, loading = !hadRows, error = null) }
        viewModelScope.launch {
            when (val result = source.hidden()) {
                is AppResult.Success -> _state.update {
                    it.copy(loading = false, refreshing = false, channels = result.data)
                }
                is AppResult.Failure -> if (hadRows) {
                    _state.update { it.copy(loading = false, refreshing = false) }
                    _message.value = UsMessage(errorText(result.error, "We couldn't refresh this list."))
                } else {
                    _state.update {
                        it.copy(loading = false, refreshing = false, error = errorText(result.error, LOAD_FAILED))
                    }
                }
            }
        }
    }

    /** "Show again": the channel's reels and videos return to the feeds. */
    fun showAgain(channel: HiddenChannel) {
        if (!inFlight.add(channel.authorId)) return
        val index = _state.value.channels.indexOfFirst { it.authorId == channel.authorId }
        if (index < 0) {
            inFlight.remove(channel.authorId)
            return
        }
        _state.update { s -> s.copy(channels = s.channels.filterNot { it.authorId == channel.authorId }) }
        viewModelScope.launch {
            val result = source.showAgain(channel.authorId)
            inFlight.remove(channel.authorId)
            when (result) {
                is AppResult.Success ->
                    _message.value = UsMessage("${channel.label()} will be recommended again", UsMessageType.Success)
                is AppResult.Failure -> {
                    _state.update { s ->
                        if (s.channels.any { it.authorId == channel.authorId }) {
                            s
                        } else {
                            s.copy(channels = s.channels.toMutableList().apply { add(index.coerceIn(0, size), channel) })
                        }
                    }
                    _message.value = UsMessage(errorText(result.error, "Could not update that preference. Try again."))
                }
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    private fun errorText(error: AppError, fallback: String): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        else -> fallback
    }

    private companion object {
        const val LOAD_FAILED = "Couldn't load this list. Try again in a moment."
    }
}

/** The web's fallback when the profile could not be read: "Channel". */
internal fun HiddenChannel.label(): String =
    name.ifBlank { username.takeIf { it.isNotBlank() }?.let { "@$it" } ?: "Channel" }
