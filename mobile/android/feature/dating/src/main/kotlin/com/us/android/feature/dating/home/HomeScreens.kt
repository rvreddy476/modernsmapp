package com.us.android.feature.dating.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportSheet
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.listPadding

enum class HomeTab(val label: String) { PULSE("Pulse"), SPARKS("Sparks"), MATCHES("Matches") }

/** Dating home once the profile is active: Pulse, incoming sparks and matches. */
@Composable
fun DatingHomeScreen(
    initialTab: HomeTab,
    onBack: () -> Unit,
    onOpenMatch: (matchId: String) -> Unit,
    onOpenPerson: (userId: String) -> Unit,
    onOpenSafety: () -> Unit,
    onOpenPremium: () -> Unit,
    onOpenSettings: () -> Unit,
    pulse: PulseViewModel = hiltViewModel(),
    sparks: SparksViewModel = hiltViewModel(),
    matches: MatchesViewModel = hiltViewModel(),
) {
    var tab by rememberSaveable { mutableStateOf(initialTab) }
    val pulseMessage by pulse.message.collectAsStateWithLifecycle()
    val sparksMessage by sparks.message.collectAsStateWithLifecycle()
    val pulseMatch by pulse.celebration.collectAsStateWithLifecycle()
    val sparksMatch by sparks.celebration.collectAsStateWithLifecycle()

    DatingScreen(
        title = "Dating",
        onBack = onBack,
        message = if (tab == HomeTab.SPARKS) sparksMessage else pulseMessage,
        onDismissMessage = { if (tab == HomeTab.SPARKS) sparks.dismissMessage() else pulse.dismissMessage() },
        actions = {
            IconButton(onClick = onOpenSafety) { Icon(UsIcons.Flag, contentDescription = "Safety", tint = UsTheme.extended.textPrimary) }
            IconButton(onClick = onOpenPremium) { Icon(UsIcons.HeartHandshake, contentDescription = "Premium", tint = UsTheme.extended.textPrimary) }
            IconButton(onClick = onOpenSettings) { Icon(UsIcons.Settings, contentDescription = "Privacy and settings", tint = UsTheme.extended.textPrimary) }
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(top = padding.calculateTopPadding())) {
            TabRow(
                selectedTabIndex = tab.ordinal,
                containerColor = UsTheme.extended.bgCanvas,
                contentColor = UsTheme.extended.textPrimary,
            ) {
                HomeTab.entries.forEach { entry ->
                    Tab(selected = tab == entry, onClick = { tab = entry }, text = { Text(entry.label) })
                }
            }
            Box(Modifier.fillMaxSize().padding(bottom = padding.calculateBottomPadding())) {
                when (tab) {
                    HomeTab.PULSE -> PulseDeck(pulse)
                    HomeTab.SPARKS -> SparksList(sparks, onOpenPerson)
                    HomeTab.MATCHES -> MatchesList(matches, onOpenMatch)
                }
            }
        }
    }

    (pulseMatch ?: sparksMatch)?.let { match ->
        ConfirmDialog(
            title = "It's a match",
            body = if (match.name.isBlank()) "You both sparked. Say hello." else "You and ${match.name} both sparked. Say hello.",
            confirmLabel = "Open match",
            dismissLabel = "Later",
            onConfirm = {
                pulse.dismissCelebration()
                sparks.dismissCelebration()
                onOpenMatch(match.matchId)
            },
            onDismiss = {
                pulse.dismissCelebration()
                sparks.dismissCelebration()
            },
        )
    }
}

