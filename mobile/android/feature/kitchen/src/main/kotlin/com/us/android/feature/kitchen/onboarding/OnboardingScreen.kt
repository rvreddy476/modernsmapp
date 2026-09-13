package com.us.android.feature.kitchen.onboarding

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenPill
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LoadingPane
import com.us.android.feature.kitchen.ui.MessagePane
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.RestaurantStatusText
import com.us.android.feature.kitchen.ui.listPadding

@Composable
fun OnboardingScreen(
    onBack: (() -> Unit)?,
    onOpenStep: (StepEditor) -> Unit,
    onRestaurantChanged: () -> Unit,
    onOpenKitchen: () -> Unit,
    onSignOut: () -> Unit,
    viewModel: OnboardingViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.restaurantChanges) {
        if (state.restaurantChanges > 0) onRestaurantChanged()
    }
    // Re-read on every return from a step: a status may have moved.
    LifecycleResumeEffect(Unit) {
        viewModel.refreshRestaurant()
        onPauseOrDispose { }
    }

    KitchenScreen(
        title = "Set up your kitchen",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        val restaurant = state.restaurant
        when {
            restaurant == null && state.loading -> LoadingPane(Modifier.padding(padding))
            restaurant == null -> MessagePane(
                title = "Couldn't load your kitchen",
                body = state.loadError.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::refreshRestaurant,
                secondaryLabel = "Sign out",
                onSecondary = onSignOut,
                modifier = Modifier.padding(padding),
            )
            else -> OnboardingContent(
                padding = padding,
                restaurant = restaurant,
                state = state,
                onOpenStep = onOpenStep,
                onOpenKitchen = onOpenKitchen,
                onSubmit = viewModel::submit,
                onSignOut = onSignOut,
            )
        }
    }
}

@Composable
private fun OnboardingContent(
    padding: PaddingValues,
    restaurant: PartnerRestaurantDto,
    state: OnboardingUiState,
    onOpenStep: (StepEditor) -> Unit,
    onOpenKitchen: () -> Unit,
    onSubmit: () -> Unit,
    onSignOut: () -> Unit,
) {
    val checklist = state.checklist
    // food-service accepts a submit from DRAFT or REJECTED (submit_post_409_not_draft).
    val canSubmit = restaurant.status == RestaurantStatusText.DRAFT || restaurant.status == RestaurantStatusText.REJECTED
    LazyColumn(
        contentPadding = listPadding(padding),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        item(key = "header") {
            Column {
                Text(
                    text = restaurant.name,
                    style = MaterialTheme.typography.headlineSmall,
                    color = UsTheme.extended.textPrimary,
                )
                Row(
                    modifier = Modifier.padding(top = UsTheme.spacing.s),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                ) {
                    KitchenPill(RestaurantStatusText.label(restaurant.status), RestaurantStatusText.tone(restaurant.status))
                    if (restaurant.city.isNotBlank()) {
                        Text(restaurant.city, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                    }
                }
            }
        }
        if (restaurant.status != RestaurantStatusText.DRAFT) {
            item(key = "status") { ReviewStatusCard(restaurant.status, onOpenKitchen) }
        }
        item(key = "progress") { ProgressCard(checklist) }
        items(checklist.rows, key = { it.step.wire }) { row ->
            ChecklistRowItem(row = row, onClick = { onOpenStep(row.editor) })
        }
        if (checklist.unrecognised.isNotEmpty()) {
            item(key = "unrecognised") {
                KitchenCard {
                    CardHeading(
                        title = "Something else is needed",
                        body = "Feast asked for a step this version of the app doesn't know. Update Feast Kitchen to finish it.",
                        titleColor = UsTheme.extended.statusWarning,
                    )
                }
            }
        }
        item(key = "submit") {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsButton(
                    text = if (checklist.isChecked) "Submit for review" else "Check & submit for review",
                    onClick = onSubmit,
                    enabled = canSubmit,
                    loading = state.submitting,
                    modifier = Modifier.fillMaxWidth(),
                )
                InfoNote(
                    if (canSubmit) {
                        "This checks every step with Feast. If anything is missing you'll see it here; if nothing is, your kitchen is sent for review."
                    } else {
                        "Your kitchen has been submitted. You can still update any step."
                    },
                )
                TextButton(onClick = onSignOut, modifier = Modifier.align(Alignment.CenterHorizontally)) {
                    Text("Sign out", color = UsTheme.extended.textMuted)
                }
            }
        }
    }
}

