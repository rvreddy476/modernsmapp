package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.profile.data.EditProfileField
import com.us.android.core.profile.data.EditableProfile
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Edits the signed-in user's profile.
 *
 * The whole design follows from one captured fact: `PUT /v1/profiles/me` is a
 * full replacement, and an omitted key erases the stored value. So the form is
 * never "the fields the user changed" — it is always a complete snapshot,
 * seeded from `/me` before anything is editable and sent back in full on save.
 * See `UpdateProfileRequest` for how that invariant is enforced on the wire.
 */
@HiltViewModel
class EditProfileViewModel @Inject constructor(
    private val repository: ProfileRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<EditProfileUiState>(EditProfileUiState.Loading)
    val state: StateFlow<EditProfileUiState> = _state.asStateFlow()

    init {
        load()
    }

    /**
     * Fetches the snapshot the form is seeded from.
     *
     * Nothing is editable until this succeeds. A form that opened empty on a
     * failed load would let the user save blanks over their real profile.
     */
    fun load() {
        _state.value = EditProfileUiState.Loading
        viewModelScope.launch {
            when (val result = repository.getOwnProfile()) {
                is AppResult.Failure -> _state.value = EditProfileUiState.Error(
                    message = ProfileErrorText.forLoad(result.error),
                    retryable = ProfileErrorText.isRetryable(result.error),
                )

                is AppResult.Success -> {
                    val snapshot = EditableProfile.from(result.data)
                    // `original` and `form` start identical: loaded, and not
                    // yet dirty.
                    _state.value = EditProfileUiState.Editing(
                        original = snapshot,
                        form = snapshot,
                    )
                }
            }
        }
    }

    /**
     * One field changed on the complete snapshot.
     *
     * Note what this does NOT do: accumulate a set of touched fields. There is
     * no such set anywhere in this feature, because the save path has no use
     * for one — it sends everything either way.
     */
    fun onFieldChange(field: EditProfileField, value: String) = _state.update { state ->
        val editing = state as? EditProfileUiState.Editing ?: return@update state
        editing.copy(
            form = editing.form.with(field, value),
            // Clearing this field's error as the user types keeps the marker
            // attached to the value that caused it, rather than to the input.
            fieldErrors = editing.fieldErrors - field,
            message = null,
        )
    }

    fun onMemberSinceBadgeChange(value: Boolean) = _state.update { state ->
        val editing = state as? EditProfileUiState.Editing ?: return@update state
        editing.copy(form = editing.form.withMemberSinceBadge(value), message = null)
    }

    fun save() {
        val current = _state.value as? EditProfileUiState.Editing ?: return
        if (!current.canSave) return

        val errors = validate(current.form)
        if (errors.isNotEmpty()) {
            // A summary alongside the inline markers: on a form this tall the
            // offending field is often scrolled out of view, so an inline
            // error alone reads as "the button did nothing".
            _state.value = current.copy(
                fieldErrors = errors,
                message = UsMessage(
                    text = "Some details need fixing — check the highlighted fields.",
                    type = UsMessageType.Error,
                ),
            )
            return
        }

        _state.value = current.copy(isSaving = true, fieldErrors = emptyMap(), message = null)
        viewModelScope.launch {
            // `current.form` is the COMPLETE snapshot, not a diff against
            // `current.original`. Sending a diff here is the one change that
            // would silently clear every field the user did not touch.
            val result = repository.updateProfile(current.form)
            _state.update { state ->
                val editing = state as? EditProfileUiState.Editing ?: return@update state
                when (result) {
                    // Re-seeded from the RESPONSE, not from what was sent: the
                    // server is the authority on what it stored, and this also
                    // leaves the form clean rather than permanently dirty.
                    is AppResult.Success -> {
                        val stored = EditableProfile.from(result.data)
                        editing.copy(
                            original = stored,
                            form = stored,
                            isSaving = false,
                            saved = true,
                        )
                    }

                    // The form is left exactly as the user typed it. A rejected
                    // request changed nothing server-side, so retrying sends
                    // the same complete snapshot again.
                    is AppResult.Failure -> editing.copy(
                        isSaving = false,
                        message = UsMessage(
                            text = ProfileErrorText.forSave(result.error),
                            type = if (result.error.isTransient()) {
                                UsMessageType.Warning
                            } else {
                                UsMessageType.Error
                            },
                        ),
                    )
                }
            }
        }
    }

    fun dismissMessage() = _state.update { state ->
        (state as? EditProfileUiState.Editing)?.copy(message = null) ?: state
    }

    /**
     * Client-side pre-flight.
     *
     * Every rule below is CLIENT-ONLY. The capture found no server-side
     * validation on any of these fields — `{}` was accepted with a `200` — so
     * nothing here is mirroring a backend gate the way registration does.
     * They exist to catch input that would be stored happily and then fail to
     * render, or that the user plainly did not mean.
     *
     * Blank is never an error. The server permits every one of these fields to
     * be empty, real accounts reach that state, and rejecting it would make
     * clearing a field impossible on the only screen that can clear it.
     */
    private fun validate(form: EditableProfile): Map<EditProfileField, String> = buildMap {
        PROFILE_TEXT_LIMITS.forEach { (field, limit) ->
            if (form.value(field).length > limit) {
                put(field, "Use $limit characters or fewer")
            }
        }
        if (form.statusEmoji.codePointCount(0, form.statusEmoji.length) > MAX_STATUS_EMOJI_CODEPOINTS) {
            put(EditProfileField.STATUS_EMOJI, "Use one short emoji")
        }
        if (form.ctaUrl.isNotBlank() && !WEBSITE_PATTERN.matches(form.ctaUrl.trim())) {
            put(EditProfileField.CTA_URL, "Enter an http or https web address")
        }
        if (form.timezone.isNotBlank() && !TIMEZONE_PATTERN.matches(form.timezone.trim())) {
            put(EditProfileField.TIMEZONE, "Use an IANA timezone such as Asia/Kolkata")
        }
        if (form.website.isNotBlank() && !WEBSITE_PATTERN.matches(form.website.trim())) {
            put(EditProfileField.WEBSITE, "Enter a web address like example.com")
        }
        // The one rule with a concrete rendering consequence: the theme colour
        // is read back as a hex string and parsed to paint the profile. A
        // malformed value saves fine and then fails at paint time, on a screen
        // far away from this one.
        if (form.profileThemeColor.isNotBlank() &&
            !HEX_COLOR_PATTERN.matches(form.profileThemeColor.trim())
        ) {
            put(EditProfileField.THEME_COLOR, "Use a hex colour like #1A73E8")
        }
    }

    private companion object {
        const val MAX_DISPLAY_NAME = 50
        const val MAX_BIO = 300
        const val MAX_SHORT_TEXT = 80
        const val MAX_STATUS_TEXT = 120
        const val MAX_CTA_LABEL = 40
        const val MAX_STATUS_EMOJI_CODEPOINTS = 4

        val PROFILE_TEXT_LIMITS = listOf(
            EditProfileField.DISPLAY_NAME to MAX_DISPLAY_NAME,
            EditProfileField.FIRST_NAME to MAX_SHORT_TEXT,
            EditProfileField.LAST_NAME to MAX_SHORT_TEXT,
            EditProfileField.PREFERRED_NAME to MAX_SHORT_TEXT,
            EditProfileField.PRONOUNS to MAX_SHORT_TEXT,
            EditProfileField.BIO to MAX_BIO,
            EditProfileField.CATEGORY to MAX_SHORT_TEXT,
            EditProfileField.PROFESSION to MAX_SHORT_TEXT,
            EditProfileField.LOCATION to MAX_SHORT_TEXT,
            EditProfileField.STATUS_TEXT to MAX_STATUS_TEXT,
            EditProfileField.CTA_LABEL to MAX_CTA_LABEL,
        )

        /** `#RRGGBB`, the form the capture returned (`#1A73E8`). */
        val HEX_COLOR_PATTERN = Regex("^#[0-9A-Fa-f]{6}$")

        /**
         * A sanity check, not a URL parser. The scheme is optional because
         * people type `example.com`, and a rule strict enough to be worth
         * writing here would reject valid addresses the server accepts.
         */
        val WEBSITE_PATTERN = Regex("""^(https?://)?[^\s.]+\.[^\s]{2,}$""")
        val TIMEZONE_PATTERN = Regex("^[A-Za-z_+-]+(?:/[A-Za-z0-9_+.-]+)+$")
    }
}

/** Failures worth softening to a warning: nothing is wrong, just not now. */
private fun AppError.isTransient(): Boolean =
    this is AppError.NoNetwork || this is AppError.Timeout || this is AppError.RateLimited
