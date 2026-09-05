package com.us.android.feature.chat.ui.community

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.chat.data.Community
import com.us.android.core.chat.data.CommunityEvent
import com.us.android.core.chat.data.CommunityUpdate
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.home.ChatTogglePill
import com.us.android.feature.chat.ui.home.HeaderGlyph
import com.us.android.feature.chat.ui.home.memberCountLabel
import com.us.android.feature.chat.ui.home.pressScale
import com.us.android.feature.chat.ui.home.rememberMediaUrl

/**
 * A community's page: the header (avatar, name, @handle, members, About,
 * Join/Joined, the bell, ≡ with Report / Admins (owner) / Edit (owner or
 * admin) / Leave / Delete (owner)), then the updates newest first as cards.
 * Members react; nobody replies — there is no composer for them. Admins get
 * the "+" FAB that opens the composer.
 */
@Composable
fun CommunityPageScreen(
    destinations: CommunityPageDestinations,
    viewModel: CommunityPageViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var reportOpen by rememberSaveable { mutableStateOf(false) }
    var reportingUpdate by rememberSaveable { mutableStateOf<String?>(null) }
    LaunchedEffect(state.closed) { if (state.closed) destinations.onClosed() }

    UsScaffold(
        topBar = {
            CommunityTopBar(
                community = state.community,
                onBack = destinations.onBack,
                onReport = { reportOpen = true },
                onAdmins = { destinations.onAdmins(state.communityId) },
                onEdit = { destinations.onEdit(state.communityId) },
                onLeave = viewModel::toggleMembership,
                onDelete = viewModel::deleteCommunity,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        val community = state.community
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            when {
                state.loading -> UsLoadingState(label = "Loading community")
                community == null -> UsErrorState(
                    message = "This community couldn't be loaded.",
                    onRetry = viewModel::refresh,
                )
                else -> UpdatesFeed(
                    state = state,
                    community = community,
                    viewModel = viewModel,
                    onReportUpdate = { reportingUpdate = it },
                )
            }
            if (community?.canPost == true) {
                ComposeFab(
                    onClick = { destinations.onPost(state.communityId) },
                    modifier = Modifier.align(Alignment.BottomEnd).padding(UsTheme.spacing.xxxxl),
                )
            }
            UsMessageHost(message = state.message, onDismiss = viewModel::dismissMessage)
        }
    }
    if (reportOpen) {
        ReportSheet(
            title = "Report community",
            onDismiss = { reportOpen = false },
            onSend = { reason, details ->
                reportOpen = false
                viewModel.report(reason, details)
            },
        )
    }
    reportingUpdate?.let { updateId ->
        ReportSheet(
            title = "Report update",
            onDismiss = { reportingUpdate = null },
            onSend = { reason, details ->
                reportingUpdate = null
                viewModel.reportUpdate(updateId, reason, details)
            },
        )
    }
}

@Composable
private fun CommunityTopBar(
    community: Community?,
    onBack: () -> Unit,
    onReport: () -> Unit,
    onAdmins: () -> Unit,
    onEdit: () -> Unit,
    onLeave: () -> Unit,
    onDelete: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
    ) {
        HeaderGlyph(icon = UsIcons.Back, description = "Back", onClick = onBack, tag = "community_back")
        Text(
            text = community?.name.orEmpty(),
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (community != null) {
            Box {
                HeaderGlyph(
                    icon = UsIcons.Menu,
                    description = "More",
                    onClick = { menuOpen = true },
                    tag = "community_menu",
                )
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    val pick: (() -> Unit) -> Unit = { action ->
                        menuOpen = false
                        action()
                    }
                    CommunityMenuItems(
                        community = community,
                        onReport = { pick(onReport) },
                        onAdmins = { pick(onAdmins) },
                        onEdit = { pick(onEdit) },
                        onLeave = { pick(onLeave) },
                        onDelete = { pick(onDelete) },
                    )
                }
            }
        }
    }
}

/** Report for everyone; Admins and Delete for the owner; Edit for admins; Leave for a member who is not the owner. */
@Composable
private fun CommunityMenuItems(
    community: Community,
    onReport: () -> Unit,
    onAdmins: () -> Unit,
    onEdit: () -> Unit,
    onLeave: () -> Unit,
    onDelete: () -> Unit,
) {
    DropdownMenuItem(
        text = { Text("Report") },
        onClick = onReport,
        modifier = Modifier.testTag("community_menu_report"),
    )
    if (community.isOwner) {
        DropdownMenuItem(
            text = { Text("Admins") },
            onClick = onAdmins,
            modifier = Modifier.testTag("community_menu_admins"),
        )
    }
    if (community.isAdmin) {
        DropdownMenuItem(
            text = { Text("Edit") },
            onClick = onEdit,
            modifier = Modifier.testTag("community_menu_edit"),
        )
    }
    if (community.isMember && !community.isOwner) {
        DropdownMenuItem(
            text = { Text("Leave") },
            onClick = onLeave,
            modifier = Modifier.testTag("community_menu_leave"),
        )
    }
    if (community.isOwner) {
        DropdownMenuItem(
            text = { Text("Delete community") },
            onClick = onDelete,
            modifier = Modifier.testTag("community_menu_delete"),
        )
    }
}

