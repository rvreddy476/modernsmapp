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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.tube.ui.shimmer

/**
 * The page before its first page: one hero-shaped block, then card-shaped
 * blocks, all shimmering. The list's layout, so the real cards land where
 * the eye already is. One announcement for the whole thing.
 */
@Composable
internal fun TubeHomeSkeleton(modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .semantics { contentDescription = "Loading videos" }
            .testTag("tube_skeleton"),
    ) {
        SkeletonCard(hero = true)
        repeat(SKELETON_CARDS) { SkeletonCard(hero = false) }
    }
}

/** A list of card-shaped blocks — the Subscriptions page's skeleton. */
@Composable
internal fun TubeListSkeleton(modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .semantics { contentDescription = "Loading videos" }
            .testTag("tube_skeleton"),
    ) {
        repeat(SKELETON_CARDS + 1) { SkeletonCard(hero = false) }
    }
}

/** A shelf of small card-shaped blocks — the You page's shelves while they load. */
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
                        .shimmer(RoundedCornerShape(UsTheme.radii.small)),
                )
                SkeletonLine(fraction = 0.9f)
                SkeletonLine(fraction = 0.5f)
            }
        }
    }
}

@Composable
private fun SkeletonCard(hero: Boolean) {
    val gutter = if (hero) 0.dp else UsTheme.spacing.pageHorizontal
    val corner = if (hero) 0.dp else UsTheme.radii.medium
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .padding(horizontal = gutter)
                .fillMaxWidth()
                .aspectRatio(LANDSCAPE)
                .shimmer(RoundedCornerShape(corner)),
        )
        Row(
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            verticalAlignment = Alignment.Top,
        ) {
            Box(modifier = Modifier.size(UsAvatarSize.Post.diameter).shimmer(CircleShape))
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                SkeletonLine(fraction = 0.85f)
                SkeletonLine(fraction = 0.6f)
                SkeletonLine(fraction = 0.4f)
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
private const val SKELETON_SHELF_CARDS = 3
private val LINE_HEIGHT = 12.dp
