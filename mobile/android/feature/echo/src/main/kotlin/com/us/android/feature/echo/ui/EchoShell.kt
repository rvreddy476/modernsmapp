package com.us.android.feature.echo.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.Text
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.echo.data.EchoProfile
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.data.compactCount
import kotlinx.coroutines.launch

/** Echo's bottom bar: X's Home, Explore, Notifications, and Chat (which leaves for the app's inbox). */
enum class EchoTab(val label: String, val icon: ImageVector) {
    HOME("Home", UsIcons.Home),
    EXPLORE("Explore", UsIcons.Search),
    NOTIFICATIONS("Notifications", UsIcons.Notifications),
}

/**
 * X's hide-on-scroll: scrolling the list down slides the top bar up and the
 * bottom bar down by the same fraction; scrolling up brings both back.
 * [hidden] runs 0 (shown) to 1 (gone).
 */
@Stable
class EchoBarsState {
    var hidden by mutableFloatStateOf(0f)
        private set
    var headerHeight by mutableIntStateOf(0)
    var bottomHeight by mutableIntStateOf(0)

    val connection = object : NestedScrollConnection {
        override fun onPreScroll(available: Offset, source: NestedScrollSource): Offset {
            val span = headerHeight.takeIf { it > 0 } ?: return Offset.Zero
            hidden = (hidden - available.y / span).coerceIn(0f, 1f)
            return Offset.Zero
        }
    }

    fun show() {
        hidden = 0f
    }
}

/**
 * Echo as its own app inside the app: its top bar (the account's avatar
 * opens the drawer, the Echo mark centred), its tabs, its bottom bar and the
 * compose button. The VChat bar is not drawn under it.
 */
@Composable
@Suppress("LongMethod") // the frame: drawer, three tabs, two bars and the compose button
fun EchoShell(
    destinations: EchoDestinations,
    shell: EchoShellViewModel = hiltViewModel(),
    home: EchoHomeViewModel = hiltViewModel(),
) {
    val me by shell.me.collectAsStateWithLifecycle()
    val urls by shell.mediaUrls.collectAsStateWithLifecycle()
    val unread by shell.unread.collectAsStateWithLifecycle()
    val homeState by home.state.collectAsStateWithLifecycle()
    val drawer = rememberDrawerState(DrawerValue.Closed)
    val scope = rememberCoroutineScope()
    var tab by rememberSaveable { mutableStateOf(EchoTab.HOME) }
    val bars = remember { EchoBarsState() }
    val avatarUrl = me?.avatarMediaId?.let(urls::get)
    val openDrawer: () -> Unit = { scope.launch { drawer.open() } }

    LaunchedEffect(homeState.openComposer) {
        homeState.openComposer?.let {
            home.composerOpened()
            destinations.onCompose(it)
        }
    }
    LaunchedEffect(tab) { bars.show() }
    // X: Back closes the open drawer first; it does not leave Echo.
    BackHandler(enabled = drawer.isOpen) { scope.launch { drawer.close() } }

    EchoLinks(destinations) {
        ModalNavigationDrawer(
            drawerState = drawer,
            drawerContent = {
                EchoDrawer(
                    me = me,
                    avatarUrl = avatarUrl,
                    destinations = destinations,
                    onClose = { scope.launch { drawer.close() } },
                )
            },
        ) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(UsTheme.extended.bgCanvas)
                    .nestedScroll(bars.connection),
            ) {
                when (tab) {
                    EchoTab.HOME -> HomeTab(
                        viewModel = home,
                        destinations = destinations,
                        bars = bars,
                        avatar = { ShellAvatar(me, avatarUrl, openDrawer) },
                    )
                    EchoTab.EXPLORE -> ExploreTab(
                        destinations = destinations,
                        bars = bars,
                        avatar = { ShellAvatar(me, avatarUrl, openDrawer) },
                    )
                    EchoTab.NOTIFICATIONS -> NotificationsTab(
                        destinations = destinations,
                        bars = bars,
                        avatar = { ShellAvatar(me, avatarUrl, openDrawer) },
                    )
                }
                if (tab == EchoTab.HOME) {
                    ComposeButton(
                        onPost = home::compose,
                        onThread = home::composeThread,
                        modifier = Modifier
                            .align(Alignment.BottomEnd)
                            .graphicsLayer { translationY = bars.hidden * bars.bottomHeight }
                            .padding(bottom = with(LocalDensity.current) { bars.bottomHeight.toDp() }),
                    )
                }
                EchoBottomBar(
                    selected = tab,
                    unread = unread,
                    onSelect = { tab = it },
                    onChat = destinations.onOpenChat,
                    modifier = Modifier
                        .align(Alignment.BottomCenter)
                        .onSizeChanged { bars.bottomHeight = it.height }
                        .graphicsLayer { translationY = bars.hidden * size.height },
                )
            }
        }
    }
    homeState.blocked?.let {
        EligibilitySheet(
            blocked = it,
            onDismiss = home::dismissBlocked,
            premiumEntryShown = destinations.premiumEntryShown,
            onGetPremium = destinations.onGetPremium,
        )
    }
}