@Composable
private fun ReviewStatusCard(status: String, onOpenKitchen: () -> Unit) {
    KitchenCard {
        when (status) {
            RestaurantStatusText.ACTIVE -> {
                CardHeading(
                    title = "Your kitchen is live",
                    body = "Customers can find you and order.",
                    titleColor = UsTheme.extended.statusSuccess,
                )
                UsButton(text = "Open kitchen", onClick = onOpenKitchen, modifier = Modifier.fillMaxWidth())
            }
            RestaurantStatusText.PENDING_REVIEW -> CardHeading(
                title = "Submitted for review",
                body = "Feast is checking your licence, tax and bank details. We'll let you know when your kitchen is approved.",
                titleColor = UsTheme.extended.statusWarning,
            )
            RestaurantStatusText.REJECTED -> CardHeading(
                title = "Changes needed",
                body = "Feast couldn't approve your kitchen yet. Update the steps that need changes, then submit again.",
                titleColor = UsTheme.extended.statusDanger,
            )
            else -> CardHeading(RestaurantStatusText.label(status), null)
        }
    }
}

@Composable
private fun ProgressCard(checklist: KitchenChecklist) {
    KitchenCard {
        if (checklist.isChecked) {
            val total = checklist.rows.size
            val done = checklist.rows.count { it.status == RowStatus.DONE }
            CardHeading(
                title = if (checklist.isReady) "Everything's in place" else "$done of $total steps done",
                body = if (checklist.isReady) null else "Tap a step to finish it, then check again.",
            )
            LinearProgressIndicator(
                progress = { if (total == 0) 0f else done.toFloat() / total },
                modifier = Modifier.fillMaxWidth(),
                color = UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.borderSubtle,
            )
        } else {
            CardHeading(
                title = "Complete each step",
                body = "Feast confirms which steps are done when you check. Saving a step doesn't tick it here until then.",
            )
        }
    }
}

@Composable
private fun ChecklistRowItem(row: ChecklistRow, onClick: () -> Unit) {
    KitchenCard(onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            StepMark(row.status)
            Column(
                modifier = Modifier
                    .weight(1f)
                    .padding(horizontal = UsTheme.spacing.l),
            ) {
                Text(row.title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                Text(row.detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
            when (row.status) {
                RowStatus.DONE -> KitchenPill("Done", PillTone.Positive)
                RowStatus.TO_DO -> KitchenPill("To do", PillTone.Accent)
                RowStatus.UNCHECKED -> Unit
            }
            Icon(
                imageVector = UsIcons.ChevronRight,
                contentDescription = null,
                tint = UsTheme.extended.textDim,
                modifier = Modifier.padding(start = UsTheme.spacing.s).size(18.dp),
            )
        }
    }
}

@Composable
private fun StepMark(status: RowStatus) {
    val size = Modifier.size(28.dp)
    when (status) {
        RowStatus.DONE -> Box(
            modifier = size.background(UsTheme.extended.statusSuccess.copy(alpha = 0.18f), CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(UsIcons.Check, contentDescription = "Done", tint = UsTheme.extended.statusSuccess, modifier = Modifier.size(16.dp))
        }
        RowStatus.TO_DO -> Box(size.border(1.5.dp, UsTheme.extended.accentSolid, CircleShape))
        RowStatus.UNCHECKED -> Box(size.border(1.5.dp, UsTheme.extended.textGhost, CircleShape))
    }
}
