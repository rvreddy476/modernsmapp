package com.us.android.feature.mopedu.captain

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Divider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType

enum class OnboardingStep(val stepNumber: Int, val title: String) {
    PROFILE(1, "Profile Details"),
    VEHICLE(2, "Vehicle Registration"),
    DOCUMENTS(3, "KYC Documents"),
    SUBSCRIPTION(4, "Subscription Plan"),
    STATUS(5, "Approval Status"),
}

@Composable
fun MopeduCaptainOnboardingScreen(
    currentStep: OnboardingStep,
    profile: PartnerProfile?,
    vehicle: Vehicle?,
    documents: List<PartnerDocument>,
    plans: List<SubscriptionPlan>,
    subscription: PartnerSubscription?,
    isLoading: Boolean,
    errorMessage: String?,
    onNavigateBack: () -> Unit,
    onSubmitProfile: (fullName: String, phone: String, email: String?) -> Unit,
    onSubmitVehicle: (type: VehicleType, regNumber: String, brand: String, model: String) -> Unit,
    onSubmitDocument: (type: String, number: String, fileUrl: String) -> Unit,
    onStartDigiLocker: () -> Unit,
    onSelectPlan: (planId: String) -> Unit,
    onRefreshStatus: () -> Unit,
    onProceedToConsole: () -> Unit,
    modifier: Modifier = Modifier,
) {
    UsScaffold(
        topBar = {
            UsTopBar(
                title = "Captain Onboarding",
                onBack = onNavigateBack,
            )
        },
        modifier = modifier,
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .background(Color(0xFF0F172A)),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(16.dp),
            ) {
                // Step Progress Indicator
                StepProgressHeader(
                    currentStep = currentStep,
                    modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp),
                )

                if (errorMessage != null) {
                    Card(
                        colors = CardDefaults.cardColors(containerColor = Color(0x33EF4444)),
                        shape = RoundedCornerShape(12.dp),
                        modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp),
                    ) {
                        Text(
                            text = errorMessage,
                            color = Color(0xFFFCA5A5),
                            style = MaterialTheme.typography.bodyMedium,
                            modifier = Modifier.padding(12.dp),
                        )
                    }
                }

                if (isLoading) {
                    Box(
                        modifier = Modifier.fillMaxWidth().weight(1f),
                        contentAlignment = Alignment.Center,
                    ) {
                        CircularProgressIndicator(color = Color(0xFFF59E0B))
                    }
                } else {
                    Box(modifier = Modifier.weight(1f)) {
                        when (currentStep) {
                            OnboardingStep.PROFILE -> ProfileStepCard(
                                profile = profile,
                                onSubmit = onSubmitProfile,
                            )
                            OnboardingStep.VEHICLE -> VehicleStepCard(
                                vehicle = vehicle,
                                onSubmit = onSubmitVehicle,
                            )
                            OnboardingStep.DOCUMENTS -> DocumentsStepCard(
                                documents = documents,
                                onSubmitDocument = onSubmitDocument,
                                onStartDigiLocker = onStartDigiLocker,
                            )
                            OnboardingStep.SUBSCRIPTION -> SubscriptionStepCard(
                                plans = plans,
                                onSelectPlan = onSelectPlan,
                            )
                            OnboardingStep.STATUS -> StatusStepCard(
                                profile = profile,
                                vehicle = vehicle,
                                documents = documents,
                                subscription = subscription,
                                onRefresh = onRefreshStatus,
                                onProceed = onProceedToConsole,
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun StepProgressHeader(
    currentStep: OnboardingStep,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        OnboardingStep.entries.forEach { step ->
            val isCompleted = step.stepNumber < currentStep.stepNumber
            val isCurrent = step == currentStep
            val circleColor = when {
                isCompleted -> Color(0xFF10B981)
                isCurrent -> Color(0xFFF59E0B)
                else -> Color(0xFF334155)
            }

            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                modifier = Modifier.weight(1f),
            ) {
                Box(
                    modifier = Modifier
                        .size(28.dp)
                        .clip(CircleShape)
                        .background(circleColor),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = if (isCompleted) "✓" else "${step.stepNumber}",
                        color = Color.White,
                        fontSize = 12.sp,
                        fontWeight = FontWeight.Bold,
                    )
                }
                Spacer(modifier = Modifier.height(4.dp))
                Text(
                    text = step.title.split(" ").first(),
                    fontSize = 10.sp,
                    color = if (isCurrent) Color(0xFFF59E0B) else Color.LightGray,
                    textAlign = TextAlign.Center,
                )
            }
        }
    }
}

// --- Step 1: Profile ---

@Composable
private fun ProfileStepCard(
    profile: PartnerProfile?,
    onSubmit: (fullName: String, phone: String, email: String?) -> Unit,
) {
    var fullName by remember { mutableStateOf(profile?.fullName ?: "") }
    var phone by remember { mutableStateOf(profile?.phone ?: "") }
    var email by remember { mutableStateOf(profile?.email ?: "") }

    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item {
            Text(
                text = "Partner Information",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
            Text(
                text = "Enter your personal details to create your captain account.",
                style = MaterialTheme.typography.bodySmall,
                color = Color.LightGray,
                modifier = Modifier.padding(top = 4.dp, bottom = 16.dp),
            )

            UsTextField(
                value = fullName,
                onValueChange = { fullName = it },
                label = "Full Name (as per Aadhaar)",
                placeholder = "e.g. Rahul Sharma",
                modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp),
            )

            UsTextField(
                value = phone,
                onValueChange = { phone = it },
                label = "Mobile Phone Number",
                placeholder = "e.g. +919876543210",
                modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp),
            )

            UsTextField(
                value = email,
                onValueChange = { email = it },
                label = "Email Address (Optional)",
                placeholder = "e.g. rahul@example.com",
                modifier = Modifier.fillMaxWidth().padding(bottom = 24.dp),
            )

            UsButton(
                text = "Continue to Vehicle Setup",
                onClick = {
                    if (fullName.isNotBlank() && phone.isNotBlank()) {
                        onSubmit(fullName.trim(), phone.trim(), email.trim().ifEmpty { null })
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

// --- Step 2: Vehicle ---

@Composable
private fun VehicleStepCard(
    vehicle: Vehicle?,
    onSubmit: (type: VehicleType, regNumber: String, brand: String, model: String) -> Unit,
) {
    var selectedTab by remember { mutableStateOf(if (vehicle?.vehicleType == VehicleType.AUTO) 1 else 0) }
    var regNumber by remember { mutableStateOf(vehicle?.registrationNumber ?: "") }
    var brand by remember { mutableStateOf(vehicle?.brand ?: "") }
    var model by remember { mutableStateOf(vehicle?.model ?: "") }

    val vehicleType = if (selectedTab == 0) VehicleType.BIKE else VehicleType.AUTO

    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item {
            Text(
                text = "Vehicle Details",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
            Text(
                text = "Select vehicle type and enter registration plate details.",
                style = MaterialTheme.typography.bodySmall,
                color = Color.LightGray,
                modifier = Modifier.padding(top = 4.dp, bottom = 16.dp),
            )

            TabRow(
                selectedTabIndex = selectedTab,
                containerColor = Color(0xFF1E293B),
                contentColor = Color(0xFFF59E0B),
                modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).padding(bottom = 16.dp),
            ) {
                Tab(
                    selected = selectedTab == 0,
                    onClick = { selectedTab = 0 },
                    text = { Text("🏍️ Mopedu Bike") },
                )
                Tab(
                    selected = selectedTab == 1,
                    onClick = { selectedTab = 1 },
                    text = { Text("🛺 Mopedu Auto") },
                )
            }

            UsTextField(
                value = regNumber,
                onValueChange = { regNumber = it.uppercase() },
                label = "Registration Number (Number Plate)",
                placeholder = "e.g. KA01AB1234",
                modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp),
            )

            UsTextField(
                value = brand,
                onValueChange = { brand = it },
                label = "Vehicle Manufacturer / Brand",
                placeholder = if (vehicleType == VehicleType.BIKE) "e.g. Bajaj / Honda / TVS" else "e.g. Bajaj Auto / Piaggio",
                modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp),
            )

            UsTextField(
                value = model,
                onValueChange = { model = it },
                label = "Vehicle Model",
                placeholder = if (vehicleType == VehicleType.BIKE) "e.g. Pulsar 150 / Activa 6G" else "e.g. Compact 4S / RE Optima",
                modifier = Modifier.fillMaxWidth().padding(bottom = 24.dp),
            )

            UsButton(
                text = "Save Vehicle & Continue",
                onClick = {
                    if (regNumber.isNotBlank()) {
                        onSubmit(vehicleType, regNumber.trim(), brand.trim(), model.trim())
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

// --- Step 3: Documents & KYC ---

@Composable
private fun DocumentsStepCard(
    documents: List<PartnerDocument>,
    onSubmitDocument: (type: String, number: String, fileUrl: String) -> Unit,
    onStartDigiLocker: () -> Unit,
) {
    var dlNumber by remember { mutableStateOf("") }
    var rcNumber by remember { mutableStateOf("") }

    val dlDoc = documents.firstOrNull { it.documentType == "driving_license" }
    val rcDoc = documents.firstOrNull { it.documentType == "vehicle_rc" }
    val aadhaarDoc = documents.firstOrNull { it.documentType == "aadhaar" }

    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item {
            Text(
                text = "KYC & Document Verification",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
            Text(
                text = "Upload required legal documents and verify Aadhaar via DigiLocker.",
                style = MaterialTheme.typography.bodySmall,
                color = Color.LightGray,
                modifier = Modifier.padding(top = 4.dp, bottom = 16.dp),
            )

            // Aadhaar DigiLocker Card
            Card(
                colors = CardDefaults.cardColors(containerColor = Color(0xFF1E293B)),
                shape = RoundedCornerShape(12.dp),
                modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp),
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = "🇮🇳 Aadhaar DigiLocker KYC",
                            fontWeight = FontWeight.Bold,
                            color = Color.White,
                        )
                        DocStatusBadge(status = aadhaarDoc?.status ?: "pending")
                    }
                    Text(
                        text = "Instant 1-click verification via Govt. DigiLocker portal. No raw Aadhaar number stored.",
                        style = MaterialTheme.typography.bodySmall,
                        color = Color.LightGray,
                        modifier = Modifier.padding(vertical = 8.dp),
                    )
                    UsButton(
                        text = if (aadhaarDoc?.status == "verified") "Aadhaar Verified ✓" else "Verify with DigiLocker",
                        onClick = onStartDigiLocker,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }

            // Driving License Card
            Card(
                colors = CardDefaults.cardColors(containerColor = Color(0xFF1E293B)),
                shape = RoundedCornerShape(12.dp),
                modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp),
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = "🪪 Driving License (DL)",
                            fontWeight = FontWeight.Bold,
                            color = Color.White,
                        )
                        DocStatusBadge(status = dlDoc?.status ?: "pending")
                    }
                    Spacer(modifier = Modifier.height(8.dp))
                    UsTextField(
                        value = dlNumber,
                        onValueChange = { dlNumber = it.uppercase() },
                        label = "DL Number",
                        placeholder = "e.g. DL-1420110012345",
                        modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp),
                    )
                    UsButton(
                        text = "Submit Driving License",
                        onClick = {
                            if (dlNumber.isNotBlank()) {
                                onSubmitDocument("driving_license", dlNumber.trim(), "https://media.atpost.us/kyc/dl_${dlNumber}.jpg")
                            }
                        },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }

            // Vehicle RC Card
            Card(
                colors = CardDefaults.cardColors(containerColor = Color(0xFF1E293B)),
                shape = RoundedCornerShape(12.dp),
                modifier = Modifier.fillMaxWidth().padding(bottom = 24.dp),
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = "📄 Vehicle RC (Registration Certificate)",
                            fontWeight = FontWeight.Bold,
                            color = Color.White,
                        )
                        DocStatusBadge(status = rcDoc?.status ?: "pending")
                    }
                    Spacer(modifier = Modifier.height(8.dp))
                    UsTextField(
                        value = rcNumber,
                        onValueChange = { rcNumber = it.uppercase() },
                        label = "RC Number / Registration",
                        placeholder = "e.g. KA01AB1234",
                        modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp),
                    )
                    UsButton(
                        text = "Submit Vehicle RC",
                        onClick = {
                            if (rcNumber.isNotBlank()) {
                                onSubmitDocument("vehicle_rc", rcNumber.trim(), "https://media.atpost.us/kyc/rc_${rcNumber}.jpg")
                            }
                        },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

// --- Step 4: Subscriptions ---

@Composable
private fun SubscriptionStepCard(
    plans: List<SubscriptionPlan>,
    onSelectPlan: (planId: String) -> Unit,
) {
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item {
            Text(
                text = "Zero Commission Subscription Plans",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
            Text(
                text = "Keep 100% of your trip fares. Select a flexible plan with daily lead allowance.",
                style = MaterialTheme.typography.bodySmall,
                color = Color.LightGray,
                modifier = Modifier.padding(top = 4.dp, bottom = 16.dp),
            )
        }

        items(plans) { plan ->
            Card(
                colors = CardDefaults.cardColors(containerColor = Color(0xFF1E293B)),
                shape = RoundedCornerShape(12.dp),
                modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp),
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = plan.name,
                            fontWeight = FontWeight.Bold,
                            fontSize = 16.sp,
                            color = Color.White,
                        )
                        Text(
                            text = plan.price.formattedINR,
                            fontWeight = FontWeight.Bold,
                            fontSize = 18.sp,
                            color = Color(0xFFF59E0B),
                        )
                    }
                    Text(
                        text = "Billing: ${plan.billingCycle.replace("_", " ").uppercase()} • Daily leads: ${plan.dailyLeadCap ?: "Unlimited"}",
                        style = MaterialTheme.typography.bodySmall,
                        color = Color.LightGray,
                        modifier = Modifier.padding(vertical = 6.dp),
                    )
                    Spacer(modifier = Modifier.height(4.dp))
                    UsButton(
                        text = if (plan.price.paise == 0L) "Activate Free 7-Day Trial" else "Subscribe for ${plan.price.formattedINR}",
                        onClick = { onSelectPlan(plan.id) },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

// --- Step 5: Status & Review ---

@Composable
private fun StatusStepCard(
    profile: PartnerProfile?,
    vehicle: Vehicle?,
    documents: List<PartnerDocument>,
    subscription: PartnerSubscription?,
    onRefresh: () -> Unit,
    onProceed: () -> Unit,
) {
    val isKycApproved = profile?.kycStatus == "approved"
    val isVehicleApproved = vehicle?.status == "approved"
    val isSubActive = subscription?.status in setOf("trial", "active")
    val canGoOnline = isKycApproved && isVehicleApproved && isSubActive

    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(16.dp))
                    .background(
                        Brush.verticalGradient(
                            listOf(Color(0xFF1E293B), Color(0xFF0F172A))
                        )
                    )
                    .padding(20.dp),
                contentAlignment = Alignment.Center,
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(
                        text = if (canGoOnline) "🎉 Captain Account Ready!" else "⏳ Verification In Progress",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = if (canGoOnline) Color(0xFF10B981) else Color(0xFFF59E0B),
                    )
                    Spacer(modifier = Modifier.height(6.dp))
                    Text(
                        text = if (canGoOnline)
                            "All compliance checks have passed. You are ready to start accepting ride requests."
                        else
                            "Your profile and documents are being reviewed by the operations team. Approvals usually take under 2 hours.",
                        style = MaterialTheme.typography.bodySmall,
                        color = Color.LightGray,
                        textAlign = TextAlign.Center,
                    )
                }
            }

            Spacer(modifier = Modifier.height(16.dp))
            Text(
                text = "Compliance Checklist",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                modifier = Modifier.padding(bottom = 8.dp),
            )

            ChecklistItem(
                title = "Partner Profile",
                subtitle = "${profile?.fullName} (${profile?.phone})",
                isComplete = profile?.status in setOf("submitted", "under_review", "approved"),
            )

            ChecklistItem(
                title = "Vehicle Registration",
                subtitle = "${vehicle?.registrationNumber ?: "Pending"} (${vehicle?.vehicleType?.displayName ?: "Bike"})",
                isComplete = isVehicleApproved,
            )

            ChecklistItem(
                title = "KYC Documents",
                subtitle = "${documents.size} submitted • Status: ${profile?.kycStatus?.uppercase() ?: "PENDING"}",
                isComplete = isKycApproved,
            )

            ChecklistItem(
                title = "Active Subscription",
                subtitle = "Plan: ${subscription?.planName ?: "None"} (${subscription?.status?.uppercase() ?: "INACTIVE"})",
                isComplete = isSubActive,
            )

            Spacer(modifier = Modifier.height(24.dp))

            if (canGoOnline) {
                UsButton(
                    text = "Launch Captain Console 🚀",
                    onClick = onProceed,
                    modifier = Modifier.fillMaxWidth(),
                )
            } else {
                UsButton(
                    text = "Refresh Verification Status 🔄",
                    onClick = onRefresh,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

@Composable
private fun ChecklistItem(
    title: String,
    subtitle: String,
    isComplete: Boolean,
) {
    Card(
        colors = CardDefaults.cardColors(containerColor = Color(0xFF1E293B)),
        shape = RoundedCornerShape(10.dp),
        modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(12.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = title,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                    fontSize = 14.sp,
                )
                Text(
                    text = subtitle,
                    color = Color.LightGray,
                    fontSize = 12.sp,
                )
            }
            Box(
                modifier = Modifier
                    .size(24.dp)
                    .clip(CircleShape)
                    .background(if (isComplete) Color(0xFF10B981) else Color(0xFF64748B)),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = if (isComplete) "✓" else "⋯",
                    color = Color.White,
                    fontWeight = FontWeight.Bold,
                    fontSize = 12.sp,
                )
            }
        }
    }
}

@Composable
private fun DocStatusBadge(status: String) {
    val (bg, label) = when (status.lowercase()) {
        "verified", "approved" -> Pair(Color(0xFF065F46), "VERIFIED ✓")
        "submitted", "under_review" -> Pair(Color(0xFF92400E), "UNDER REVIEW ⋯")
        "rejected" -> Pair(Color(0xFF991B1B), "REJECTED ✕")
        else -> Pair(Color(0xFF334155), "PENDING")
    }

    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(6.dp))
            .background(bg)
            .padding(horizontal = 8.dp, vertical = 4.dp),
    ) {
        Text(
            text = label,
            fontSize = 10.sp,
            fontWeight = FontWeight.Bold,
            color = Color.White,
        )
    }
}
