package com.us.android.core.profile.data

import com.us.android.core.model.Profile

/**
 * The complete set of fields `PUT /v1/profiles/me` accepts, as one value.
 *
 * This type exists because the endpoint is a full replacement. The repository
 * deliberately does NOT expose `updateProfile(displayName = ...)` or any other
 * per-field entry point: there is one way to save, it takes a whole snapshot,
 * and the snapshot can only be produced by [from] (seeded from a loaded `/me`)
 * or by [with] (one field changed on an existing complete snapshot). A caller
 * holding only the field the user typed has nothing to pass, so the partial
 * save is never written in the first place.
 *
 * Framework-free by design — no Compose, no Android, no serialization. Per the
 * reuse strategy §2.4 this is the kind of rule that moves into a shared
 * module when iOS starts, and it cannot if it drags a UI dependency along.
 */
data class EditableProfile(
    val displayName: String,
    val firstName: String,
    val lastName: String,
    val preferredName: String,
    val pronouns: String,
    val bio: String,
    val dateOfBirth: String,
    val gender: String,
    val category: String,
    val profession: String,
    val website: String,
    val location: String,
    val statusText: String,
    val statusEmoji: String,
    val profileThemeColor: String,
    val ctaLabel: String,
    val ctaUrl: String,
    val memberSinceBadge: Boolean,
    val timezone: String,
) {

    /**
     * This snapshot with one field replaced. Every other field keeps the value
     * it was loaded with, which is what makes an untouched field survive the
     * replacement.
     */
    // Exhaustive enum-to-field mapping; splitting would weaken compile-time coverage.
    @Suppress("CyclomaticComplexMethod")
    fun with(field: EditProfileField, value: String): EditableProfile = when (field) {
        EditProfileField.DISPLAY_NAME -> copy(displayName = value)
        EditProfileField.FIRST_NAME -> copy(firstName = value)
        EditProfileField.LAST_NAME -> copy(lastName = value)
        EditProfileField.PREFERRED_NAME -> copy(preferredName = value)
        EditProfileField.PRONOUNS -> copy(pronouns = value)
        EditProfileField.BIO -> copy(bio = value)
        EditProfileField.DATE_OF_BIRTH -> copy(dateOfBirth = value)
        EditProfileField.GENDER -> copy(gender = value)
        EditProfileField.CATEGORY -> copy(category = value)
        EditProfileField.PROFESSION -> copy(profession = value)
        EditProfileField.WEBSITE -> copy(website = value)
        EditProfileField.LOCATION -> copy(location = value)
        EditProfileField.STATUS_TEXT -> copy(statusText = value)
        EditProfileField.STATUS_EMOJI -> copy(statusEmoji = value)
        EditProfileField.THEME_COLOR -> copy(profileThemeColor = value)
        EditProfileField.CTA_LABEL -> copy(ctaLabel = value)
        EditProfileField.CTA_URL -> copy(ctaUrl = value)
        EditProfileField.TIMEZONE -> copy(timezone = value)
    }

    fun withMemberSinceBadge(value: Boolean) = copy(memberSinceBadge = value)

    /** The current value of one field, for rendering. */
    @Suppress("CyclomaticComplexMethod") // Exhaustive inverse mapping paired with with().
    fun value(field: EditProfileField): String = when (field) {
        EditProfileField.DISPLAY_NAME -> displayName
        EditProfileField.FIRST_NAME -> firstName
        EditProfileField.LAST_NAME -> lastName
        EditProfileField.PREFERRED_NAME -> preferredName
        EditProfileField.PRONOUNS -> pronouns
        EditProfileField.BIO -> bio
        EditProfileField.DATE_OF_BIRTH -> dateOfBirth
        EditProfileField.GENDER -> gender
        EditProfileField.CATEGORY -> category
        EditProfileField.PROFESSION -> profession
        EditProfileField.WEBSITE -> website
        EditProfileField.LOCATION -> location
        EditProfileField.STATUS_TEXT -> statusText
        EditProfileField.STATUS_EMOJI -> statusEmoji
        EditProfileField.THEME_COLOR -> profileThemeColor
        EditProfileField.CTA_LABEL -> ctaLabel
        EditProfileField.CTA_URL -> ctaUrl
        EditProfileField.TIMEZONE -> timezone
    }

    companion object {
        /**
         * Seeds the form from a loaded profile.
         *
         * The argument is the whole [Profile], not a handful of strings, so a
         * newly editable field cannot be added to [EditableProfile] and then
         * forgotten here — the constructor call below stops compiling.
         */
        fun from(profile: Profile) = EditableProfile(
            displayName = profile.displayName,
            firstName = profile.personal?.firstName.orEmpty(),
            lastName = profile.personal?.lastName.orEmpty(),
            preferredName = profile.personal?.preferredName.orEmpty(),
            pronouns = profile.pronouns,
            bio = profile.bio,
            dateOfBirth = profile.personal?.dateOfBirth?.take(DATE_PREFIX_LENGTH).orEmpty(),
            gender = profile.personal?.gender.orEmpty(),
            category = profile.category,
            profession = profile.profession,
            website = profile.website,
            location = profile.location,
            statusText = profile.statusText,
            statusEmoji = profile.statusEmoji,
            profileThemeColor = profile.profileThemeColor,
            ctaLabel = profile.ctaLabel,
            ctaUrl = profile.ctaUrl,
            memberSinceBadge = profile.memberSinceBadge,
            timezone = profile.personal?.timezone.orEmpty(),
        )

        private const val DATE_PREFIX_LENGTH = 10
    }
}

/**
 * Identifies one editable field.
 *
 * Both `when` blocks above are exhaustive over this enum, so adding a constant
 * here produces two compile errors rather than a field that silently renders
 * blank and then saves blank over the user's real value.
 */
enum class EditProfileField {
    DISPLAY_NAME,
    FIRST_NAME,
    LAST_NAME,
    PREFERRED_NAME,
    PRONOUNS,
    BIO,
    DATE_OF_BIRTH,
    GENDER,
    CATEGORY,
    PROFESSION,
    WEBSITE,
    LOCATION,
    STATUS_TEXT,
    STATUS_EMOJI,
    THEME_COLOR,
    CTA_LABEL,
    CTA_URL,
    TIMEZONE,
}
