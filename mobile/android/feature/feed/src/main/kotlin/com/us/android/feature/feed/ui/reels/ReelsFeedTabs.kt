package com.us.android.feature.feed.ui.reels

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The tabs in the middle of the Reels header (web parity, 2026-10-24):
 * Explore, Following, For You — the web's sidebar rows for `/reels/explore`,
 * `/reels?feed=following` and `/reels`. Following and For You switch the
 * stage's feed in place ([ReelsViewModel.selectFeed]); Explore is a page of
 * its own, pushed over Reels, so it is a tab that never looks selected.
 *
 * White on the header's scrim, the active one bold and underlined — Home's
 * header tabs ([com.us.android.feature.feed.ui.HomeStageHeader]). Sat in the
 * header's 64dp row between the sound control and the corner glyphs; on a
 * narrow phone the row scrolls sideways rather than run under either.
 */
@Composable
internal fun ReelsFeedTabs(
    selected: ReelsFeed,
    onSelect: (ReelsFeed) -> Unit,
    onOpenExplore: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(TopAppBarDefaults.TopAppBarExpandedHeight)
            .padding(start = TABS_START_CLEARANCE, end = TABS_END_CLEARANCE),
        contentAlignment = Alignment.Center,
    ) {
        Row(
            modifier = Modifier
                .horizontalScroll(rememberScrollState())
                .selectableGroup()
                .testTag("reels_tabs"),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            ReelsHeaderTab(label = EXPLORE_LABEL, active = false, tag = "reels_tab:explore", onClick = onOpenExplore)
            ReelsFeed.entries.forEach { feed ->
                ReelsHeaderTab(
                    label = feed.label,
                    active = feed == selected,
                    tag = "reels_tab:${feed.name.lowercase()}",
                    onClick = { onSelect(feed) },
                )
            }
        }
    }
}

@Composable
private fun ReelsHeaderTab(label: String, active: Boolean, tag: String, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .width(IntrinsicSize.Max)
            .selectable(selected = active, role = Role.Tab, onClick = onClick)
            .padding(vertical = UsTheme.spacing.s)
            .testTag(tag),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = if (active) FontWeight.Bold else FontWeight.SemiBold,
            color = if (active) UsTheme.extended.onMedia else UsTheme.extended.onMediaMuted,
            maxLines = 1,
        )
        Box(
            modifier = Modifier
                .padding(top = UsTheme.spacing.xs)
                .fillMaxWidth(UNDERLINE_FRACTION)
                .height(UNDERLINE_HEIGHT)
                .clip(CircleShape)
                .background(if (active) UsTheme.extended.onMedia else Color.Transparent),
        )
    }
}

/**
 * The Following feed with nothing in it (the web's FollowingEmpty, T-19):
 * what the tab is for, and the way back to For You. On the dark stage, so
 * the words are the stage's.
 */
@Composable
internal fun ReelsFollowingEmpty(onForYou: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.pageHorizontal)
            .testTag("reels_following_empty"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = FOLLOWING_EMPTY_TITLE,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.onMedia,
            textAlign = TextAlign.Center,
        )
        Text(
            text = FOLLOWING_EMPTY_DETAIL,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.onMediaMuted,
            textAlign = TextAlign.Center,
        )
        UsButton(text = "Go to For You", onClick = onForYou, modifier = Modifier.testTag("reels_following_empty:for_you"))
    }
}

internal const val EXPLORE_LABEL = "Explore"
internal const val FOLLOWING_EMPTY_TITLE = "Follow creators to see their reels"
internal const val FOLLOWING_EMPTY_DETAIL = "Reels from people you follow show up here. Until then, For You has plenty."

/** Clear of the sound control at the header's left (its 40dp pill and its 16dp inset). */
private val TABS_START_CLEARANCE = 60.dp

/** Clear of the two corner glyphs at the right (Clear screen and More). */
private val TABS_END_CLEARANCE = 100.dp
private val UNDERLINE_HEIGHT = 2.dp
private const val UNDERLINE_FRACTION = 0.6f
