package com.us.android.feature.dating.safety

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.home.parseInstant
import com.us.android.feature.dating.home.reportFailure
import com.us.android.feature.dating.network.PastMatchDto
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import javax.inject.Inject

/*
 * Past matches (mechanic M19): someone from a match that has ended can still
 * be reported, for a while — unmatched, blocked, expired or closed. The
 * section sits in the safety centre, which is also where a scam alert (M17)
 * lands.
 *
 * `GET /past-matches` answering `404 MECHANIC_NOT_ENABLED` hides the section.
 * Report opens the ordinary report sheet for that person; a row the server
 * says was reported already shows "Reported" instead.
 */

/** LOADING and HIDDEN draw nothing; FAILED offers a retry. */
enum class PastMatchesPhase { LOADING, HIDDEN, FAILED, READY }

/** One ended match, in display terms. [ended] is the server's code; [endedAt] null when it did not parse. */
data class PastMatchUi(
    val matchId: String,
    val userId: String,
    /** The first name, or [PastMatchesCopy.SOMEONE] when the server sent none. */
    val name: String,
    val ended: String,
    val endedAt: Instant?,
    val reported: Boolean,
)

data class PastMatchesUi(
    val phase: PastMatchesPhase = PastMatchesPhase.LOADING,
    val items: List<PastMatchUi> = emptyList(),
    /** How far back the list reaches, in days; 0 when the server did not say. */
    val windowDays: Int = 0,
    /** The report sheet is open for this row. */
    val reporting: PastMatchUi? = null,
    val message: UsMessage? = null,
)

/** The words. Our own. */
object PastMatchesCopy {
    const val SECTION = "Report someone from a past match"
    const val SOMEONE = "Someone"
    const val REPORT = "Report"
    const val REPORTED = "Reported"
    const val EMPTY = "No matches have ended recently."
    const val LOAD_FAILED = "Your past matches didn't load."
    const val THANKS = "Thanks for telling us. Our safety team will look into it."

    fun intro(windowDays: Int): String =
        if (windowDays > 0) {
            "Matches that ended in the last $windowDays days. If something wasn't right, you can still tell us."
        } else {
            "Matches that ended recently. If something wasn't right, you can still tell us."
        }

    /** "Unmatched", or "Unmatched on 12 Sep" when the time is known. An unknown code still reads as ended. */
    fun endedLine(ended: String, endedAt: Instant?, zone: ZoneId): String {
        val how = when (ended) {
            "unmatched" -> "Unmatched"
            "blocked" -> "Blocked"
            "expired" -> "Expired"
            else -> "Ended"
        }
        return if (endedAt != null) "$how on ${DAY.format(endedAt.atZone(zone))}" else how
    }

    private val DAY = DateTimeFormatter.ofPattern("d MMM", Locale.ENGLISH)
}

@HiltViewModel
class PastMatchesViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val safety: SafetyActions,
) : ViewModel() {

    private val _state = MutableStateFlow(PastMatchesUi())
    val state: StateFlow<PastMatchesUi> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.pastMatches()) {
                is DatingResult.Success -> _state.update {
                    it.copy(
                        phase = PastMatchesPhase.READY,
                        items = result.value.data.filter { row -> row.person.userId.isNotBlank() }.map { row -> row.toUi() },
                        windowDays = result.value.meta?.windowDays?.coerceAtLeast(0) ?: 0,
                    )
                }
                is DatingResult.Failure -> _state.update {
                    when {
                        isOff(result.error) -> PastMatchesUi(phase = PastMatchesPhase.HIDDEN)
                        it.phase == PastMatchesPhase.READY -> it
                        else -> it.copy(phase = PastMatchesPhase.FAILED)
                    }
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** Opens the report sheet for [matchId]'s person. A row already reported offers nothing. */
    fun startReport(matchId: String) = _state.update { s ->
        val row = s.items.firstOrNull { it.matchId == matchId }?.takeIf { !it.reported } ?: return@update s
        s.copy(reporting = row)
    }

    fun dismissReport() = _state.update { it.copy(reporting = null) }

    fun report(draft: ReportDraft) {
        _state.update { it.copy(reporting = null) }
        viewModelScope.launch {
            when (val result = safety.report(draft)) {
                is DatingResult.Success -> _state.update { s ->
                    s.copy(
                        items = s.items.map { if (it.userId == draft.targetId) it.copy(reported = true) else it },
                        message = successMessage(PastMatchesCopy.THANKS),
                    )
                }
                is DatingResult.Failure -> _state.update { it.copy(message = reportFailure(result.error)) }
            }
        }
    }

    private fun isOff(error: DatingError): Boolean = error.code == CODE_MECHANIC_NOT_ENABLED

    private fun PastMatchDto.toUi() = PastMatchUi(
        matchId = matchId,
        userId = person.userId,
        name = person.firstName.trim().takeIf { it.isNotEmpty() } ?: PastMatchesCopy.SOMEONE,
        ended = ended,
        endedAt = parseInstant(endedAt),
        reported = reported,
    )

    private companion object {
        const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
    }
}

/** The safety centre's past-matches section. Nothing while loading or while the server's flag is off. */
internal fun LazyListScope.pastMatchesSection(state: PastMatchesUi, viewModel: PastMatchesViewModel, zone: ZoneId) {
    when (state.phase) {
        PastMatchesPhase.LOADING, PastMatchesPhase.HIDDEN -> return
        PastMatchesPhase.FAILED -> {
            item { SectionLabel(PastMatchesCopy.SECTION) }
            item {
                DatingCard {
                    Text(PastMatchesCopy.LOAD_FAILED, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    TextButton(onClick = viewModel::refresh) { Text("Try again", color = UsTheme.extended.accentSolid) }
                }
            }
        }
        PastMatchesPhase.READY -> {
            item { SectionLabel(PastMatchesCopy.SECTION) }
            item {
                DatingCard {
                    InfoNote(PastMatchesCopy.intro(state.windowDays))
                    if (state.items.isEmpty()) {
                        Text(PastMatchesCopy.EMPTY, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    }
                    state.items.forEach { row -> PastMatchRow(row, zone, onReport = { viewModel.startReport(row.matchId) }) }
                }
            }
        }
    }
}

@Composable
private fun PastMatchRow(row: PastMatchUi, zone: ZoneId, onReport: () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        Column(Modifier.weight(1f)) {
            Text(row.name, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            Text(PastMatchesCopy.endedLine(row.ended, row.endedAt, zone), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
        if (row.reported) {
            Pill(PastMatchesCopy.REPORTED, Tone.Neutral)
        } else {
            TextButton(onClick = onReport) { Text(PastMatchesCopy.REPORT, color = UsTheme.extended.statusDanger) }
        }
    }
}
