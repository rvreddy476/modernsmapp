package com.us.android.feature.dating.home

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingIntent
import com.us.android.feature.dating.DistanceBucket
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.network.CardPhotoDto
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.ProfileDetailDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.photos.PhotoRules
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * What a viewer may read about someone BEFORE matching — the block the server
 * sends on a deck card, an incoming spark and `GET /people/:userId`.
 *
 * Every member may be empty: the server omits what a person has not written,
 * and the screens render NOTHING for an empty one — no blank heading. The
 * sealed fields (religion, community, exact location, birth date, a hidden
 * last-active) are not here and are never asked for.
 */
data class PersonDetailUi(
    val bio: String?,
    val prompts: List<PromptUi>,
    val languages: List<String>,
    /** The approved gallery, primary first, each already resolved to its own variant. */
    val gallery: List<GalleryPhotoUi>,
) {
    val isEmpty: Boolean get() = bio == null && prompts.isEmpty() && languages.isEmpty() && gallery.isEmpty()
}

/** One catalogue question and this person's answer. */
data class PromptUi(val promptId: Int, val question: String, val answer: String)

/**
 * One gallery photo, already resolved to the variant ITS OWN `state` allows.
 *
 * The resolution happens here, once, so no composable can reach a full image
 * from a blurred entry: a gallery that mixes a public photo with a match_only
 * one carries a `/full` url beside a `/blurred` one, exactly as the server said.
 */
data class GalleryPhotoUi(val photoId: String, val url: String)

/**
 * The detail block in display terms, or null when the person has written
 * nothing at all (the server omits the whole block then, and so do we).
 */
internal fun ProfileDetailDto?.toUi(urls: DatingPhotoUrls): PersonDetailUi? {
    val dto = this ?: return null
    val detail = PersonDetailUi(
        bio = dto.bio.trim().takeIf { it.isNotBlank() },
        prompts = dto.prompts.mapNotNull { it.toUi() },
        languages = dto.languages.map { it.trim() }.filter { it.isNotBlank() },
        gallery = dto.photos.mapNotNull { it.toUi(urls) },
    )
    return detail.takeIf { !it.isEmpty }
}

private fun com.us.android.feature.dating.network.DetailPromptDto.toUi(): PromptUi? {
    val q = question.trim()
    val a = answer.trim()
    return if (q.isBlank() || a.isBlank()) null else PromptUi(promptId, q, a)
}

/**
 * A gallery entry through the fail-closed rule: only the exact state `full`
 * yields the full variant, so a blank, unknown or future state is blurred.
 */
private fun CardPhotoDto.toUi(urls: DatingPhotoUrls): GalleryPhotoUi? {
    val resolved = urls.forGalleryPhoto(this) ?: return null
    val id = id.takeIf { it.isNotBlank() } ?: PhotoRules.photoIdOf(url) ?: return null
    return GalleryPhotoUi(photoId = id, url = resolved)
}

/** Someone else's card, on its own screen, with the same pre-match detail the deck shows. */
data class PersonUi(
    val userId: String,
    val name: String?,
    val age: Int?,
    /** A city name, or null. Never coordinates. */
    val city: String?,
    /** An intent label ("Casual"), or null for blank and unknown codes. */
    val intent: String?,
    /** A bucket label, or null. Never a number. */
    val distance: String?,
    /**
     * The server's coarse last-active label, or null when it sent none — which
     * is what a hidden last-active looks like. Null renders NOTHING: the screen
     * never substitutes "Unknown" or any other stand-in.
     */
    val lastActive: String?,
    val verified: Boolean,
    val photoUrl: String?,
    val photoId: String?,
    val detail: PersonDetailUi?,
)

sealed interface PersonState {
    data object Loading : PersonState

    data class Loaded(val person: PersonUi) : PersonState

    /** The viewer may not see them (404), or they are gone. */
    data class Gone(val message: String) : PersonState

    data class Failed(val message: String) : PersonState
}

/**
 * `GET /people/:userId` on its own screen: the person a spark came from, with
 * enough detail to decide. Read-only — the decision itself stays on the spark.
 */
@HiltViewModel
class PersonViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DatingRepository,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    private val userId: String = checkNotNull(savedStateHandle["userId"])

    private val _state = MutableStateFlow<PersonState>(PersonState.Loading)
    val state: StateFlow<PersonState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.value = when (val result = repository.person(userId)) {
                is DatingResult.Success -> result.value?.let { PersonState.Loaded(it.toUi()) }
                    ?: PersonState.Gone("They're no longer available.")
                is DatingResult.Failure -> PersonState.Failed(DatingCopy.forError(result.error))
            }
        }
    }

    private fun DatingPersonDto.toUi(): PersonUi = PersonUi(
        userId = userId.takeIf { it.isNotBlank() } ?: this@PersonViewModel.userId,
        name = firstName.takeIf { it.isNotBlank() },
        age = age.takeIf { it > 0 },
        city = city.trim().takeIf { it.isNotBlank() },
        intent = DatingIntent.labelFor(intent),
        distance = DistanceBucket.labelFor(distanceBucket),
        lastActive = lastActiveLabel?.trim()?.takeIf { it.isNotBlank() },
        verified = verified,
        photoUrl = urls.forPerson(this),
        photoId = PhotoRules.photoIdOf(primaryPhotoUrl),
        detail = detail.toUi(urls),
    )
}
