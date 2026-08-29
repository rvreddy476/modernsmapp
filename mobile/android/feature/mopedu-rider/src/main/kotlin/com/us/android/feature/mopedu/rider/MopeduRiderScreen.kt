package com.us.android.feature.mopedu.rider

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
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
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Divider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.maps.MopeduMapView
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.QuoteOption

@Composable
fun MopeduRiderRoute(
    onNavigateBack: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: MopeduRiderViewModel = hiltViewModel(),
) {
    if (!MopeduFeatureGate.isEnabled) {
        UsScaffold(
            topBar = {
                UsTopBar(
                    title = "Mopedu Rides",
                    onBack = onNavigateBack,
                )
            },
            modifier = modifier,
        ) { padding ->
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(padding)
                    .padding(24.dp),
                contentAlignment = Alignment.Center,
            ) {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                ) {
                    Text(
                        text = "Mopedu Mobility Pilot",
                        style = MaterialTheme.typography.titleLarge,
                        fontWeight = FontWeight.Bold,
                        color = Color.White,
                    )
                    Spacer(modifier = Modifier.height(8.dp))
                    Text(
                        text = "The Mopedu mobility pilot is currently unavailable in this release.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = Color.LightGray,
                        textAlign = TextAlign.Center,
                    )
                    Spacer(modifier = Modifier.height(24.dp))
                    UsButton(
                        text = "Return",
                        onClick = onNavigateBack,
                    )
                }
            }
        }
        return
    }

    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    MopeduRiderScreen(
        uiState = uiState,
        onNavigateBack = onNavigateBack,
        onRequestQuote = viewModel::requestQuote,
        onSelectOption = viewModel::selectVehicleOption,
        onConfirmBooking = viewModel::confirmBooking,
        onTriggerSOS = viewModel::triggerSOS,
        onGenerateShare = viewModel::generateShareLink,
        onSubmitRating = viewModel::submitRating,
        onReset = viewModel::resetToNewBooking,
        modifier = modifier,
    )
}

