package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.profile.data.EditProfileField
import com.us.android.core.profile.data.EditableProfile

/**
 * Everything the edit-profile screen renders, as one immutable value.
 *
 * Sealed for the same reason [ProfileUiState] is: a struct of nullable fields
 * plus `isLoading` permits states that cannot exist, and every screen then
 * invents its own precedence order for them.
 *
 * `@Immutable` is load-bearing rather than decorative — Compose only skips
 * recomposition when it can prove stability.
 */
@Immutable
sealed interface EditProfileUiState {

    /** Fetching the `/me` snapshot the form will be seeded from. */
    data object Loading : EditProfileUiState

    /**
     * The form could not be seeded.
     *
     * There is no "edit anyway" path from here, and that is deliberate. This
     * screen saves by full replacement, so a form the user could fill in
     * without a loaded snapshot would submit blanks over every field they did
     * not happen to type into.
     */
    @Immutable
    data class Error(
        val message: String,
        val retryable: Boolean,
    ) : EditProfileUiState

    /**
     * The loaded form.
     *
     * [original] and [form] are BOTH complete snapshots. Keeping the loaded
     * baseline alongside the edited copy is what makes dirty tracking a value
     * comparison instead of a per-field bookkeeping exercise — and it means an
     * untouched field is not "absent", it is present holding its loaded value,
     * ready to be sent back unchanged.
     */
    @Immutable
    data class Editing(
        val original: EditableProfile,
        val form: EditableProfile,
        /**
         * Inline validation failures, keyed by field.
         *
         * A map rather than seven nullable strings because every editable
         * field here is the same type and gets the same treatment; the map is
         * always rebuilt, never mutated in place, which is the promise
         * `@Immutable` makes on its behalf.
         */
        val fieldErrors: Map<EditProfileField, String> = emptyMap(),
        val isSaving: Boolean = false,
        /**
         * Transient feedback with no field to attach to — offline, rate
         * limits, server faults. Shown in the shared message host rather than
         * replacing the form, because losing a page of typing to a failed save
         * is worse than the failure.
         */
        val message: UsMessage? = null,
        /** Set once the server has stored the snapshot. Drives navigation. */
        val saved: Boolean = false,
    ) : EditProfileUiState {

        /** True when the form differs from what was loaded. */
        val isDirty: Boolean get() = form != original

        /**
         * A pristine form has nothing to save, and saving it anyway would
         * still be a full replacement — a round trip whose only possible
         * outcomes are "no change" and "something went wrong".
         */
        val canSave: Boolean get() = isDirty && !isSaving

        fun errorFor(field: EditProfileField): String? = fieldErrors[field]
    }
}