@Composable
private fun PulseDeck(viewModel: PulseViewModel) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val busy by viewModel.busy.collectAsStateWithLifecycle()
    var reporting by remember { mutableStateOf<CardUi?>(null) }
    var blocking by remember { mutableStateOf<CardUi?>(null) }

    when (val s = state) {
        ListState.Loading -> LoadingPane()
        is ListState.Failed -> MessagePane(title = "Pulse didn't load", body = s.message, primaryLabel = "Try again", onPrimary = viewModel::refresh)
        is ListState.Items -> if (s.items.isEmpty()) {
            MessagePane(
                title = if (s.gated) "Pulse isn't ready for you yet" else "You're all caught up",
                body = if (s.gated) "We're opening Pulse gradually. Check back soon." else "New people show up every day.",
                icon = UsIcons.Compass,
                secondaryLabel = "Refresh",
                onSecondary = viewModel::refresh,
            )
        } else {
            val pager = rememberPagerState(pageCount = { s.items.size })
            HorizontalPager(
                state = pager,
                contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = UsTheme.spacing.pageHorizontal),
                pageSpacing = UsTheme.spacing.l,
                modifier = Modifier.fillMaxSize(),
            ) { page ->
                val card = s.items[page]
                PulseCard(
                    card = card,
                    busy = busy == card.userId,
                    onSpark = { viewModel.spark(card.userId) },
                    onPass = { viewModel.pass(card.userId) },
                    onStash = { viewModel.stash(card.userId) },
                    onReport = { reporting = card },
                    onBlock = { blocking = card },
                )
            }
        }
    }

    reporting?.let { card ->
        ReportSheet(
            initial = ReportDraft(targetId = card.userId, photoIds = listOfNotNull(card.photoId)),
            name = card.name,
            onSubmit = {
                viewModel.report(it)
                reporting = null
            },
            onDismiss = { reporting = null },
        )
    }
    blocking?.let { card ->
        ConfirmDialog(
            title = "Block ${card.name}?",
            body = "You won't see each other in Pulse, sparks or matches again.",
            confirmLabel = "Block",
            destructive = true,
            onConfirm = {
                viewModel.block(card.userId)
                blocking = null
            },
            onDismiss = { blocking = null },
        )
    }
}

@Composable
private fun PulseCard(
    card: CardUi,
    busy: Boolean,
    onSpark: () -> Unit,
    onPass: () -> Unit,
    onStash: () -> Unit,
    onReport: () -> Unit,
    onBlock: () -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxSize().padding(vertical = UsTheme.spacing.l), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        // The card scrolls: the photo decides at a glance, the description and
        // prompt answers below are what someone actually sparks on.
        Column(
            modifier = Modifier.weight(1f).verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(PHOTO_RATIO)
                .clip(RoundedCornerShape(UsTheme.radii.card)),
        ) {
            PersonGallery(
                photos = card.detail?.gallery.orEmpty(),
                fallbackUrl = card.photoUrl,
                name = card.name,
                modifier = Modifier.fillMaxSize(),
            )
            Box(Modifier.align(Alignment.TopEnd)) {
                IconButton(onClick = { menu = true }, modifier = Modifier.padding(6.dp).background(UsTheme.extended.bgCanvas.copy(alpha = 0.7f), CircleShape)) {
                    Icon(UsIcons.More, contentDescription = "More", tint = UsTheme.extended.textPrimary)
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text("Report") }, onClick = { menu = false; onReport() })
                    DropdownMenuItem(text = { Text("Block") }, onClick = { menu = false; onBlock() })
                }
            }
            Column(
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .background(UsTheme.extended.bgCanvas.copy(alpha = 0.72f))
                    .padding(UsTheme.spacing.xxl),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    Text("${card.name}, ${card.age}", style = MaterialTheme.typography.headlineSmall, color = UsTheme.extended.textPrimary)
                    if (card.verified) Pill("Verified", Tone.Positive)
                }
                val place = listOfNotNull(card.city.takeIf { it.isNotBlank() }, card.distance).joinToString(" · ")
                if (place.isNotBlank()) Text(place, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                card.lastActive?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
                card.reasons.take(MAX_REASONS).forEach {
                    Text("• $it", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }
            PersonDetailBody(card.detail)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), verticalAlignment = Alignment.CenterVertically) {
            UsSecondaryButton(text = "Pass", enabled = !busy, onClick = onPass, modifier = Modifier.weight(1f))
            IconButton(onClick = onStash, enabled = !busy) {
                Icon(UsIcons.BookmarkOutline, contentDescription = "Save for later", tint = UsTheme.extended.textPrimary)
            }
            UsButton(text = "Spark", loading = busy, onClick = onSpark, modifier = Modifier.weight(1f))
        }
    }
}

