package com.us.android.feature.dating.profile

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.FieldRefusalDetailsDto
import com.us.android.feature.dating.network.UpsertProfileRequest
import com.us.android.feature.dating.ui.BottomAction
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.MultiOptionChips
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.SingleOptionChips
import com.us.android.feature.dating.ui.listPadding
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import kotlin.math.roundToInt

/*
 * "About me" (mechanic M6): up to the server's number of interests, a height
 * within its range, languages, and the four lifestyle basics. Optional
 * everywhere — reached from Privacy and settings, and offered beside the
 * prompts while photos are added — and never a step the gate waits on.
 */

/** A picker the server can refuse, by the `details.field` it names. */
enum class AboutMeField(val wire: String) {
    INTERESTS("interests"),
    LANGUAGES("language_prefs"),
    HEIGHT("height_cm"),
    DRINKING("drinking"),
    SMOKING("smoking"),
    EXERCISE("exercise"),
    DIET("diet"),
    ;

    companion object {
        fun fromWire(wire: String?): AboutMeField? = entries.firstOrNull { it.wire == wire }

        fun of(basic: LifestyleBasic): AboutMeField = when (basic) {
            LifestyleBasic.DRINKING -> DRINKING
            LifestyleBasic.SMOKING -> SMOKING
            LifestyleBasic.EXERCISE -> EXERCISE
            LifestyleBasic.DIET -> DIET
        }
    }
}

/** What the person has picked. Codes only; a basic that is null is "Prefer not to say". */
data class AboutMeDraft(
    val interests: List<String> = emptyList(),
    val heightCm: Int? = null,
    val languages: List<String> = emptyList(),
    val basics: Map<LifestyleBasic, String?> = emptyMap(),
) {
    fun basic(basic: LifestyleBasic): String? = basics[basic]
}

enum class AboutMePhase { LOADING, FAILED, READY }

data class AboutMeUiState(
    val phase: AboutMePhase = AboutMePhase.LOADING,
    val options: ProfileOptionsUi? = null,
    val draft: AboutMeDraft = AboutMeDraft(),
    /** What the server holds, so only a change is sent. */
    val saved: AboutMeDraft = AboutMeDraft(),
    val saving: Boolean = false,
    /** A refusal under the picker it belongs to. */
    val fieldErrors: Map<AboutMeField, String> = emptyMap(),
    val message: UsMessage? = null,
    /** Bumped on each save the server took; the screen then closes. */
    val savedCount: Int = 0,
) {
    val dirty: Boolean get() = draft != saved
}

