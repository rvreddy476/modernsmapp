package com.us.android.feature.dating.filters

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RangeSlider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingIntent
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.data.valueOrNull
import com.us.android.feature.dating.network.FieldRefusalDetailsDto
import com.us.android.feature.dating.network.PassFiltersDto
import com.us.android.feature.dating.network.PassFiltersRequest
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.PreferencesRequest
import com.us.android.feature.dating.network.PrivacyUpdateRequest
import com.us.android.feature.dating.onboarding.DatingChoices
import com.us.android.feature.dating.profile.FieldError
import com.us.android.feature.dating.profile.LifestyleBasic
import com.us.android.feature.dating.profile.ProfileOption
import com.us.android.feature.dating.profile.ProfileOptionsStore
import com.us.android.feature.dating.profile.ProfileOptionsUi
import com.us.android.feature.dating.ui.BottomAction
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.MultiOptionChips
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.SingleOptionChips
import com.us.android.feature.dating.ui.infoMessage
import com.us.android.feature.dating.ui.listPadding
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import kotlin.math.roundToInt

/*
 * Filters (mechanic M6), from the deck's top bar.
 *
 * Free for everyone: an age range, how far, and what people are looking for.
 * With a Premium pass: verified people only, a height range, languages and the
 * four lifestyle basics.
 *
 * How the screen decides what it is:
 *  - `pass_filters` ABSENT from `GET /preferences`: the server's filters flag
 *    is off. Only the free section is drawn, with the older numeric distance,
 *    and nothing new is ever sent.
 *  - `pass_filters.active` false: no pass. The pass section is drawn LOCKED with
 *    the Premium upsell; whatever is stored is shown and can still be cleared.
 *  - `403 FILTERS_REQUIRE_PASS` on a save: the upsell opens and the section locks.
 */

/** The pass section's choices. Codes only; empty lists and null heights mean "anyone". */
data class PassFiltersDraft(
    val verifiedOnly: Boolean = false,
    val minHeightCm: Int? = null,
    val maxHeightCm: Int? = null,
    val languages: List<String> = emptyList(),
    val basics: Map<LifestyleBasic, List<String>> = emptyMap(),
) {
    fun basic(basic: LifestyleBasic): List<String> = basics[basic].orEmpty()

    val isEmpty: Boolean
        get() = !verifiedOnly && minHeightCm == null && maxHeightCm == null && languages.isEmpty() &&
            LifestyleBasic.entries.all { basic(it).isEmpty() }

    /** The whole set, as the server replaces it. */
    fun toRequest(): PassFiltersRequest = PassFiltersRequest(
        verifiedOnly = verifiedOnly,
        minHeightCm = minHeightCm,
        maxHeightCm = maxHeightCm,
        languages = languages,
        drinking = basic(LifestyleBasic.DRINKING),
        smoking = basic(LifestyleBasic.SMOKING),
        exercise = basic(LifestyleBasic.EXERCISE),
        diet = basic(LifestyleBasic.DIET),
    )
}

data class FiltersDraft(
    val minAge: Int = FiltersRules.MIN_AGE,
    val maxAge: Int = FiltersRules.MAX_AGE,
    /** Flag on: the distance bucket code. */
    val distanceBucket: String? = null,
    /** Flag off: the older numeric distance. */
    val distanceKm: Int = FiltersRules.DEFAULT_DISTANCE_KM,
    val intents: List<String> = emptyList(),
    val pass: PassFiltersDraft = PassFiltersDraft(),
    /** Mechanic M12: the preferences marked as dealbreakers. Only ones that are set count; see [FiltersRules.dealbreakersFor]. */
    val dealbreakers: Set<Dealbreaker> = emptySet(),
)

/**
 * A preference that can be made a dealbreaker (mechanic M12): then only people
 * who fit it are shown this person too. Age, distance and intent are free; the
 * rest are pass filters, and need a pass to be set as dealbreakers.
 */
enum class Dealbreaker(val code: String, val needsPass: Boolean) {
    AGE("age", false),
    DISTANCE("distance", false),
    INTENT("intent", false),
    VERIFIED("verified", true),
    HEIGHT("height", true),
    LANGUAGES("languages", true),
    DRINKING("drinking", true),
    SMOKING("smoking", true),
    EXERCISE("exercise", true),
    DIET("diet", true),
    ;

    companion object {
        /** Null for a code this app does not know: it is dropped, never shown raw. */
        fun fromCode(code: String?): Dealbreaker? = entries.firstOrNull { it.code == code }

        fun of(basic: LifestyleBasic): Dealbreaker = when (basic) {
            LifestyleBasic.DRINKING -> DRINKING
            LifestyleBasic.SMOKING -> SMOKING
            LifestyleBasic.EXERCISE -> EXERCISE
            LifestyleBasic.DIET -> DIET
        }
    }
}