@Composable
private fun UpdatesFeed(
    state: CommunityPageUiState,
    community: Community,
    viewModel: CommunityPageViewModel,
    onReportUpdate: (String) -> Unit,
) {
    LazyColumn(
        modifier = Modifier.fillMaxSize().testTag("community_updates"),
        verticalArrangement = Arrangement.spacedBy(CARD_GAP),
        contentPadding = PaddingValues(bottom = FEED_BOTTOM),
    ) {
        item(key = "header") {
            CommunityHeader(
                community = community,
                busyMembership = state.busyMembership,
                busyMute = state.busyMute,
                onToggleMembership = viewModel::toggleMembership,
                onToggleMute = viewModel::toggleMute,
            )
        }
        if (state.updates.isEmpty()) {
            item(key = "empty") {
                Text(
                    text = if (community.canPost) "No updates yet — post the first one." else "No updates yet.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.fillMaxWidth().padding(
                        UsTheme.spacing.xxxxl
                    ).testTag("community_updates_empty"),
                )
            }
        }
        items(state.updates, key = { it.id }) { update ->
            LaunchedEffect(update.id) { viewModel.onUpdateShown(update.id) }
            UpdateCard(
                update = update,
                community = community,
                reacting = state.reactingTo == update.id,
                onOpenReactions = { viewModel.openReactions(update.id) },
                onReact = { emoji -> viewModel.react(update, emoji) },
                onReport = { onReportUpdate(update.id) },
            )
        }
        if (state.nextCursor != null) {
            item(key = "more") {
                LaunchedEffect(state.updates.size) { viewModel.loadMore() }
                UsLoadingState(label = "Loading more", modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l))
            }
        }
    }
}

/** Avatar, name, @handle · members, About, and the two controls: Join/Joined and the bell. */
@Composable
private fun CommunityHeader(
    community: Community,
    busyMembership: Boolean,
    busyMute: Boolean,
    onToggleMembership: () -> Unit,
    onToggleMute: () -> Unit,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("community_header"),
    ) {
        UsAvatar(
            name = community.name,
            size = UsAvatarSize.Large,
            seed = community.handle,
            imageUrl = rememberMediaUrl(community.avatarMediaId),
            hasRing = community.isVerified,
        )
        Text(
            text = community.name,
            style = MaterialTheme.typography.headlineSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Text(
            text = "${community.handleForDisplay} · ${memberCountLabel(community.memberCount)}" +
                if (community.visibility == Community.VISIBILITY_PRIVATE) " · Private" else "",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        if (community.description.isNotBlank()) {
            Text(
                text = community.description,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textBody,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = UsTheme.spacing.xs),
            )
        }
        if (!community.isBanned) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.padding(top = UsTheme.spacing.s),
            ) {
                ChatTogglePill(
                    text = when {
                        community.isOwner -> "Owner"
                        community.isAdmin -> "Admin"
                        community.isMember -> "Joined"
                        else -> "Join"
                    },
                    selected = community.isMember,
                    onClick = { if (!community.isOwner && !community.isAdmin) onToggleMembership() },
                    busy = busyMembership,
                    tag = "community_join",
                )
                if (community.isMember) {
                    val shape = RoundedCornerShape(UsTheme.radii.full)
                    Box(
                        modifier = Modifier
                            .background(UsTheme.extended.glassBg, shape)
                            .border(HAIRLINE, UsTheme.extended.glassBorder, shape),
                    ) {
                        HeaderGlyph(
                            icon = if (community.viewerMuted) UsIcons.SoundOff else UsIcons.Notifications,
                            description = if (community.viewerMuted) "Unmute updates" else "Mute updates",
                            onClick = { if (!busyMute) onToggleMute() },
                            size = BELL_TARGET,
                            glyph = BELL_GLYPH,
                            tag = "community_mute",
                        )
                    }
                }
            }
        }
    }
}

/**
 * One update: the author line, the title, the body clamped to five lines
 * with "more", the pictures, the event block, then the reaction bar — the
 * counts with the viewer's own emoji highlighted — and, on tap, a small
 * strip of six emoji.
 */