@HiltViewModel
class AboutMeViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val options: ProfileOptionsStore,
    private val session: DatingSession,
) : ViewModel() {

    private val _state = MutableStateFlow(AboutMeUiState())
    val state: StateFlow<AboutMeUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.update { it.copy(phase = AboutMePhase.LOADING) }
        viewModelScope.launch {
            val lists = options.get()
            val profile = repository.profile()
            if (lists == null || profile !is DatingResult.Success) {
                val message = (profile as? DatingResult.Failure)?.let { DatingCopy.message(it.error) }
                _state.update { it.copy(phase = AboutMePhase.FAILED, message = message) }
                return@launch
            }
            profile.value?.let(session::setProfile)
            val draft = draftFrom(profile.value, lists)
            _state.update { AboutMeUiState(phase = AboutMePhase.READY, options = lists, draft = draft, saved = draft) }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun toggleInterest(code: String) = _state.update { s ->
        val lists = s.options ?: return@update s
        val picked = s.draft.interests
        when {
            code in picked -> s.edit(AboutMeField.INTERESTS) { copy(interests = picked - code) }
            lists.interestLabel(code) == null -> s
            picked.size >= lists.maxInterests ->
                s.copy(fieldErrors = s.fieldErrors + (AboutMeField.INTERESTS to AboutMeCopy.interestLimit(lists.maxInterests)))
            else -> s.edit(AboutMeField.INTERESTS) { copy(interests = picked + code) }
        }
    }

    fun toggleLanguage(code: String) = _state.update { s ->
        val lists = s.options ?: return@update s
        val picked = s.draft.languages
        when {
            code in picked -> s.edit(AboutMeField.LANGUAGES) { copy(languages = picked - code) }
            lists.languageLabel(code) == null -> s
            picked.size >= lists.maxLanguages ->
                s.copy(fieldErrors = s.fieldErrors + (AboutMeField.LANGUAGES to AboutMeCopy.languageLimit(lists.maxLanguages)))
            else -> s.edit(AboutMeField.LANGUAGES) { copy(languages = picked + code) }
        }
    }

    /** Kept inside the server's range. A height once saved can be changed but not removed: the server has no "unset". */
    fun setHeight(cm: Int) = _state.update { s ->
        val range = s.options?.heightRange ?: return@update s
        s.edit(AboutMeField.HEIGHT) { copy(heightCm = cm.coerceIn(range)) }
    }

    /** [code] null is "Prefer not to say", sent as `""`, which clears it. */
    fun setBasic(basic: LifestyleBasic, code: String?) = _state.update { s ->
        val lists = s.options ?: return@update s
        if (code != null && lists.basicLabel(basic, code) == null) return@update s
        s.edit(AboutMeField.of(basic)) { copy(basics = basics + (basic to code)) }
    }

    fun save() {
        val current = _state.value
        if (current.saving || current.phase != AboutMePhase.READY) return
        if (!current.dirty) {
            _state.update { it.copy(savedCount = it.savedCount + 1) }
            return
        }
        _state.update { it.copy(saving = true, fieldErrors = emptyMap()) }
        viewModelScope.launch {
            when (val result = repository.upsertProfile(requestFor(current.saved, current.draft))) {
                is DatingResult.Success -> {
                    session.setProfile(result.value)
                    _state.update {
                        it.copy(
                            saving = false,
                            saved = it.draft,
                            savedCount = it.savedCount + 1,
                            message = successMessage(AboutMeCopy.SAVED),
                        )
                    }
                }
                is DatingResult.Failure -> refused(result.error)
            }
        }
    }

    private fun refused(error: DatingError) {
        val field = if (error.code in FIELD_CODES) {
            AboutMeField.fromWire(error.detailsAs(repository.json, FieldRefusalDetailsDto.serializer())?.field)
        } else {
            null
        }
        val words = DatingCopy.forError(error, repository.json)
        _state.update {
            if (field != null) {
                it.copy(saving = false, fieldErrors = mapOf(field to words))
            } else {
                it.copy(saving = false, message = DatingCopy.message(error, repository.json))
            }
        }
    }

    /** Applies [change] to the draft and clears that picker's refusal. */
    private fun AboutMeUiState.edit(field: AboutMeField, change: AboutMeDraft.() -> AboutMeDraft): AboutMeUiState =
        copy(draft = draft.change(), fieldErrors = fieldErrors - field)

    companion object {
        val FIELD_CODES = setOf(
            "INVALID_INTEREST",
            "TOO_MANY_INTEREST",
            "INVALID_LANGUAGE",
            "TOO_MANY_LANGUAGE",
            "INVALID_HEIGHT",
            "INVALID_LIFESTYLE",
        )

        /**
         * The profile as the editor starts: only codes the lists still name,
         * so a save can never resend something the server would refuse.
         */
        fun draftFrom(profile: DatingProfileDto?, lists: ProfileOptionsUi): AboutMeDraft = AboutMeDraft(
            interests = profile?.interests.orEmpty().filter { lists.interestLabel(it) != null }.distinct().take(lists.maxInterests),
            heightCm = profile?.heightCm?.takeIf { it in lists.heightRange },
            languages = profile?.languagePrefs.orEmpty().filter { lists.languageLabel(it) != null }.distinct().take(lists.maxLanguages),
            basics = LifestyleBasic.entries.associateWith { basic ->
                profile?.basicCode(basic)?.takeIf { lists.basicLabel(basic, it) != null }
            },
        )

        /**
         * Only what changed: a list goes whole (`[]` clears it), a basic goes as
         * its code or `""` for "Prefer not to say", and an untouched field is
         * left out, so a language written before the fixed list is not dropped
         * by an edit to something else.
         */
        fun requestFor(saved: AboutMeDraft, draft: AboutMeDraft): UpsertProfileRequest = UpsertProfileRequest(
            interests = draft.interests.takeIf { it != saved.interests },
            languagePrefs = draft.languages.takeIf { it != saved.languages },
            heightCm = draft.heightCm?.takeIf { it != saved.heightCm },
            drinking = changedBasic(saved, draft, LifestyleBasic.DRINKING),
            smoking = changedBasic(saved, draft, LifestyleBasic.SMOKING),
            exercise = changedBasic(saved, draft, LifestyleBasic.EXERCISE),
            diet = changedBasic(saved, draft, LifestyleBasic.DIET),
        )

        private fun changedBasic(saved: AboutMeDraft, draft: AboutMeDraft, basic: LifestyleBasic): String? {
            val now = draft.basic(basic)
            if (now == saved.basic(basic)) return null
            return now ?: ""
        }

        private fun DatingProfileDto.basicCode(basic: LifestyleBasic): String? = when (basic) {
            LifestyleBasic.DRINKING -> drinking
            LifestyleBasic.SMOKING -> smoking
            LifestyleBasic.EXERCISE -> exercise
            LifestyleBasic.DIET -> diet
        }?.trim()?.takeIf { it.isNotEmpty() }
    }
}

/** The words of "About me". Our own. */
object AboutMeCopy {
    const val TITLE = "About me"
    const val INTRO = "All optional. What you add here shows on your profile, so people have more to go on."
    const val INTERESTS = "Interests"
    const val HEIGHT = "Height"
    const val ADD_HEIGHT = "Add my height"
    const val LANGUAGES = "Languages I speak"
    const val BASICS = "Lifestyle"
    const val PREFER_NOT = "Prefer not to say"
    const val SAVE = "Save"
    const val SAVED = "Saved. It's on your profile now."
    const val LOAD_FAILED_TITLE = "This didn't load"
    const val HEIGHT_FOREVER = "Once added, your height can be changed but not removed."

    fun interestLimit(max: Int) = "You can pick up to $max interests."

    fun languageLimit(max: Int) = "You can pick up to $max languages."

    fun count(picked: Int, max: Int) = "$picked of $max"
}

/** "About me". [onDone] closes it after a save; leaving without saving keeps what the server has. */
@Composable
fun AboutMeScreen(onBack: () -> Unit, onDone: () -> Unit = onBack, viewModel: AboutMeViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.savedCount) { if (state.savedCount > 0) onDone() }

    DatingScreen(
        title = AboutMeCopy.TITLE,
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.phase == AboutMePhase.READY) {
                BottomAction(label = AboutMeCopy.SAVE, onClick = viewModel::save, enabled = !state.saving, loading = state.saving)
            }
        },
    ) { padding ->
        val lists = state.options
        when {
            state.phase == AboutMePhase.LOADING -> LoadingPane()
            state.phase == AboutMePhase.FAILED || lists == null -> MessagePane(
                title = AboutMeCopy.LOAD_FAILED_TITLE,
                body = DatingCopy.GENERIC,
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
            )
            else -> AboutMeForm(state, lists, viewModel, padding)
        }
    }
}

