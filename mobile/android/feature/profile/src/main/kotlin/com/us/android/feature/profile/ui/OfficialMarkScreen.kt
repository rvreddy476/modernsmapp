package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Settings › Account › Request verification › Official mark (2026-11-15). The words match
 * the web's page word for word; every step is the server's `next`.
 */
@Composable
fun OfficialMarkScreen(
    onBack: () -> Unit,
    /** Opens Verify you're a real person, offered when the server answers PERSON_VERIFICATION_REQUIRED. */
    onOpenPersonVerification: () -> Unit = {},
    viewModel: OfficialMarkViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar(title = "Official mark", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
                .testTag("official_mark_screen"),
        ) {
            Text(
                text = "For organisations, brands, media outlets and public bodies. " +
                    "Your account must already be verified as a person.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            state.personRequired?.let {
                ErrorCard(it)
                UsButton(
                    text = "Verify you're a real person",
                    onClick = onOpenPersonVerification,
                    modifier = Modifier.fillMaxWidth().testTag("official_verify_person"),
                )
            }
            val step = state.step
            when {
                state.loading && step == null -> Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                step == null -> LoadFailed(state.loadError, viewModel::load)
                else -> OfficialStepContent(step, state, viewModel)
            }
            if (step != null && step != OfficialStep.Done) GovernmentInvite(state, viewModel)
        }
    }
}

@Composable
private fun OfficialStepContent(step: OfficialStep, state: OfficialMarkUiState, viewModel: OfficialMarkViewModel) {
    when (step) {
        OfficialStep.Apply -> ApplyForm(state, viewModel)
        OfficialStep.EmailCode -> EmailCodeStep(state, viewModel)
        OfficialStep.Review -> UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("official_review")) {
            Text(
                "Your application is being reviewed.",
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.SemiBold,
            )
            Text(
                "We'll let you know when it's done.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
        }
        OfficialStep.Done -> {
            val mark = if (state.isGovernment) "Government mark" else "Official mark"
            UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("official_done")) {
                Text(mark, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text(
                    "${state.status?.organisationName.orEmpty()} holds the $mark.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }
    }
}

@Composable
private fun ApplyForm(state: OfficialMarkUiState, viewModel: OfficialMarkViewModel) {
    val busy = state.submitting
    if (state.showRefused) {
        UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("official_refused")) {
            Text("Your last application was not accepted.", style = MaterialTheme.typography.bodyMedium)
        }
    }
    HelpedField(
        value = state.legalName,
        onValueChange = viewModel::onLegalName,
        label = "Registered legal name",
        helper = "As on the GST or company registration",
        enabled = !busy,
        tag = "official_legal_name",
    )
    UsChoiceRow(
        options = RegistrationKind.entries.map { UsChoice(it, it.label) },
        selected = state.registrationKind,
        onSelect = { picked -> picked?.let(viewModel::onRegistrationKind) },
        label = "",
        enabled = !busy,
        allowDeselect = false,
        modifier = Modifier.testTag("official_registration_kind"),
    )
    UsTextField(
        value = state.registration,
        onValueChange = viewModel::onRegistration,
        label = state.registrationKind.label,
        enabled = !busy,
        modifier = Modifier.fillMaxWidth().testTag("official_registration"),
    )
    HelpedField(
        value = state.domain,
        onValueChange = viewModel::onDomain,
        label = "Official domain",
        helper = "Your organisation's own website, for example example.com",
        enabled = !busy,
        tag = "official_domain",
        keyboardType = KeyboardType.Uri,
    )
    HelpedField(
        value = state.email,
        onValueChange = viewModel::onEmail,
        label = "Signatory's work e-mail",
        helper = "Must be on the official domain. We'll send a code there.",
        enabled = !busy,
        tag = "official_email",
        keyboardType = KeyboardType.Email,
    )
    UsButton(
        text = "Send code",
        onClick = viewModel::submit,
        enabled = state.canSubmit,
        loading = busy,
        modifier = Modifier.fillMaxWidth().testTag("official_send_code"),
    )
    state.error?.let { ErrorLine(it) }
}

@Composable
private fun EmailCodeStep(state: OfficialMarkUiState, viewModel: OfficialMarkViewModel) {
    Text(
        "Enter the 6-digit code we sent to the signatory's work e-mail.",
        style = MaterialTheme.typography.bodyMedium,
    )
    UsTextField(
        value = state.code,
        onValueChange = viewModel::onCode,
        label = "Code",
        enabled = !state.submitting,
        keyboardType = KeyboardType.NumberPassword,
        modifier = Modifier.fillMaxWidth().testTag("official_code"),
    )
    UsButton(
        text = "Confirm",
        onClick = viewModel::confirm,
        enabled = state.canConfirm,
        loading = state.submitting,
        modifier = Modifier.fillMaxWidth().testTag("official_confirm"),
    )
    state.error?.let { ErrorLine(it) }
    TextButton(
        onClick = viewModel::startAgain,
        enabled = !state.submitting,
        modifier = Modifier.testTag("official_start_again"),
    ) { Text("Start again") }
}

@Composable
private fun GovernmentInvite(state: OfficialMarkUiState, viewModel: OfficialMarkViewModel) {
    if (!state.inviteOpen) {
        TextButton(onClick = viewModel::openInvite, modifier = Modifier.testTag("official_gov_open")) {
            Text("I have a Government invitation")
        }
        return
    }
    UsTextField(
        value = state.inviteCode,
        onValueChange = viewModel::onInviteCode,
        label = "Invitation code",
        enabled = !state.accepting,
        modifier = Modifier.fillMaxWidth().testTag("official_gov_code"),
    )
    UsButton(
        text = "Accept",
        onClick = viewModel::accept,
        enabled = state.canAccept,
        loading = state.accepting,
        modifier = Modifier.fillMaxWidth().testTag("official_gov_accept"),
    )
    state.inviteError?.let { ErrorLine(it) }
}

@Suppress("LongParameterList") // a text field plus its helper line, each part named at the call site
@Composable
private fun HelpedField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    helper: String,
    enabled: Boolean,
    tag: String,
    keyboardType: KeyboardType = KeyboardType.Text,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        UsTextField(
            value = value,
            onValueChange = onValueChange,
            label = label,
            enabled = enabled,
            keyboardType = keyboardType,
            modifier = Modifier.fillMaxWidth().testTag(tag),
        )
        Text(helper, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
    }
}

@Composable
private fun LoadFailed(message: String?, onRetry: () -> Unit) {
    ErrorLine(message ?: "Something went wrong. Try again.")
    UsSecondaryButton(text = "Try again", onClick = onRetry, modifier = Modifier.fillMaxWidth())
}

@Composable
private fun ErrorCard(message: String) {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("official_person_required")) {
        Text(message, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.statusDanger)
    }
}

@Composable
private fun ErrorLine(message: String) {
    Text(
        text = message,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.statusDanger,
        modifier = Modifier.testTag("official_error"),
    )
}
