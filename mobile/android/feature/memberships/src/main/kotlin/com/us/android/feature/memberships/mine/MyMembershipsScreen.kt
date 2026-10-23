package com.us.android.feature.memberships.mine

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.memberships.data.rupees
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.memberships.navigation.MembershipsDestinations

/** The viewer's memberships (paid memberships, 2026-10-23): each opens its detail. */
@Composable
fun MyMembershipsScreen(
    destinations: MembershipsDestinations,
    viewModel: MyMembershipsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar(title = "My memberships", onBack = destinations.onBack) }) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding).testTag("my_memberships")) {
            when {
                state.loading && state.rows.isEmpty() -> UsLoadingState(label = "Loading memberships")
                state.loadError != null && state.rows.isEmpty() ->
                    UsErrorState(message = state.loadError.orEmpty(), onRetry = viewModel::refresh)
                state.rows.isEmpty() -> UsEmptyState(
                    title = "No memberships yet",
                    detail = "When you join a creator's membership, it shows here with when to renew.",
                    modifier = Modifier.testTag("my_memberships_empty"),
                )
                else -> LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = PaddingValues(vertical = UsTheme.spacing.l),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    items(state.rows, key = { it.membership.subscriptionId }) { row ->
                        MembershipCard(
                            row = row,
                            onClick = { destinations.onOpenMembership(row.membership.subscriptionId) }
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun MembershipCard(row: MembershipRow, onClick: () -> Unit) {
    val m = row.membership
    UsSurfaceCard(onClick = onClick, modifier = Modifier.fillMaxWidth().testTag("my_membership:${m.subscriptionId}")) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = row.creatorName.ifBlank { "Creator" },
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                )
                Text(
                    text = "${m.levelName.ifBlank { "Membership" }} · ${rupees(m.pricePaise)} a month",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textBody,
                )
                Text(
                    text = m.timeLine(),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (m.hasAccess) UsTheme.extended.textMuted else UsTheme.extended.statusWarning,
                )
            }
            Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted)
        }
    }
}