/** A control the server can refuse, by its `details.field` (or by the code where it names none). */
enum class FiltersField { AGE, DISTANCE, INTENT, HEIGHT, LANGUAGES, DRINKING, SMOKING, EXERCISE, DIET }

enum class FiltersPhase { LOADING, FAILED, READY }

data class FiltersUiState(
    val phase: FiltersPhase = FiltersPhase.LOADING,
    val options: ProfileOptionsUi? = null,
    /** `pass_filters` was present: the server's filters flag is on. */
    val flagOn: Boolean = false,
    /** `pass_filters.active`: the caller holds a pass and the pass filters apply. */
    val passActive: Boolean = false,
    /** The older privacy "verified only" switch is on; Filters now owns it. */
    val privacyVerifiedOnly: Boolean = false,
    /** Mechanic M12: `dealbreakers` was present on `GET /preferences`. False hides every Dealbreaker switch. */
    val dealbreakersOn: Boolean = false,
    val draft: FiltersDraft = FiltersDraft(),
    val saved: FiltersDraft = FiltersDraft(),
    val saving: Boolean = false,
    val fieldErrors: Map<FiltersField, String> = emptyMap(),
    val message: UsMessage? = null,
    /** The Premium upsell is open. */
    val upsell: Boolean = false,
    /** Bumped on each save; the screen then closes and the deck reloads. */
    val savedCount: Int = 0,
) {
    /** The pass section is drawn but cannot be set, only cleared. */
    val locked: Boolean get() = flagOn && !passActive

    val dirty: Boolean get() = draft != saved

    /** A Dealbreaker switch is drawn: the mechanic is on and the preference it belongs to is set. */
    fun showsDealbreaker(dealbreaker: Dealbreaker): Boolean = dealbreakersOn && FiltersRules.isSet(draft, dealbreaker, flagOn)

    /** A pass dealbreaker without a pass: it can be switched off, and switching it on opens the upsell. */
    fun dealbreakerLocked(dealbreaker: Dealbreaker): Boolean = dealbreaker.needsPass && !passActive
}

