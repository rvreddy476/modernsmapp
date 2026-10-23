package com.us.android.feature.memberships.creator

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.EligibilityItem
import com.us.android.core.memberships.data.LevelRules
import com.us.android.core.memberships.data.MembershipEligibility
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.membershipCode
import com.us.android.core.memberships.data.membershipRefusal
import com.us.android.core.memberships.data.notEligibleItems
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** A level being written: a new one ([levelId] null) or an edit. Text as typed; checked by [LevelRules]. */
data class LevelDraft(
    val levelId: String? = null,
    val name: String = "",
    val price: String = "",
    /** One perk per line, as typed. */
    val perks: String = "",
    val problems: List<String> = emptyList(),
    val saving: Boolean = false,
    val error: String? = null,
) {
    val perkLines: List<String> get() = perks.lines()
    val isNew: Boolean get() = levelId == null
}

data class CreatorMembershipsUiState(
    val loading: Boolean = true,
    val loadError: String? = null,
    /** Memberships are switched off on this server. */
    val unavailable: String? = null,
    /** Null when it could not be read; the server still checks it on the first level. */
    val eligibility: MembershipEligibility? = null,
    val levels: List<MembershipLevel> = emptyList(),
    val draft: LevelDraft? = null,
    /** A level whose on/off switch is on its way. */
    val busyLevelId: String? = null,
    val message: String? = null,
) {
    val activeCount: Int get() = levels.count { it.isActive }

    /**
     * A new level may be started: fewer than three active, and either the
     * creator already offers memberships or is eligible to start. With
     * eligibility unknown the form is offered; the server decides.
     */
    val canAddLevel: Boolean
        get() = activeCount < LevelRules.MAX_ACTIVE_LEVELS &&
            (levels.isNotEmpty() || eligibility?.eligible != false)

    val totalMembers: Int get() = levels.sumOf { it.memberCount }
}

/**
 * The creator's Memberships page (paid memberships, 2026-10-23): whether
 * they may offer memberships and what is missing, in plain words; their
 * levels with member counts; and the form to add, edit or turn a level off,
 * refusing here exactly what the server would refuse.
 */
@HiltViewModel
class CreatorMembershipsViewModel @Inject constructor(
    private val repository: MembershipsRepository,
    private val session: SessionStateProvider,
) : ViewModel() {

    private val _state = MutableStateFlow(CreatorMembershipsUiState())
    val state: StateFlow<CreatorMembershipsUiState> = _state.asStateFlow()

    private val ownId: String
        get() = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    init {
        viewModelScope.launch { load() }
    }

    fun refresh() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch { load() }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun startNew() {
        if (!_state.value.canAddLevel) return
        _state.update { it.copy(draft = LevelDraft()) }
    }

    fun startEdit(level: MembershipLevel) {
        _state.update {
            it.copy(
                draft = LevelDraft(
                    levelId = level.id,
                    name = level.name,
                    price = (level.pricePaise / PAISE_PER_RUPEE).toString(),
                    perks = level.perks.joinToString("\n"),
                ),
            )
        }
    }

    fun cancelDraft() = _state.update { it.copy(draft = null) }

    fun editDraft(name: String? = null, price: String? = null, perks: String? = null) {
        _state.update { s ->
            val d = s.draft ?: return@update s
            s.copy(
                draft = d.copy(
                    name = name ?: d.name,
                    price = price ?: d.price,
                    perks = perks ?: d.perks,
                    problems = emptyList(),
                    error = null,
                ),
            )
        }
    }

    fun saveDraft() {
        val draft = _state.value.draft ?: return
        if (draft.saving) return
        val problems = LevelRules.problems(draft.name, draft.price, draft.perkLines)
        if (problems.isNotEmpty()) {
            _state.update { it.copy(draft = draft.copy(problems = problems)) }
            return
        }
        val price = requireNotNull(LevelRules.parseRupees(draft.price))
        _state.update { it.copy(draft = draft.copy(saving = true, problems = emptyList(), error = null)) }
        viewModelScope.launch {
            val result = if (draft.isNew) {
                repository.createLevel(draft.name, price, draft.perkLines)
            } else {
                repository.updateLevel(
                    levelId = requireNotNull(draft.levelId),
                    name = draft.name,
                    pricePaise = price,
                    perks = draft.perkLines,
                )
            }
            when (result) {
                is AppResult.Success -> {
                    val saved = if (draft.isNew) "Level added." else "Level saved."
                    _state.update { it.copy(draft = null, message = saved) }
                    load()
                }
                is AppResult.Failure -> {
                    val missing = result.error.notEligibleItems()
                    if (missing != null) {
                        _state.update {
                            it.copy(
                                draft = null,
                                eligibility = MembershipEligibility(eligible = false, missing = missing),
                                message = "You can't offer memberships yet.",
                            )
                        }
                    } else {
                        _state.update { s ->
                            s.copy(draft = s.draft?.copy(saving = false, error = membershipRefusal(result.error)))
                        }
                    }
                }
            }
        }
    }

    /** Turns a level off (no new members; existing ones keep it to the end of their month) or back on. */
    fun setActive(level: MembershipLevel, active: Boolean) {
        val s = _state.value
        if (s.busyLevelId != null || level.isActive == active) return
        if (active && !LevelRules.canAddActive(s.levels)) {
            _state.update { it.copy(message = "You can have up to ${LevelRules.MAX_ACTIVE_LEVELS} active levels.") }
            return
        }
        _state.update { it.copy(busyLevelId = level.id) }
        viewModelScope.launch {
            when (val result = repository.updateLevel(level.id, isActive = active)) {
                is AppResult.Success -> {
                    val done = if (active) "Level turned on." else "Level turned off."
                    _state.update { it.copy(busyLevelId = null, message = done) }
                    load()
                }
                is AppResult.Failure -> _state.update {
                    it.copy(busyLevelId = null, message = membershipRefusal(result.error))
                }
            }
        }
    }

    private suspend fun load() {
        val creatorId = ownId
        val (levels, eligibility) = kotlinx.coroutines.coroutineScope {
            val levels = async { repository.myLevels() }
            val eligibility = async { if (creatorId.isBlank()) null else repository.eligibility(creatorId) }
            levels.await() to eligibility.await()
        }
        when (levels) {
            is AppResult.Failure -> _state.update {
                if (levels.error.membershipCode() == NOT_ENABLED) {
                    it.copy(loading = false, unavailable = membershipRefusal(levels.error))
                } else {
                    it.copy(
                        loading = false,
                        loadError = membershipRefusal(levels.error, "Your memberships didn't load.")
                    )
                }
            }
            is AppResult.Success -> _state.update {
                it.copy(
                    loading = false,
                    loadError = null,
                    unavailable = null,
                    levels = levels.data.sortedWith(
                        compareByDescending<MembershipLevel> { l -> l.isActive }.thenBy { l -> l.pricePaise }
                    ),
                    eligibility = (eligibility as? AppResult.Success)?.data ?: it.eligibility,
                )
            }
        }
    }

    private companion object {
        const val NOT_ENABLED = "MEMBERSHIPS_NOT_ENABLED"
        const val PAISE_PER_RUPEE = 100L
    }
}

/** The page's lines for what is missing, in the server's order. */
internal fun missingLines(items: List<EligibilityItem>): List<String> = items.map { it.sentence }
