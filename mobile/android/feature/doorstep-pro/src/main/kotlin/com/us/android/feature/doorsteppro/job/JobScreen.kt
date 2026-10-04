package com.us.android.feature.doorsteppro.job

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.camera.rememberPhotoSource
import com.us.android.feature.doorsteppro.data.ExtraDto
import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.domain.JobStatus
import com.us.android.feature.doorsteppro.domain.OtpEntry
import com.us.android.feature.doorsteppro.domain.OtpRules
import com.us.android.feature.doorsteppro.domain.PhotoPhase
import com.us.android.feature.doorsteppro.domain.VisitActions
import com.us.android.feature.doorsteppro.domain.VisitStep
import com.us.android.feature.doorsteppro.model.Paise
import com.us.android.feature.doorsteppro.model.toRupeeText
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.ChoiceRow
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LabeledValue
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.MessagePane
import com.us.android.feature.doorsteppro.ui.NavHandOff
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.QuantityStepper
import com.us.android.feature.doorsteppro.ui.StarPicker
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.countdownText
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.humanise
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.openNavigation
import com.us.android.feature.doorsteppro.ui.slotRangeText
import com.us.android.feature.doorsteppro.ui.tone

@Composable
@Suppress("LongMethod")
fun JobScreen(onBack: () -> Unit, onOpenChat: (String) -> Unit, viewModel: JobViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var navFailed by remember { mutableStateOf(false) }
    LaunchedEffect(state.released) { if (state.released) onBack() }
    val camera = rememberPhotoSource(onPicked = viewModel::onPhoto)

    state.dialog?.let { dialog -> JobDialogs(dialog, viewModel) }
    state.extraDraft?.let { ExtraDialog(state, viewModel, onTakeEvidence = { viewModel.willCapture(PhotoPhase.EXTRA_EVIDENCE); camera.takePhoto() }) }

    ProScreen(
        title = state.job?.serviceName ?: "Job",
        onBack = onBack,
        message = state.message ?: if (navFailed) errorMessage("No maps app on this phone can open the address.") else null,
        onDismissMessage = {
            navFailed = false
            viewModel.dismissMessage()
        },
        actions = {
            IconButton(onClick = viewModel::refresh) { Icon(UsIcons.RotateCw, contentDescription = "Refresh", tint = UsTheme.extended.textMuted) }
        },
    ) { padding ->
        val job = state.job
        val actions = state.actions
        when {
            state.loading && job == null -> LoadingPane()
            job == null || actions == null -> MessagePane(
                title = "Couldn't load this job",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
            )
            else -> LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                item { SummaryCard(job) }
                item {
                    CustomerCard(
                        job = job,
                        actions = actions,
                        onNavigate = { uri -> navFailed = !context.openNavigation(uri) },
                        onChat = { onOpenChat(job.bookingId) },
                    )
                }
                item {
                    StepCard(
                        state = state,
                        job = job,
                        actions = actions,
                        viewModel = viewModel,
                        onTakePhoto = { phase ->
                            viewModel.willCapture(phase)
                            camera.takePhoto()
                        },
                    )
                }
                if (actions.canProposeExtras || state.extras.isNotEmpty()) {
                    item { ExtrasCard(state, actions, viewModel) }
                }
                if (actions.canSos || actions.canCancel || actions.canUnsafeExit) {
                    item { SafetyCard(actions, viewModel) }
                }
            }
        }
    }
}

@Composable
private fun SummaryCard(job: ProJobDto) {
    val status = JobStatus.of(job.status)
    ProCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(job.serviceName, humanise(job.categorySlug), modifier = Modifier.weight(1f))
            Pill(status.label, status.tone())
        }
        Text(slotRangeText(job.slotStart, job.slotEnd), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
        job.items.forEach { line ->
            Text(
                "${line.name}" + if (line.quantity > 1) " × ${line.quantity}" else "",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
        }
        LabeledValue("You earn", "about ${Paise(job.earningEstimatePaise).toRupeeText()}")
    }
}

