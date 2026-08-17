package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.profile.data.EditProfileField
import com.us.android.feature.profile.data.EditableProfile

/**
 * Edit-profile screen — stateful entry point.
 *
 * Collects state and forwards events, nothing else. Everything that renders is
 * stateless below, which is what makes every state previewable and
 * screenshot-testable without a ViewModel, a DI graph or a network fake.
 */
@Composable
fun EditProfileScreen(
    onBack: () -> Unit,
    onSaved: () -> Unit,
    viewModel: EditProfileViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    // Navigation is driven by the saved flag rather than by the button's click
    // handler: the save is asynchronous, and leaving on the tap would dismiss
    // the screen before the server had accepted anything.
    val saved = (state as? EditProfileUiState.Editing)?.saved == true
    LaunchedEffect(saved) {
        if (saved) onSaved()
    }

    EditProfileContent(
        state = state,
        onFieldChange = viewModel::onFieldChange,
        onSave = viewModel::save,
        onRetry = viewModel::load,
        onDismissMessage = viewModel::dismissMessage,
        onBack = onBack,
    )
}

/** Stateless renderer. Receives immutable state and callbacks; fetches nothing. */
@Composable
internal fun EditProfileContent(
    state: EditProfileUiState,
    onFieldChange: (EditProfileField, String) -> Unit,
    onSave: () -> Unit,
    onRetry: () -> Unit,
    onDismissMessage: () -> Unit,
    modifier: Modifier = Modifier,
    onBack: (() -> Unit)? = null,
) {
    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = "Edit profile", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            when (state) {
                is EditProfileUiState.Loading -> UsLoadingState(
                    modifier = Modifier.padding(padding),
                    label = "Loading your profile",
                )

                is EditProfileUiState.Error -> UsErrorState(
                    message = state.message,
                    modifier = Modifier.padding(padding),
                    onRetry = if (state.retryable) onRetry else null,
                )

                is EditProfileUiState.Editing -> EditProfileForm(
                    state = state,
                    onFieldChange = onFieldChange,
                    onSave = onSave,
                    modifier = Modifier.padding(padding),
                )
            }

            UsMessageHost(
                message = (state as? EditProfileUiState.Editing)?.message,
                onDismiss = onDismissMessage,
            )
        }
    }
}

@Composable
private fun EditProfileForm(
    state: EditProfileUiState.Editing,
    onFieldChange: (EditProfileField, String) -> Unit,
    onSave: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        // Stated plainly because the endpoint's behaviour is genuinely
        // surprising: everything on this form is written together, so a field
        // left alone is re-saved with the value it already had, and a field
        // cleared here is cleared on the server.
        Text(
            text = "These details are saved together. Clearing a field removes it from your profile.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(top = UsTheme.spacing.xxl),
        )

        EditProfileFields(state = state, onFieldChange = onFieldChange)

        UsButton(
            text = "Save changes",
            onClick = onSave,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.canSave,
            loading = state.isSaving,
        )

        // Present but inert when there is nothing to undo, rather than hidden:
        // a control that appears only once the form is dirty moves the save
        // button under the user's thumb mid-edit.
        UsSecondaryButton(
            text = if (state.isDirty) "Unsaved changes" else "Up to date",
            onClick = {},
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = UsTheme.spacing.xxxxl),
            enabled = false,
        )
    }
}

@Composable
private fun EditProfileFields(
    state: EditProfileUiState.Editing,
    onFieldChange: (EditProfileField, String) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl)) {
        // Driven from one list rather than seven hand-written blocks. Every
        // editable field is the same type and gets the same treatment, and a
        // field added to `EditProfileField` that is missing here shows up as a
        // gap in the form instead of hiding among near-identical copies.
        FIELD_SPECS.forEach { spec ->
            UsTextField(
                value = state.form.value(spec.field),
                onValueChange = { onFieldChange(spec.field, it) },
                label = spec.label,
                placeholder = spec.placeholder,
                errorText = state.errorFor(spec.field),
                enabled = !state.isSaving,
                singleLine = spec.singleLine,
                keyboardType = spec.keyboardType,
            )
        }
    }
}

/**
 * How one editable field is presented.
 *
 * Labels and placeholders are presentation and will be translated; the
 * [field] is the wire identity and must not drift when the wording changes —
 * the same split [com.us.android.core.designsystem.component.UsChoice] makes.
 */
private data class FieldSpec(
    val field: EditProfileField,
    val label: String,
    val placeholder: String?,
    val singleLine: Boolean = true,
    val keyboardType: KeyboardType = KeyboardType.Text,
)

/**
 * Deliberately all text inputs.
 *
 * `UsChoiceRow` would suit `category`, and the design system has one — but the
 * capture recorded exactly one live value (`personal`) and no enumeration of
 * what else the server accepts. A pill row built from guesses would constrain
 * users to those guesses and, on a full-replacement save, would overwrite a
 * category this client does not know about with one it does. Text stays until
 * the allowed set is captured.
 *
 * `UsDatePickerField` is likewise unused here: `dob` appears on the `/me`
 * response but was never observed in an accepted request body, so it is not
 * an editable field on this screen.
 */