@Composable
private fun UpdateCard(
    update: CommunityUpdate,
    community: Community,
    reacting: Boolean,
    onOpenReactions: () -> Unit,
    onReact: (String) -> Unit,
    onReport: () -> Unit,
) {
    var expanded by rememberSaveable(update.id) { mutableStateOf(false) }
    val shape = RoundedCornerShape(UsTheme.radii.card)
    Column(
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = CARD_MARGIN)
            .background(UsTheme.extended.bgCardSolid, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(CARD_PADDING)
            .testTag("community_update:${update.id}"),
    ) {
        UpdateAuthorLine(update = update, community = community, onReport = onReport)
        update.title?.let { title ->
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
        }
        if (update.body.isNotBlank()) {
            Text(
                text = update.body,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textBody,
                maxLines = if (expanded) Int.MAX_VALUE else BODY_CLAMP,
                overflow = TextOverflow.Ellipsis,
            )
            val clamped = update.body.lines().size > BODY_CLAMP || update.body.length > BODY_CLAMP_CHARS
            if (!expanded && clamped) {
                Text(
                    text = "more",
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.accentSolid,
                    modifier = Modifier.pressScale({ expanded = true }).testTag("community_update_more:${update.id}"),
                )
            }
        }
        if (update.mediaIds.isNotEmpty()) MediaGrid(mediaIds = update.mediaIds)
        update.event?.let { EventBlock(it) }
        ReactionBar(update = update, reacting = reacting, onOpen = onOpenReactions, onReact = onReact)
    }
}

/** The community as author, the time with Pinned and the type beside it, and the flag. */
@Composable
private fun UpdateAuthorLine(update: CommunityUpdate, community: Community, onReport: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(
            name = community.name,
            size = UsAvatarSize.Small,
            seed = community.handle,
            imageUrl = rememberMediaUrl(community.avatarMediaId),
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = community.name,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = listOfNotNull(
                    formatRelativeTime(update.publishedAt).takeIf { it.isNotBlank() },
                    if (update.isPinned) "Pinned" else null,
                    if (update.updateType.isNotBlank() && update.updateType != "post") {
                        update.updateType.replaceFirstChar { it.uppercase() }
                    } else {
                        null
                    },
                ).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        HeaderGlyph(
            icon = UsIcons.Flag,
            description = "Report update",
            onClick = onReport,
            size = FLAG_TARGET,
            glyph = FLAG_GLYPH,
            tint = UsTheme.extended.textDim,
        )
    }
}

/** One picture full width, two side by side, three or four in a 2×2. */
@Composable
private fun MediaGrid(mediaIds: List<String>) {
    val shape = RoundedCornerShape(UsTheme.radii.media)
    Column(verticalArrangement = Arrangement.spacedBy(GRID_GAP)) {
        mediaIds.take(MAX_GRID).chunked(2).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(GRID_GAP)) {
                row.forEach { id ->
                    AsyncImage(
                        model = rememberMediaUrl(id),
                        contentDescription = "Picture",
                        contentScale = ContentScale.Crop,
                        modifier = Modifier
                            .weight(1f)
                            .aspectRatio(if (row.size == 1) WIDE_ASPECT else 1f)
                            .clip(shape)
                            .background(UsTheme.extended.bgCard),
                    )
                }
            }
        }
    }
}

/** The event: a calendar tile with the date, the title, the time and the place. */
@Composable
private fun EventBlock(event: CommunityEvent) {
    val shape = RoundedCornerShape(UsTheme.radii.panel)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgRaised, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(UsTheme.spacing.l)
            .testTag("community_event"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(EVENT_TILE)
                .background(UsTheme.extended.ctaGradient, RoundedCornerShape(UsTheme.radii.medium)),
        ) {
            Icon(imageVector = UsIcons.Clock, contentDescription = null, tint = Color.White)
        }
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(LINE_GAP)) {
            Text(
                text = event.title.ifBlank { "Event" },
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = eventWhen(event),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textBody,
            )
            if (event.location.isNotBlank()) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)
                ) {
                    Icon(
                        imageVector = UsIcons.MapPin,
                        contentDescription = null,
                        tint = UsTheme.extended.textMuted,
                        modifier = Modifier.size(PIN_GLYPH),
                    )
                    Text(
                        text = event.location,
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                }
            }
        }
    }
}

/** "Sat 12 Sep · 09:00 – 12:00", from the RFC3339 instants; the raw text when they do not parse. */
internal fun eventWhen(event: CommunityEvent): String {
    val zone = java.time.ZoneId.systemDefault()
    val dayFormat = java.time.format.DateTimeFormatter.ofPattern("EEE d MMM")
    val timeFormat = java.time.format.DateTimeFormatter.ofPattern("HH:mm")
    val start = runCatching { java.time.Instant.parse(event.startsAt).atZone(zone) }.getOrNull()
        ?: return event.startsAt
    val end = runCatching { java.time.Instant.parse(event.endsAt).atZone(zone) }.getOrNull()
    return buildString {
        append(start.format(dayFormat)).append(" · ").append(start.format(timeFormat))
        if (end != null) {
            append(" – ")
            if (end.toLocalDate() != start.toLocalDate()) append(end.format(dayFormat)).append(" ")
            append(end.format(timeFormat))
        }
    }
}