@Composable
private fun CustomerCard(job: ProJobDto, actions: VisitActions, onNavigate: (String) -> Unit, onChat: () -> Unit) {
    ProCard {
        val address = job.address
        CardHeading(
            title = job.customerFirstName?.let { "Customer: $it" } ?: "Customer",
            subtitle = if (address == null) "${job.locality} — the address shows from acceptance until 2 hours after the job." else null,
        )
        if (address != null) {
            Text(
                listOfNotNull(address.line1, address.line2, address.locality, address.pincode).joinToString(", "),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
            address.landmark?.let { LabeledValue("Landmark", it) }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            val geo = address?.let { NavHandOff.geoUri(it.lat, it.lng, it.locality) }
            if (actions.canNavigate && geo != null) {
                UsSecondaryButton(text = "Navigate", onClick = { onNavigate(geo) }, modifier = Modifier.weight(1f))
            }
            if (actions.canChat) {
                UsSecondaryButton(text = "Chat", onClick = onChat, modifier = Modifier.weight(1f))
            }
        }
    }
}

@Composable
@Suppress("LongMethod")
private fun StepCard(state: JobUiState, job: ProJobDto, actions: VisitActions, viewModel: JobViewModel, onTakePhoto: (PhotoPhase) -> Unit) {
    when (actions.step) {
        VisitStep.GO_EN_ROUTE -> ProCard {
            CardHeading("Head to the customer", "Tap when you leave. The customer sees you're on the way.")
            UsButton(text = "Start travelling", onClick = viewModel::goEnRoute, loading = state.busy, modifier = Modifier.fillMaxWidth())
        }
        VisitStep.MARK_ARRIVED -> ProCard {
            CardHeading("On the way", "Tap when you reach the address. Your location is checked against it.")
            UsButton(text = "I've arrived", onClick = viewModel::markArrived, loading = state.busy, modifier = Modifier.fillMaxWidth())
        }
        VisitStep.START_WITH_OTP -> ProCard {
            CardHeading("Before you start", "Photos of the work area and your kit only — never the customer.")
            PhotoRow(PhotoPhase.BEFORE, state.photos[PhotoPhase.BEFORE], job.photosRequired.before, state, onTakePhoto)
            if (job.photosRequired.kitSeal > 0) {
                PhotoRow(PhotoPhase.KIT_SEAL, state.photos[PhotoPhase.KIT_SEAL], job.photosRequired.kitSeal, state, onTakePhoto)
            }
            OtpBox(
                title = "Customer's start code",
                entry = state.startOtp,
                now = state.now,
                onChange = viewModel::onStartOtp,
                enabled = state.missingToStart.isEmpty(),
            )
            UsButton(
                text = "Start job",
                onClick = viewModel::submitStart,
                loading = state.busy,
                enabled = state.missingToStart.isEmpty() && OtpRules.canSubmit(state.startOtp, state.now),
                modifier = Modifier.fillMaxWidth(),
            )
            NoShowRow(actions, viewModel)
        }
        VisitStep.WORK -> ProCard {
            CardHeading("Job in progress", "Need more work than booked? Propose an extra below — the customer approves it in their app.")
            UsButton(
                text = "Work done",
                onClick = viewModel::finish,
                loading = state.busy,
                enabled = state.undecidedExtras == 0,
                modifier = Modifier.fillMaxWidth(),
            )
            if (state.undecidedExtras > 0) InfoNote("Waiting for the customer to decide on ${state.undecidedExtras} extra(s).", tone = Tone.Warning)
        }
        VisitStep.COMPLETE_WITH_OTP -> ProCard {
            CardHeading("Finish up", "Photos of the finished work, then the customer's end code.")
            if (state.status == JobStatus.AWAITING_EXTRAS_PAYMENT) {
                InfoNote("The customer is paying for the extras in their app. The end code works once it's paid.", tone = Tone.Warning)
            }
            PhotoRow(PhotoPhase.AFTER, state.photos[PhotoPhase.AFTER], job.photosRequired.after, state, onTakePhoto)
            OtpBox(
                title = "Customer's end code",
                entry = state.endOtp,
                now = state.now,
                onChange = viewModel::onEndOtp,
                enabled = state.missingToComplete.isEmpty(),
            )
            UsButton(
                text = "Complete job",
                onClick = viewModel::submitComplete,
                loading = state.busy,
                enabled = state.missingToComplete.isEmpty() && OtpRules.canSubmit(state.endOtp, state.now),
                modifier = Modifier.fillMaxWidth(),
            )
        }
        VisitStep.RATE -> ProCard {
            if (state.rated) {
                CardHeading("Thanks for your rating", "It helps keep Doorstep safe for professionals.")
            } else {
                CardHeading(
                    if (state.status == JobStatus.CUSTOMER_NO_SHOW) "Customer wasn't there" else "Job complete",
                    "How was the customer?",
                )
                StarPicker(stars = state.ratingStars, onPick = viewModel::setStars)
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s), modifier = Modifier.fillMaxWidth()) {
                    JobUiState.RATING_TAGS.take(TAGS_PER_ROW).forEach { TagChip(it, it in state.ratingTags, viewModel::toggleTag) }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s), modifier = Modifier.fillMaxWidth()) {
                    JobUiState.RATING_TAGS.drop(TAGS_PER_ROW).forEach { TagChip(it, it in state.ratingTags, viewModel::toggleTag) }
                }
                UsTextField(value = state.ratingComment, onValueChange = viewModel::setComment, label = "Comment (optional)", singleLine = false)
                UsButton(
                    text = "Submit rating",
                    onClick = viewModel::submitRating,
                    loading = state.busy,
                    enabled = state.ratingStars > 0,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        VisitStep.NONE -> ProCard {
            CardHeading(state.status.label, "Nothing more to do on this job.")
        }
    }
}

@Composable
private fun TagChip(tag: String, selected: Boolean, onToggle: (String) -> Unit) {
    FilterChip(
        selected = selected,
        onClick = { onToggle(tag) },
        label = { Text(tag) },
        colors = FilterChipDefaults.filterChipColors(selectedContainerColor = UsTheme.extended.accentSolid.copy(alpha = CHIP_ALPHA)),
    )
}

@Composable
private fun PhotoRow(phase: PhotoPhase, have: Int, need: Int, state: JobUiState, onTakePhoto: (PhotoPhase) -> Unit) {
    val uploading = state.uploadingPhase == phase
    Row(verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(phase.label, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
            Text(
                if (need > 0) "$have of $need taken" else "$have taken",
                style = MaterialTheme.typography.bodySmall,
                color = if (have >= need) UsTheme.extended.statusSuccess else UsTheme.extended.textMuted,
            )
        }
        UsSecondaryButton(text = if (have >= need) "Add another" else "Take photo", onClick = { onTakePhoto(phase) }, enabled = state.uploadingPhase == null)
    }
    if (uploading) {
        LinearProgressIndicator(
            progress = { state.uploadProgress },
            modifier = Modifier.fillMaxWidth(),
            color = UsTheme.extended.accentSolid,
            trackColor = UsTheme.extended.borderSubtle,
        )
    }
}

@Composable
private fun OtpBox(title: String, entry: OtpEntry, now: java.time.Instant, onChange: (String) -> Unit, enabled: Boolean) {
    val locked = OtpRules.isLocked(entry, now)
    Text(title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
    UsOtpField(
        value = entry.code,
        onValueChange = onChange,
        length = OtpRules.LENGTH,
        enabled = enabled && !locked,
        errorText = OtpRules.message(entry, now),
        autoFocus = false,
    )
    if (!enabled) InfoNote("Take the photos above first.")
}

@Composable
private fun NoShowRow(actions: VisitActions, viewModel: JobViewModel) {
    val wait = actions.noShowInSeconds
    when {
        actions.noShowAvailable -> UsSecondaryButton(
            text = "Customer isn't here",
            onClick = { viewModel.showDialog(JobDialog.NO_SHOW) },
            modifier = Modifier.fillMaxWidth(),
        )
        wait != null -> InfoNote("Customer not answering? You can mark a no-show in ${countdownText(wait)}.")
    }
}

@Composable
private fun ExtrasCard(state: JobUiState, actions: VisitActions, viewModel: JobViewModel) {
    ProCard {
        CardHeading("Extras", "From the rate card only. The customer approves each one; nothing is taken in cash.")
        state.extras.forEach { extra -> ExtraRow(extra, onWithdraw = { viewModel.withdrawExtra(extra.id) }) }
        if (actions.canProposeExtras) {
            when {
                state.extraOptionsUnavailable -> InfoNote("The rate card isn't available yet — extras can't be proposed from the app.", tone = Tone.Warning)
                state.extraOptions?.isEmpty() == true -> InfoNote("This job takes no extras.")
                else -> UsSecondaryButton(
                    text = "Propose an extra",
                    onClick = viewModel::openExtraDraft,
                    enabled = state.extraOptions != null,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

@Composable
private fun ExtraRow(extra: ExtraDto, onWithdraw: () -> Unit) {
    val (label, tone) = when (extra.status) {
        "approved" -> "Approved" to Tone.Positive
        "declined" -> "Declined" to Tone.Danger
        "withdrawn" -> "Withdrawn" to Tone.Neutral
        "billed" -> "Billed" to Tone.Positive
        else -> "Waiting" to Tone.Warning
    }
    Row(verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(extra.name + if (extra.quantity > 1) " × ${extra.quantity}" else "", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
            Text(Paise(extra.totalPaise).toRupeeText(), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
        Pill(label, tone)
        if (extra.status == JobUiState.PROPOSED) {
            TextButton(onClick = onWithdraw) { Text("Withdraw") }
        }
    }
}

@Composable
private fun ExtraDialog(state: JobUiState, viewModel: JobViewModel, onTakeEvidence: () -> Unit) {
    val draft = state.extraDraft ?: return
    AlertDialog(
        onDismissRequest = viewModel::closeExtraDraft,
        title = { Text("Propose an extra") },
        text = {
            Column(
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                state.extraOptions.orEmpty().forEach { option ->
                    ChoiceRow(selected = draft.option == option, onClick = { viewModel.pickExtraOption(option) }, modifier = Modifier.fillMaxWidth()) {
                        Column(Modifier.weight(1f)) {
                            Text(option.name, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
                            option.description?.takeIf { it.isNotBlank() }?.let {
                                Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                            }
                        }
                        Spacer(Modifier.width(UsTheme.spacing.s))
                        Text(Paise(option.unitPricePaise).toRupeeText(), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
                    }
                }
                draft.option?.let { option ->
                    if (option.maxQuantity > 1) {
                        QuantityStepper(
                            quantity = draft.quantity,
                            onDecrease = { viewModel.setExtraQuantity(draft.quantity - 1) },
                            onIncrease = { viewModel.setExtraQuantity(draft.quantity + 1) },
                        )
                    }
                    Text(
                        "Customer pays ${(Paise(option.unitPricePaise) * draft.quantity).toRupeeText()}",
                        style = MaterialTheme.typography.titleSmall,
                        color = UsTheme.extended.textPrimary,
                    )
                }
                UsSecondaryButton(
                    text = when {
                        draft.uploadingEvidence || state.uploadingPhase == PhotoPhase.EXTRA_EVIDENCE -> "Uploading photo…"
                        draft.evidenceMediaId != null -> "Photo added · retake"
                        else -> "Add a photo (optional)"
                    },
                    onClick = onTakeEvidence,
                    enabled = state.uploadingPhase == null,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            TextButton(onClick = viewModel::proposeExtra, enabled = draft.option != null && !state.busy && state.uploadingPhase == null) {
                Text("Send to customer")
            }
        },
        dismissButton = { TextButton(onClick = viewModel::closeExtraDraft) { Text("Cancel") } },
    )
}

@Composable
private fun SafetyCard(actions: VisitActions, viewModel: JobViewModel) {
    ProCard {
        CardHeading("Safety", "If you feel unsafe, leave. There is no penalty.")
        if (actions.canSos) {
            UsButton(text = "SOS", onClick = { viewModel.showDialog(JobDialog.SOS) }, modifier = Modifier.fillMaxWidth())
        }
        if (actions.canUnsafeExit) {
            UsSecondaryButton(text = "Unsafe, leaving", onClick = { viewModel.showDialog(JobDialog.UNSAFE_EXIT) }, modifier = Modifier.fillMaxWidth())
        }
        if (actions.canCancel) {
            UsSecondaryButton(text = "Give this job back", onClick = { viewModel.showDialog(JobDialog.CANCEL) }, modifier = Modifier.fillMaxWidth())
        }
    }
}

@Composable
private fun JobDialogs(dialog: JobDialog, viewModel: JobViewModel) {
    var note by remember(dialog) { mutableStateOf("") }
    val (title, body, confirm) = when (dialog) {
        JobDialog.SOS -> Triple("Send SOS?", "Doorstep's safety team is alerted with your location and will call you. In danger, also call 112.", "Send SOS")
        JobDialog.UNSAFE_EXIT -> Triple(
            "Leave this job?",
            "Leave now if you feel unsafe. Doorstep is alerted, the customer gets another professional or a refund, and there's no penalty for you.",
            "I'm leaving",
        )
        JobDialog.CANCEL -> Triple(
            "Give this job back?",
            "It goes to another professional. It counts against your acceptance rate — use \"Unsafe, leaving\" instead if you feel unsafe.",
            "Give it back",
        )
        JobDialog.NO_SHOW -> Triple(
            "Mark the customer as not here?",
            "Only if you've waited and couldn't reach them in the app chat. A no-show fee applies to the customer.",
            "Customer isn't here",
        )
    }
    AlertDialog(
        onDismissRequest = { viewModel.showDialog(null) },
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Text(body)
                if (dialog != JobDialog.NO_SHOW) {
                    UsTextField(
                        value = note,
                        onValueChange = { note = it.take(MAX_NOTE) },
                        label = if (dialog == JobDialog.CANCEL) "Reason" else "What's happening (optional)",
                        singleLine = false,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = {
                    when (dialog) {
                        JobDialog.SOS -> viewModel.sos(note)
                        JobDialog.UNSAFE_EXIT -> viewModel.unsafeExit(note)
                        JobDialog.CANCEL -> viewModel.giveBack(note)
                        JobDialog.NO_SHOW -> viewModel.customerNoShow()
                    }
                },
            ) { Text(confirm) }
        },
        dismissButton = { TextButton(onClick = { viewModel.showDialog(null) }) { Text("Back") } },
    )
}

private const val MAX_NOTE = 500
private const val TAGS_PER_ROW = 3
private const val CHIP_ALPHA = 0.14f