/** The top bar's account avatar: opens the drawer. */
@Composable
internal fun ShellAvatar(me: EchoProfile?, url: String?, onClick: () -> Unit) {
    UsAvatar(
        name = me?.name.orEmpty(),
        seed = me?.userId.orEmpty(),
        imageUrl = url,
        size = UsAvatarSize.Small,
        contentDescription = "Open menu",
        modifier = Modifier.clip(CircleShape).clickable(onClick = onClick).testTag("echo_drawer_open"),
    )
}

/** The Echo mark, centred in the Home top bar. */
@Composable
internal fun EchoMark(modifier: Modifier = Modifier) {
    Text(
        text = ECHO_TITLE,
        style = MaterialTheme.typography.titleLarge,
        fontWeight = FontWeight.Black,
        color = UsTheme.extended.textPrimary,
        modifier = modifier.semantics { contentDescription = ECHO_TITLE }.testTag("echo_logo"),
    )
}

@Composable
private fun EchoBottomBar(
    selected: EchoTab,
    unread: Int,
    onSelect: (EchoTab) -> Unit,
    onChat: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxWidth().background(UsTheme.extended.bgCanvas)) {
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        Row(
            modifier = Modifier.fillMaxWidth().height(BAR_HEIGHT),
            horizontalArrangement = Arrangement.SpaceAround,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            EchoTab.entries.forEach { item ->
                val active = item == selected
                BarItem(
                    icon = item.icon,
                    label = item.label,
                    active = active,
                    badge = if (item == EchoTab.NOTIFICATIONS) unread else 0,
                    tag = item.name.lowercase(),
                    onClick = { onSelect(item) },
                )
            }
            BarItem(icon = UsIcons.Comment, label = "Chat", active = false, badge = 0, tag = "chat", onClick = onChat)
        }
    }
}

@Composable
private fun BarItem(icon: ImageVector, label: String, active: Boolean, badge: Int, tag: String, onClick: () -> Unit) {
    val tint = if (active) UsTheme.extended.textPrimary else UsTheme.extended.textTertiary
    Box(
        modifier = Modifier
            .size(BAR_HEIGHT)
            .clip(CircleShape)
            .clickable(onClick = onClick)
            .semantics { contentDescription = if (badge > 0) "$label, $badge new" else label }
            .testTag("echo_bar:$tag"),
        contentAlignment = Alignment.Center,
    ) {
        BadgedBox(
            badge = {
                if (badge > 0) {
                    Badge(containerColor = UsTheme.extended.accent, contentColor = UsTheme.extended.onAccent) {
                        Text(if (badge > MAX_BADGE) "$MAX_BADGE+" else badge.toString())
                    }
                }
            },
        ) {
            Icon(
                icon,
                contentDescription = null,
                tint = tint,
                modifier = Modifier.size(if (active) ICON_ACTIVE else ICON)
            )
        }
    }
}

