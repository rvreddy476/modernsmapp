package com.us.android.feature.commerce.profile

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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.commerce.ui.pressScale
import kotlinx.coroutines.launch

/**
 * MStore's profile menu (founder, 2026-09-05): the person at the top, then
 * their orders, favourites, addresses, payments, purchase history and
 * settings, and last the switch into MSeller — "Start selling" for someone
 * with no shop, "Seller dashboard" for someone with one.
 *
 * The Momentum sheet idiom, the same one Tube's More wears: navy
 * `bgCardSolid`, 28dp corners, a 55% scrim, a 32x4 grab handle inside the
 * content, 52dp rows with no ripple. A row slides the sheet away FIRST and
 * then navigates, so what it opens lands on a clear screen rather than under
 * a dismissing overlay.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StoreProfileSheet(
    destinations: StoreMenuDestinations,
    onDismiss: () -> Unit,
    viewModel: StoreProfileViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()

    fun leaveThen(action: () -> Unit) {
        scope.launch { sheetState.hide() }.invokeOnCompletion {
            onDismiss()
            action()
        }
    }

    fun onRow(row: StoreMenuRow) = leaveThen {
        when (row) {
            StoreMenuRow.ORDERS -> destinations.onOrders()
            StoreMenuRow.FAVOURITES -> destinations.onFavourites()
            StoreMenuRow.ADDRESSES -> destinations.onAddresses()
            StoreMenuRow.PAYMENTS -> destinations.onPayments()
            StoreMenuRow.PURCHASE_HISTORY -> destinations.onPurchaseHistory()
            StoreMenuRow.SETTINGS -> destinations.onSettings()
            StoreMenuRow.START_SELLING, StoreMenuRow.SELLER_DASHBOARD -> destinations.onSeller()
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
        modifier = Modifier.testTag("mstore_profile_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = CONTENT_BOTTOM),
        ) {
            GrabHandle()
            PersonHeader(state)
            Hairline()
            state.rows.forEach { row ->
                MenuRow(
                    row = row,
                    detail = sellingRowDetail(state.seller).takeIf { row.opensSeller },
                    onClick = { onRow(row) },
                )
            }
        }
    }
}

/** The person: their avatar and their name, so the menu is theirs and not the app's. */
@Composable
private fun PersonHeader(state: StoreProfileState) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = ROW_SIDE, vertical = HEADER_VERTICAL),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_GAP),
    ) {
        UsAvatar(
            name = state.person.name,
            seed = state.person.seed,
            imageUrl = state.person.avatarUrl,
            size = UsAvatarSize.Large,
        )
        Text(
            text = state.person.name,
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
    }
}

/** One 52dp row: the glyph, the label, an optional second line for the seller switch. */
@Composable
private fun MenuRow(row: StoreMenuRow, detail: String?, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = detail?.let { "${row.label}. $it" } ?: row.label
            }
            .padding(horizontal = ROW_SIDE, vertical = ROW_VERTICAL)
            .testTag("mstore_menu_row:${row.name.lowercase()}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_GAP),
    ) {
        Icon(
            imageVector = row.icon,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(ROW_GLYPH),
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = row.label,
                style = MaterialTheme.typography.bodyLarge,
                fontSize = ROW_TEXT_SIZE,
                color = UsTheme.extended.textPrimary,
            )
            if (detail != null) {
                Text(
                    text = detail,
                    style = MaterialTheme.typography.labelSmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }
    }
}

/** 32x4, muted at 35%: a handle, not a decoration. */
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

/** The app's hairline: 1dp in the subtle border token. */
@Composable
private fun Hairline() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = ROW_SIDE)
            .height(HAIRLINE)
            .background(UsTheme.extended.borderSubtle),
    )
}

private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private val SHEET_RADIUS = 28.dp
private val CONTENT_BOTTOM = 12.dp
private val HANDLE_TOP = 10.dp
private val HANDLE_BOTTOM = 8.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HAIRLINE = 1.dp
private val HEADER_VERTICAL = 12.dp
private val ROW_VERTICAL = 14.dp
private val ROW_SIDE = 20.dp
private val ROW_GAP = 16.dp
private val ROW_GLYPH = 22.dp
private val ROW_TEXT_SIZE = 15.sp
