package com.us.android.feature.doorstep.bookings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.domain.BookingStatus
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.Pill
import com.us.android.feature.doorstep.ui.listPadding
import com.us.android.feature.doorstep.ui.slotRangeText
import com.us.android.feature.doorstep.ui.tone

@Composable
fun BookingsScreen(
    onBack: () -> Unit,
    onOpenBooking: (bookingId: String) -> Unit,
    onBrowse: () -> Unit,
    viewModel: BookingsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.refresh()
        onPauseOrDispose { }
    }

    DoorstepScreen(title = "My bookings", onBack = onBack) { padding ->
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = listPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item {
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    BookingsFilter.entries.forEach { filter ->
                        UsPillButton(text = filter.label, onClick = { viewModel.select(filter) }, filled = state.filter == filter)
                    }
                }
            }
            when {
                state.loading -> item { LoadingPane(modifier = Modifier.fillMaxWidth()) }
                state.error != null && state.items.isEmpty() -> item {
                    MessagePane(
                        title = "Couldn't load your bookings",
                        body = state.error.orEmpty(),
                        primaryLabel = "Try again",
                        onPrimary = viewModel::refresh,
                    )
                }
                state.items.isEmpty() -> item {
                    MessagePane(
                        title = if (state.filter == BookingsFilter.UPCOMING) "Nothing booked" else "No past bookings",
                        body = "Book cleaning, repairs, salon and more at fixed prices.",
                        icon = UsIcons.Wrench,
                        primaryLabel = "Browse services",
                        onPrimary = onBrowse,
                    )
                }
                else -> {
                    items(state.items, key = { it.id }) { booking ->
                        val status = BookingStatus.of(booking.status)
                        DoorstepCard(onClick = { onOpenBooking(booking.id) }) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Column(Modifier.weight(1f)) {
                                    Text(booking.serviceName, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                                    Text(
                                        slotRangeText(booking.slotStart, booking.slotEnd),
                                        style = MaterialTheme.typography.bodySmall,
                                        color = UsTheme.extended.textMuted,
                                    )
                                }
                                Pill(status.label, status.tone())
                            }
                            Text(
                                Paise(booking.totalPaise).toRupeeText(),
                                style = MaterialTheme.typography.bodyMedium,
                                color = UsTheme.extended.textSecondary,
                            )
                        }
                    }
                    if (state.nextCursor != null) {
                        item {
                            UsSecondaryButton(
                                text = if (state.loadingMore) "Loading…" else "Show more",
                                onClick = viewModel::loadMore,
                                enabled = !state.loadingMore,
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                    }
                }
            }
        }
    }
}
