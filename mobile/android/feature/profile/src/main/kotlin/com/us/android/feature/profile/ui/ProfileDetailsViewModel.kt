package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.ProfileAboutItem
import com.us.android.core.profile.data.ProfileDetailsRepository
import com.us.android.core.profile.data.ProfileLink
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ProfileDetailsUiState(
    val loading: Boolean = true,
    val username: String = "",
    val about: List<ProfileAboutItem> = emptyList(),
    val links: List<ProfileLink> = emptyList(),
    val aboutDraft: ProfileAboutItem? = null,
    val linkDraft: ProfileLink? = null,
    val busy: Boolean = false,
    val error: String? = null,
)

@HiltViewModel
class ProfileDetailsViewModel @Inject constructor(
    private val settings: ProfileDetailsRepository,
    private val profiles: ProfileRepository,
) : ViewModel() {
    private val _state = MutableStateFlow(ProfileDetailsUiState())
    val state: StateFlow<ProfileDetailsUiState> = _state.asStateFlow()
    init { load() }

    fun load() {
        _state.value = ProfileDetailsUiState(loading = true)
        viewModelScope.launch {
            val (profile, about, links) = coroutineScope {
                val p = async { profiles.getOwnProfile() }
                val a = async { settings.about() }
                val l = async { settings.links() }
                Triple(p.await(), a.await(), l.await())
            }
            if (profile !is AppResult.Success || about !is AppResult.Success || links !is AppResult.Success) {
                _state.value = ProfileDetailsUiState(loading = false, error = "Profile details could not be loaded.")
                return@launch
            }
            _state.value = ProfileDetailsUiState(
                loading = false, username = profile.data.username,
                about = about.data, links = links.data,
            )
        }
    }

    fun editAbout(value: ProfileAboutItem?) = _state.update {
        it.copy(
            aboutDraft = value ?: ProfileAboutItem("", "education", "", "", "", "public", it.about.size),
            error = null
        )
    }
    fun updateAbout(block: (ProfileAboutItem) -> ProfileAboutItem) = _state.update {
        it.copy(aboutDraft = it.aboutDraft?.let(block), error = null)
    }
    fun saveAbout() {
        val current = _state.value
        val draft = current.aboutDraft ?: return
        if (current.busy || draft.title.isBlank()) return
        runMutation({ settings.saveAbout(draft) }) { saved ->
            current.copy(
                about = (current.about.filterNot { it.itemId == saved.itemId } + saved).sortedBy { it.sortOrder },
                aboutDraft = null,
                busy = false,
                error = "About item saved.",
            )
        }
    }
    fun deleteAbout(value: ProfileAboutItem) = runUnitMutation({ settings.deleteAbout(value) }) {
        it.copy(about = it.about - value, aboutDraft = null, error = "About item removed.")
    }

    fun editLink(value: ProfileLink?) = _state.update {
        it.copy(linkDraft = value ?: ProfileLink("", "", "", "other", "public", false, it.links.size), error = null)
    }
    fun updateLink(block: (ProfileLink) -> ProfileLink) = _state.update {
        it.copy(linkDraft = it.linkDraft?.let(block), error = null)
    }
    fun saveLink() {
        val current = _state.value
        val draft = current.linkDraft ?: return
        if (current.busy || draft.title.isBlank() || draft.url.isBlank()) return
        runMutation({ settings.saveLink(draft) }) { saved ->
            current.copy(
                links = (current.links.filterNot { it.id == saved.id } + saved).sortedBy { it.sortOrder },
                linkDraft = null,
                busy = false,
                error = "Profile link saved.",
            )
        }
    }
    fun deleteLink(value: ProfileLink) = runUnitMutation({ settings.deleteLink(value) }) {
        it.copy(links = it.links - value, linkDraft = null, error = "Profile link removed.")
    }

    fun changeHandle(value: String) {
        val normalized = value.trim().lowercase()
        if (!HANDLE.matches(normalized)) {
            _state.update { it.copy(error = "Use 3–30 lowercase letters, numbers or underscores.") }
            return
        }
        runUnitMutation(
            { settings.changeHandle(normalized) }
        ) { it.copy(username = normalized, error = "Handle changed.") }
    }

    fun dismissEditor() = _state.update { it.copy(aboutDraft = null, linkDraft = null, error = null) }

    private fun <T> runMutation(call: suspend () -> AppResult<T>, success: (T) -> ProfileDetailsUiState) {
        val current = _state.value
        if (current.busy) return
        _state.value = current.copy(busy = true, error = null)
        viewModelScope.launch {
            _state.value = when (val result = call()) {
                is AppResult.Success -> success(result.data)
                is AppResult.Failure -> current.copy(busy = false, error = "The change did not save.")
            }
        }
    }

    private fun runUnitMutation(
        call: suspend () -> AppResult<Unit>,
        success: (ProfileDetailsUiState) -> ProfileDetailsUiState,
    ) {
        val current = _state.value
        if (current.busy) return
        _state.value = current.copy(busy = true, error = null)
        viewModelScope.launch {
            _state.value = when (call()) {
                is AppResult.Success -> success(current).copy(busy = false)
                is AppResult.Failure -> current.copy(busy = false, error = "The change did not save.")
            }
        }
    }

    private companion object { val HANDLE = Regex("^[a-z0-9_]{3,30}$") }
}
