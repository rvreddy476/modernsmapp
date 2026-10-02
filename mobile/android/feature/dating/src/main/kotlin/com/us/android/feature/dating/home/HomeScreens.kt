package com.us.android.feature.dating.home

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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportSheet
import com.us.android.feature.dating.travel.TravelCopy
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.listPadding

enum class HomeTab(val label: String) { PULSE("Pulse"), PICKS(PicksCopy.TAB), SPARKS("Sparks"), MATCHES("Matches") }

/**
 * The tabs drawn: Picks (mechanic M7) only while the server offers them, so a
 * switched-off mechanic leaves no empty tab behind.
 */
internal fun visibleTabs(picks: Boolean): List<HomeTab> = HomeTab.entries.filter { it != HomeTab.PICKS || picks }

/**
 * Dating home once the profile is active: Pulse, today's picks (mechanic M7,
 * in [PicksTab]), who sparked you (the "liked you" grid, mechanic M4, in
 * [LikedYouGrid]) and matches.
 *
 * Picks are a tab of their own rather than a strip above the deck: they are a
 * fixed set for the day that is browsed and scrolled, while the deck is one
 * card at a time under a drag gesture. Sharing a screen would cost the deck
 * its height and put a scroll on top of a swipe.
 *
 * A new match — from the deck or from a spark sent back — takes the whole
 * screen (see [MatchCelebrationScreen]) until it is answered. It is state on
 * the two view models rather than a route: they already own the moment and
 * what is known about the person, and "Keep browsing" is then just this screen
 * again, exactly as it was left.
 */
@Composable
fun DatingHomeScreen(
    initialTab: HomeTab,
    onBack: () -> Unit,
    onOpenMatch: (matchId: String) -> Unit,
    onOpenChat: (conversationId: String, title: String) -> Unit,
    onOpenPerson: (userId: String) -> Unit,
    onOpenSafety: () -> Unit,
    onOpenPremium: () -> Unit,
    onOpenSettings: () -> Unit,
    onOpenFilters: () -> Unit = {},
    onOpenTravel: () -> Unit = {},
    pulse: PulseViewModel = hiltViewModel(),
    picks: PicksViewModel = hiltViewModel(),
    sparks: LikedYouViewModel = hiltViewModel(),
    matches: MatchesViewModel = hiltViewModel(),
) {
    var chosen by rememberSaveable { mutableStateOf(initialTab) }
    val picksVisible by picks.visible.collectAsStateWithLifecycle()
    val tabs = visibleTabs(picksVisible)
    // Picks switched off while chosen: back to the deck.
    val tab = chosen.takeIf { it in tabs } ?: HomeTab.PULSE
    val deck by pulse.deck.collectAsStateWithLifecycle()
    val pulseMessage by pulse.message.collectAsStateWithLifecycle()
    val picksMessage by picks.message.collectAsStateWithLifecycle()
    val sparksMessage by sparks.message.collectAsStateWithLifecycle()
    val pulseMatch by pulse.celebration.collectAsStateWithLifecycle()
    val picksMatch by picks.celebration.collectAsStateWithLifecycle()
    val sparksMatch by sparks.celebration.collectAsStateWithLifecycle()
    val pulseHello by pulse.hello.collectAsStateWithLifecycle()
    val picksHello by picks.hello.collectAsStateWithLifecycle()
    val sparksHello by sparks.hello.collectAsStateWithLifecycle()

    // "Say hello": the chat when the match has one, the match itself until it does.
    LaunchedEffect(pulseHello, picksHello, sparksHello) {
        val target = pulseHello ?: picksHello ?: sparksHello ?: return@LaunchedEffect
        pulse.helloHandled()
        picks.helloHandled()
        sparks.helloHandled()
        when (target) {
            is HelloTarget.Chat -> onOpenChat(target.conversationId, target.title)
            is HelloTarget.Match -> onOpenMatch(target.matchId)
        }
    }

    val celebration = pulseMatch ?: picksMatch ?: sparksMatch
    if (celebration != null) {
        val from = when {
            pulseMatch != null -> HomeTab.PULSE
            picksMatch != null -> HomeTab.PICKS
            else -> HomeTab.SPARKS
        }
        MatchCelebrationScreen(
            match = celebration,
            onSayHello = {
                when (from) {
                    HomeTab.PULSE -> pulse.sayHello()
                    HomeTab.PICKS -> picks.sayHello()
                    else -> sparks.sayHello()
                }
            },
            onKeepBrowsing = {
                when (from) {
                    HomeTab.PULSE -> pulse.dismissCelebration()
                    HomeTab.PICKS -> picks.dismissCelebration()
                    else -> sparks.dismissCelebration()
                }
            },
        )
        return
    }

    DatingScreen(
        title = "Dating",
        onBack = onBack,
        message = when (tab) {
            HomeTab.SPARKS -> sparksMessage
            HomeTab.PICKS -> picksMessage
            else -> pulseMessage
        },
        onDismissMessage = {
            when (tab) {
                HomeTab.SPARKS -> sparks.dismissMessage()
                HomeTab.PICKS -> picks.dismissMessage()
                else -> pulse.dismissMessage()
            }
        },
        actions = {
            // Mechanic M8: travel changes both the deck and the picks, so it sits on both.
            if (deck.travelEnabled && (tab == HomeTab.PULSE || tab == HomeTab.PICKS)) {
                IconButton(onClick = onOpenTravel) {
                    Icon(UsIcons.MapPin, contentDescription = TravelCopy.ENTRY, tint = UsTheme.extended.textPrimary)
                }
            }
            // Mechanic M6: the deck's filters, drawn on the deck only.
            if (tab == HomeTab.PULSE) {
                IconButton(onClick = onOpenFilters) { Icon(UsIcons.Sliders, contentDescription = "Filters", tint = UsTheme.extended.textPrimary) }
            }
            IconButton(onClick = onOpenSafety) { Icon(UsIcons.Flag, contentDescription = "Safety", tint = UsTheme.extended.textPrimary) }
            IconButton(onClick = onOpenPremium) { Icon(UsIcons.HeartHandshake, contentDescription = "Premium", tint = UsTheme.extended.textPrimary) }
            IconButton(onClick = onOpenSettings) { Icon(UsIcons.Settings, contentDescription = "Privacy and settings", tint = UsTheme.extended.textPrimary) }
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(top = padding.calculateTopPadding())) {
            TabRow(
                selectedTabIndex = tabs.indexOf(tab).coerceAtLeast(0),
                containerColor = UsTheme.extended.bgCanvas,
                contentColor = UsTheme.extended.textPrimary,
            ) {
                tabs.forEach { entry ->
                    Tab(selected = tab == entry, onClick = { chosen = entry }, text = { Text(entry.label, maxLines = 1) })
                }
            }
            Box(Modifier.fillMaxSize().padding(bottom = padding.calculateBottomPadding())) {
                when (tab) {
                    HomeTab.PULSE -> PulseDeck(pulse, onOpenPerson, onOpenPremium, onOpenTravel)
                    HomeTab.PICKS -> PicksTab(picks)
                    HomeTab.SPARKS -> LikedYouGrid(sparks, onOpenPerson, onOpenPremium)
                    HomeTab.MATCHES -> MatchesList(matches, onOpenMatch)
                }
            }
        }
    }
}

