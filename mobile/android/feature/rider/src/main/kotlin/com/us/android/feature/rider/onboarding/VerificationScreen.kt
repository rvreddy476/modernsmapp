package com.us.android.feature.rider.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.DocumentStatusText
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LabeledValue
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.NavRow
import com.us.android.feature.rider.ui.PartnerStatusText
import com.us.android.feature.rider.ui.PillTone
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderPill
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.SectionHeader
import com.us.android.feature.rider.ui.contentPadding
import com.us.android.feature.rider.ui.datePart
import com.us.android.feature.rider.ui.humanise

/** The onboarding checklist and the review status in one place. Re-reads the status every time it is shown. */
@Composable
fun VerificationScreen(
    onBack: (() -> Unit)?,
    onOpenStep: (RiderStep) -> Unit,
    onOpenHome: () -> Unit,
    onSignOut: () -> Unit,
    viewModel: VerificationViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) { viewModel.refresh() }
    }

    RiderScreen(
        title = "Verification",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        actions = {
            IconButton(onClick = viewModel::refresh) {
                Icon(UsIcons.RotateCw, contentDescription = "Refresh", tint = UsTheme.extended.textMuted)
            }
        },
    ) { padding ->
        val checklist = state.checklist
        if (state.loading || checklist == null) {
            LoadingPane()
            return@RiderScreen
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = contentPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item {
                RiderCard {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CardHeading("Your account", modifier = Modifier.weight(1f))
                        if (state.status.isNotBlank()) {
                            RiderPill(PartnerStatusText.label(state.status), PartnerStatusText.tone(state.status))
                        }
                    }
                    InfoNote(
                        when {
                            state.canGoOnline -> "You're approved. Go online to receive delivery jobs."
                            state.status == "REJECTED" -> "Feast couldn't approve this account. Check the documents below."
                            checklist.isReady -> "Everything is submitted. Feast is reviewing your documents."
                            else -> "Finish the steps below. Feast reviews your documents once they're all in."
                        },
                        tone = if (state.canGoOnline) PillTone.Positive else PillTone.Neutral,
                    )
                    if (state.canGoOnline) {
                        UsButton(text = "Go to home", onClick = onOpenHome, modifier = Modifier.fillMaxWidth())
                    }
                }
            }
            item { SectionHeader("Steps") }
            items(checklist.rows, key = { it.step.wire }) { row ->
                val (label, tone) = when (row.status) {
                    RowStatus.DONE -> "Done" to PillTone.Positive
                    RowStatus.TO_DO -> "To do" to PillTone.Accent
                    RowStatus.NOT_NEEDED -> "Not needed" to PillTone.Neutral
                }
                NavRow(
                    title = row.step.title,
                    detail = stepDetail(row, state.vehicleType),
                    onClick = if (row.status == RowStatus.NOT_NEEDED) null else ({ onOpenStep(row.step) }),
                ) { RiderPill(label, tone) }
            }
            if (checklist.unrecognised.isNotEmpty()) {
                item { InfoNote("Feast needs one more thing this app version doesn't know about. Update Feast Rider.", tone = PillTone.Warning) }
            }
            if (state.documents.isNotEmpty() || state.checks.isNotEmpty()) {
                item { SectionHeader("Review") }
                items(state.checks, key = { "check-${it.kind}" }) { check ->
                    RiderCard {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            CardHeading("${humanise(check.kind)} via DigiLocker", modifier = Modifier.weight(1f))
                            RiderPill(if (check.valid) "Verified" else "Expired", if (check.valid) PillTone.Positive else PillTone.Danger)
                        }
                        LabeledValue("Name on document", check.nameOnDocumentMasked)
                        check.validUntil?.let { LabeledValue("Valid until", it) }
                    }
                }
                items(state.documents, key = { it.id }) { document ->
                    RiderCard {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            CardHeading(humanise(document.documentType), modifier = Modifier.weight(1f))
                            RiderPill(DocumentStatusText.label(document.status), DocumentStatusText.tone(document.status))
                        }
                        document.numberMasked?.let { LabeledValue("Number", it) }
                        document.expiresAt?.let { LabeledValue("Expires", datePart(it)) }
                        document.rejectionReason?.let { InfoNote(it, tone = PillTone.Danger) }
                    }
                }
            }
            item { UsSecondaryButton(text = "Sign out", onClick = onSignOut, modifier = Modifier.fillMaxWidth()) }
        }
    }
}

private fun stepDetail(row: RiderChecklist.Row, vehicleType: String): String = when (row.step) {
    RiderStep.VEHICLE -> "Your name, phone and what you ride"
    RiderStep.AADHAAR_DIGILOCKER -> "Verified in DigiLocker — Feast never sees your Aadhaar number"
    RiderStep.DRIVING_LICENCE -> if (row.status == RowStatus.NOT_NEEDED) "Not needed for a bicycle" else "Number and a photo"
    RiderStep.VEHICLE_RC -> if (row.status == RowStatus.NOT_NEEDED) "Not needed for a bicycle" else "Registration number and a photo"
    RiderStep.SELFIE -> "A clear photo of your face"
    RiderStep.PAYOUT_ACCOUNT -> "Where Feast pays your earnings"
}.let { detail -> if (row.step == RiderStep.VEHICLE && vehicleType.isNotBlank()) "$detail · ${humanise(vehicleType)}" else detail }
