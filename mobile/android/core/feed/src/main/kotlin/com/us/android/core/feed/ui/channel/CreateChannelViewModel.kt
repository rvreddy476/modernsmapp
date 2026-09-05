package com.us.android.core.feed.ui.channel

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.ChannelAbout
import com.us.android.core.feed.data.ChannelCreateError
import com.us.android.core.feed.data.ChannelHandle
import com.us.android.core.feed.data.ChannelName
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.model.Channel
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the handle field says under itself, from the live check. */
sealed interface HandleCheck {
    /** Nothing typed yet, or the client rules already refused it. */
    data object Idle : HandleCheck
    data object Checking : HandleCheck
    data object Available : HandleCheck

    /** Taken; [suggestion] is the server's alternative, tappable. */
    data class Taken(val suggestion: String?) : HandleCheck

    /** The check could not be made; the create will be the check. */
    data object Unreachable : HandleCheck
}

data class CreateChannelUiState(
    val name: String = "",
    val handle: String = "",
    val about: String = "",
    /** The profile photo, as the channel's avatar preview. */
    val avatarUrl: String? = null,
    val avatarName: String = "You",
    val avatarSeed: String = "",
    val check: HandleCheck = HandleCheck.Idle,
    val submitting: Boolean = false,
    /** The server's refusal, pointed at a field when it names one. */
    val nameError: String? = null,
    val handleError: String? = null,
    val aboutError: String? = null,
    val error: String? = null,
    /** Set once; the sheet closes on it. */
    val created: Channel? = null,
    /** True until the profile prefill answers, so the fields do not flash empty then filled. */
    val prefilling: Boolean = true,
) {
    val nameProblem: String? get() = ChannelName.problem(name)
    val handleProblem: String? get() = ChannelHandle.problem(handle)
    val canSubmit: Boolean
        get() = !submitting && nameProblem == null && handleProblem == null &&
            ChannelAbout.problem(about) == null && check !is HandleCheck.Taken
}

/**
 * The "Create your channel" sheet's state (Tube, 2026-09-05): the name with
 * its counter, the handle prefilled from a suggestion and checked live —
 * 400 ms after the last keystroke — the optional About, and the create.
 * The server is the authority on every rule; the client's checks only
 * stop a request that would certainly fail.
 */
@HiltViewModel
class CreateChannelViewModel @Inject constructor(
    private val channels: ChannelRepository,
    private val profiles: ProfileRepository,
    private val media: MediaRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(CreateChannelUiState())
    val state: StateFlow<CreateChannelUiState> = _state.asStateFlow()

    private var checkJob: Job? = null

    init {
        viewModelScope.launch { prefill() }
    }

    private suspend fun prefill() {
        val own = (profiles.getOwnProfile() as? AppResult.Success)?.data
        val name = own?.displayName?.trim().orEmpty().ifBlank { own?.username.orEmpty() }
        val suggestion = ChannelHandle.suggest(own?.username, name)
        val avatar = own?.avatarMediaId?.takeIf { it.isNotBlank() }?.let { id ->
            (media.delivery(id) as? AppResult.Success)?.data?.takeIf { it.isReady }?.posterUrl
        }
        _state.update {
            it.copy(
                name = if (it.name.isBlank()) name.take(ChannelName.MAX_LENGTH) else it.name,
                handle = if (it.handle.isBlank()) suggestion else it.handle,
                avatarUrl = avatar,
                avatarName = name.ifBlank { "You" },
                avatarSeed = own?.userId.orEmpty(),
                prefilling = false,
            )
        }
        if (_state.value.handle.isNotBlank()) scheduleCheck(immediate = true)
    }

    fun onNameChanged(value: String) = _state.update {
        it.copy(name = value.take(ChannelName.MAX_LENGTH), nameError = null, error = null)
    }

    /** Every keystroke is normalized to the rules, so the field only ever shows a legal handle. */
    fun onHandleChanged(value: String) {
        val handle = ChannelHandle.normalize(value)
        _state.update { it.copy(handle = handle, handleError = null, error = null, check = HandleCheck.Idle) }
        scheduleCheck(immediate = false)
    }

    fun onAboutChanged(value: String) = _state.update {
        it.copy(about = value.take(ChannelAbout.MAX_LENGTH), aboutError = null, error = null)
    }

    /** The server's suggestion, taken. */
    fun useSuggestion() {
        val suggestion = (_state.value.check as? HandleCheck.Taken)?.suggestion ?: return
        onHandleChanged(suggestion)
    }

    private fun scheduleCheck(immediate: Boolean) {
        checkJob?.cancel()
        val handle = _state.value.handle
        if (!ChannelHandle.isValid(handle)) return
        checkJob = viewModelScope.launch {
            if (!immediate) delay(CHECK_DEBOUNCE_MILLIS)
            _state.update { it.copy(check = HandleCheck.Checking) }
            val answer = channels.handleAvailable(handle)
            _state.update { current ->
                // A keystroke since this check started makes its answer stale.
                if (current.handle != handle) return@update current
                current.copy(
                    check = when {
                        answer == null -> HandleCheck.Unreachable
                        answer.available -> HandleCheck.Available
                        else -> HandleCheck.Taken(answer.suggestion)
                    },
                )
            }
        }
    }

    fun create() {
        val current = _state.value
        if (!current.canSubmit) return
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            when (val result = channels.create(current.name, current.handle, current.about)) {
                is AppResult.Success -> _state.update { it.copy(submitting = false, created = result.data) }
                is AppResult.Failure -> _state.update { it.refused(ChannelRepository.createError(result.error)) }
            }
        }
    }

    private fun CreateChannelUiState.refused(error: ChannelCreateError): CreateChannelUiState = when (error) {
        ChannelCreateError.HandleTaken ->
            copy(submitting = false, handleError = "That handle is taken.", check = HandleCheck.Taken(null))
        ChannelCreateError.ChannelExists ->
            copy(submitting = false, error = "You already have a channel.")
        is ChannelCreateError.InvalidName -> copy(submitting = false, nameError = error.message)
        is ChannelCreateError.InvalidHandle -> copy(submitting = false, handleError = error.message)
        is ChannelCreateError.InvalidAbout -> copy(submitting = false, aboutError = error.message)
        is ChannelCreateError.Other -> copy(submitting = false, error = error.message)
    }

    private companion object {
        const val CHECK_DEBOUNCE_MILLIS = 400L
    }
}