/** The counts, the viewer's own emoji highlighted, and the strip that opens on tap. */
@Composable
private fun ReactionBar(
    update: CommunityUpdate,
    reacting: Boolean,
    onOpen: () -> Unit,
    onReact: (String) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier.fillMaxWidth(),
        ) {
            update.reactions.sortedByDescending { it.count }.take(MAX_REACTION_CHIPS).forEach { reaction ->
                val mine = reaction.emoji == update.viewerReaction
                val shape = RoundedCornerShape(UsTheme.radii.full)
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                    modifier = Modifier
                        .background(if (mine) Color.White else UsTheme.extended.glassBg, shape)
                        .border(HAIRLINE, if (mine) Color.White else UsTheme.extended.glassBorder, shape)
                        .pressScale({ onReact(reaction.emoji) })
                        .padding(horizontal = CHIP_HORIZONTAL, vertical = CHIP_VERTICAL)
                        .testTag("community_reaction:${update.id}:${reaction.emoji}"),
                ) {
                    Text(text = reaction.emoji, fontSize = CHIP_EMOJI)
                    Text(
                        text = reaction.count.toString(),
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Bold,
                        color = if (mine) UsTheme.extended.brandNavy else UsTheme.extended.textPrimary,
                    )
                }
            }
            Box(modifier = Modifier.weight(1f))
            Text(
                text = viewsLabel(update.viewCount),
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textDim,
            )
            HeaderGlyph(
                icon = UsIcons.Smile,
                description = "React",
                onClick = onOpen,
                size = REACT_TARGET,
                glyph = REACT_GLYPH,
                tag = "community_react:${update.id}",
            )
        }
        if (reacting) {
            val shape = RoundedCornerShape(UsTheme.radii.full)
            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                modifier = Modifier
                    .background(UsTheme.extended.bgRaised, shape)
                    .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s)
                    .testTag("community_reaction_strip:${update.id}"),
            ) {
                REACTION_STRIP.forEach { emoji ->
                    val mine = emoji == update.viewerReaction
                    Box(
                        contentAlignment = Alignment.Center,
                        modifier = Modifier
                            .size(STRIP_TARGET)
                            .background(if (mine) Color.White else Color.Transparent, CircleShape)
                            .pressScale({ onReact(emoji) })
                            .testTag("community_strip:${update.id}:$emoji"),
                    ) {
                        Text(text = emoji, fontSize = STRIP_EMOJI)
                    }
                }
            }
        }
    }
}

private fun viewsLabel(count: Int): String = when {
    count <= 0 -> ""
    count == 1 -> "1 view"
    else -> "$count views"
}

/** The "+" that opens the composer for admins — the ember disc with a white plus. */
@Composable
private fun ComposeFab(onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(FAB_SIZE)
            .shadow(
                FAB_SHADOW,
                CircleShape,
                ambientColor = UsTheme.extended.accentDeep,
                spotColor = UsTheme.extended.accentDeep
            )
            .background(UsTheme.extended.ctaGradient, CircleShape)
            .pressScale(onClick)
            .testTag("community_compose"),
    ) {
        Icon(imageVector = UsIcons.Create, contentDescription = "Post an update", tint = Color.White)
    }
}

internal val REACTION_STRIP = listOf("👍", "❤️", "🔥", "👏", "😂", "😮")

private const val BODY_CLAMP = 5
private const val BODY_CLAMP_CHARS = 280
private const val MAX_GRID = 4
private const val MAX_REACTION_CHIPS = 3
private const val WIDE_ASPECT = 16f / 9f
private val CARD_GAP = 10.dp
private val CARD_MARGIN = 12.dp
private val CARD_PADDING = 14.dp
private val FEED_BOTTOM = 112.dp
private val HAIRLINE = 1.dp
private val LINE_GAP = 2.dp
private val GRID_GAP = 4.dp
private val EVENT_TILE = 44.dp
private val PIN_GLYPH = 14.dp
private val CHIP_HORIZONTAL = 10.dp
private val CHIP_VERTICAL = 4.dp
private val CHIP_EMOJI = 14.sp
private val REACT_TARGET = 32.dp
private val REACT_GLYPH = 20.dp
private val STRIP_TARGET = 40.dp
private val STRIP_EMOJI = 22.sp
private val FLAG_TARGET = 32.dp
private val FLAG_GLYPH = 16.dp
private val BELL_TARGET = 32.dp
private val BELL_GLYPH = 18.dp
private val FAB_SIZE = 56.dp
private val FAB_SHADOW = 12.dp