@Suppress("TooManyFunctions")
@HiltViewModel
class FiltersViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val optionsStore: ProfileOptionsStore,
    private val session: DatingSession,
) : ViewModel() {

    private val _state = MutableStateFlow(FiltersUiState())
    val state: StateFlow<FiltersUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.update { it.copy(phase = FiltersPhase.LOADING) }
        viewModelScope.launch {
            val preferences = repository.preferences()
            if (preferences !is DatingResult.Success) {
                val error = (preferences as DatingResult.Failure).error
                _state.update { it.copy(phase = FiltersPhase.FAILED, message = DatingCopy.message(error)) }
                return@launch
            }
            val flagOn = preferences.value.passFilters != null
            // The lists are needed only for what the flag adds: buckets, languages, basics.
            val options = if (flagOn) optionsStore.get() else null
            if (flagOn && options == null) {
                _state.update { it.copy(phase = FiltersPhase.FAILED) }
                return@launch
            }
            val privacyVerifiedOnly = flagOn && repository.privacy().valueOrNull()?.verifiedOnlyFilter == true
            val draft = FiltersRules.draftFrom(preferences.value, options, privacyVerifiedOnly)
            _state.update {
                FiltersUiState(
                    phase = FiltersPhase.READY,
                    options = options,
                    flagOn = flagOn,
                    passActive = preferences.value.passFilters?.active == true,
                    privacyVerifiedOnly = privacyVerifiedOnly,
                    dealbreakersOn = preferences.value.dealbreakers != null,
                    draft = draft,
                    saved = draft,
                )
            }
        }
    }

    /**
     * Mechanic M12: marks [dealbreaker] on or off. Switching a pass one ON
     * without a pass opens the upsell instead; switching any one off always works.
     */
    fun setDealbreaker(dealbreaker: Dealbreaker, on: Boolean) = _state.update {
        when {
            it.phase != FiltersPhase.READY || !it.dealbreakersOn -> it
            on && it.dealbreakerLocked(dealbreaker) -> it.copy(upsell = true)
            else -> it.copy(
                draft = it.draft.copy(dealbreakers = if (on) it.draft.dealbreakers + dealbreaker else it.draft.dealbreakers - dealbreaker),
            )
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun dismissUpsell() = _state.update { it.copy(upsell = false) }

    /** A locked control was tapped: say why, rather than doing nothing. */
    fun showUpsell() = _state.update { it.copy(upsell = true) }

    fun setAges(min: Int, max: Int) = edit(FiltersField.AGE) {
        val low = min.coerceIn(FiltersRules.MIN_AGE, FiltersRules.SLIDER_MAX_AGE)
        val high = max.coerceIn(low, FiltersRules.SLIDER_MAX_AGE)
        // The top of the slider is "and older": the server's own ceiling.
        copy(minAge = low, maxAge = if (high >= FiltersRules.SLIDER_MAX_AGE) FiltersRules.MAX_AGE else high)
    }

    fun setDistanceBucket(code: String?) = edit(FiltersField.DISTANCE) { copy(distanceBucket = code ?: distanceBucket) }

    fun setDistanceKm(km: Int?) = edit(FiltersField.DISTANCE) { copy(distanceKm = km ?: distanceKm) }

    fun toggleIntent(code: String) = edit(FiltersField.INTENT) {
        if (DatingIntent.fromCode(code) == null) this else copy(intents = intents.toggled(code))
    }

    fun setVerifiedOnly(on: Boolean) = editPass(null, clearing = !on) { copy(verifiedOnly = on) }

    /** Null on both ends is "any height". */
    fun setHeightRange(min: Int?, max: Int?) = editPass(FiltersField.HEIGHT, clearing = min == null && max == null) {
        copy(minHeightCm = min, maxHeightCm = max)
    }

    fun toggleLanguage(code: String) = editPass(FiltersField.LANGUAGES, clearing = code in _state.value.draft.pass.languages) {
        copy(languages = languages.toggled(code))
    }

    fun toggleBasic(basic: LifestyleBasic, code: String) =
        editPass(FiltersRules.fieldOf(basic), clearing = code in _state.value.draft.pass.basic(basic)) {
            copy(basics = basics + (basic to basic(basic).toggled(code)))
        }

    /** Clearing needs no pass: the server always allows it. */
    fun clearPassFilters() = _state.update {
        it.copy(draft = it.draft.copy(pass = PassFiltersDraft()), fieldErrors = it.fieldErrors - PASS_FIELDS)
    }

    fun save() {
        val current = _state.value
        if (current.saving || current.phase != FiltersPhase.READY) return
        if (!current.dirty) {
            _state.update { it.copy(savedCount = it.savedCount + 1) }
            return
        }
        FiltersRules.localProblem(current.draft)?.let { (field, words) ->
            _state.update { it.copy(fieldErrors = it.fieldErrors + (field to words)) }
            return
        }
        // Setting a pass filter without a pass can only be refused: say so first.
        if (current.locked && current.draft.pass != current.saved.pass && !current.draft.pass.isEmpty) {
            _state.update { it.copy(upsell = true) }
            return
        }
        // So can a pass dealbreaker newly marked without one (mechanic M12).
        if (FiltersRules.addsLockedDealbreaker(current)) {
            _state.update { it.copy(upsell = true) }
            return
        }
        _state.update { it.copy(saving = true, fieldErrors = emptyMap()) }
        send(current, FiltersRules.requestFor(current))
    }

    private fun send(current: FiltersUiState, request: PreferencesRequest) {
        viewModelScope.launch {
            when (val result = repository.updatePreferences(request)) {
                is DatingResult.Success -> saved(current, result.value)
                is DatingResult.Failure -> if (dealbreakersSwitchedOff(result.error, request)) {
                    retryWithoutDealbreakers(current, request)
                } else {
                    refused(result.error)
                }
            }
        }
    }

    /** The dealbreakers mechanic went off since the read: only a write that carried them is refused for it. */
    private fun dealbreakersSwitchedOff(error: DatingError, request: PreferencesRequest): Boolean =
        request.dealbreakers != null && error.code == CODE_MECHANIC_NOT_ENABLED

    /** The switches go, and whatever else was changed is saved without them. */
    private fun retryWithoutDealbreakers(current: FiltersUiState, request: PreferencesRequest) {
        val hidden = current.copy(
            dealbreakersOn = false,
            draft = current.draft.copy(dealbreakers = emptySet()),
            saved = current.saved.copy(dealbreakers = emptySet()),
        )
        _state.update {
            it.copy(dealbreakersOn = false, draft = it.draft.copy(dealbreakers = emptySet()), saved = it.saved.copy(dealbreakers = emptySet()))
        }
        val rest = request.copy(dealbreakers = null)
        if (rest == PreferencesRequest()) {
            _state.update { it.copy(saving = false, message = infoMessage(FiltersCopy.DEALBREAKERS_GONE)) }
        } else {
            send(hidden, rest)
        }
    }

    private suspend fun saved(sent: FiltersUiState, answer: PreferencesDto) {
        // The older privacy switch also narrows the deck; turning "verified only"
        // off here turns it off there too. Clearing it never needs a pass.
        val privacyCleared = sent.privacyVerifiedOnly && sent.saved.pass.verifiedOnly && !sent.draft.pass.verifiedOnly
        val privacyOk = !privacyCleared ||
            repository.updatePrivacy(PrivacyUpdateRequest(verifiedOnlyFilter = false)) is DatingResult.Success
        session.filtersChanged()
        _state.update {
            it.copy(
                saving = false,
                passActive = answer.passFilters?.active ?: it.passActive,
                // The PUT answers with the GET's view: no list means the mechanic is off now.
                dealbreakersOn = answer.dealbreakers != null,
                privacyVerifiedOnly = it.privacyVerifiedOnly && !privacyCleared,
                saved = it.draft,
                savedCount = if (privacyOk) it.savedCount + 1 else it.savedCount,
                message = if (privacyOk) null else DatingCopy.message(DatingError.Unexpected(null, null)),
            )
        }
    }

    private fun refused(error: DatingError) {
        // A pass that ran out since the read: the pass section and pass dealbreakers lock, and Premium is offered.
        if (error.code == CODE_FILTERS_REQUIRE_PASS || error.code == CODE_DEALBREAKERS_REQUIRE_PASS) {
            _state.update { it.copy(saving = false, upsell = true, passActive = false) }
            return
        }
        val field = FiltersRules.fieldFor(error, repository.json)
        val words = DatingCopy.forError(error, repository.json)
        _state.update {
            if (field != null) {
                it.copy(saving = false, fieldErrors = mapOf(field to words))
            } else {
                it.copy(saving = false, message = DatingCopy.message(error, repository.json))
            }
        }
    }

    private fun edit(field: FiltersField, change: FiltersDraft.() -> FiltersDraft) = _state.update {
        if (it.phase != FiltersPhase.READY) it else it.copy(draft = it.draft.change(), fieldErrors = it.fieldErrors - field)
    }

    /** A pass-section change. While locked only a change that CLEARS is taken; anything else opens the upsell. */
    private fun editPass(field: FiltersField?, clearing: Boolean, change: PassFiltersDraft.() -> PassFiltersDraft) = _state.update {
        when {
            it.phase != FiltersPhase.READY || !it.flagOn -> it
            it.locked && !clearing -> it.copy(upsell = true)
            else -> it.copy(
                draft = it.draft.copy(pass = it.draft.pass.change()),
                fieldErrors = if (field == null) it.fieldErrors else it.fieldErrors - field,
            )
        }
    }

    private companion object {
        const val CODE_FILTERS_REQUIRE_PASS = "FILTERS_REQUIRE_PASS"
        const val CODE_DEALBREAKERS_REQUIRE_PASS = "DEALBREAKERS_REQUIRE_PASS"
        const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        val PASS_FIELDS = setOf(
            FiltersField.HEIGHT,
            FiltersField.LANGUAGES,
            FiltersField.DRINKING,
            FiltersField.SMOKING,
            FiltersField.EXERCISE,
            FiltersField.DIET,
        )
    }
}

private fun List<String>.toggled(code: String): List<String> = if (code in this) this - code else this + code

/** The pure rules: reading the server's preferences, writing a save, and mapping a refusal to its control. */
object FiltersRules {
    const val MIN_AGE = 18
    const val MAX_AGE = 120

    /** The slider's top stop, which means "and older". */
    const val SLIDER_MAX_AGE = 70
    const val DEFAULT_DISTANCE_KM = 25

    fun draftFrom(prefs: PreferencesDto, options: ProfileOptionsUi?, privacyVerifiedOnly: Boolean): FiltersDraft {
        val pass = prefs.passFilters
        return FiltersDraft(
            minAge = prefs.minAge?.takeIf { it in MIN_AGE..MAX_AGE } ?: MIN_AGE,
            maxAge = prefs.maxAge?.takeIf { it in MIN_AGE..MAX_AGE } ?: MAX_AGE,
            distanceBucket = prefs.distanceBucket?.takeIf { options?.distanceLabel(it) != null },
            distanceKm = prefs.distanceKm.takeIf { it > 0 } ?: DEFAULT_DISTANCE_KM,
            intents = prefs.intentFilter.orEmpty().filter { DatingIntent.fromCode(it) != null }.distinct(),
            pass = if (pass == null || options == null) PassFiltersDraft() else passDraftFrom(pass, options, privacyVerifiedOnly),
            dealbreakers = prefs.dealbreakers.orEmpty().mapNotNull { Dealbreaker.fromCode(it) }.toSet(),
        )
    }

    /**
     * Whether the preference [dealbreaker] belongs to is set, so it can be a
     * dealbreaker at all. Age and distance always hold a value; the rest are
     * set once something is chosen. The pass ones exist only with the M6 flag on.
     */
    fun isSet(draft: FiltersDraft, dealbreaker: Dealbreaker, flagOn: Boolean): Boolean {
        val pass = draft.pass
        return when (dealbreaker) {
            Dealbreaker.AGE -> true
            Dealbreaker.DISTANCE -> !flagOn || draft.distanceBucket != null
            Dealbreaker.INTENT -> draft.intents.isNotEmpty()
            Dealbreaker.VERIFIED -> flagOn && pass.verifiedOnly
            Dealbreaker.HEIGHT -> flagOn && (pass.minHeightCm != null || pass.maxHeightCm != null)
            Dealbreaker.LANGUAGES -> flagOn && pass.languages.isNotEmpty()
            Dealbreaker.DRINKING -> flagOn && pass.basic(LifestyleBasic.DRINKING).isNotEmpty()
            Dealbreaker.SMOKING -> flagOn && pass.basic(LifestyleBasic.SMOKING).isNotEmpty()
            Dealbreaker.EXERCISE -> flagOn && pass.basic(LifestyleBasic.EXERCISE).isNotEmpty()
            Dealbreaker.DIET -> flagOn && pass.basic(LifestyleBasic.DIET).isNotEmpty()
        }
    }

    /**
     * The dealbreaker codes a save sends: the marked ones whose preference is
     * still set, in the server's order. A preference cleared takes its
     * dealbreaker with it.
     *
     * Without a pass, the pass dealbreakers saved earlier go along unchanged:
     * the server keeps them (they count again with the next pass) and refuses
     * only a NEW one ([addsLockedDealbreaker]). With the filters flag off this
     * screen cannot show their preferences at all, so a saved one is kept as
     * it is rather than dropped for looking unset.
     */
    fun dealbreakersFor(draft: FiltersDraft, flagOn: Boolean): List<String> =
        Dealbreaker.entries.filter { it in draft.dealbreakers && (isSet(draft, it, flagOn) || (it.needsPass && !flagOn)) }.map { it.code }

    /** Without a pass, the save would newly mark a pass dealbreaker: the server can only refuse that. */
    fun addsLockedDealbreaker(state: FiltersUiState): Boolean {
        if (!state.dealbreakersOn || state.passActive) return false
        val before = dealbreakersFor(state.saved, state.flagOn).toSet()
        return dealbreakersFor(state.draft, state.flagOn).any { code ->
            code !in before && Dealbreaker.fromCode(code)?.needsPass == true
        }
    }

    private fun passDraftFrom(pass: PassFiltersDto, options: ProfileOptionsUi, privacyVerifiedOnly: Boolean) = PassFiltersDraft(
        verifiedOnly = pass.verifiedOnly || privacyVerifiedOnly,
        minHeightCm = pass.minHeightCm?.takeIf { it in options.heightRange },
        maxHeightCm = pass.maxHeightCm?.takeIf { it in options.heightRange },
        languages = pass.languages.filter { options.languageLabel(it) != null }.distinct(),
        basics = mapOf(
            LifestyleBasic.DRINKING to pass.drinking.known(options, LifestyleBasic.DRINKING),
            LifestyleBasic.SMOKING to pass.smoking.known(options, LifestyleBasic.SMOKING),
            LifestyleBasic.EXERCISE to pass.exercise.known(options, LifestyleBasic.EXERCISE),
            LifestyleBasic.DIET to pass.diet.known(options, LifestyleBasic.DIET),
        ),
    )

    private fun List<String>.known(options: ProfileOptionsUi, basic: LifestyleBasic) =
        filter { options.basicLabel(basic, it) != null }.distinct()

    /**
     * Only what changed. With the flag off nothing new is sent: the distance
     * stays numeric and there is no pass section. The pass filters go as one
     * set whenever any of them changed.
     */
    fun requestFor(state: FiltersUiState): PreferencesRequest {
        val draft = state.draft
        val saved = state.saved
        val agesChanged = draft.minAge != saved.minAge || draft.maxAge != saved.maxAge
        val dealbreakers = dealbreakersFor(draft, state.flagOn)
        return PreferencesRequest(
            minAge = draft.minAge.takeIf { agesChanged },
            maxAge = draft.maxAge.takeIf { agesChanged },
            intentFilter = draft.intents.takeIf { it != saved.intents },
            distanceBucket = draft.distanceBucket.takeIf { state.flagOn && it != saved.distanceBucket },
            distanceKm = draft.distanceKm.takeIf { !state.flagOn && it != saved.distanceKm },
            passFilters = draft.pass.toRequest().takeIf { state.flagOn && draft.pass != saved.pass },
            // Mechanic M12: the WHOLE list whenever it changed; never while the mechanic is off.
            dealbreakers = dealbreakers.takeIf { state.dealbreakersOn && it != dealbreakersFor(saved, state.flagOn) },
        )
    }

    /** A problem the app can see before asking the server. */
    fun localProblem(draft: FiltersDraft): Pair<FiltersField, String>? {
        val min = draft.pass.minHeightCm
        val max = draft.pass.maxHeightCm
        return when {
            draft.minAge > draft.maxAge -> FiltersField.AGE to "The youngest age needs to be below the oldest."
            min != null && max != null && min > max -> FiltersField.HEIGHT to "The shortest height needs to be below the tallest."
            else -> null
        }
    }

    fun fieldOf(basic: LifestyleBasic): FiltersField = when (basic) {
        LifestyleBasic.DRINKING -> FiltersField.DRINKING
        LifestyleBasic.SMOKING -> FiltersField.SMOKING
        LifestyleBasic.EXERCISE -> FiltersField.EXERCISE
        LifestyleBasic.DIET -> FiltersField.DIET
    }

    /** The control a refusal belongs to: its `details.field`, else its code. Null: a message, not a field. */
    fun fieldFor(error: DatingError, json: kotlinx.serialization.json.Json): FiltersField? {
        val field = error.detailsAs(json, FieldRefusalDetailsDto.serializer())?.field
        return when (error.code) {
            "INVALID_DISTANCE_BUCKET", "INVALID_DISTANCE_KM" -> FiltersField.DISTANCE
            "INVALID_AGE_RANGE" -> FiltersField.AGE
            "INVALID_INTENT_FILTER" -> FiltersField.INTENT
            "INVALID_HEIGHT" -> FiltersField.HEIGHT
            "INVALID_LANGUAGE", "TOO_MANY_LANGUAGE" -> FiltersField.LANGUAGES
            "INVALID_LIFESTYLE" -> LifestyleBasic.entries.firstOrNull { it.field == field }?.let(::fieldOf)
            else -> null
        }
    }

    /** "24–34", or "24 and older" at the slider's top stop. */
    fun ageLabel(min: Int, max: Int): String =
        if (max >= SLIDER_MAX_AGE) "$min and older" else "$min–$max"

    fun heightLabel(min: Int?, max: Int?): String = when {
        min == null && max == null -> FiltersCopy.ANY_HEIGHT
        min != null && max != null -> "$min–$max cm"
        min != null -> "$min cm and taller"
        else -> "Up to $max cm"
    }
}

/** The words of Filters. Our own. */
object FiltersCopy {
    const val TITLE = "Filters"
    const val FREE_SECTION = "Who you see"
    const val AGE = "Age"
    const val DISTANCE = "How far"
    const val INTENT = "Looking for"
    const val INTENT_ANY = "Leave all off to see everyone."
    const val PASS_SECTION = "With Premium"
    const val VERIFIED_ONLY = "Verified people only"
    const val VERIFIED_ONLY_BODY = "Only people who passed the face check."
    const val HEIGHT = "Height"
    const val ANY_HEIGHT = "Any height"
    const val LANGUAGES = "Speaks"
    const val SAVE = "Save filters"
    const val LOAD_FAILED = "Filters didn't load"
    const val LOCKED_BODY =
        "These filters come with a Premium pass. Anything you set before is kept, and it works again while you hold a pass."
    const val SEE_PREMIUM = "See Premium"
    const val CLEAR = "Clear these filters"
    const val UPSELL_TITLE = "Filters with Premium"
    const val UPSELL_BODY =
        "A Premium pass lets you narrow Pulse by verified profiles, height, languages and lifestyle. Age, distance and what people are looking for stay free."
    const val NOT_NOW = "Not now"

    // Mechanic M12 — dealbreakers.
    const val DEALBREAKER = "Dealbreaker"
    const val DEALBREAKER_BODY = "Only people who fit this will see you, too."
    const val DEALBREAKER_LOCKED = "Dealbreakers on this come with a Premium pass."
    const val DEALBREAKERS_GONE = "Dealbreakers aren't available right now, so nothing was changed."
}

/** Filters. [onOpenPremium] is where the upsell leads; a save closes the screen and the deck reloads. */
@Composable
fun FiltersScreen(onBack: () -> Unit, onOpenPremium: () -> Unit, viewModel: FiltersViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.savedCount) { if (state.savedCount > 0) onBack() }

    DatingScreen(
        title = FiltersCopy.TITLE,
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.phase == FiltersPhase.READY) {
                BottomAction(label = FiltersCopy.SAVE, onClick = viewModel::save, enabled = !state.saving, loading = state.saving)
            }
        },
    ) { padding ->
        when (state.phase) {
            FiltersPhase.LOADING -> LoadingPane()
            FiltersPhase.FAILED -> MessagePane(
                title = FiltersCopy.LOAD_FAILED,
                body = DatingCopy.GENERIC,
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
            )
            FiltersPhase.READY -> FiltersForm(state, viewModel, padding)
        }
    }

    if (state.upsell) {
        ConfirmDialog(
            title = FiltersCopy.UPSELL_TITLE,
            body = FiltersCopy.UPSELL_BODY,
            confirmLabel = FiltersCopy.SEE_PREMIUM,
            dismissLabel = FiltersCopy.NOT_NOW,
            onConfirm = {
                viewModel.dismissUpsell()
                onOpenPremium()
            },
            onDismiss = viewModel::dismissUpsell,
        )
    }
}

