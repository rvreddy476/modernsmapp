package com.us.android.feature.tube.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.ChannelState
import com.us.android.feature.tube.navigation.TubeDestinations
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The rows of the video app's More sheet (founder, 2026-09-05): the
 * viewer's channel — or the invitation to make one — Subscriptions, the
 * scheduled posts, the saved videos, and Notifications. Every row goes
 * somewhere real; there is no row for a page this build does not have.
 */
enum class TubeMenuRow(val label: String, val icon: ImageVector) {
    YOUR_CHANNEL("Your channel", UsIcons.Tv),
    CREATE_CHANNEL("Create your channel", UsIcons.Tv),
    SUBSCRIPTIONS("Subscriptions", UsIcons.ListVideo),
    SCHEDULED("Scheduled posts", UsIcons.Clock),
    SAVED("Saved videos", UsIcons.BookmarkOutline),
    NOTIFICATIONS("Notifications", UsIcons.Notifications),
}

/**
 * Which rows the sheet shows for what is known about the viewer's channel.
 * Only a server that SAID "none" (the 404) turns the first row into
 * "Create your channel"; while the answer is unknown, or the lookup failed,
 * the row still reads "Your channel" and the You page it opens shows the
 * real state, with Retry — a sheet must not invent a channel's absence
 * from a network blip. Pure, so it is a table test.
 */
fun tubeMenuRows(channel: ChannelState): List<TubeMenuRow> = listOf(
    if (channel is ChannelState.None) TubeMenuRow.CREATE_CHANNEL else TubeMenuRow.YOUR_CHANNEL,
    TubeMenuRow.SUBSCRIPTIONS,
    TubeMenuRow.SCHEDULED,
    TubeMenuRow.SAVED,
    TubeMenuRow.NOTIFICATIONS,
)

/** The sheet's one question: does the viewer have a channel? The cached answer, loaded once per process. */
@HiltViewModel
class TubeMenuViewModel @Inject constructor(private val channels: ChannelRepository) : ViewModel() {
    val channel: StateFlow<ChannelState> = channels.own

    init {
        viewModelScope.launch { channels.ensureLoaded() }
    }
}

/**
 * The video app's More sheet, opened by the header's ≡ (founder,
 * 2026-09-05). The Momentum sheet idiom — navy `bgCardSolid`, 28dp
 * corners, a 55% scrim, a 32×4 grab handle inside the content, 52dp rows
 * with no ripple — and the Create sheet's rule: a row slides the sheet
 * away FIRST and then acts, so what it opens lands on a clear screen.
 * "Create your channel" hands back to the page, which mounts the create
 * sheet once this one has left; every other row is a destination `:app`
 * resolves through [TubeDestinations].
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TubeMenuSheet(
    destinations: TubeDestinations,
    onCreateChannel: () -> Unit,
    onDismiss: () -> Unit,
    viewModel: TubeMenuViewModel = hiltViewModel(),
) {
    val channel by viewModel.channel.collectAsStateWithLifecycle()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()

    fun leaveThen(action: () -> Unit) {
        scope.launch { sheetState.hide() }.invokeOnCompletion {
            onDismiss()
            action()
        }
    }

    fun onRow(row: TubeMenuRow) = leaveThen {
        when (row) {
            TubeMenuRow.YOUR_CHANNEL -> destinations.onOpenTab(TubeTab.YOU)
            TubeMenuRow.CREATE_CHANNEL -> onCreateChannel()
            TubeMenuRow.SUBSCRIPTIONS -> destinations.onOpenTab(TubeTab.SUBSCRIPTIONS)
            TubeMenuRow.SCHEDULED -> destinations.onOpenScheduled()
            TubeMenuRow.SAVED -> destinations.onOpenSaved()
            TubeMenuRow.NOTIFICATIONS -> destinations.onOpenNotifications()
        }
    }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
        modifier = Modifier.testTag("tube_menu_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = CONTENT_BOTTOM),
        ) {
            GrabHandle()
            tubeMenuRows(channel).forEach { row ->
                MenuRow(row = row, onClick = { onRow(row) })
            }
        }
    }
}

/** One 52dp row: the glyph in white, the label, a dip on press and no ripple. */
@Composable
private fun MenuRow(row: TubeMenuRow, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(ROW_HEIGHT)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = row.label
            }
            .padding(horizontal = ROW_SIDE)
            .testTag("tube_menu_row:${row.name.lowercase()}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_GAP),
    ) {
        Icon(
            imageVector = row.icon,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(ROW_GLYPH),
        )
        Text(
            text = row.label,
            style = MaterialTheme.typography.bodyLarge,
            fontSize = ROW_TEXT_SIZE,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
    }
}

/** 32×4, muted at 35%: a handle, not a decoration. */
@Composable
private fun GrabHandle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = HANDLE_TOP, bottom = HANDLE_BOTTOM),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(width = HANDLE_WIDTH, height = HANDLE_HEIGHT)
                .clip(CircleShape)
                .background(UsTheme.extended.textMuted.copy(alpha = HANDLE_ALPHA)),
        )
    }
}

private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private val SHEET_RADIUS = 28.dp
private val CONTENT_BOTTOM = 12.dp
private val HANDLE_TOP = 10.dp
private val HANDLE_BOTTOM = 8.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val ROW_HEIGHT = 52.dp
private val ROW_SIDE = 20.dp
private val ROW_GAP = 16.dp
private val ROW_GLYPH = 22.dp
private val ROW_TEXT_SIZE = 15.sp
