package com.us.android.feature.live.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveStreamDto
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The live-now list plus the door to going live yourself. */
@HiltViewModel
class LiveHubViewModel @Inject constructor(
    private val api: LiveApi,
) : ViewModel() {

    data class UiState(
        val loading: Boolean = true,
        val streams: List<LiveStreamDto> = emptyList(),
        val error: String? = null,
    )

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = _state.value.copy(loading = true, error = null)
        viewModelScope.launch {
            runCatching { api.listLiveNow().data.orEmpty() }
                .onSuccess { streams ->
                    _state.value = UiState(loading = false, streams = streams)
                }
                .onFailure { error ->
                    _state.value = _state.value.copy(
                        loading = false,
                        error = error.message ?: "Couldn't load live streams",
                    )
                }
        }
    }
}