/** An incoming Super Spark: a star and our own words, in the warm status colour. */
@Composable
internal fun SuperSparkMark() {
    val color = UsTheme.extended.statusWarning
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier.padding(vertical = UsTheme.spacing.xs),
    ) {
        Icon(UsIcons.Star, contentDescription = null, tint = color, modifier = Modifier.size(14.dp))
        Text(SUPER_SPARK_MARK, style = MaterialTheme.typography.labelMedium, color = color)
    }
}

/** The incoming-row label for a Super Spark. */
const val SUPER_SPARK_MARK = "Sent you a Super Spark"

@Composable
private fun MatchesList(viewModel: MatchesViewModel, onOpenMatch: (String) -> Unit) {
    LaunchedEffect(Unit) { viewModel.refresh() }
    val state by viewModel.state.collectAsStateWithLifecycle()
    val now by rememberNow()
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
                                // Mechanic M5: who starts, and the time left.
                                match.firstMove?.let { FirstMoveRowTag(it, now) }
                                // City sits with the distance it belongs to. The
                                // row stays one line: last active is on the match
                                // itself, where there is room for it. A first-move
                                // match drops "say hello": its tag says who starts.
                                val line = listOfNotNull(
                                    matchStatusLabel(match.status).takeIf { it.isNotBlank() && match.firstMove == null },
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
    val firstMoveActions by viewModel.firstMove.collectAsStateWithLifecycle()
    val now by rememberNow()
    val waitingActions = remember(viewModel) {
        WaitingActions(
            onStartAnswer = viewModel::startAnswer,
            onEditAnswer = viewModel::editAnswer,
            onSendAnswer = viewModel::sendAnswer,
            onCancelAnswer = viewModel::cancelAnswer,
            onExtend = viewModel::extend,
        )
    }
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
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(top = padding.calculateTopPadding(), bottom = padding.calculateBottomPadding()),
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
                    matchStatusLabel(s.match.status).takeIf { it.isNotBlank() && s.match.firstMove == null },
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
                // Mechanic M5 decides how a first-move match is started; see FirstMoveUi.
                val move = s.match.firstMove
                when {
                    move == null -> UsButton(text = "Open chat", onClick = viewModel::openChat, modifier = Modifier.fillMaxWidth())
                    move.youMoveFirst -> YouStartPanel(move, now, onOpenChat = viewModel::openChat)
                    else -> WaitingPanel(name = s.match.name, firstMove = move, actions = firstMoveActions, now = now, on = waitingActions)
                }
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
