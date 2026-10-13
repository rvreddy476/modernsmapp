package com.us.android.feature.rider.job

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.PillTone
import com.us.android.feature.rider.ui.RiderCard

@Composable
internal fun BatchRouteCard(state: ActiveJobUiState, onSuggest: () -> Unit, onConfirm: (String) -> Unit) {
    val assignment = state.assignment ?: return
    val batch = state.batch ?: return
    if (!BatchRouteRules.visible(assignment, batch)) return
    val ready = BatchRouteRules.ready(assignment, batch, state.batchJobs)
    RiderCard {
        CardHeading("Plan your ${batch.members.size} deliveries", "One restaurant pickup · ${batch.members.size} drop-offs")
        Text("Saved stop order", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textMuted)
        batch.members.sortedBy { it.sequence }.forEachIndexed { index, member ->
            val job = state.batchJobs.singleOrNull { it.orderId == member.orderId }
            RouteStop(index + 1, job?.let { "Order ${it.orderNumber}" } ?: BatchRouteRules.label(member.orderId, assignment))
            if (job?.status == "ASSIGNED" && job.id != assignment.id) {
                UsPillButton(text = "Confirm order ${job.orderNumber}", onClick = { onConfirm(job.orderId) }, enabled = !state.busy, filled = false)
            }
        }
        if (!ready) InfoNote(if (state.batchJobs.isEmpty()) "Checking your batch orders. If this persists, check your connection." else "Confirm every batch order before requesting a suggestion.")
        UsPillButton(
            text = if (state.routePreview == null) "Suggest route" else "Refresh suggestion",
            onClick = onSuggest,
            filled = false,
            busy = state.routeLoading,
            enabled = !state.busy && ready,
            modifier = Modifier.fillMaxWidth(),
        )
        state.routeError?.let { InfoNote(it, tone = PillTone.Warning) }
        state.routePreview?.let { preview ->
            Text("Suggested stop order", style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.accentSolid)
            preview.orderIds.forEachIndexed { index, orderId ->
                val job = state.batchJobs.singleOrNull { it.orderId == orderId }
                RouteStop(index + 1, job?.let { "Order ${it.orderNumber}" } ?: BatchRouteRules.label(orderId, assignment))
            }
            InfoNote("Suggestion only. Your saved stop order, assignments and customer ETAs haven't changed.")
            InfoNote("Stop-order advice from Google. Use two-wheeler navigation for each leg; this isn't a navigation route.")
        }
    }
}

@Composable
private fun RouteStop(number: Int, label: String) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Text(
            "$number",
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.accentSolid,
            modifier = Modifier.background(UsTheme.extended.bgRaised, CircleShape).padding(UsTheme.spacing.s),
        )
        Text(label, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
    }
}
