package com.us.android.feature.dating.home

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.DateCheckinDto
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.SafetyActions
import com.us.android.feature.dating.ui.infoMessage
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/*
 * After-date check-ins (mechanic M14).
 *
 * A few hours after a planned meet the server asks both people how it went:
 * a push that opens the match with `?checkin=1`, and a row on
 * `GET /date-checkins`, which the matches list shows as "How did it go?" cards.
 * Anyone may also answer unasked, from the match screen.
 *
 * The sheet asks whether they met; after "yes", whether they would meet again
 * and whether they felt safe — both optional. An answer of "did not feel safe"
 * comes back with `offer_report`, and the sheet then offers the ordinary report
 * flow for that person. Nothing is decided automatically, here or on the server.
 *
 * `GET /date-checkins` answering `404 MECHANIC_NOT_ENABLED` (or the POST doing
 * so later) hides every part of it: cards, entry and sheet.
 */

/** Did they meet. [wire] is the server's code; [label] our words. */
enum class DateMet(val wire: String, val label: String) {
    YES("yes", "Yes, we met"),
    NOT_YET("not_yet", "Not yet"),
    NO("no", "No"),
}

/** Would they meet again, after meeting. */
enum class DateAgain(val wire: String, val label: String) {
    YES("yes", "Yes"),
    UNSURE("unsure", "Not sure"),
    NO("no", "No"),
}

/** Whom a check-in is about: the match, the other person's id, and their first name if the server sent one. */
data class CheckInTarget(val matchId: String, val userId: String, val name: String?)

/** LOADING draws nothing (nor does a failed first read); HIDDEN: the server's flag is off. */
enum class CheckInPhase { LOADING, HIDDEN, READY }

/** The open sheet. [offerReport]: the answer was taken and they did not feel safe — the supportive step. */
data class CheckInSheetUi(
    val target: CheckInTarget,
    val met: DateMet? = null,
    val again: DateAgain? = null,
    val feltSafe: Boolean? = null,
    val sending: Boolean = false,
    /** A refusal of the answer itself, shown in the sheet; the choices stay. */
    val error: String? = null,
    val offerReport: Boolean = false,
) {
    val canSend: Boolean get() = met != null && !sending && !offerReport

    /** The follow-up questions only follow a meeting. */
    val asksMore: Boolean get() = met == DateMet.YES
}

data class DateCheckInUi(
    val phase: CheckInPhase = CheckInPhase.LOADING,
    /** The asks waiting for an answer, newest as the server lists them. */
    val prompts: List<CheckInTarget> = emptyList(),
    val sheet: CheckInSheetUi? = null,
    /** The report sheet, opened from the supportive step. */
    val reporting: CheckInTarget? = null,
    /** Bumped when a report from here went through: the person is now blocked, and screens showing them re-read. */
    val reported: Int = 0,
    val message: UsMessage? = null,
) {
    /** The match screen's unprompted entry is offered. */
    val available: Boolean get() = phase == CheckInPhase.READY
}

/** The check-in words. Our own. */
object CheckInCopy {
    const val CARD_TITLE = "How did it go?"
    const val CARD_ACTION = "Answer"
    const val MATCH_ENTRY = "How did your date go?"
    const val MET = "Did you meet?"
    const val AGAIN = "Would you like to see them again?"
    const val FELT_SAFE = "Did you feel safe?"
    const val OPTIONAL = "These two are optional."
    const val SEND = "Send"
    const val THANKS = "Thanks for telling us."
    const val UNSAFE_TITLE = "We're sorry it didn't feel right"
    const val NOT_NOW = "Not now"
    const val DANGER = "If you're in danger right now, call 112."
    const val REPORTED = "Thanks for telling us. We've blocked them for you."
    const val GONE = "This match isn't available any more."
    const val YES = "Yes"
    const val NO = "No"

