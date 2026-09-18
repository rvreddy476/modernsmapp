package com.us.android.feature.mopedu.captain

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import com.us.android.feature.mopedu.captain.data.CaptainDocumentTypes
import com.us.android.feature.mopedu.captain.data.PartnerReview
import com.us.android.feature.mopedu.captain.data.ReviewState
import com.us.android.feature.mopedu.captain.ui.CaptainCard
import com.us.android.feature.mopedu.captain.ui.CaptainPill
import com.us.android.feature.mopedu.captain.ui.CaptainScreen
import com.us.android.feature.mopedu.captain.ui.CardHeading
import com.us.android.feature.mopedu.captain.ui.InfoNote
import com.us.android.feature.mopedu.captain.ui.LoadingPane
import com.us.android.feature.mopedu.captain.ui.PillTone
import com.us.android.feature.mopedu.captain.ui.SectionHeader
import com.us.android.feature.mopedu.captain.ui.StatusBadge

/**
 * Onboarding: profile → vehicle → documents → verification, on Momentum tokens
 * only. The plan is its own screen, reached once the review approves.
 *
 * Verification is automatic for DigiLocker-verified documents: the status
 * step shows "Verifying…" while the server is polled, then Home (through the
 * plans) or "Under review" with exactly what is pending. Nothing here tells a
 * captain to wait for a person unless the server put them in that queue.
 */