@Composable
private fun AboutMeForm(
    state: AboutMeUiState,
    lists: ProfileOptionsUi,
    viewModel: AboutMeViewModel,
    padding: androidx.compose.foundation.layout.PaddingValues,
) {
    val enabled = !state.saving
    LazyColumn(contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { InfoNote(AboutMeCopy.INTRO) }
        item {
            SectionLabel(AboutMeCopy.INTERESTS) {
                Text(AboutMeCopy.count(state.draft.interests.size, lists.maxInterests), style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textMuted)
            }
        }
        item {
            DatingCard {
                MultiOptionChips(
                    options = lists.interests,
                    selected = state.draft.interests,
                    onToggle = viewModel::toggleInterest,
                    enabled = enabled,
                    atLimit = state.draft.interests.size >= lists.maxInterests,
                )
                FieldError(state.fieldErrors[AboutMeField.INTERESTS])
            }
        }
        item { SectionLabel(AboutMeCopy.HEIGHT) }
        item { HeightCard(state, lists, enabled, viewModel::setHeight) }
        item {
            SectionLabel(AboutMeCopy.LANGUAGES) {
                Text(AboutMeCopy.count(state.draft.languages.size, lists.maxLanguages), style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textMuted)
            }
        }
        item {
            DatingCard {
                MultiOptionChips(
                    options = lists.languages,
                    selected = state.draft.languages,
                    onToggle = viewModel::toggleLanguage,
                    enabled = enabled,
                    atLimit = state.draft.languages.size >= lists.maxLanguages,
                )
                FieldError(state.fieldErrors[AboutMeField.LANGUAGES])
            }
        }
        item { SectionLabel(AboutMeCopy.BASICS) }
        LifestyleBasic.entries.forEach { basic ->
            item(key = basic.field) {
                DatingCard {
                    Text(basic.title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                    SingleOptionChips(
                        options = lists.basicOptions(basic),
                        selected = state.draft.basic(basic),
                        onSelect = { viewModel.setBasic(basic, it) },
                        enabled = enabled,
                        noneLabel = AboutMeCopy.PREFER_NOT,
                    )
                    FieldError(state.fieldErrors[AboutMeField.of(basic)])
                }
            }
        }
    }
}

@Composable
private fun HeightCard(state: AboutMeUiState, lists: ProfileOptionsUi, enabled: Boolean, onHeight: (Int) -> Unit) {
    val range = lists.heightRange
    DatingCard {
        val height = state.draft.heightCm
        if (height == null) {
            UsSecondaryButton(
                text = AboutMeCopy.ADD_HEIGHT,
                enabled = enabled,
                onClick = { onHeight(DEFAULT_HEIGHT.coerceIn(range)) },
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(lists.heightLabel(height).orEmpty(), style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
            }
            Slider(
                value = height.toFloat(),
                onValueChange = { onHeight(it.roundToInt()) },
                valueRange = range.first.toFloat()..range.last.toFloat(),
                steps = (range.last - range.first - 1).coerceAtLeast(0),
                enabled = enabled,
                colors = SliderDefaults.colors(thumbColor = UsTheme.extended.accentSolid, activeTrackColor = UsTheme.extended.accentSolid),
            )
            InfoNote(AboutMeCopy.HEIGHT_FOREVER)
        }
        FieldError(state.fieldErrors[AboutMeField.HEIGHT])
    }
}

@Composable
internal fun FieldError(text: String?) {
    text?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.statusDanger) }
}

private const val DEFAULT_HEIGHT = 165