@Composable
fun MopeduRiderScreen(
    uiState: RiderUiState,
    onNavigateBack: () -> Unit,
    onRequestQuote: () -> Unit,
    onSelectOption: (QuoteOption) -> Unit,
    onConfirmBooking: () -> Unit,
    onTriggerSOS: () -> Unit,
    onGenerateShare: () -> Unit,
    onSubmitRating: (Int, String) -> Unit,
    onReset: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val pickup = when (uiState) {
        is RiderUiState.LocationSelect -> uiState.pickup
        is RiderUiState.QuoteSelect -> uiState.quote.pickup
        is RiderUiState.SearchingCaptain -> uiState.booking.pickup
        is RiderUiState.CaptainAssigned -> uiState.booking.pickup
        is RiderUiState.ArrivedAtPickup -> uiState.booking.pickup
        is RiderUiState.TripInProgress -> uiState.booking.pickup
        is RiderUiState.TripCompleted -> null
    }

    val drop = when (uiState) {
        is RiderUiState.LocationSelect -> uiState.drop
        is RiderUiState.QuoteSelect -> uiState.quote.drop
        is RiderUiState.SearchingCaptain -> uiState.booking.drop
        is RiderUiState.CaptainAssigned -> uiState.booking.drop
        is RiderUiState.ArrivedAtPickup -> uiState.booking.drop
        is RiderUiState.TripInProgress -> uiState.booking.drop
        is RiderUiState.TripCompleted -> null
    }

    val captainLocation = when (uiState) {
        is RiderUiState.CaptainAssigned -> uiState.captainLocation
        else -> null
    }

    UsScaffold(
        topBar = {
            UsTopBar(
                title = "Mopedu Mobility",
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
            // Background Live Map
            MopeduMapView(
                pickup = pickup,
                drop = drop,
                captainLocation = captainLocation,
                modifier = Modifier.fillMaxSize(),
            )

            // Dynamic Foreground Status / Bottom Sheet Sheet
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .align(Alignment.BottomCenter)
                    .padding(16.dp),
            ) {
                when (uiState) {
                    is RiderUiState.LocationSelect -> {
                        LocationSearchCard(
                            pickup = uiState.pickup,
                            drop = uiState.drop,
                            onRequestQuote = onRequestQuote,
                        )
                    }
                    is RiderUiState.QuoteSelect -> {
                        QuoteSelectionCard(
                            options = uiState.quote.options,
                            selectedOption = uiState.selectedOption,
                            onSelectOption = onSelectOption,
                            onConfirmBooking = onConfirmBooking,
                        )
                    }
                    is RiderUiState.SearchingCaptain -> {
                        SearchingCaptainCard(
                            booking = uiState.booking,
                        )
                    }
                    is RiderUiState.CaptainAssigned -> {
                        CaptainAssignedCard(
                            booking = uiState.booking,
                            etaMinutes = uiState.etaMinutes,
                        )
                    }
                    is RiderUiState.ArrivedAtPickup -> {
                        ArrivedWithOtpCard(
                            otp = uiState.otp,
                            captain = uiState.booking.captain,
                        )
                    }
                    is RiderUiState.TripInProgress -> {
                        TripInProgressCard(
                            booking = uiState.booking,
                            shareLink = uiState.shareLink,
                            sosTriggered = uiState.sosTriggered,
                            onTriggerSOS = onTriggerSOS,
                            onGenerateShare = onGenerateShare,
                        )
                    }
                    is RiderUiState.TripCompleted -> {
                        TripCompletedCard(
                            receipt = uiState.receipt,
                            ratingSubmitted = uiState.ratingSubmitted,
                            onSubmitRating = onSubmitRating,
                            onReset = onReset,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun LocationSearchCard(
    pickup: GeoPoint,
    drop: GeoPoint,
    onRequestQuote: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Text(
                text = "Where are you going?",
                style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold),
                color = Color.White,
            )
            Spacer(modifier = Modifier.height(16.dp))

            // Pickup Row
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(12.dp)
                        .background(Color(0xFF00C853), CircleShape),
                )
                Spacer(modifier = Modifier.width(12.dp))
                Column {
                    Text("Pickup Location", fontSize = 11.sp, color = Color.Gray)
                    Text(pickup.address, fontSize = 14.sp, color = Color.White, fontWeight = FontWeight.Medium)
                }
            }

            Spacer(modifier = Modifier.height(12.dp))
            Divider(color = Color(0x22FFFFFF))
            Spacer(modifier = Modifier.height(12.dp))

            // Dropoff Row
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(12.dp)
                        .background(Color(0xFFFF5252), CircleShape),
                )
                Spacer(modifier = Modifier.width(12.dp))
                Column {
                    Text("Destination", fontSize = 11.sp, color = Color.Gray)
                    Text(drop.address, fontSize = 14.sp, color = Color.White, fontWeight = FontWeight.Medium)
                }
            }

            Spacer(modifier = Modifier.height(20.dp))
            UsButton(
                text = "View Fares & Availability",
                onClick = onRequestQuote,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun QuoteSelectionCard(
    options: List<QuoteOption>,
    selectedOption: QuoteOption,
    onSelectOption: (QuoteOption) -> Unit,
    onConfirmBooking: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Text(
                text = "Choose Vehicle",
                style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold),
                color = Color.White,
            )
            Spacer(modifier = Modifier.height(12.dp))

            options.forEach { opt ->
                val isSelected = opt.vehicleType == selectedOption.vehicleType
                val borderModifier = if (isSelected) {
                    Modifier.border(2.dp, Color(0xFF00E676), RoundedCornerShape(16.dp))
                } else {
                    Modifier.border(1.dp, Color(0x22FFFFFF), RoundedCornerShape(16.dp))
                }

                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 4.dp)
                        .clip(RoundedCornerShape(16.dp))
                        .background(if (isSelected) Color(0x3300E676) else Color(0x1AFFFFFF))
                        .then(borderModifier)
                        .clickable { onSelectOption(opt) }
                        .padding(14.dp),
                ) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Column {
                            Text(
                                text = opt.vehicleType.displayName,
                                style = MaterialTheme.typography.bodyLarge.copy(fontWeight = FontWeight.Bold),
                                color = Color.White,
                            )
                            Text(
                                text = "${opt.pickupETASeconds / 60} mins away • ${(opt.distanceMeters / 1000.0)} km",
                                fontSize = 12.sp,
                                color = Color.Gray,
                            )
                        }
                        Text(
                            text = opt.totalFare.formattedINR,
                            style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold),
                            color = Color(0xFF00E676),
                        )
                    }
                }
            }

            Spacer(modifier = Modifier.height(16.dp))
            UsButton(
                text = "Book ${selectedOption.vehicleType.displayName} • ${selectedOption.totalFare.formattedINR}",
                onClick = onConfirmBooking,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun SearchingCaptainCard(
    booking: com.us.android.core.mobility.model.RideBooking,
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
                .padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            CircularProgressIndicator(color = Color(0xFF00E676), modifier = Modifier.size(48.dp))
            Spacer(modifier = Modifier.height(16.dp))
            Text(
                text = "Connecting nearby captains...",
                style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold),
                color = Color.White,
            )
            Spacer(modifier = Modifier.height(6.dp))
            Text(
                text = "Dispatching request to nearest verified partners in Hyderabad",
                fontSize = 12.sp,
                color = Color.Gray,
                textAlign = TextAlign.Center,
            )
        }
    }
}