@Composable
@Suppress("LongParameterList")
fun MopeduCaptainOnboardingScreen(
    state: CaptainUiState.Onboarding,
    onBack: (() -> Unit)?,
    onSubmitProfile: (fullName: String, phone: String, email: String?) -> Unit,
    onSubmitVehicle: (type: VehicleType, regNumber: String, brand: String, model: String) -> Unit,
    onPickDocumentPhoto: (type: String, uri: String) -> Unit,
    onSubmitDocument: (type: String, number: String) -> Unit,
    onTakeSelfie: () -> Unit,
    onStartDigiLocker: () -> Unit,
    onSubmitForVerification: () -> Unit,
    onBackToDocuments: () -> Unit,
    onRefreshStatus: () -> Unit,
    onProceedToConsole: () -> Unit,
    onDismissError: () -> Unit,
    modifier: Modifier = Modifier,
) {
    CaptainScreen(
        title = "Become a captain",
        onBack = onBack,
        message = state.errorMessage?.let { UsMessage(it) },
        onDismissMessage = onDismissError,
        modifier = modifier,
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            StepProgressHeader(currentStep = state.step, modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.xxl))
            if (state.isLoading) {
                LoadingPane(modifier = Modifier.weight(1f))
            } else {
                Box(modifier = Modifier.weight(1f)) {
                    when (state.step) {
                        OnboardingStep.PROFILE -> ProfileStep(state.profile, onSubmitProfile)
                        OnboardingStep.VEHICLE -> VehicleStep(state.vehicle, onSubmitVehicle)
                        OnboardingStep.DOCUMENTS -> DocumentsStep(
                            documents = state.documents,
                            uploads = state.uploads,
                            onPickDocumentPhoto = onPickDocumentPhoto,
                            onSubmitDocument = onSubmitDocument,
                            onTakeSelfie = onTakeSelfie,
                            onStartDigiLocker = onStartDigiLocker,
                            onSubmitForVerification = onSubmitForVerification,
                        )
                        OnboardingStep.STATUS -> StatusStep(
                            profile = state.profile,
                            vehicle = state.vehicle,
                            documents = state.documents,
                            review = state.review,
                            isVerifying = state.isVerifying,
                            onRefresh = onRefreshStatus,
                            onBackToDocuments = onBackToDocuments,
                            onProceed = onProceedToConsole,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun StepProgressHeader(currentStep: OnboardingStep, modifier: Modifier = Modifier) {
    Row(modifier = modifier, horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
        OnboardingStep.entries.forEach { step ->
            val isCompleted = step.stepNumber < currentStep.stepNumber
            val isCurrent = step == currentStep
            val circle = when {
                isCompleted -> UsTheme.extended.statusSuccess
                isCurrent -> UsTheme.extended.accentSolid
                else -> UsTheme.extended.bgRaised
            }
            Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.weight(1f)) {
                Box(modifier = Modifier.size(28.dp).clip(CircleShape).background(circle), contentAlignment = Alignment.Center) {
                    Text(
                        text = if (isCompleted) "✓" else "${step.stepNumber}",
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = FontWeight.Bold,
                        color = if (isCompleted || isCurrent) UsTheme.extended.textPrimary else UsTheme.extended.textDim,
                    )
                }
                Text(
                    text = step.title,
                    style = MaterialTheme.typography.labelSmall,
                    color = if (isCurrent) UsTheme.extended.accentSolid else UsTheme.extended.textMuted,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.padding(top = UsTheme.spacing.xs),
                )
            }
        }
    }
}

@Composable
private fun ProfileStep(profile: PartnerProfile?, onSubmit: (String, String, String?) -> Unit) {
    var fullName by remember { mutableStateOf(profile?.fullName.orEmpty()) }
    var phone by remember { mutableStateOf(profile?.phone.orEmpty()) }
    var email by remember { mutableStateOf(profile?.email.orEmpty()) }
    LazyColumn(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { CardHeading("Your details", "Enter your details as they appear on your Aadhaar.") }
        item { UsTextField(value = fullName, onValueChange = { fullName = it }, label = "Full name", placeholder = "e.g. Rahul Sharma", modifier = Modifier.fillMaxWidth()) }
        item { UsTextField(value = phone, onValueChange = { phone = it }, label = "Mobile number", placeholder = "+91 98765 43210", modifier = Modifier.fillMaxWidth()) }
        item { UsTextField(value = email, onValueChange = { email = it }, label = "Email (optional)", placeholder = "you@example.com", modifier = Modifier.fillMaxWidth()) }
        item {
            UsButton(
                text = "Continue",
                onClick = { onSubmit(fullName.trim(), phone.trim(), email.trim().ifEmpty { null }) },
                enabled = fullName.isNotBlank() && phone.isNotBlank(),
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m),
            )
        }
    }
}

@Composable
private fun VehicleStep(vehicle: Vehicle?, onSubmit: (VehicleType, String, String, String) -> Unit) {
    var type by remember { mutableStateOf(vehicle?.vehicleType?.takeIf { it == VehicleType.AUTO } ?: VehicleType.BIKE) }
    var regNumber by remember { mutableStateOf(vehicle?.registrationNumber.orEmpty()) }
    var brand by remember { mutableStateOf(vehicle?.brand.orEmpty()) }
    var model by remember { mutableStateOf(vehicle?.model.orEmpty()) }
    LazyColumn(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { CardHeading("Your vehicle", "Pick the vehicle type and enter the number plate.") }
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                listOf(VehicleType.BIKE, VehicleType.AUTO).forEach { option ->
                    CaptainCard(modifier = Modifier.weight(1f), onClick = { type = option }, highlighted = type == option) {
                        Text(
                            text = option.displayName,
                            style = MaterialTheme.typography.labelLarge,
                            color = UsTheme.extended.textPrimary,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
        }
        item { UsTextField(value = regNumber, onValueChange = { regNumber = it.uppercase() }, label = "Registration number", placeholder = "TS09AB1234", modifier = Modifier.fillMaxWidth()) }
        item { UsTextField(value = brand, onValueChange = { brand = it }, label = "Make", placeholder = if (type == VehicleType.BIKE) "Honda, TVS, Bajaj" else "Bajaj, Piaggio", modifier = Modifier.fillMaxWidth()) }
        item { UsTextField(value = model, onValueChange = { model = it }, label = "Model", placeholder = if (type == VehicleType.BIKE) "Activa 6G" else "RE Compact", modifier = Modifier.fillMaxWidth()) }
        item {
            UsButton(
                text = "Save vehicle",
                onClick = { onSubmit(type, regNumber.trim(), brand.trim(), model.trim()) },
                enabled = regNumber.isNotBlank(),
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m),
            )
        }
    }
}

/**
 * Aadhaar through DigiLocker, the selfie, the licence and the RC, then
 * "Submit for verification". The selfie is a `profile_photo` document taken on
 * its own screen (front camera, one retake) and uploaded through :core:media
 * before the record goes in, so the server's face check has a media id. The
 * DL and RC cards — the manual fallback when DigiLocker is skipped — take a
 * photo through the same uploader and submit only once it is confirmed.
 */
@Composable
@Suppress("LongParameterList")
private fun DocumentsStep(
    documents: List<PartnerDocument>,
    uploads: Map<String, DocumentUpload>,
    onPickDocumentPhoto: (String, String) -> Unit,
    onSubmitDocument: (String, String) -> Unit,
    onTakeSelfie: () -> Unit,
    onStartDigiLocker: () -> Unit,
    onSubmitForVerification: () -> Unit,
) {
    var dlNumber by remember { mutableStateOf("") }
    var rcNumber by remember { mutableStateOf("") }
    var picking by remember { mutableStateOf<String?>(null) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        val type = picking
        picking = null
        if (uri != null && type != null) onPickDocumentPhoto(type, uri.toString())
    }
    val dl = documents.firstOrNull { it.documentType == DOC_DRIVING_LICENCE }
    val rc = documents.firstOrNull { it.documentType == DOC_VEHICLE_RC }
    val aadhaar = documents.firstOrNull { it.documentType == DOC_AADHAAR }
    val selfie = documents.firstOrNull { it.documentType == DOC_SELFIE }
    val aadhaarDone = aadhaar != null && aadhaar.status !in setOf("rejected", "pending")
    LazyColumn(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { CardHeading("Documents", "Verify your Aadhaar through DigiLocker, take a selfie, and add your licence and RC.") }
        item {
            CaptainCard {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CardHeading("Aadhaar via DigiLocker", "No Aadhaar number is stored by Mopedu.", modifier = Modifier.weight(1f))
                    StatusBadge(aadhaar?.status ?: "pending")
                }
                UsButton(
                    text = if (aadhaar?.status == "verified") "Verified" else "Verify with DigiLocker",
                    onClick = onStartDigiLocker,
                    enabled = aadhaar?.status != "verified",
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        item {
            CaptainCard {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CardHeading("Selfie", "Matched to your Aadhaar photo. Look straight at the camera in good light.", modifier = Modifier.weight(1f))
                    StatusBadge(selfie?.status ?: "pending")
                }
                UsButton(
                    text = if (selfie != null) "Retake selfie" else "Take a selfie",
                    onClick = onTakeSelfie,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        item {
            DocumentCard(
                title = "Driving licence",
                document = dl,
                upload = uploads[DOC_DRIVING_LICENCE],
                number = dlNumber,
                onNumberChanged = { dlNumber = it.uppercase() },
                placeholder = "DL-1420110012345",
                onPickPhoto = {
                    picking = DOC_DRIVING_LICENCE
                    picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                },
                onSubmit = { onSubmitDocument(DOC_DRIVING_LICENCE, dlNumber.trim()) },
            )
        }
        item {
            DocumentCard(
                title = "Vehicle RC",
                document = rc,
                upload = uploads[DOC_VEHICLE_RC],
                number = rcNumber,
                onNumberChanged = { rcNumber = it.uppercase() },
                placeholder = "TS09AB1234",
                onPickPhoto = {
                    picking = DOC_VEHICLE_RC
                    picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                },
                onSubmit = { onSubmitDocument(DOC_VEHICLE_RC, rcNumber.trim()) },
            )
        }
        item {
            InfoNote(
                "Documents verified through DigiLocker are approved automatically, usually within seconds. " +
                    "Only a document you upload yourself is checked by a person.",
            )
        }
        item {
            UsButton(
                text = "Submit for verification",
                onClick = onSubmitForVerification,
                enabled = aadhaarDone && selfie != null,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m, bottom = UsTheme.spacing.xxxxl),
            )
        }
    }
}

/** A number, a photo through :core:media, and Submit — enabled only once the photo is CONFIRMED. */
@Composable
@Suppress("LongParameterList")
private fun DocumentCard(
    title: String,
    document: PartnerDocument?,
    upload: DocumentUpload?,
    number: String,
    onNumberChanged: (String) -> Unit,
    placeholder: String,
    onPickPhoto: () -> Unit,
    onSubmit: () -> Unit,
) {
    CaptainCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(title, document?.rejectionReason, modifier = Modifier.weight(1f))
            StatusBadge(document?.status ?: "pending")
        }
        UsTextField(value = number, onValueChange = onNumberChanged, label = "Number", placeholder = placeholder, modifier = Modifier.fillMaxWidth())
        when (upload) {
            null -> UsSecondaryButton(text = "Add a photo", onClick = onPickPhoto, modifier = Modifier.fillMaxWidth())
            is DocumentUpload.Uploading -> LinearProgressIndicator(
                progress = { upload.progress },
                modifier = Modifier.fillMaxWidth(),
                color = UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.borderSubtle,
            )
            is DocumentUpload.Uploaded -> {
                InfoNote("Photo uploaded.", tone = PillTone.Positive)
                UsSecondaryButton(text = "Change photo", onClick = onPickPhoto, modifier = Modifier.fillMaxWidth())
            }
            is DocumentUpload.Failed -> {
                InfoNote(upload.message, tone = PillTone.Danger)
                UsSecondaryButton(text = "Add a photo", onClick = onPickPhoto, modifier = Modifier.fillMaxWidth())
            }
        }
        UsButton(
            text = "Submit",
            onClick = onSubmit,
            enabled = number.isNotBlank() && upload is DocumentUpload.Uploaded,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/**
 * The verdict. "Verifying…" while the server is polled; then approved (on to
 * the plans), under review (what is pending, and that a notification follows),
 * or incomplete (what is still needed, and the way back to Documents).
 */
@Composable
@Suppress("LongParameterList", "LongMethod")
private fun StatusStep(
    profile: PartnerProfile?,
    vehicle: Vehicle?,
    documents: List<PartnerDocument>,
    review: PartnerReview,
    isVerifying: Boolean,
    onRefresh: () -> Unit,
    onBackToDocuments: () -> Unit,
    onProceed: () -> Unit,
) {
    val vehicleApproved = vehicle?.status == "approved"
    val pending = review.pending.map(::pendingLabel)
    LazyColumn(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        item {
            when {
                isVerifying -> CaptainCard(highlighted = true) {
                    CardHeading("Verifying…", "Checking your documents with DigiLocker. This usually takes a few seconds.", tone = PillTone.Accent)
                    UsButton(text = "Verifying", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
                }
                review.state == ReviewState.APPROVED -> CaptainCard(highlighted = true) {
                    CardHeading("You're verified", "Every check has passed. Pick a plan to start receiving rides.", tone = PillTone.Positive)
                    UsButton(text = "Continue", onClick = onProceed, modifier = Modifier.fillMaxWidth())
                }
                review.state == ReviewState.UNDER_REVIEW -> CaptainCard(highlighted = true) {
                    CardHeading(
                        title = "Under review",
                        body = if (pending.isEmpty()) {
                            "A document you uploaded yourself is being checked by a person."
                        } else {
                            "Being checked by a person: ${pending.joinToString(", ")}."
                        },
                        tone = PillTone.Warning,
                    )
                    InfoNote("What to do: nothing for now. Uploaded documents are checked within a day, and you'll get a notification the moment it's done.")
                    UsSecondaryButton(text = "Check again", onClick = onRefresh, modifier = Modifier.fillMaxWidth())
                }
                else -> CaptainCard(highlighted = true) {
                    CardHeading(
                        title = "Not verified yet",
                        body = if (pending.isEmpty()) {
                            "We couldn't complete verification just now."
                        } else {
                            "Still needed: ${pending.joinToString(", ")}."
                        },
                        tone = PillTone.Warning,
                    )
                    InfoNote(
                        if (pending.isEmpty()) {
                            "What to do: check again in a moment, or go back to Documents and submit again."
                        } else {
                            "What to do: add what's missing in Documents, then submit for verification again."
                        },
                    )
                    UsButton(text = "Go to documents", onClick = onBackToDocuments, modifier = Modifier.fillMaxWidth())
                    UsSecondaryButton(text = "Check again", onClick = onRefresh, modifier = Modifier.fillMaxWidth())
                }
            }
        }
        item { SectionHeader("Checklist") }
        item { ChecklistItem("Profile", listOfNotNull(profile?.fullName, profile?.phone).joinToString(" · "), profile?.status in setOf("submitted", "under_review", "approved")) }
        item { ChecklistItem("Vehicle", "${vehicle?.registrationNumber ?: "Pending"} · ${vehicle?.vehicleType?.displayName ?: ""}", vehicleApproved) }
        item { ChecklistItem("Documents", "${documents.size} submitted · ${review.state.code.replace('_', ' ')}", review.isApproved) }
    }
}

@Composable
private fun ChecklistItem(title: String, subtitle: String, complete: Boolean) {
    CaptainCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(title, subtitle, modifier = Modifier.weight(1f))
            CaptainPill(if (complete) "Done" else "Pending", if (complete) PillTone.Positive else PillTone.Neutral)
        }
    }
}

/** A pending item as the server names it, in the captain's words. */
internal fun pendingLabel(code: String): String = when (code) {
    DOC_SELFIE, "selfie" -> "your selfie"
    DOC_AADHAAR -> "Aadhaar via DigiLocker"
    DOC_DRIVING_LICENCE -> "your driving licence"
    DOC_VEHICLE_RC -> "the vehicle RC"
    "vehicle" -> "your vehicle"
    "profile" -> "your profile"
    else -> code.replace('_', ' ')
}

private const val DOC_AADHAAR = CaptainDocumentTypes.AADHAAR
private const val DOC_SELFIE = CaptainDocumentTypes.SELFIE
private const val DOC_DRIVING_LICENCE = CaptainDocumentTypes.DRIVING_LICENCE
private const val DOC_VEHICLE_RC = CaptainDocumentTypes.VEHICLE_RC