/**
 * X's floating compose button: a tap writes a post; a long press opens
 * "Post" and "Thread" above it.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ComposeButton(onPost: () -> Unit, onThread: () -> Unit, modifier: Modifier = Modifier) {
    var open by rememberSaveable { mutableStateOf(false) }
    Column(
        modifier = modifier.padding(UsTheme.spacing.xxl),
        horizontalAlignment = Alignment.End,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        AnimatedVisibility(visible = open, enter = fadeIn(), exit = fadeOut()) {
            Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                MiniAction("Thread", UsIcons.Type, "thread") {
                    open = false
                    onThread()
                }
                MiniAction("Post", UsIcons.SquarePen, "post") {
                    open = false
                    onPost()
                }
            }
        }
        Box(
            modifier = Modifier
                .size(FAB)
                .shadow(FAB_ELEVATION, CircleShape)
                .clip(CircleShape)
                .background(UsTheme.extended.accent)
                .combinedClickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = androidx.compose.material3.ripple(),
                    onClick = {
                        if (open) open = false else onPost()
                    },
                    onLongClick = { open = !open },
                )
                .semantics { contentDescription = "Post" }
                .testTag("echo_compose"),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                if (open) UsIcons.Close else UsIcons.Create,
                contentDescription = null,
                tint = UsTheme.extended.onAccent,
            )
        }
    }
}

@Composable
private fun MiniAction(label: String, icon: ImageVector, tag: String, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier.clickable(onClick = onClick).testTag("echo_compose:$tag"),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .background(UsTheme.extended.bgCardSolid, RoundedCornerShape(UsTheme.radii.full))
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.xs),
        )
        Box(
            modifier = Modifier.size(MINI_FAB).clip(CircleShape).background(UsTheme.extended.bgCardSolid),
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = UsTheme.extended.accent)
        }
    }
}

/**
 * X's drawer: the account (avatar, name, mark, @handle, Following /
 * Followers), then Profile, Premium (only while it may be sold), Bookmarks,
 * Scheduled, Settings, and the way back to VChat.
 */
@Composable
private fun EchoDrawer(me: EchoProfile?, avatarUrl: String?, destinations: EchoDestinations, onClose: () -> Unit) {
    fun go(action: () -> Unit): () -> Unit = {
        onClose()
        action()
    }
    ModalDrawerSheet(drawerContainerColor = UsTheme.extended.bgCanvas, modifier = Modifier.testTag("echo_drawer")) {
        Column(modifier = Modifier.padding(UsTheme.spacing.xxl)) {
            UsAvatar(
                name = me?.name.orEmpty(),
                seed = me?.userId.orEmpty(),
                imageUrl = avatarUrl,
                size = UsAvatarSize.Medium,
                modifier = Modifier.clickable(onClick = go { me?.let { destinations.onOpenEchoProfile(it.userId) } }),
            )
            Row(modifier = Modifier.padding(top = UsTheme.spacing.l), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = me?.name.orEmpty(),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                )
                me?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
            }
            Text(
                text = me?.handle.orEmpty(),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary
            )
            Row(
                modifier = Modifier.padding(top = UsTheme.spacing.l),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl)
            ) {
                CountLabel(me?.following ?: 0, "Following")
                CountLabel(me?.followers ?: 0, "Followers")
            }
        }
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        DrawerRow(UsIcons.Profile, "Profile", "profile", go { me?.let { destinations.onOpenEchoProfile(it.userId) } })
        if (destinations.premiumEntryShown()) {
            DrawerRow(
                UsIcons.Sparkles,
                "Premium",
                "premium",
                go(destinations.onGetPremium)
            )
        }
        DrawerRow(UsIcons.BookmarkOutline, "Bookmarks", "bookmarks", go(destinations.onOpenBookmarks))
        DrawerRow(UsIcons.Clock, "Scheduled", "scheduled", go(destinations.onOpenScheduled))
        DrawerRow(UsIcons.Settings, "Settings", "settings", go(destinations.onOpenSettings))
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        DrawerRow(UsIcons.Back, "Back to VChat", "vchat", go(destinations.onBackToVChat))
    }
}

@Composable
internal fun CountLabel(count: Long, label: String, onClick: (() -> Unit)? = null) {
    Row(modifier = if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier) {
        Text(
            text = compactCount(count).ifEmpty { "0" },
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = " $label",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textTertiary,
        )
    }
}

@Composable
private fun DrawerRow(icon: ImageVector, label: String, tag: String, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
            .testTag("echo_drawer:$tag"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        Icon(icon, contentDescription = null, tint = UsTheme.extended.textPrimary)
        Text(
            text = label,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary
        )
    }
}

const val ECHO_TITLE = "Echo"
private const val MAX_BADGE = 99
private val BAR_HEIGHT = 52.dp
private val ICON = 24.dp
private val ICON_ACTIVE = 26.dp
private val FAB = 56.dp
private val MINI_FAB = 44.dp
private val FAB_ELEVATION = 6.dp
