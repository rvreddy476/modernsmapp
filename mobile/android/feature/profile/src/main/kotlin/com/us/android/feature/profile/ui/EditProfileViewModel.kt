package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.profile.data.EditProfileField
import com.us.android.core.profile.data.EditableProfile
import com.us.android.core.profile.data.ProfileClock
import com.us.android.core.profile.data.ProfileIdentityRules
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
    private val clock: ProfileClock,
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
                        latestBirthDate = ProfileIdentityRules.latestEligibleBirthDate(
                            ProfileIdentityRules.todayInIndia(clock),
                        ),
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

        val errors = validate(current.form, current.original)
        if (errors.isNotEmpty()) {
            // A summary alongside the inline markers: on a form this tall the
            // offending field is often scrolled out of view, so an inline
            // error alone reads as "the button did nothing".
            _state.value = current.copy(fieldErrors = errors, message = FIX_FIELDS_MESSAGE)
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
                    // the same complete snapshot again. A 422 field refusal
                    // (first name, date of birth) is marked on its field.
                    is AppResult.Failure -> ProfileErrorText.fieldForSave(result.error)?.let { fieldError ->
                        editing.copy(
                            isSaving = false,
                            fieldErrors = editing.fieldErrors + fieldError,
                            message = FIX_FIELDS_MESSAGE,
                        )
                    } ?: editing.copy(
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
     * First name and date of birth mirror profile-service's identity-field
     * rules exactly ([ProfileIdentityRules]); a mismatch there is a 422. The
     * rest are client pre-flight for fields the server checks more loosely or
     * not at all — they catch input that would be stored happily and then fail
     * to render, or that the user plainly did not mean.
     *
     * Blank is not an error on the other fields: the server permits them
     * empty, and this is the only screen that can clear them. A first name is
     * the exception — a stored one cannot be cleared.
     */
    private fun validate(form: EditableProfile, original: EditableProfile): Map<EditProfileField, String> = buildMap {
        // profile-service skips a first name whose trimmed value equals the
        // stored one, as it skips an unchanged date of birth. So a stored name
        // from before these rules does not block edits to other fields; only
        // a name the user actually changed is checked.
        if (ProfileIdentityRules.trimFirstName(form.firstName) != original.firstName) {
            ProfileIdentityRules.validateFirstNameChange(form.firstName, original.firstName)
                ?.let { put(EditProfileField.FIRST_NAME, it.message) }
        }
        ProfileIdentityRules.validateDateOfBirthChange(
            value = form.dateOfBirth,
            stored = original.dateOfBirth,
            today = ProfileIdentityRules.todayInIndia(clock),
        )?.let { put(EditProfileField.DATE_OF_BIRTH, it.message) }
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

        val FIX_FIELDS_MESSAGE = UsMessage(
            text = "Some details need fixing — check the highlighted fields.",
            type = UsMessageType.Error,
        )

        /** First name is absent on purpose: [ProfileIdentityRules] owns it (50, not 80). */
        val PROFILE_TEXT_LIMITS = listOf(
            EditProfileField.DISPLAY_NAME to MAX_DISPLAY_NAME,
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