@Composable
private fun CaptainAssignedCard(
    booking: com.us.android.core.mobility.model.RideBooking,
    etaMinutes: Int,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column {
                    Text("Captain on the way", fontSize = 12.sp, color = Color(0xFF00E676), fontWeight = FontWeight.Bold)
                    Text("Arriving in $etaMinutes mins", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
                }
                Box(
                    modifier = Modifier
                        .background(Color(0xFF2C3240), RoundedCornerShape(8.dp))
                        .padding(horizontal = 10.dp, vertical = 6.dp),
                ) {
                    Text(booking.captain?.vehicleNumber ?: "", color = Color(0xFFFFD600), fontWeight = FontWeight.Bold, fontSize = 13.sp)
                }
            }

            Spacer(modifier = Modifier.height(14.dp))
            Divider(color = Color(0x22FFFFFF))
            Spacer(modifier = Modifier.height(14.dp))

            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(44.dp)
                        .background(Color(0xFF37474F), CircleShape),
                    contentAlignment = Alignment.Center,
                ) {
                    Text("👨‍✈️", fontSize = 20.sp)
                }
                Spacer(modifier = Modifier.width(12.dp))
                Column {
                    Text(booking.captain?.name ?: "", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 15.sp)
                    Text("${booking.captain?.vehicleModel} • ⭐ ${booking.captain?.rating}", color = Color.Gray, fontSize = 12.sp)
                }
            }
        }
    }
}

@Composable
private fun ArrivedWithOtpCard(
    otp: String,
    captain: com.us.android.core.mobility.model.CaptainInfo?,
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
            Text("Captain has Arrived!", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color(0xFF00E676))
            Spacer(modifier = Modifier.height(8.dp))
            Text("Share this Start OTP verbally with your Captain:", fontSize = 13.sp, color = Color.Gray)
            Spacer(modifier = Modifier.height(12.dp))

            // 4-Digit OTP Display Box
            Box(
                modifier = Modifier
                    .background(Color(0x3300E676), RoundedCornerShape(16.dp))
                    .border(2.dp, Color(0xFF00E676), RoundedCornerShape(16.dp))
                    .padding(horizontal = 28.dp, vertical = 10.dp),
            ) {
                Text(
                    text = otp,
                    fontSize = 32.sp,
                    fontWeight = FontWeight.ExtraBold,
                    letterSpacing = 8.sp,
                    color = Color.White,
                )
            }

            Spacer(modifier = Modifier.height(16.dp))
            Text("${captain?.name ?: "Captain"} • ${captain?.vehicleNumber ?: "Vehicle"}", color = Color.LightGray, fontSize = 13.sp)
        }
    }
}