@Composable
private fun SparksList(viewModel: SparksViewModel, onOpenPerson: (String) -> Unit) {
    LaunchedEffect(Unit) { viewModel.refresh() }
    val state by viewModel.state.collectAsStateWithLifecycle()
    var reporting by remember { mutableStateOf<IncomingSparkUi?>(null) }
    when (val s = state) {
        ListState.Loading -> LoadingPane()
        is ListState.Failed -> MessagePane(title = "Sparks didn't load", body = s.message, primaryLabel = "Try again", onPrimary = viewModel::refresh)
        is ListState.Items -> if (s.items.isEmpty()) {
            MessagePane(title = "No sparks yet", body = "When someone sparks you, they'll show up here.", icon = UsIcons.HeartOutline)
        } else {
            LazyColumn(
                contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                items(s.items, key = { it.sparkId }) { spark ->
                    DatingCard(onClick = { onOpenPerson(spark.fromUserId) }) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                            DatingPhoto(url = spark.photoUrl, contentDescription = null, modifier = Modifier.size(56.dp).clip(CircleShape))
                            Column(Modifier.weight(1f)) {
                                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                                    Text(
                                        personLine(spark.name, spark.age) ?: "Someone sparked you",
                                        style = MaterialTheme.typography.titleMedium,
                                        color = UsTheme.extended.textPrimary,
                                    )
                                    if (spark.verified) Pill("Verified", Tone.Positive)
                                }
                                // One line, the deck's separator: city and intent
                                // join the distance the row already carried rather
                                // than adding rows to a row that is already dense.
                                val about = listOfNotNull(spark.city, spark.intent, spark.distance).joinToString(" · ")
                                if (about.isNotBlank()) {
                                    Text(about, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                                }
                                spark.note?.let { Text("“$it”", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary) }
                            }
                            IconButton(onClick = { reporting = spark }) { Icon(UsIcons.Flag, contentDescription = "Report", tint = UsTheme.extended.textMuted) }
                        }
                        // The same pre-match detail the deck shows: decline or
                        // spark back is a decision, so it needs the same to go on.
                        spark.detail?.gallery?.takeIf { it.isNotEmpty() }?.let { gallery ->
                            PersonGallery(
                                photos = gallery,
                                fallbackUrl = spark.photoUrl,
                                name = spark.name,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .aspectRatio(PHOTO_RATIO)
                                    .clip(RoundedCornerShape(UsTheme.radii.card)),
                            )
                        }
                        PersonDetailBody(spark.detail)
                        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                            UsSecondaryButton(text = "Decline", onClick = { viewModel.decline(spark) }, modifier = Modifier.weight(1f))
                            UsButton(text = "Spark back", onClick = { viewModel.accept(spark) }, modifier = Modifier.weight(1f))
                        }
                    }
                }
            }
        }
    }
    reporting?.let { spark ->
        ReportSheet(
            initial = ReportDraft(targetId = spark.fromUserId, sparkIds = listOf(spark.sparkId)),
            name = spark.name,
            onSubmit = {
                viewModel.report(it)
                reporting = null
            },
            onDismiss = { reporting = null },
        )
    }
}

@Composable
private fun MatchesList(viewModel: MatchesViewModel, onOpenMatch: (String) -> Unit) {
    LaunchedEffect(Unit) { viewModel.refresh() }
    val state by viewModel.state.collectAsStateWithLifecycle()
    when (val s = state) {
        ListState.Loading -> LoadingPane()
        is ListState.Failed -> MessagePane(title = "Matches didn't load", body = s.message, primaryLabel = "Try again", onPrimary = viewModel::refresh)
        is ListState.Items -> if (s.items.isEmpty()) {
            MessagePane(title = "No matches yet", body = "When you and someone both spark, you'll match here.", icon = UsIcons.HeartHandshake)
        } else {
            LazyColumn(
                contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                items(s.items, key = { it.matchId }) { match ->
                    DatingCard(onClick = { onOpenMatch(match.matchId) }) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                            DatingPhoto(url = match.photoUrl, contentDescription = null, modifier = Modifier.size(56.dp).clip(CircleShape))
                            Column(Modifier.weight(1f)) {
                                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                                    Text(
                                        personLine(match.name, match.age) ?: "Your match",
                                        style = MaterialTheme.typography.titleMedium,
                                        color = UsTheme.extended.textPrimary,
                                    )
                                    if (match.verified) Pill("Verified", Tone.Positive)
                                }
                                // City sits with the distance it belongs to. The
                                // row stays one line: last active is on the match
                                // itself, where there is room for it.
                                val line = listOfNotNull(
                                    matchStatusLabel(match.status).takeIf { it.isNotBlank() },
                                    match.city,
                                    match.distance,
                                ).joinToString(" · ")
                                if (line.isNotBlank()) {
                                    Text(line, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                                }
                            }
                            Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textDim)
                        }
                    }
                }
            }
        }
    }
}

