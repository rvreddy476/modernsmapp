package com.us.android.feature.dating.profile

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.network.OptionDto
import com.us.android.feature.dating.network.ProfileOptionsDto
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/*
 * Mechanic M6: the fixed lists behind interests, languages, height, the four
 * lifestyle basics and the distance filter, as `GET /profile/options` serves
 * them. The SERVER's labels are the only words the app shows for a code; a code
 * the lists do not name is shown nowhere.
 */

/** One choice: what goes on the wire, and what the person reads. */
data class ProfileOption(val code: String, val label: String)

/** The four single-choice basics, in the order a profile shows them. */
enum class LifestyleBasic(val field: String, val title: String) {
    DRINKING("drinking", "Drinking"),
    SMOKING("smoking", "Smoking"),
    EXERCISE("exercise", "Exercise"),
    DIET("diet", "Diet"),
}

/** The option lists in display terms. Built from the server's answer, with safe limits when a member is missing. */
data class ProfileOptionsUi(
    val interests: List<ProfileOption>,
    val maxInterests: Int,
    val languages: List<ProfileOption>,
    val maxLanguages: Int,
    val heightRange: IntRange,
    val basics: Map<LifestyleBasic, List<ProfileOption>>,
    val distanceBuckets: List<ProfileOption>,
) {
    fun basicOptions(basic: LifestyleBasic): List<ProfileOption> = basics[basic].orEmpty()

    fun interestLabel(code: String?): String? = interests.labelOf(code)

    fun languageLabel(code: String?): String? = languages.labelOf(code)

    fun basicLabel(basic: LifestyleBasic, code: String?): String? = basicOptions(basic).labelOf(code)

    fun distanceLabel(code: String?): String? = distanceBuckets.labelOf(code)

    /** "172 cm", or null when the height is unset or outside the server's range. */
    fun heightLabel(cm: Int?): String? = cm?.takeIf { it in heightRange }?.let { "$it cm" }

    /**
     * A person's basics, codes → labels. An unknown code, a blank one or a
     * height outside the range contributes nothing: a future or stale value
     * never reaches the screen as raw wire text.
     */
    fun basicsOf(codes: ProfileBasicsCodes): ProfileBasicsUi = ProfileBasicsUi(
        interests = codes.interests.distinct().mapNotNull { interestLabel(it) },
        height = heightLabel(codes.heightCm),
        lines = LifestyleBasic.entries.mapNotNull { basic ->
            basicLabel(basic, codes.of(basic))?.let { BasicLine(basic.title, it) }
        },
    )

    /**
     * Languages for a card: a known code becomes its label. Anything else is
     * text the person wrote before languages became a fixed list, and is kept
     * as written; a blank entry is dropped.
     */
    fun languageLabels(values: List<String>): List<String> =
        values.map { it.trim() }.filter { it.isNotEmpty() }.map { languageLabel(it) ?: it }.distinct()

    companion object {
        const val DEFAULT_MAX_INTERESTS = 10
        const val DEFAULT_MAX_LANGUAGES = 8
        const val DEFAULT_MIN_HEIGHT = 120
        const val DEFAULT_MAX_HEIGHT = 230

        fun from(dto: ProfileOptionsDto): ProfileOptionsUi {
            val min = dto.heightCm.min.takeIf { it > 0 } ?: DEFAULT_MIN_HEIGHT
            val max = dto.heightCm.max.takeIf { it >= min } ?: DEFAULT_MAX_HEIGHT.coerceAtLeast(min)
            return ProfileOptionsUi(
                interests = dto.interests.clean(),
                maxInterests = dto.maxInterests.takeIf { it > 0 } ?: DEFAULT_MAX_INTERESTS,
                languages = dto.languages.clean(),
                maxLanguages = dto.maxLanguages.takeIf { it > 0 } ?: DEFAULT_MAX_LANGUAGES,
                heightRange = min..max,
                basics = mapOf(
                    LifestyleBasic.DRINKING to dto.drinking.clean(),
                    LifestyleBasic.SMOKING to dto.smoking.clean(),
                    LifestyleBasic.EXERCISE to dto.exercise.clean(),
                    LifestyleBasic.DIET to dto.diet.clean(),
                ),
                distanceBuckets = dto.distanceBuckets.clean(),
            )
        }
    }
}

/** An entry needs both a code and a label; a repeated code keeps its first label. */
private fun List<OptionDto>.clean(): List<ProfileOption> =
    mapNotNull { o ->
        val code = o.code.trim()
        val label = o.label.trim()
        if (code.isEmpty() || label.isEmpty()) null else ProfileOption(code, label)
    }.distinctBy { it.code }

private fun List<ProfileOption>.labelOf(code: String?): String? {
    val wanted = code?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    return firstOrNull { it.code == wanted }?.label
}

/** A person's interests and basics as the server sent them: CODES, unresolved. */
data class ProfileBasicsCodes(
    val interests: List<String> = emptyList(),
    val heightCm: Int? = null,
    val drinking: String? = null,
    val smoking: String? = null,
    val exercise: String? = null,
    val diet: String? = null,
) {
    val isEmpty: Boolean
        get() = interests.isEmpty() && heightCm == null &&
            LifestyleBasic.entries.all { of(it) == null }

    fun of(basic: LifestyleBasic): String? = when (basic) {
        LifestyleBasic.DRINKING -> drinking
        LifestyleBasic.SMOKING -> smoking
        LifestyleBasic.EXERCISE -> exercise
        LifestyleBasic.DIET -> diet
    }
}

/** One basic on a card: "Drinking" · "Socially". */
data class BasicLine(val title: String, val label: String)

/** A person's basics, ready to draw: interest labels, a height line, and one line per basic they set. */
data class ProfileBasicsUi(
    val interests: List<String>,
    val height: String?,
    val lines: List<BasicLine>,
) {
    val isEmpty: Boolean get() = interests.isEmpty() && height == null && lines.isEmpty()
}

/**
 * The option lists, read once and kept for the session. A failed read keeps
 * nothing, so the next screen that needs them asks again; until then the
 * screens draw no interests or basics rather than codes.
 */
@Singleton
class ProfileOptionsStore @Inject constructor(
    private val repository: DatingRepository,
) {
    private val mutex = Mutex()
    private val _options = MutableStateFlow<ProfileOptionsUi?>(null)
    val options: StateFlow<ProfileOptionsUi?> = _options.asStateFlow()

    /** The cached lists, or one read of them. Null when the read failed. */
    suspend fun get(): ProfileOptionsUi? {
        _options.value?.let { return it }
        return mutex.withLock {
            _options.value ?: when (val result = repository.profileOptions()) {
                is DatingResult.Success -> ProfileOptionsUi.from(result.value).also { _options.value = it }
                is DatingResult.Failure -> null
            }
        }
    }
}

/** Gives a composable the session's option lists, reading them the first time. */
@HiltViewModel
class ProfileOptionsViewModel @Inject constructor(
    private val store: ProfileOptionsStore,
) : ViewModel() {
    val options: StateFlow<ProfileOptionsUi?> = store.options

    init {
        viewModelScope.launch { store.get() }
    }
}

/** The option lists, or null until they have loaded. Cards draw no interests or basics while null. */
@Composable
fun rememberProfileOptions(viewModel: ProfileOptionsViewModel = hiltViewModel()): ProfileOptionsUi? {
    val options by viewModel.options.collectAsStateWithLifecycle()
    return options
}
