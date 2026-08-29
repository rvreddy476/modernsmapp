package com.us.android.feature.mopedu.captain

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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Divider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.maps.MopeduMapView
import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.VehicleType

@Composable
fun MopeduCaptainRoute(
    onNavigateBack: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: MopeduCaptainViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    MopeduCaptainScreen(
        uiState = uiState,
        onNavigateBack = onNavigateBack,
        onToggleOnline = viewModel::toggleOnline,
        onAcceptOffer = viewModel::acceptOffer,
        onRejectOffer = viewModel::rejectOffer,
        onMarkArrived = viewModel::markArrived,
        onOtpChanged = viewModel::onOtpInputChanged,
        onVerifyOtp = viewModel::verifyOtpAndStart,
        onCompleteTrip = viewModel::completeTrip,
        onConfirmCash = viewModel::confirmCashPayment,
        onSubmitProfile = viewModel::submitProfile,
        onSubmitVehicle = viewModel::submitVehicle,
        onSubmitDocument = viewModel::submitDocument,
        onStartDigiLocker = viewModel::startDigiLocker,
        onSelectPlan = viewModel::selectPlan,
        onRefreshStatus = viewModel::refreshOnboardingStatus,
        onProceedToConsole = viewModel::proceedToConsole,
        onOpenOnboarding = viewModel::openOnboarding,
        modifier = modifier,
    )
}

@Composable
fun MopeduCaptainScreen(
    uiState: CaptainUiState,
    onNavigateBack: () -> Unit,
    onToggleOnline: () -> Unit,
    onAcceptOffer: (CaptainOffer) -> Unit,
    onRejectOffer: () -> Unit,
    onMarkArrived: () -> Unit,
    onOtpChanged: (String) -> Unit,
    onVerifyOtp: () -> Unit,
    onCompleteTrip: () -> Unit,
    onConfirmCash: () -> Unit,
    onSubmitProfile: (fullName: String, phone: String, email: String?) -> Unit,
    onSubmitVehicle: (type: VehicleType, regNumber: String, brand: String, model: String) -> Unit,
    onSubmitDocument: (type: String, number: String, fileUrl: String) -> Unit,
    onStartDigiLocker: () -> Unit,
    onSelectPlan: (planId: String) -> Unit,
    onRefreshStatus: () -> Unit,
    onProceedToConsole: () -> Unit,
    onOpenOnboarding: (OnboardingStep) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (uiState is CaptainUiState.Onboarding) {
        MopeduCaptainOnboardingScreen(
            currentStep = uiState.step,
            profile = uiState.profile,
            vehicle = uiState.vehicle,
            documents = uiState.documents,
            plans = uiState.plans,
            subscription = uiState.subscription,
            isLoading = uiState.isLoading,
            errorMessage = uiState.errorMessage,
            onNavigateBack = onNavigateBack,
            onSubmitProfile = onSubmitProfile,
            onSubmitVehicle = onSubmitVehicle,
            onSubmitDocument = onSubmitDocument,
            onStartDigiLocker = onStartDigiLocker,
            onSelectPlan = onSelectPlan,
            onRefreshStatus = onRefreshStatus,
            onProceedToConsole = onProceedToConsole,
            modifier = modifier,
        )
        return
    }

    val (pickup, drop) = when (uiState) {
        is CaptainUiState.EnRouteToPickup -> Pair(uiState.booking.pickup, uiState.booking.drop)
        is CaptainUiState.VerifyOtp -> Pair(uiState.booking.pickup, uiState.booking.drop)
        is CaptainUiState.TripInProgress -> Pair(uiState.booking.pickup, uiState.booking.drop)
        is CaptainUiState.CollectPayment -> Pair(uiState.booking.pickup, uiState.booking.drop)
        is CaptainUiState.OnlineIdle -> Pair(uiState.incomingOffer?.pickup, uiState.incomingOffer?.drop)
        else -> Pair(null, null)
    }

    UsScaffold(
        topBar = {
            UsTopBar(
                title = "Mopedu Captain Console",
                onBack = onNavigateBack,
            )
        },
        modifier = modifier,
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            MopeduMapView(
                pickup = pickup,
                drop = drop,
                modifier = Modifier.fillMaxSize(),
            )

            // Top Status Bar (Online/Offline Toggle & Earnings)
            val captainState = when (uiState) {
                is CaptainUiState.Offline -> uiState.captainState
                is CaptainUiState.OnlineIdle -> uiState.captainState
                else -> null
            }
            if (captainState != null) {
                CaptainHeaderStats(
                    state = captainState,
                    onToggleOnline = onToggleOnline,
                    onOpenProfile = { onOpenOnboarding(OnboardingStep.STATUS) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .align(Alignment.TopCenter)
                        .padding(16.dp),
                )
            }

            // Bottom Sheets
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .align(Alignment.BottomCenter)
                    .padding(16.dp),
            ) {
                when (uiState) {
                    is CaptainUiState.Offline -> {
                        CaptainOfflineCard(
                            onGoOnline = onToggleOnline,
                            onOpenKyc = { onOpenOnboarding(OnboardingStep.STATUS) },
                        )
                    }
                    is CaptainUiState.OnlineIdle -> {
                        if (uiState.incomingOffer != null) {
                            IncomingOfferCard(
                                offer = uiState.incomingOffer,
                                onAccept = { onAcceptOffer(uiState.incomingOffer) },
                                onReject = onRejectOffer,
                            )
                        } else {
                            CaptainOnlineIdleCard(
                                onGoOffline = onToggleOnline,
                            )
                        }
                    }
                    is CaptainUiState.EnRouteToPickup -> {
                        CaptainEnRouteCard(
                            booking = uiState.booking,
                            onMarkArrived = onMarkArrived,
                        )
                    }
                    is CaptainUiState.VerifyOtp -> {
                        CaptainVerifyOtpCard(
                            booking = uiState.booking,
                            otpInput = uiState.otpInput,
                            onOtpChanged = onOtpChanged,
                            onVerifyOtp = onVerifyOtp,
                            isVerifying = uiState.isVerifying,
                            errorMessage = uiState.errorMessage,
                        )
                    }
                    is CaptainUiState.TripInProgress -> {
                        CaptainTripInProgressCard(
                            booking = uiState.booking,
                            onCompleteTrip = onCompleteTrip,
                        )
                    }
                    is CaptainUiState.CollectPayment -> {
                        CollectPaymentCard(
                            booking = uiState.booking,
                            onConfirmCash = onConfirmCash,
                        )
                    }
                    else -> {}
                }
            }
        }
    }
}