@Composable
private fun FiltersForm(state: FiltersUiState, viewModel: FiltersViewModel, padding: PaddingValues) {
    val enabled = !state.saving
    LazyColumn(contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { SectionLabel(FiltersCopy.FREE_SECTION) }
        item { AgeCard(state, enabled, viewModel) }
        item { DistanceCard(state, enabled, viewModel) }
        item { IntentCard(state, enabled, viewModel) }
        val options = state.options
        if (state.flagOn && options != null) passSection(state, options, enabled, viewModel)
    }
}

@Composable
private fun AgeCard(state: FiltersUiState, enabled: Boolean, viewModel: FiltersViewModel) {
    val draft = state.draft
    val shownMax = draft.maxAge.coerceAtMost(FiltersRules.SLIDER_MAX_AGE)
    DatingCard {
        LabelledValue(FiltersCopy.AGE, FiltersRules.ageLabel(draft.minAge, shownMax))
        RangeSlider(
            value = draft.minAge.toFloat()..shownMax.toFloat(),
            onValueChange = { viewModel.setAges(it.start.roundToInt(), it.endInclusive.roundToInt()) },
            valueRange = FiltersRules.MIN_AGE.toFloat()..FiltersRules.SLIDER_MAX_AGE.toFloat(),
            steps = FiltersRules.SLIDER_MAX_AGE - FiltersRules.MIN_AGE - 1,
            enabled = enabled,
            colors = sliderColors(),
        )
        FieldError(state.fieldErrors[FiltersField.AGE])
        DealbreakerRow(state, Dealbreaker.AGE, enabled, viewModel)
    }
}

