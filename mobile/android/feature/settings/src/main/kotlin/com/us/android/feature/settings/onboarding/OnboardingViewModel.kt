package com.us.android.feature.settings.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.AppModule
import com.us.android.core.profile.data.ModulePreferences
import com.us.android.core.profile.data.ModulePreferencesRepository
import com.us.android.core.profile.data.ModulePrefsState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface OnboardingUiState {
    data object Loading : OnboardingUiState

    /**
     * [value] is the draft; [original] is what the server last confirmed.
     * [saved] flips once per successful save and is what the screen
     * navigates on — an event carried as state so it survives rotation.
     */
    data class Editing(
        val original: ModulePreferences,
        val value: ModulePreferences,
        val saving: Boolean = false,
        val saved: Boolean = false,
        val message: String? = null,
    ) : OnboardingUiState {
        val dirty get() = original != value
    }
}

/**
 * The module picker, shared by first-login onboarding and the settings form.
 *
 * Seeds from whatever the repository already holds. Onboarding always finds
 * a Loaded state (the shell only shows it after one arrived), so the draft
 * starts from the server's defaults: everything on, feed first. The settings
 * form may arrive before a refresh; it refreshes and falls back to defaults
 * only when the endpoint is unreachable, and says so on save.
 */
@HiltViewModel
class OnboardingViewModel @Inject constructor(
    private val repository: ModulePreferencesRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<OnboardingUiState>(OnboardingUiState.Loading)
    val state: StateFlow<OnboardingUiState> = _state.asStateFlow()

    init {
        val loaded = repository.state.value as? ModulePrefsState.Loaded
        if (loaded != null) {
            seed(loaded.prefs)
        } else {
            viewModelScope.launch {
                repository.refresh()
                seed((repository.state.value as? ModulePrefsState.Loaded)?.prefs ?: ModulePreferences.DEFAULT)
            }
        }
    }

    fun toggleModule(module: AppModule, enabled: Boolean) = edit { current ->
        current.withModules(if (enabled) current.modules + module else current.modules - module)
    }

    fun selectHome(module: AppModule) = edit { current -> current.withHome(module) }

    /**
     * Always completes onboarding. From the settings form the flag is a
     * no-op the server tolerates; from onboarding it is the whole point.
     */
    fun save() {
        val current = _state.value as? OnboardingUiState.Editing ?: return
        if (current.saving) return
        _state.value = current.copy(saving = true, message = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.save(current.value, completeOnboarding = true)) {
                is AppResult.Success -> OnboardingUiState.Editing(result.data, result.data, saved = true)
                is AppResult.Failure -> current.copy(
                    saving = false,
                    message = "Your choices could not be saved. Check your connection and try again.",
                )
            }
        }
    }

    private fun seed(prefs: ModulePreferences) {
        _state.value = OnboardingUiState.Editing(prefs, prefs)
    }

    private fun edit(block: (ModulePreferences) -> ModulePreferences) = _state.update { state ->
        val editing = state as? OnboardingUiState.Editing ?: return@update state
        if (editing.saving) return@update editing
        editing.copy(value = block(editing.value), message = null)
    }
}
