package com.us.android.feature.rider.job

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.repository.RiderAssignmentStep
import com.us.android.feature.rider.money.RiderMoney
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LabeledValue
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.MessagePane
import com.us.android.feature.rider.ui.PillTone
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.contentPadding
import java.time.ZoneId

@Composable
fun ActiveJobScreen(onBack: () -> Unit, onFinished: () -> Unit, viewModel: ActiveJobViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var handOffFailure by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(viewModel) { viewModel.finishedEvents.collect { onFinished() } }

    if (state.confirmingRelease) {
        AlertDialog(
            onDismissRequest = viewModel::cancelRelease,
            title = { Text("Release this job?") },
            text = { Text("It goes back to be offered to another rider. Releasing jobs often can affect your account.") },
            confirmButton = { TextButton(onClick = { viewModel.perform(RiderAssignmentStep.REJECT) }) { Text("Release") } },
            dismissButton = { TextButton(onClick = viewModel::cancelRelease) { Text("Keep the job") } },
        )
    }

    RiderScreen(title = "Current job", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        val assignment = state.assignment
        val actions = state.actions
        val details = state.details
        when {
            state.loading -> LoadingPane()
            assignment == null || actions == null || details == null || !actions.isActive -> MessagePane(
                title = "No active job",
                body = "Stay online — accepted offers show up here.",
                icon = UsIcons.Package,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            else -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(contentPadding(padding)),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                RiderCard {
                    CardHeading(details.restaurantName, "Order ${assignment.orderNumber}")
                    LabeledValue("You earn", RiderMoney.text(details.pay), emphasise = true)
                    LabeledValue("Step", stepLabel(actions.phase))
                    details.etaText(ZoneId.systemDefault())?.let { LabeledValue("Customer ETA", it) }
                }

                actions.pickupCode?.let { code ->
                    RiderCard {
                        Column(modifier = Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
                            Text("Pickup code", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textMuted)
                            Text(code, fontSize = 44.sp, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.accentSolid)
                            Text(
                                "Show this to the restaurant. They enter it to hand over the order.",
                                style = MaterialTheme.typography.bodySmall,
                                color = UsTheme.extended.textMuted,
                            )
                        }
                    }
                }

                RiderCard {
                    CardHeading("Pickup", details.restaurantAddressLines.joinToString(", ").ifBlank { null })
                    val pickup = details.navigation.pickup
                    if (actions.navigateToRestaurant && pickup != null) {
                        UsSecondaryButton(
                            text = "Navigate to restaurant",
                            onClick = { handOffFailure = if (context.openNavigation(pickup)) null else NO_MAPS },
                            modifier = Modifier.fillMaxWidth(),
                        )
                        if (pickup.source == NavIntent.Source.NAME_SEARCH) {
                            InfoNote("Maps searches by the restaurant's name — its exact location isn't on file.")
                        }
                    }
                    details.restaurantPhone?.let { phone ->
                        UsSecondaryButton(
                            text = "Call restaurant",
                            onClick = { handOffFailure = if (context.dialNumber(phone)) null else NO_DIALLER },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }

                details.customer?.let { customer ->
                    RiderCard {
                        CardHeading(
                            customer.firstName?.let { "Deliver to $it" } ?: "Drop-off",
                            customer.addressLines.joinToString("\n").ifBlank { null },
                        )
                        customer.landmark?.let { LabeledValue("Landmark", it) }
                        customer.instructions?.let { InfoNote("Instructions: $it") }
                        details.navigation.drop?.let { drop ->
                            UsSecondaryButton(
                                text = "Navigate to customer",
                                onClick = { handOffFailure = if (context.openNavigation(drop)) null else NO_MAPS },
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                    }
                }
                handOffFailure?.let { InfoNote(it, tone = PillTone.Warning) }

                actions.step?.let { step ->
                    UsButton(text = stepButton(step), onClick = { viewModel.perform(step) }, loading = state.busy, modifier = Modifier.fillMaxWidth())
                }
                if (actions.phase == JobPhase.AT_RESTAURANT) {
                    InfoNote("Waiting for the restaurant to enter your pickup code. This screen moves on by itself.")
                }

                if (actions.canEnterDeliveryCode) {
                    RiderCard {
                        CardHeading("Complete the delivery", "Ask the customer for the delivery code shown in their app.")
                        UsOtpField(
                            value = state.code,
                            onValueChange = viewModel::onCode,
                            length = 4,
                            enabled = !state.verifying && !state.codeLocked,
                            errorText = state.codeError,
                            autoFocus = false,
                        )
                        UsButton(
                            text = "Complete delivery",
                            onClick = viewModel::verifyCode,
                            enabled = !state.codeLocked,
                            loading = state.verifying,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }

                if (actions.canRelease) {
                    UsSecondaryButton(text = "Release this job", onClick = viewModel::askRelease, enabled = !state.busy, modifier = Modifier.fillMaxWidth())
                }
            }
        }
    }
}

private fun stepLabel(phase: JobPhase): String = when (phase) {
    JobPhase.CONFIRM -> "Confirm you're taking it"
    JobPhase.TO_RESTAURANT -> "Head to the restaurant"
    JobPhase.AT_RESTAURANT -> "At the restaurant"
    JobPhase.TO_CUSTOMER -> "Head to the customer"
    JobPhase.AT_CUSTOMER -> "At the customer"
    JobPhase.DELIVERED -> "Delivered"
    JobPhase.ENDED -> "Ended"
}

private fun stepButton(step: RiderAssignmentStep): String = when (step) {
    RiderAssignmentStep.ACCEPT -> "Confirm job"
    RiderAssignmentStep.ARRIVED_AT_RESTAURANT -> "I've arrived at the restaurant"
    RiderAssignmentStep.ARRIVED_AT_CUSTOMER -> "I've arrived at the customer"
    RiderAssignmentStep.REJECT -> "Release this job"
}

private const val NO_MAPS = "No app on this phone can open maps. Install Google Maps, or use the address above."
private const val NO_DIALLER = "No app on this phone can make calls."