@Composable
private fun TripInProgressCard(
    booking: com.us.android.core.mobility.model.RideBooking,
    shareLink: String?,
    sosTriggered: Boolean,
    onTriggerSOS: () -> Unit,
    onGenerateShare: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = Color(0xEB1E222B)),
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(24.dp)),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column {
                    Text("Trip In Progress", fontSize = 12.sp, color = Color(0xFF00E676), fontWeight = FontWeight.Bold)
                    Text("En route to destination", style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold), color = Color.White)
                }
                // SOS Button
                Box(
                    modifier = Modifier
                        .background(if (sosTriggered) Color(0xFFFF1744) else Color(0x33FF1744), RoundedCornerShape(12.dp))
                        .border(1.dp, Color(0xFFFF1744), RoundedCornerShape(12.dp))
                        .clickable { onTriggerSOS() }
                        .padding(horizontal = 14.dp, vertical = 8.dp),
                ) {
                    Text(if (sosTriggered) "SOS ACTIVE" else "🆘 SOS", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 13.sp)
                }
            }

            Spacer(modifier = Modifier.height(14.dp))
            Divider(color = Color(0x22FFFFFF))
            Spacer(modifier = Modifier.height(14.dp))

            // Action Buttons
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(Color(0x22FFFFFF), RoundedCornerShape(12.dp))
                        .clickable { onGenerateShare() }
                        .padding(12.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(if (shareLink != null) "✓ Link Shared: $shareLink" else "🔗 Share Live Trip with Contacts", color = Color.White, fontSize = 13.sp, fontWeight = FontWeight.Medium)
                }
            }
        }
    }
}

@Composable
private fun TripCompletedCard(
    receipt: com.us.android.core.mobility.model.RideReceipt,
    ratingSubmitted: Boolean,
    onSubmitRating: (Int, String) -> Unit,
    onReset: () -> Unit,
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
            Text("Trip Completed!", style = MaterialTheme.typography.titleLarge.copy(fontWeight = FontWeight.Bold), color = Color(0xFF00E676))
            Spacer(modifier = Modifier.height(6.dp))
            Text("Total Fare: ${receipt.totalFare.formattedINR}", fontSize = 22.sp, fontWeight = FontWeight.Bold, color = Color.White)
            Text("Paid via ${receipt.paymentMethod.uppercase()} • Settled", fontSize = 12.sp, color = Color.Gray)

            Spacer(modifier = Modifier.height(16.dp))
            Divider(color = Color(0x22FFFFFF))
            Spacer(modifier = Modifier.height(16.dp))

            if (!ratingSubmitted) {
                Text("How was your ride?", color = Color.LightGray, fontSize = 14.sp)
                Spacer(modifier = Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf(1, 2, 3, 4, 5).forEach { stars ->
                        Box(
                            modifier = Modifier
                                .size(40.dp)
                                .background(Color(0x22FFFFFF), CircleShape)
                                .clickable { onSubmitRating(stars, "Great ride!") },
                            contentAlignment = Alignment.Center,
                        ) {
                            Text("⭐", fontSize = 18.sp)
                        }
                    }
                }
            } else {
                Text("✓ Thank you for your feedback!", color = Color(0xFF00E676), fontWeight = FontWeight.Bold, fontSize = 14.sp)
            }

            Spacer(modifier = Modifier.height(20.dp))
            UsButton(
                text = "Book Another Ride",
                onClick = onReset,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}
