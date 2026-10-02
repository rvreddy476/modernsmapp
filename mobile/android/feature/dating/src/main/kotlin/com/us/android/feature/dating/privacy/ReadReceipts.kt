package com.us.android.feature.dating.privacy

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.ReadReceiptsDto
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.SectionLabel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where the read-receipts section stands. HIDDEN: the server's flag is off, and nothing is drawn. */
enum class ReadReceiptsPhase { LOADING, HIDDEN, FAILED, READY }

/**
 * Read receipts (mechanic M9), in display terms. [enabled] is the person's
 * choice, [active] whether it applies now, [available] whether they hold a
 * pass — each as the server last said.
 */
data class ReadReceiptsUi(
    val phase: ReadReceiptsPhase = ReadReceiptsPhase.LOADING,
    val enabled: Boolean = false,
    val active: Boolean = false,
    val available: Boolean = false,
    val busy: Boolean = false,
    /** The Premium upsell is open. */
    val upsell: Boolean = false,
    val message: UsMessage? = null,
) {
    /** No pass: the switch cannot be turned on, and the section leads to Premium. */
    val locked: Boolean get() = !available

    /**
     * The switch is drawn for a pass holder, and for anyone who still has it on
     * — a pass that ran out — so it can always be turned off.
     */
    val showSwitch: Boolean get() = available || enabled

    /** On, but with no pass behind it: the server withholds the receipts. Always [locked] too. */
    val paused: Boolean get() = enabled && !active
}

/** The read-receipts words. Our own. */
object ReadReceiptsCopy {
    const val SECTION = "Messages"
    const val TITLE = "Read receipts"
    const val BODY = "See when your matches have read your messages."
    const val LOCKED = "Read receipts come with a Premium pass."
    const val PAUSED = "Off for now: they come back with a pass. You can switch them off any time."
    const val SEE_PREMIUM = "See Premium"
    const val NOT_NOW = "Not now"
    const val UPSELL_TITLE = "Read receipts come with a pass"
    const val UPSELL_BODY = "With a Premium pass you can see when your matches have read what you sent."
    const val LOAD_FAILED = "Your read-receipts setting didn't load."
}

/**
 * `GET`/`PUT /read-receipts` (mechanic M9). The switch saves on its own, like
 * every switch in Privacy.
 *
 * Turning it ON without a pass opens the upsell instead of asking the server;
 * a `403 READ_RECEIPTS_REQUIRE_PASS` (a pass that ran out since the read) does
 * the same. Turning it OFF is always sent. A `MECHANIC_NOT_ENABLED` answer
 * from either route hides the section.
 *
 * The chat thread shows "Seen" from what chat-service sends, and chat-service
 * already withholds it unless this setting and a pass allow it.
 */
@HiltViewModel
class ReadReceiptsViewModel @Inject constructor(
    private val repository: DatingRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ReadReceiptsUi())
    val state: StateFlow<ReadReceiptsUi> = _state.asStateFlow()

    /** The first [shown] follows the read [init] already started. */
    private var shownOnce = false

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.readReceipts()) {
                is DatingResult.Success -> _state.update { it.withServer(result.value) }
                is DatingResult.Failure -> _state.update {
                    when {
                        isOff(result.error) -> ReadReceiptsUi(phase = ReadReceiptsPhase.HIDDEN)
                        it.phase == ReadReceiptsPhase.READY -> it
                        else -> it.copy(phase = ReadReceiptsPhase.FAILED)
                    }
                }
            }
        }
    }

    /** The screen is shown again — back from Premium, where a pass may have landed. */
    fun shown() {
        if (!shownOnce) {
            shownOnce = true
            return
        }
        if (_state.value.phase != ReadReceiptsPhase.HIDDEN) refresh()
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun dismissUpsell() = _state.update { it.copy(upsell = false) }

    fun setEnabled(enabled: Boolean) {
        val current = _state.value
        if (current.busy || current.phase != ReadReceiptsPhase.READY) return
        if (enabled && current.locked) {
            _state.update { it.copy(upsell = true) }
            return
        }
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            when (val result = repository.setReadReceipts(enabled)) {
                is DatingResult.Success -> _state.update { it.withServer(result.value).copy(busy = false) }
                is DatingResult.Failure -> refused(result.error)
            }
        }
    }

    private fun refused(error: DatingError) {
        _state.update {
            when {
                isOff(error) -> ReadReceiptsUi(phase = ReadReceiptsPhase.HIDDEN)
                // The pass ran out since the read: the setting stays off and Premium is offered.
                error.code == CODE_REQUIRES_PASS -> it.copy(busy = false, available = false, active = false, upsell = true)
                else -> it.copy(busy = false, message = DatingCopy.message(error, repository.json))
            }
        }
    }

    private fun ReadReceiptsUi.withServer(dto: ReadReceiptsDto): ReadReceiptsUi = copy(
        phase = ReadReceiptsPhase.READY,
        enabled = dto.enabled,
        active = dto.active,
        available = dto.available,
        // A pass that has landed closes the upsell.
        upsell = upsell && !dto.available,
    )

    private fun isOff(error: DatingError): Boolean = error.code == CODE_MECHANIC_NOT_ENABLED

    private companion object {
        const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        const val CODE_REQUIRES_PASS = "READ_RECEIPTS_REQUIRE_PASS"
    }
}

/** The "Messages" section of Privacy. Draws nothing while loading or while the server's flag is off. */
internal fun LazyListScope.readReceiptsSection(
    state: ReadReceiptsUi,
    viewModel: ReadReceiptsViewModel,
    onOpenPremium: () -> Unit,
) {
    when (state.phase) {
        ReadReceiptsPhase.LOADING, ReadReceiptsPhase.HIDDEN -> return
        ReadReceiptsPhase.FAILED -> {
            item { SectionLabel(ReadReceiptsCopy.SECTION) }
            item {
                DatingCard {
                    Text(ReadReceiptsCopy.LOAD_FAILED, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    TextButton(onClick = viewModel::refresh) { Text("Try again", color = UsTheme.extended.accentSolid) }
                }
            }
        }
        ReadReceiptsPhase.READY -> {
            item { SectionLabel(ReadReceiptsCopy.SECTION) }
            item {
                DatingCard {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                        if (!state.showSwitch) {
                            Icon(UsIcons.Lock, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(20.dp))
                        }
                        Column(Modifier.weight(1f)) {
                            Text(ReadReceiptsCopy.TITLE, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                            Text(ReadReceiptsCopy.BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                        if (state.showSwitch) {
                            Switch(
                                checked = state.enabled,
                                onCheckedChange = viewModel::setEnabled,
                                enabled = !state.busy,
                                colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
                            )
                        }
                    }
                    if (state.locked) {
                        // Still on from an earlier pass, or never on: either way a pass is what it needs.
                        InfoNote(if (state.paused) ReadReceiptsCopy.PAUSED else ReadReceiptsCopy.LOCKED)
                        UsButton(text = ReadReceiptsCopy.SEE_PREMIUM, onClick = onOpenPremium, modifier = Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}
