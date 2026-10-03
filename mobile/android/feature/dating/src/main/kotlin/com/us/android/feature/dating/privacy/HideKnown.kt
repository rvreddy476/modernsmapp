package com.us.android.feature.dating.privacy

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.HideKnownDto
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.Tone
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/*
 * Hide me from people I know (mechanic M16).
 *
 * On: the person's accepted Momentum connections won't see them on Pulse, and
 * they won't see those connections — either way, in the deck and in picks.
 * The server takes a snapshot of the connections when it is turned on and
 * refreshes it daily; "Hidden from N connections" is that snapshot's size.
 * Phone contacts are not part of it.
 *
 * `GET`/`PUT /hide-known`. Turning it on answers `503 HIDE_KNOWN_UNAVAILABLE`
 * when the connections cannot be read: the switch stays off and the section
 * offers to try again. `404 MECHANIC_NOT_ENABLED` hides the section.
 */

/** LOADING and HIDDEN draw nothing; FAILED offers a retry of the read. */
enum class HideKnownPhase { LOADING, HIDDEN, FAILED, READY }

data class HideKnownUi(
    val phase: HideKnownPhase = HideKnownPhase.LOADING,
    val enabled: Boolean = false,
    /** How many connections the current snapshot hides; 0 when the server sent none. */
    val hiddenCount: Int = 0,
    val busy: Boolean = false,
    /** Turning it on was refused because the connections could not be read: the retry is offered. */
    val unavailable: Boolean = false,
    val message: UsMessage? = null,
)

/** The words. Our own. */
object HideKnownCopy {
    const val SECTION = "People you know"
    const val TITLE = "Hide me from people I know"
    const val BODY = "Your Momentum connections won't see you on Pulse, and you won't see them."
    const val NONE_YET = "On. You don't have any connections to hide from yet."
    const val UNAVAILABLE = "We couldn't check your connections just now, so this is still off. Try again in a moment."
    const val TRY_AGAIN = "Try again"
    const val CONTACTS = "This uses your Momentum connections only, never your phone's contacts."
    const val LOAD_FAILED = "This setting didn't load."

    /** "Hidden from 1 connection" / "Hidden from 12 connections". */
    fun hiddenFrom(count: Int): String = when {
        count <= 0 -> NONE_YET
        count == 1 -> "Hidden from 1 connection"
        else -> "Hidden from $count connections"
    }
}

@HiltViewModel
class HideKnownViewModel @Inject constructor(
    private val repository: DatingRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(HideKnownUi())
    val state: StateFlow<HideKnownUi> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.hideKnown()) {
                is DatingResult.Success -> _state.update { it.withServer(result.value) }
                is DatingResult.Failure -> _state.update {
                    when {
                        isOff(result.error) -> HideKnownUi(phase = HideKnownPhase.HIDDEN)
                        it.phase == HideKnownPhase.READY -> it
                        else -> it.copy(phase = HideKnownPhase.FAILED)
                    }
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun setEnabled(enabled: Boolean) {
        val current = _state.value
        if (current.busy || current.phase != HideKnownPhase.READY) return
        _state.update { it.copy(busy = true, unavailable = false) }
        viewModelScope.launch {
            when (val result = repository.setHideKnown(enabled)) {
                is DatingResult.Success -> _state.update { it.withServer(result.value).copy(busy = false) }
                is DatingResult.Failure -> refused(result.error)
            }
        }
    }

    /** The retry the 503 offers: turning it on again. */
    fun retry() = setEnabled(true)

    private fun refused(error: DatingError) {
        _state.update {
            when {
                isOff(error) -> HideKnownUi(phase = HideKnownPhase.HIDDEN)
                // Nothing changed on the server: the switch stays as it was, and the retry is offered.
                error.code == CODE_UNAVAILABLE -> it.copy(busy = false, unavailable = true)
                else -> it.copy(busy = false, message = DatingCopy.message(error, repository.json))
            }
        }
    }

    private fun HideKnownUi.withServer(dto: HideKnownDto) = copy(
        phase = HideKnownPhase.READY,
        enabled = dto.enabled,
        hiddenCount = dto.hiddenCount.coerceAtLeast(0),
        unavailable = false,
    )

    private fun isOff(error: DatingError): Boolean = error.code == CODE_MECHANIC_NOT_ENABLED

    private companion object {
        const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        const val CODE_UNAVAILABLE = "HIDE_KNOWN_UNAVAILABLE"
    }
}

/** The "People you know" section of Privacy. Nothing while loading or while the server's flag is off. */
internal fun LazyListScope.hideKnownSection(state: HideKnownUi, viewModel: HideKnownViewModel) {
    when (state.phase) {
        HideKnownPhase.LOADING, HideKnownPhase.HIDDEN -> return
        HideKnownPhase.FAILED -> {
            item { SectionLabel(HideKnownCopy.SECTION) }
            item {
                DatingCard {
                    Text(HideKnownCopy.LOAD_FAILED, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    TextButton(onClick = viewModel::refresh) { Text(HideKnownCopy.TRY_AGAIN, color = UsTheme.extended.accentSolid) }
                }
            }
        }
        HideKnownPhase.READY -> {
            item { SectionLabel(HideKnownCopy.SECTION) }
            item {
                DatingCard {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(HideKnownCopy.TITLE, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                            Text(HideKnownCopy.BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                        Switch(
                            checked = state.enabled,
                            onCheckedChange = viewModel::setEnabled,
                            enabled = !state.busy,
                            colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
                        )
                    }
                    if (state.enabled) {
                        Text(HideKnownCopy.hiddenFrom(state.hiddenCount), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                    }
                    if (state.unavailable) {
                        InfoNote(HideKnownCopy.UNAVAILABLE, tone = Tone.Warning)
                        TextButton(onClick = viewModel::retry, enabled = !state.busy) {
                            Text(HideKnownCopy.TRY_AGAIN, color = UsTheme.extended.accentSolid)
                        }
                    }
                    InfoNote(HideKnownCopy.CONTACTS)
                }
            }
        }
    }
}
