package com.us.android.feature.post.createhub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.datastore.SettingsDataStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The Create sheet's one piece of remembered state: grid or compact list.
 *
 * A ViewModel rather than `rememberSaveable` because the choice is a
 * preference, not a screen state — it should be the same the next time the
 * sheet opens and after the app restarts, which only the settings store gives.
 */
@HiltViewModel
class CreateSheetViewModel @Inject constructor(
    private val settings: SettingsDataStore,
) : ViewModel() {

    /** True for the single-column list; false (the default) for the 3×2 grid. */
    val compact: StateFlow<Boolean> = settings.createCompactView
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), false)

    fun setCompact(compact: Boolean) {
        viewModelScope.launch { settings.setCreateCompactView(compact) }
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}
