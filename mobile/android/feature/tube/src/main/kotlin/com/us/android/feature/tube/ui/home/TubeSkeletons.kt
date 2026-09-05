package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.tube.ui.shimmer

/**
 * Home before its first page: full-width card-shaped blocks — the 16:9
 * frame, then a circle beside two lines — shimmering in the list's own
 * layout, so the real cards land where the eye already is. One announcement.
 */
@Composable
internal fun TubeListSkeleton(modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .semantics { contentDescription = "Loading videos" }
            .testTag("tube_list_skeleton"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        repeat(SKELETON_CARDS) {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(LANDSCAPE)
                        .shimmer(RoundedCornerShape(UsTheme.radii.large)),
                )
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                    Box(modifier = Modifier.size(CARD_AVATAR).shimmer(CircleShape))
                    Column(
                        modifier = Modifier.weight(1f),
                        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                    ) {
                        SkeletonLine(fraction = 0.9f)
                        SkeletonLine(fraction = 0.5f)
                    }
                }
            }
        }
    }
}

/** Two columns of tile-shaped blocks — the Subscriptions, You and channel pages while they load. */
@Composable
internal fun TubeGridSkeleton(modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .semantics { contentDescription = "Loading videos" }
            .testTag("tube_grid_skeleton"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        repeat(SKELETON_ROWS) { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                repeat(GRID_COLUMNS) { column ->
                    val tall = (row + column) % 2 == 1
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .aspectRatio(if (tall) PORTRAIT_TILE else LANDSCAPE)
                            .shimmer(RoundedCornerShape(UsTheme.radii.large)),
                    )
                }
            }
        }
    }
}

/** A shelf of small card-shaped blocks — a horizontal row while it loads. */
@Composable
internal fun ShelfSkeleton(modifier: Modifier = Modifier) {
    Row(
        modifier = modifier
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .semantics { contentDescription = "Loading" },
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        repeat(SKELETON_SHELF_CARDS) {
            Column(
                modifier = Modifier.width(SHELF_CARD_WIDTH),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(LANDSCAPE)
                        .shimmer(RoundedCornerShape(UsTheme.radii.large)),
                )
                SkeletonLine(fraction = 0.9f)
                SkeletonLine(fraction = 0.5f)
            }
        }
    }
}

@Composable
private fun SkeletonLine(fraction: Float) {
    Box(
        modifier = Modifier
            .fillMaxWidth(fraction)
            .height(LINE_HEIGHT)
            .shimmer(RoundedCornerShape(UsTheme.radii.pill)),
    )
}

private const val SKELETON_CARDS = 2
private const val SKELETON_ROWS = 2
private const val SKELETON_SHELF_CARDS = 3
private const val PORTRAIT_TILE = 4f / 5f
private val CARD_AVATAR = 36.dp
private val SHELF_CARD_WIDTH = 200.dp
private val LINE_HEIGHT = 12.dp