    fun cardBody(name: String?): String =
        if (name.isNullOrBlank()) "Tell us how your date went. It only takes a moment." else "Tell us how your date with $name went. It only takes a moment."

    fun sheetTitle(name: String?): String = if (name.isNullOrBlank()) "How did your date go?" else "How did it go with $name?"

    fun unsafeBody(name: String?): String {
        val who = name?.takeIf { it.isNotBlank() } ?: "them"
        return "Thank you for telling us. If something happened that shouldn't have, you can report $who. " +
            "They won't know it was you, and we'll block them for you."
    }

    fun reportAction(name: String?): String = if (name.isNullOrBlank()) "Report this person" else "Report $name"
}

/**
 * The check-in cards on the matches list, the match screen's entry and the
 * sheet both open. On the match screen the route's `checkIn` flag (the push's
 * `?checkin=1`) opens the sheet once, as soon as the match it names is known.
 */
@Suppress("TooManyFunctions")
@HiltViewModel
class DateCheckInViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DatingRepository,
    private val safety: SafetyActions,
) : ViewModel() {

    private val _state = MutableStateFlow(DateCheckInUi())
    val state: StateFlow<DateCheckInUi> = _state.asStateFlow()

    /** The match the push asked about, until its sheet has opened once. */
    private var linkMatchId: String? =
        savedStateHandle.get<String>(ARG_MATCH_ID)?.takeIf { savedStateHandle.get<Boolean>(ARG_CHECK_IN) == true }

    /** Matches a screen has described, for a link whose match has no pending ask. */
    private val known = mutableMapOf<String, CheckInTarget>()

    private var shownOnce = false

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.dateCheckins()) {
                is DatingResult.Success -> {
                    _state.update { it.copy(phase = CheckInPhase.READY, prompts = result.value.map { row -> row.toTarget() }) }
                    tryLinkOpen()
                }
                // A failed read changes nothing: the last answer stands, and nothing is drawn before one.
                is DatingResult.Failure -> if (isOff(result.error)) hide()
            }
        }
    }

    /** The list is shown again; the first showing follows init's own read. */
    fun shown() {
        if (!shownOnce) {
            shownOnce = true
            return
        }
        if (_state.value.phase != CheckInPhase.HIDDEN) refresh()
    }

    /** The match screen knows who the match is with: a `?checkin=1` link can open now. */
    fun matchKnown(target: CheckInTarget) {
        known[target.matchId] = target
        tryLinkOpen()
    }

    /** Opens the sheet for [target]: a card, or the match screen's own entry. Nothing while the mechanic is off. */
    fun open(target: CheckInTarget) {
        if (_state.value.phase != CheckInPhase.READY) return
        _state.update { it.copy(sheet = CheckInSheetUi(target)) }
    }

    fun dismissSheet() {
        if (_state.value.sheet?.sending == true) return
        _state.update { it.copy(sheet = null) }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun chooseMet(met: DateMet) = editSheet {
        // The follow-ups belong to a meeting: anything else drops them, as the server requires.
        if (met == DateMet.YES) copy(met = met) else copy(met = met, again = null, feltSafe = null)
    }

    /** Tapping the chosen answer again clears it: the question is optional. */
    fun chooseAgain(again: DateAgain) = editSheet { if (asksMore) copy(again = if (this.again == again) null else again) else this }

    fun chooseFeltSafe(feltSafe: Boolean) = editSheet { if (asksMore) copy(feltSafe = if (this.feltSafe == feltSafe) null else feltSafe) else this }

    fun send() {
        val sheet = _state.value.sheet ?: return
        val met = sheet.met ?: return
        if (!sheet.canSend) return
        _state.update { it.copy(sheet = sheet.copy(sending = true, error = null)) }
        viewModelScope.launch {
            val again = sheet.again?.wire.takeIf { sheet.asksMore }
            val feltSafe = sheet.feltSafe.takeIf { sheet.asksMore }
            when (val result = repository.dateFeedback(sheet.target.matchId, met.wire, again, feltSafe)) {
                is DatingResult.Success -> _state.update {
                    val answered = it.copy(prompts = it.prompts.filterNot { p -> p.matchId == sheet.target.matchId })
                    if (result.value.offerReport) {
                        // Did not feel safe: the sheet stays, with support and the way to report.
                        answered.copy(sheet = sheet.copy(sending = false, offerReport = true))
                    } else {
                        answered.copy(sheet = null, message = successMessage(CheckInCopy.THANKS))
                    }
                }
                is DatingResult.Failure -> refused(sheet, result.error)
            }
        }
    }

    private fun refused(sheet: CheckInSheetUi, error: DatingError) {
        val matchId = sheet.target.matchId
        when {
            isOff(error) -> hide()
            // Already answered: the ask is done either way.
            error.code == CODE_LIMIT -> _state.update {
                it.copy(prompts = it.prompts.filterNot { p -> p.matchId == matchId }, sheet = null, message = infoMessage(DatingCopy.DATE_FEEDBACK_LIMIT))
            }
            // The match is not theirs to answer about (gone, or never theirs).
            error is DatingError.Refused && error.status == HTTP_NOT_FOUND -> _state.update {
                it.copy(prompts = it.prompts.filterNot { p -> p.matchId == matchId }, sheet = null, message = infoMessage(CheckInCopy.GONE))
            }
            // INVALID_DATE_FEEDBACK, the network, anything else: said in the sheet, the choices kept.
            else -> _state.update { it.copy(sheet = sheet.copy(sending = false, error = DatingCopy.forError(error, repository.json))) }
        }
    }

    /** "Report {name}" from the supportive step: the ordinary report sheet, for that person. */
    fun startReport() {
        val sheet = _state.value.sheet?.takeIf { it.offerReport } ?: return
        _state.update { it.copy(sheet = null, reporting = sheet.target) }
    }

    fun dismissReport() = _state.update { it.copy(reporting = null) }

    fun report(draft: ReportDraft) {
        _state.update { it.copy(reporting = null) }
        viewModelScope.launch {
            when (val result = safety.report(draft)) {
                is DatingResult.Success -> _state.update { it.copy(reported = it.reported + 1, message = successMessage(CheckInCopy.REPORTED)) }
                is DatingResult.Failure -> _state.update { it.copy(message = reportFailure(result.error)) }
            }
        }
    }

    private fun tryLinkOpen() {
        val matchId = linkMatchId ?: return
        when (_state.value.phase) {
            CheckInPhase.HIDDEN -> linkMatchId = null
            CheckInPhase.LOADING -> Unit
            CheckInPhase.READY -> {
                val target = _state.value.prompts.firstOrNull { it.matchId == matchId } ?: known[matchId] ?: return
                linkMatchId = null
                open(target)
            }
        }
    }

    private fun hide() {
        linkMatchId = null
        _state.update { DateCheckInUi(phase = CheckInPhase.HIDDEN, reported = it.reported, message = it.message) }
    }

    private fun editSheet(change: CheckInSheetUi.() -> CheckInSheetUi) = _state.update { s ->
        val sheet = s.sheet?.takeIf { !it.sending && !it.offerReport } ?: return@update s
        s.copy(sheet = sheet.change().copy(error = null))
    }

    private fun isOff(error: DatingError): Boolean = error.code == CODE_MECHANIC_NOT_ENABLED

    private fun DateCheckinDto.toTarget() = CheckInTarget(
        matchId = matchId,
        userId = person.userId,
        name = person.firstName.trim().takeIf { it.isNotEmpty() },
    )

    companion object {
        const val ARG_MATCH_ID = "matchId"
        const val ARG_CHECK_IN = "checkIn"
        private const val HTTP_NOT_FOUND = 404
        private const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        private const val CODE_LIMIT = "DATE_FEEDBACK_LIMIT"
    }
}
