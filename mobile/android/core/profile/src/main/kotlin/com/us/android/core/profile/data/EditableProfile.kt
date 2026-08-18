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
    val bio: String,
    val category: String,
    val profession: String,
    val website: String,
    val location: String,
    val profileThemeColor: String,
) {

    /**
     * This snapshot with one field replaced. Every other field keeps the value
     * it was loaded with, which is what makes an untouched field survive the
     * replacement.
     */
    fun with(field: EditProfileField, value: String): EditableProfile = when (field) {
        EditProfileField.DISPLAY_NAME -> copy(displayName = value)
        EditProfileField.BIO -> copy(bio = value)
        EditProfileField.CATEGORY -> copy(category = value)
        EditProfileField.PROFESSION -> copy(profession = value)
        EditProfileField.WEBSITE -> copy(website = value)
        EditProfileField.LOCATION -> copy(location = value)
        EditProfileField.THEME_COLOR -> copy(profileThemeColor = value)
    }

    /** The current value of one field, for rendering. */
    fun value(field: EditProfileField): String = when (field) {
        EditProfileField.DISPLAY_NAME -> displayName
        EditProfileField.BIO -> bio
        EditProfileField.CATEGORY -> category
        EditProfileField.PROFESSION -> profession
        EditProfileField.WEBSITE -> website
        EditProfileField.LOCATION -> location
        EditProfileField.THEME_COLOR -> profileThemeColor
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
            bio = profile.bio,
            category = profile.category,
            profession = profile.profession,
            website = profile.website,
            location = profile.location,
            profileThemeColor = profile.profileThemeColor,
        )
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
    BIO,
    CATEGORY,
    PROFESSION,
    WEBSITE,
    LOCATION,
    THEME_COLOR,
}
