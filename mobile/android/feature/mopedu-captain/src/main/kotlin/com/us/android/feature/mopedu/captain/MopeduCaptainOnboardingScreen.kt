package com.us.android.feature.mopedu.captain

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
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
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
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import com.us.android.feature.mopedu.captain.ui.CaptainCard
import com.us.android.feature.mopedu.captain.ui.CaptainPill
import com.us.android.feature.mopedu.captain.ui.CaptainScreen
import com.us.android.feature.mopedu.captain.ui.CardHeading
import com.us.android.feature.mopedu.captain.ui.InfoNote
import com.us.android.feature.mopedu.captain.ui.LoadingPane
import com.us.android.feature.mopedu.captain.ui.PillTone
import com.us.android.feature.mopedu.captain.ui.SectionHeader
import com.us.android.feature.mopedu.captain.ui.StatusBadge

/** Onboarding: profile → vehicle → documents → plan → status, on Momentum tokens only. */
@Composable
@Suppress("LongParameterList")
fun MopeduCaptainOnboardingScreen(
    state: CaptainUiState.Onboarding,
    onBack: (() -> Unit)?,
    onSubmitProfile: (fullName: String, phone: String, email: String?) -> Unit,
    onSubmitVehicle: (type: VehicleType, regNumber: String, brand: String, model: String) -> Unit,
    onSubmitDocument: (type: String, number: String, fileUrl: String) -> Unit,
    onStartDigiLocker: () -> Unit,
    onSelectPlan: (planId: String) -> Unit,
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
                        OnboardingStep.DOCUMENTS -> DocumentsStep(state.documents, onSubmitDocument, onStartDigiLocker)
                        OnboardingStep.SUBSCRIPTION -> SubscriptionStep(state.plans, onSelectPlan)
                        OnboardingStep.STATUS -> StatusStep(state.profile, state.vehicle, state.documents, state.subscription, onRefreshStatus, onProceedToConsole)
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

@Composable
private fun DocumentsStep(documents: List<PartnerDocument>, onSubmitDocument: (String, String, String) -> Unit, onStartDigiLocker: () -> Unit) {
    var dlNumber by remember { mutableStateOf("") }
    var rcNumber by remember { mutableStateOf("") }
    val dl = documents.firstOrNull { it.documentType == "driving_license" }
    val rc = documents.firstOrNull { it.documentType == "vehicle_rc" }
    val aadhaar = documents.firstOrNull { it.documentType == "aadhaar" }
    LazyColumn(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { CardHeading("Documents", "Verify your Aadhaar through DigiLocker and add your licence and RC.") }
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
            DocumentCard("Driving licence", dl, dlNumber, { dlNumber = it.uppercase() }, "DL-1420110012345") {
                onSubmitDocument("driving_license", dlNumber.trim(), "")
            }
        }
        item {
            DocumentCard("Vehicle RC", rc, rcNumber, { rcNumber = it.uppercase() }, "TS09AB1234") {
                onSubmitDocument("vehicle_rc", rcNumber.trim(), "")
            }
        }
        item { InfoNote("Document photos are added by Mopedu support during review; enter the numbers here.") }
    }
}

@Composable
private fun DocumentCard(
    title: String,
    document: PartnerDocument?,
    number: String,
    onNumberChanged: (String) -> Unit,
    placeholder: String,
    onSubmit: () -> Unit,
) {
    CaptainCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(title, document?.rejectionReason, modifier = Modifier.weight(1f))
            StatusBadge(document?.status ?: "pending")
        }
        UsTextField(value = number, onValueChange = onNumberChanged, label = "Number", placeholder = placeholder, modifier = Modifier.fillMaxWidth())
        UsSecondaryButton(text = "Submit", onClick = onSubmit, enabled = number.isNotBlank(), modifier = Modifier.fillMaxWidth())
    }
}

@Composable
private fun SubscriptionStep(plans: List<SubscriptionPlan>, onSelectPlan: (String) -> Unit) {
    LazyColumn(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { CardHeading("Choose a plan", "Zero commission: you keep the whole fare. A plan sets your daily lead allowance.") }
        if (plans.isEmpty()) item { InfoNote("No plans are available right now. Pull to refresh in a moment.") }
        items(plans, key = { it.id }) { plan ->
            CaptainCard {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CardHeading(plan.name, plan.description.ifBlank { null }, modifier = Modifier.weight(1f))
                    Text(
                        text = plan.price.formattedINR,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.accentSolid,
                    )
                }
                Text(
                    text = "Billing: ${plan.billingCycle.replace('_', ' ')} · Daily leads: ${plan.dailyLeadCap ?: "unlimited"}",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
                UsButton(
                    text = if (plan.price.isZero) "Start free trial" else "Choose for ${plan.price.formattedINR}",
                    onClick = { onSelectPlan(plan.id) },
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

@Composable
private fun StatusStep(
    profile: PartnerProfile?,
    vehicle: Vehicle?,
    documents: List<PartnerDocument>,
    subscription: PartnerSubscription?,
    onRefresh: () -> Unit,
    onProceed: () -> Unit,
) {
    val kycApproved = profile?.kycStatus == "approved"
    val vehicleApproved = vehicle?.status == "approved"
    val subActive = subscription?.isUsable == true
    val canGoOnline = kycApproved && vehicleApproved && subActive
    LazyColumn(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        item {
            CaptainCard(highlighted = canGoOnline) {
                CardHeading(
                    title = if (canGoOnline) "You're ready to drive" else "Verification in progress",
                    body = if (canGoOnline) {
                        "Every check has passed. Go online to start receiving rides."
                    } else {
                        "Mopedu is reviewing your profile and documents. Approvals usually take under two hours."
                    },
                    tone = if (canGoOnline) PillTone.Positive else PillTone.Warning,
                )
            }
        }
        item { SectionHeader("Checklist") }
        item { ChecklistItem("Profile", listOfNotNull(profile?.fullName, profile?.phone).joinToString(" · "), profile?.status in setOf("submitted", "under_review", "approved")) }
        item { ChecklistItem("Vehicle", "${vehicle?.registrationNumber ?: "Pending"} · ${vehicle?.vehicleType?.displayName ?: ""}", vehicleApproved) }
        item { ChecklistItem("Documents", "${documents.size} submitted · KYC ${profile?.kycStatus ?: "pending"}", kycApproved) }
        item { ChecklistItem("Plan", "${subscription?.planName ?: "None"} · ${subscription?.status ?: "inactive"}", subActive) }
        item {
            if (canGoOnline) {
                UsButton(text = "Open the captain console", onClick = onProceed, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m))
            } else {
                UsSecondaryButton(text = "Refresh status", onClick = onRefresh, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m))
            }
        }
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