/**
 * "Asha, 30" — or just the name when the server sent no age, or null when it
 * sent no name either, so the caller can fall back to a neutral placeholder.
 */
fun personLine(name: String?, age: Int?): String? {
    val person = name?.takeIf { it.isNotBlank() } ?: return null
    return if (age != null && age > 0) "$person, $age" else person
}

fun matchStatusLabel(status: String): String = when (status) {
    "matched" -> "New match — say hello"
    "conversing" -> "Chatting"
    "quiet" -> "Quiet for a while"
    "expired" -> "Expired"
    else -> ""
}

/** One match. [onOpenChat] is `:app`'s edge into chat; the conversation already exists server-side. */
@Composable
fun MatchDetailScreen(
    onBack: () -> Unit,
    onOpenChat: (conversationId: String, title: String) -> Unit,
    onShareLocation: (recipientId: String) -> Unit,
    viewModel: MatchDetailViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val chat by viewModel.chat.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    var confirmUnmatch by remember { mutableStateOf(false) }
    var confirmBlock by remember { mutableStateOf(false) }
    var reporting by remember { mutableStateOf(false) }

    LaunchedEffect(chat) {
        chat?.let {
            viewModel.chatOpened()
            onOpenChat(it.conversationId, it.title)
        }
    }

    DatingScreen(title = "Match", onBack = onBack, message = message, onDismissMessage = viewModel::dismissMessage) { padding ->
        when (val s = state) {
            MatchDetailState.Loading -> LoadingPane()
            is MatchDetailState.Gone -> MessagePane(title = s.message, body = "", primaryLabel = "Back", onPrimary = onBack)
            is MatchDetailState.Loaded -> Column(
                modifier = Modifier.fillMaxSize().padding(top = padding.calculateTopPadding(), bottom = padding.calculateBottomPadding()),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                DatingPhoto(
                    url = s.match.photoUrl,
                    contentDescription = s.match.name,
                    modifier = Modifier.fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(UsTheme.radii.card)),
                )
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    Text(
                        personLine(s.match.name, s.match.age) ?: "Your match",
                        style = MaterialTheme.typography.headlineSmall,
                        color = UsTheme.extended.textPrimary,
                    )
                    if (s.match.verified) Pill("Verified", Tone.Positive)
                }
                val detail = listOfNotNull(
                    matchStatusLabel(s.match.status).takeIf { it.isNotBlank() },
                    s.match.city,
                    s.match.distance,
                ).joinToString(" · ")
                if (detail.isNotBlank()) {
                    Text(detail, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                }
                // Absent when they hide last active, and nothing stands in for it.
                s.match.lastActive?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                }
                UsButton(text = "Open chat", onClick = viewModel::openChat, modifier = Modifier.fillMaxWidth())
                UsSecondaryButton(text = "Share my live location", onClick = { onShareLocation(s.match.otherUserId) }, modifier = Modifier.fillMaxWidth())
                UsSecondaryButton(text = "Unmatch", onClick = { confirmUnmatch = true }, modifier = Modifier.fillMaxWidth())
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    UsSecondaryButton(text = "Report", onClick = { reporting = true }, modifier = Modifier.weight(1f))
                    UsSecondaryButton(text = "Block", onClick = { confirmBlock = true }, modifier = Modifier.weight(1f))
                }
            }
        }
    }

    val loaded = state as? MatchDetailState.Loaded
    if (confirmUnmatch && loaded != null) {
        ConfirmDialog(
            title = "Unmatch?",
            body = "Your chat closes and you won't match again unless you both spark again.",
            confirmLabel = "Unmatch",
            destructive = true,
            onConfirm = {
                confirmUnmatch = false
                viewModel.unmatch()
            },
            onDismiss = { confirmUnmatch = false },
        )
    }
    if (confirmBlock && loaded != null) {
        ConfirmDialog(
            title = "Block ${loaded.match.name ?: "this person"}?",
            body = "This ends the match and closes the chat. You won't see each other again.",
            confirmLabel = "Block",
            destructive = true,
            onConfirm = {
                confirmBlock = false
                viewModel.block()
            },
            onDismiss = { confirmBlock = false },
        )
    }
    if (reporting && loaded != null) {
        ReportSheet(
            initial = ReportDraft(targetId = loaded.match.otherUserId),
            name = loaded.match.name,
            onSubmit = {
                reporting = false
                viewModel.report(it)
            },
            onDismiss = { reporting = false },
        )
    }
}

private const val MAX_REASONS = 3