@Composable
private fun CaptainHeaderStats(
    state: CaptainState,
    onToggleOnline: () -> Unit,
    onOpenProfile: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Card(
        shape = RoundedCornerShape(16.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = modifier.border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(16.dp)),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Column(modifier = Modifier.clickable { onOpenProfile() }) {
                Text(
                    text = if (state.isOnline) "🟢 ONLINE" else "⚪ OFFLINE",
                    fontWeight = FontWeight.Bold,
                    color = if (state.isOnline) Color(0xFF00E676) else Color.Gray,
                    fontSize = 12.sp,
                )
                Text(
                    text = "Today: ${state.todayEarnings.formattedINR}",
                    style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.ExtraBold),
                    color = Color.White,
                )
                Text(
                    text = "⭐ ${state.rating} • ${state.totalRidesCompleted} trips",
                    fontSize = 11.sp,
                    color = Color.LightGray,
                )
            }

            UsButton(
                text = if (state.isOnline) "GO OFFLINE" else "GO ONLINE",
                onClick = onToggleOnline,
                modifier = Modifier.width(130.dp),
            )
        }
    }
}

@Composable
private fun CaptainOfflineCard(
    onGoOnline: () -> Unit,
    onOpenKyc: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(
            modifier = Modifier.padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("You are currently Offline", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
            Spacer(modifier = Modifier.height(4.dp))
            Text("Go online to start receiving nearby Mopedu ride requests.", color = Color.LightGray, fontSize = 13.sp, textAlign = TextAlign.Center)
            Spacer(modifier = Modifier.height(16.dp))
            UsButton(
                text = "GO ONLINE",
                onClick = onGoOnline,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(modifier = Modifier.height(8.dp))
            Text(
                text = "View KYC Status & Subscription Details",
                color = Color(0xFFF59E0B),
                fontSize = 12.sp,
                fontWeight = FontWeight.Medium,
                modifier = Modifier.clickable { onOpenKyc() }.padding(4.dp),
            )
        }
    }
}

@Composable
private fun CaptainOnlineIdleCard(
    onGoOffline: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(
            modifier = Modifier.padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("Looking for nearby rides...", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
            Spacer(modifier = Modifier.height(4.dp))
            Text("Keep the app open to receive new ride requests instantly.", color = Color.LightGray, fontSize = 13.sp)
            Spacer(modifier = Modifier.height(16.dp))
            UsButton(
                text = "GO OFFLINE",
                onClick = onGoOffline,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun IncomingOfferCard(
    offer: CaptainOffer,
    onAccept: () -> Unit,
    onReject: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xFF1E293B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(2.dp, Color(0xFFF59E0B), RoundedCornerShape(24.dp)),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = "🚨 NEW RIDE OFFER",
                    color = Color(0xFFF59E0B),
                    fontWeight = FontWeight.Bold,
                    fontSize = 13.sp,
                )
                Text(
                    text = offer.estimatedEarnings.formattedINR,
                    color = Color(0xFF10B981),
                    fontWeight = FontWeight.ExtraBold,
                    fontSize = 22.sp,
                )
            }

            Spacer(modifier = Modifier.height(12.dp))
            Text("Pickup: ${offer.pickup.address}", color = Color.White, fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
            Text("Drop: ${offer.drop.address}", color = Color.LightGray, fontSize = 13.sp)
            Spacer(modifier = Modifier.height(6.dp))
            Text("Distance: ${String.format("%.1f", offer.distanceKM)} km", color = Color(0xFF93C5FD), fontSize = 12.sp)

            Spacer(modifier = Modifier.height(16.dp))
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                UsButton(
                    text = "REJECT",
                    onClick = onReject,
                    modifier = Modifier.weight(1f),
                )
                UsButton(
                    text = "ACCEPT RIDE",
                    onClick = onAccept,
                    modifier = Modifier.weight(1.5f),
                )
            }
        }
    }
}

@Composable
private fun CaptainEnRouteCard(
    booking: RideBooking,
    onMarkArrived: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Text("En Route to Pickup", color = Color(0xFFF59E0B), fontSize = 12.sp, fontWeight = FontWeight.Bold)
            Text("Pickup: ${booking.pickup.address}", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
            Spacer(modifier = Modifier.height(6.dp))
            Text("Passenger: Rider #${booking.customerUserId.takeLast(6)}", color = Color.LightGray, fontSize = 13.sp)
            Spacer(modifier = Modifier.height(16.dp))
            UsButton(
                text = "I HAVE ARRIVED AT PICKUP",
                onClick = onMarkArrived,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun CaptainVerifyOtpCard(
    booking: RideBooking,
    otpInput: String,
    onOtpChanged: (String) -> Unit,
    onVerifyOtp: () -> Unit,
    isVerifying: Boolean,
    errorMessage: String?,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("Arrived at Pickup", color = Color(0xFF00E676), fontSize = 12.sp, fontWeight = FontWeight.Bold)
            Text("Ask Rider for 4-Digit Trip OTP", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
            Spacer(modifier = Modifier.height(16.dp))

            UsOtpField(
                value = otpInput,
                onValueChange = onOtpChanged,
                length = 4,
                errorText = errorMessage,
            )

            Spacer(modifier = Modifier.height(16.dp))
            UsButton(
                text = if (isVerifying) "VERIFYING..." else "VERIFY OTP & START TRIP",
                onClick = onVerifyOtp,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun CaptainTripInProgressCard(
    booking: RideBooking,
    onCompleteTrip: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Text("Trip in Progress", color = Color(0xFF00E676), fontSize = 12.sp, fontWeight = FontWeight.Bold)
            Text("Destination: ${booking.drop.address}", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
            Spacer(modifier = Modifier.height(16.dp))
            UsButton(
                text = "END TRIP AT DESTINATION",
                onClick = onCompleteTrip,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun CollectPaymentCard(
    booking: RideBooking,
    onConfirmCash: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(2.dp, Color(0xFF00E676), RoundedCornerShape(24.dp)),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("Trip Completed", color = Color(0xFF00E676), fontSize = 13.sp, fontWeight = FontWeight.Bold)
            Text("Collect Cash from Passenger", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
            Spacer(modifier = Modifier.height(12.dp))
            Text(booking.estimatedFare.formattedINR, fontSize = 32.sp, fontWeight = FontWeight.ExtraBold, color = Color(0xFF00E676))
            Spacer(modifier = Modifier.height(16.dp))
            UsButton(
                text = "CONFIRM CASH RECEIVED",
                onClick = onConfirmCash,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}
