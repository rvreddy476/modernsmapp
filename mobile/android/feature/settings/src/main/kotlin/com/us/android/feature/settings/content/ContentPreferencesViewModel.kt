package com.us.android.feature.settings.content

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.KeywordFiltersRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface ContentPreferencesUiState {
    data object Loading : ContentPreferencesUiState
    data class Error(val message: String) : ContentPreferencesUiState
    data class Editing(
        val keywords: List<String>,
        val draft: String = "",
        val draftError: String? = null,
        val saving: Boolean = false,
        val message: String? = null,
    ) : ContentPreferencesUiState
}

/**
 * Mirrors the server's `keyword-filters` validation client-side, so a keyword
 * the user typed is rejected on the spot rather than round-tripping to a
 * `400 INVALID_KEYWORD` / `TOO_MANY_KEYWORDS`. The server is still the source
 * of truth — [KeywordFiltersRepository.save] can still fail — this is only
 * about not making the user wait for an error the client can already see.
 */
object KeywordValidation {
    const val MAX_KEYWORDS = 50
    const val MAX_LENGTH = 40

    /** Lower-cased, trimmed, with a leading `#` stripped — the server's own normalisation. */
    fun normalize(raw: String): String = raw.trim().lowercase().removePrefix("#")

    /** Null means valid. [existing] is checked case-insensitively since [normalize] already lower-cases. */
    fun validate(raw: String, existing: List<String>): String? {
        val normalized = normalize(raw)
        return when {
            normalized.isEmpty() -> "Enter a keyword."
            normalized.length > MAX_LENGTH -> "Keywords can be at most $MAX_LENGTH characters."
            normalized in existing -> "You're already filtering that keyword."
            existing.size >= MAX_KEYWORDS -> "You can filter up to $MAX_KEYWORDS keywords."
            else -> null
        }
    }
}

@HiltViewModel
class ContentPreferencesViewModel @Inject constructor(
    private val repository: KeywordFiltersRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<ContentPreferencesUiState>(ContentPreferencesUiState.Loading)
    val state: StateFlow<ContentPreferencesUiState> = _state.asStateFlow()

    init { load() }

    fun load() {
        _state.value = ContentPreferencesUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.keywords()) {
                is AppResult.Success -> ContentPreferencesUiState.Editing(result.data)
                is AppResult.Failure -> ContentPreferencesUiState.Error("Keyword filters could not be loaded.")
            }
        }
    }

    fun setDraft(value: String) = _state.update { state ->
        val editing = state as? ContentPreferencesUiState.Editing ?: return@update state
        editing.copy(draft = value, draftError = null, message = null)
    }

    fun addKeyword() {
        val current = _state.value as? ContentPreferencesUiState.Editing ?: return
        if (current.saving) return
        val error = KeywordValidation.validate(current.draft, current.keywords)
        if (error != null) {
            _state.value = current.copy(draftError = error)
            return
        }
        save(current.keywords + KeywordValidation.normalize(current.draft), clearDraft = true)
    }

    fun removeKeyword(keyword: String) {
        val current = _state.value as? ContentPreferencesUiState.Editing ?: return
        if (current.saving) return
        save(current.keywords - keyword, clearDraft = false)
    }

    private fun save(keywords: List<String>, clearDraft: Boolean) {
        val current = _state.value as? ContentPreferencesUiState.Editing ?: return
        _state.value = current.copy(saving = true, draftError = null, message = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.save(keywords)) {
                is AppResult.Success -> ContentPreferencesUiState.Editing(
                    keywords = result.data,
                    draft = if (clearDraft) "" else current.draft,
                )
                is AppResult.Failure -> current.copy(saving = false, message = "Nothing changed. Please try again.")
            }
        }
    }
}