@Composable
private fun DistanceCard(state: FiltersUiState, enabled: Boolean, viewModel: FiltersViewModel) {
    val options = state.options
    DatingCard {
        Text(FiltersCopy.DISTANCE, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
        if (state.flagOn && options != null) {
            SingleOptionChips(
                options = options.distanceBuckets,
                selected = state.draft.distanceBucket,
                onSelect = viewModel::setDistanceBucket,
                enabled = enabled,
            )
        } else {
            // The flag is off: the older numeric distance, exactly as onboarding sets it.
            SingleOptionChips(
                options = DatingChoices.distances.map { ProfileOption(it.value.toString(), it.label) },
                selected = state.draft.distanceKm.toString(),
                onSelect = { viewModel.setDistanceKm(it?.toIntOrNull()) },
                enabled = enabled,
            )
        }
        FieldError(state.fieldErrors[FiltersField.DISTANCE])
        DealbreakerRow(state, Dealbreaker.DISTANCE, enabled, viewModel)
    }
}

@Composable
private fun IntentCard(state: FiltersUiState, enabled: Boolean, viewModel: FiltersViewModel) {
    DatingCard {
        Text(FiltersCopy.INTENT, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
        MultiOptionChips(
            options = DatingIntent.entries.map { ProfileOption(it.code, it.label) },
            selected = state.draft.intents,
            onToggle = viewModel::toggleIntent,
            enabled = enabled,
        )
        if (state.draft.intents.isEmpty()) InfoNote(FiltersCopy.INTENT_ANY)
        FieldError(state.fieldErrors[FiltersField.INTENT])
        DealbreakerRow(state, Dealbreaker.INTENT, enabled, viewModel)
    }
}

private fun LazyListScope.passSection(state: FiltersUiState, options: ProfileOptionsUi, enabled: Boolean, viewModel: FiltersViewModel) {
    item {
        SectionLabel(FiltersCopy.PASS_SECTION) {
            if (state.locked) Icon(UsIcons.Lock, contentDescription = "Needs Premium", tint = UsTheme.extended.textMuted, modifier = Modifier.size(16.dp))
        }
    }
    if (state.locked) {
        item {
            DatingCard {
                InfoNote(FiltersCopy.LOCKED_BODY)
                UsButton(text = FiltersCopy.SEE_PREMIUM, onClick = viewModel::showUpsell, modifier = Modifier.fillMaxWidth())
                if (!state.draft.pass.isEmpty) {
                    UsSecondaryButton(text = FiltersCopy.CLEAR, enabled = enabled, onClick = viewModel::clearPassFilters, modifier = Modifier.fillMaxWidth())
                }
            }
        }
    }
    // While locked the controls still show what is stored, and only clearing works.
    val pass = state.draft.pass
    item {
        DatingCard {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(FiltersCopy.VERIFIED_ONLY, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                    Text(FiltersCopy.VERIFIED_ONLY_BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                }
                Switch(
                    checked = pass.verifiedOnly,
                    onCheckedChange = viewModel::setVerifiedOnly,
                    enabled = enabled,
                    colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
                )
            }
            DealbreakerRow(state, Dealbreaker.VERIFIED, enabled, viewModel)
        }
    }
    item { HeightRangeCard(state, options, enabled, viewModel) }
    item {
        DatingCard {
            Text(FiltersCopy.LANGUAGES, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
            MultiOptionChips(options = options.languages, selected = pass.languages, onToggle = viewModel::toggleLanguage, enabled = enabled)
            FieldError(state.fieldErrors[FiltersField.LANGUAGES])
            DealbreakerRow(state, Dealbreaker.LANGUAGES, enabled, viewModel)
        }
    }
    LifestyleBasic.entries.forEach { basic ->
        item(key = "pass-${basic.field}") {
            DatingCard {
                Text(basic.title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                MultiOptionChips(
                    options = options.basicOptions(basic),
                    selected = pass.basic(basic),
                    onToggle = { viewModel.toggleBasic(basic, it) },
                    enabled = enabled,
                )
                FieldError(state.fieldErrors[FiltersRules.fieldOf(basic)])
                DealbreakerRow(state, Dealbreaker.of(basic), enabled, viewModel)
            }
        }
    }
}

@Composable
private fun HeightRangeCard(state: FiltersUiState, options: ProfileOptionsUi, enabled: Boolean, viewModel: FiltersViewModel) {
    val range = options.heightRange
    val pass = state.draft.pass
    val any = pass.minHeightCm == null && pass.maxHeightCm == null
    DatingCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            LabelledValue(FiltersCopy.HEIGHT, FiltersRules.heightLabel(pass.minHeightCm, pass.maxHeightCm), Modifier.weight(1f))
            Switch(
                checked = !any,
                onCheckedChange = { on -> if (on) viewModel.setHeightRange(range.first, range.last) else viewModel.setHeightRange(null, null) },
                enabled = enabled,
                colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
            )
        }
        if (!any) {
            val low = (pass.minHeightCm ?: range.first).coerceIn(range)
            val high = (pass.maxHeightCm ?: range.last).coerceIn(low, range.last)
            RangeSlider(
                value = low.toFloat()..high.toFloat(),
                onValueChange = { viewModel.setHeightRange(it.start.roundToInt(), it.endInclusive.roundToInt()) },
                valueRange = range.first.toFloat()..range.last.toFloat(),
                steps = (range.last - range.first - 1).coerceAtLeast(0),
                enabled = enabled && !state.locked,
                colors = sliderColors(),
            )
        }
        FieldError(state.fieldErrors[FiltersField.HEIGHT])
        DealbreakerRow(state, Dealbreaker.HEIGHT, enabled, viewModel)
    }
}

/**
 * Mechanic M12: the "Dealbreaker" switch under a set preference, with its one
 * line. Nothing at all while the mechanic is off or the preference is unset.
 * A pass one without a pass carries the lock; switching it on opens the upsell.
 */
@Composable
private fun DealbreakerRow(state: FiltersUiState, dealbreaker: Dealbreaker, enabled: Boolean, viewModel: FiltersViewModel) {
    if (!state.showsDealbreaker(dealbreaker)) return
    val on = dealbreaker in state.draft.dealbreakers
    val locked = state.dealbreakerLocked(dealbreaker) && !on
    HorizontalDivider(color = UsTheme.extended.borderSubtle, modifier = Modifier.padding(vertical = UsTheme.spacing.xs))
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        if (locked) {
            Icon(UsIcons.Lock, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(16.dp))
        }
        Column(Modifier.weight(1f)) {
            Text(FiltersCopy.DEALBREAKER, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
            Text(
                if (locked) FiltersCopy.DEALBREAKER_LOCKED else FiltersCopy.DEALBREAKER_BODY,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Switch(
            checked = on,
            onCheckedChange = { viewModel.setDealbreaker(dealbreaker, it) },
            enabled = enabled,
            colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
        )
    }
}

@Composable
private fun LabelledValue(title: String, value: String, modifier: Modifier = Modifier) {
    Column(modifier) {
        Text(title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
    }
}

@Composable
private fun sliderColors() = SliderDefaults.colors(
    thumbColor = UsTheme.extended.accentSolid,
    activeTrackColor = UsTheme.extended.accentSolid,
)
