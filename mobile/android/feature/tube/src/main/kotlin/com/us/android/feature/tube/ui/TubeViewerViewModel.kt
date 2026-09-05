package com.us.android.feature.tube.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/** Who is looking: the name and photo Tube's header and You page draw. */
data class TubeViewer(val userId: String, val name: String, val handle: String?, val avatarUrl: String?)

/**
 * The viewer, read once per process and shared by every Tube page
 * (2026-09-05): the header's avatar, the You header and the channels
 * strip's "You" bubble all want the same two facts, and each page asking
 * `/v1/profiles/me` for them would be the N+1 the rest of the app avoids.
 * A refreshed profile (an avatar change) lands on the next [reload].
 */
@Singleton
class TubeViewerStore @Inject constructor(
    private val profiles: ProfileRepository,
    private val media: MediaRepository,
) {
    private val _viewer = MutableStateFlow<TubeViewer?>(null)
    val viewer: StateFlow<TubeViewer?> = _viewer.asStateFlow()

    suspend fun ensureLoaded(): TubeViewer? = _viewer.value ?: reload()

    suspend fun reload(): TubeViewer? {
        val own = (profiles.getOwnProfile() as? AppResult.Success)?.data ?: return _viewer.value
        val avatar = own.avatarMediaId?.takeIf { it.isNotBlank() }?.let { id ->
            (media.delivery(id) as? AppResult.Success)?.data?.takeIf { it.isReady }?.posterUrl
        }
        return TubeViewer(
            userId = own.userId,
            name = own.displayName.ifBlank { own.username.ifBlank { "You" } },
            handle = own.username.takeIf { it.isNotBlank() }?.let { "@$it" },
            avatarUrl = avatar,
        ).also { _viewer.value = it }
    }
}

/** The header's avatar: the viewer, from the shared store. */
@HiltViewModel
class TubeViewerViewModel @Inject constructor(private val store: TubeViewerStore) : ViewModel() {
    val viewer: StateFlow<TubeViewer?> = store.viewer

    init {
        viewModelScope.launch { store.ensureLoaded() }
    }
}
