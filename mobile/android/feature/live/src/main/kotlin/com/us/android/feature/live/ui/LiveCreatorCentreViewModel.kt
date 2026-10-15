package com.us.android.feature.live.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.listApiCall
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveFollows
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.LiveUserStreams
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The creator's own LIVE centre (T-30, 2026-10-15): READ-ONLY lists of
 * their upcoming streams, past streams and recordings, from
 * `GET /v1/livestream/users/:me/streams?status=upcoming|past`. Recordings
 * are the past rows that carry a recording (`recording_post_id` or
 * `recording_url`). Nothing here edits, schedules or deletes.
 *
 * Streaming-software (RTMP) details are deliberately NOT here: the only
 * route that returns them is `POST …/ingress`, which issues the stream key
 * for an encoder stream — a write, not a read — and Android's go-live
 * creates camera streams. The key is a credential and never shown or logged.
 */
@HiltViewModel
class LiveCreatorCentreViewModel @Inject constructor(
    private val api: LiveApi,
    private val errorMapper: ErrorMapper,
    private val follows: LiveFollows,
) : ViewModel() {

    enum class Tab(val label: String) {
        Upcoming("Upcoming"),
        Past("Past"),
        Recordings("Recordings"),
    }

    data class UiState(
        val loading: Boolean = true,
        val tab: Tab = Tab.Upcoming,
        val upcoming: List<LiveStreamDto> = emptyList(),
        val past: List<LiveStreamDto> = emptyList(),
        val error: String? = null,
    ) {
        val recordings: List<LiveStreamDto> get() = past.filter(::hasRecording)

        val rows: List<LiveStreamDto>
            get() = when (tab) {
                Tab.Upcoming -> upcoming
                Tab.Past -> past
                Tab.Recordings -> recordings
            }
    }

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    private var loadJob: Job? = null

    init {
        refresh()
    }

    fun onTab(tab: Tab) {
        _state.update { it.copy(tab = tab) }
    }

    fun refresh() {
        val me = follows.ownId
        if (me.isBlank()) {
            _state.update { it.copy(loading = false, error = SIGNED_OUT_COPY) }
            return
        }
        loadJob?.cancel()
        _state.update { it.copy(loading = true, error = null) }
        loadJob = viewModelScope.launch {
            val upcoming = async { listApiCall(errorMapper) { api.userStreams(me, LiveUserStreams.UPCOMING, LIMIT) } }
            val past = async { listApiCall(errorMapper) { api.userStreams(me, LiveUserStreams.PAST, LIMIT) } }
            val soon = upcoming.await()
            val before = past.await()
            if (soon is AppResult.Failure && before is AppResult.Failure) {
                _state.update { it.copy(loading = false, error = LOAD_FAILED_COPY) }
                return@launch
            }
            _state.update {
                it.copy(
                    loading = false,
                    upcoming = (soon as? AppResult.Success)?.data.orEmpty().filter { row -> row.id.isNotBlank() },
                    past = (before as? AppResult.Success)?.data.orEmpty().filter { row -> row.id.isNotBlank() },
                )
            }
        }
    }

    internal companion object {
        const val LIMIT = 50
        const val SIGNED_OUT_COPY = "Sign in to see your live streams."
        const val LOAD_FAILED_COPY = "Couldn't load your live streams."
    }
}

/** A past stream whose recording exists. */
fun hasRecording(stream: LiveStreamDto): Boolean =
    stream.recordingPostId.isNotBlank() || stream.recordingUrl.isNotBlank()