private val FIELD_SPECS = listOf(
    FieldSpec(EditProfileField.DISPLAY_NAME, "Display name", "How your name appears"),
    FieldSpec(
        field = EditProfileField.BIO,
        label = "Bio",
        placeholder = "A short introduction",
        singleLine = false,
    ),
    FieldSpec(EditProfileField.CATEGORY, "Category", "personal"),
    FieldSpec(EditProfileField.PROFESSION, "Profession", "What you do"),
    FieldSpec(
        field = EditProfileField.WEBSITE,
        label = "Website",
        placeholder = "example.com",
        keyboardType = KeyboardType.Uri,
    ),
    FieldSpec(EditProfileField.LOCATION, "Location", "Where you're based"),
    FieldSpec(EditProfileField.THEME_COLOR, "Theme colour", "#1A73E8"),
)

// ── Previews ────────────────────────────────────────────────────────────
//
// Every state the screen can reach. These are the screenshot-test entry
// points and the reason the renderer is stateless.

/** The snapshot from the 2026-08-17 repair capture, as a loaded form. */
private val previewLoaded = EditableProfile(
    displayName = "Android Repair",
    bio = "Native bearer contract verified",
    category = "personal",
    profession = "android-contract",
    website = "",
    location = "",
    profileThemeColor = "#1A73E8",
)

@Composable
private fun PreviewHost(state: EditProfileUiState) = UsTheme {
    EditProfileContent(
        state = state,
        onFieldChange = { _, _ -> },
        onSave = {},
        onRetry = {},
        onDismissMessage = {},
        onBack = {},
    )
}

@Preview(name = "Loading", showBackground = true, backgroundColor = 0xFF000000, heightDp = 320)
@Composable
private fun EditProfileLoadingPreview() = PreviewHost(EditProfileUiState.Loading)

@Preview(name = "Loaded — pristine", showBackground = true, backgroundColor = 0xFF000000, heightDp = 900)
@Composable
private fun EditProfileLoadedPreview() = PreviewHost(
    EditProfileUiState.Editing(original = previewLoaded, form = previewLoaded),
)

@Preview(name = "Edited — dirty", showBackground = true, backgroundColor = 0xFF000000, heightDp = 900)
@Composable
private fun EditProfileDirtyPreview() = PreviewHost(
    EditProfileUiState.Editing(
        original = previewLoaded,
        // One field touched. The other six still hold their loaded values and
        // will be sent back unchanged — that is the whole contract, on screen.
        form = previewLoaded.copy(location = "Hyderabad"),
    ),
)

@Preview(name = "Saving", showBackground = true, backgroundColor = 0xFF000000, heightDp = 900)
@Composable
private fun EditProfileSavingPreview() = PreviewHost(
    EditProfileUiState.Editing(
        original = previewLoaded,
        form = previewLoaded.copy(location = "Hyderabad"),
        isSaving = true,
    ),
)

@Preview(name = "Save failed", showBackground = true, backgroundColor = 0xFF000000, heightDp = 900)
@Composable
private fun EditProfileSaveFailedPreview() = PreviewHost(
    EditProfileUiState.Editing(
        original = previewLoaded,
        form = previewLoaded.copy(location = "Hyderabad"),
        message = UsMessage(
            text = "You're offline. Nothing was saved — try again when you're back.",
            type = UsMessageType.Warning,
        ),
    ),
)

@Preview(name = "Validation errors", showBackground = true, backgroundColor = 0xFF000000, heightDp = 900)
@Composable
private fun EditProfileValidationPreview() = PreviewHost(
    EditProfileUiState.Editing(
        original = previewLoaded,
        form = previewLoaded.copy(website = "not a url", profileThemeColor = "blue"),
        fieldErrors = mapOf(
            EditProfileField.WEBSITE to "Enter a web address like example.com",
            EditProfileField.THEME_COLOR to "Use a hex colour like #1A73E8",
        ),
        message = UsMessage(
            text = "Some details need fixing — check the highlighted fields.",
            type = UsMessageType.Error,
        ),
    ),
)

@Preview(name = "Cleared profile", showBackground = true, backgroundColor = 0xFF000000, heightDp = 900)
@Composable
private fun EditProfileClearedPreview() = PreviewHost(
    EditProfileUiState.Editing(
        // The state a `{}` full replacement leaves behind. Real accounts reach
        // it, so the form has to look sane with every field empty.
        original = EditableProfile("", "", "", "", "", "", ""),
        form = EditableProfile("", "", "", "", "", "", ""),
    ),
)

@Preview(name = "Load failed", showBackground = true, backgroundColor = 0xFF000000, heightDp = 320)
@Composable
private fun EditProfileLoadFailedPreview() = PreviewHost(
    EditProfileUiState.Error(
        message = "You're offline. Check your connection and try again.",
        retryable = true,
    ),
)
