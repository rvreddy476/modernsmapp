package com.us.android.feature.tube.ui.channel

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.NotifyOn

/**
 * The one relationship control Tube draws toward a channel (founder,
 * 2026-09-12): "Subscribe" while the viewer is known not to be subscribed,
 * "Subscribed" beside the bell once they are, nothing while the edge is
 * still unknown or the channel is the viewer's own.
 *
 * Shared by the channel page and the watch screen's author row so the two
 * can never drift: a subscribe is follow plus notify made by the server in
 * one call, and a screen that drew a plain Follow beside a Subscribe
 * elsewhere would be offering two different edges for one channel.
 *
 * [tagPrefix] keeps each screen's test tags its own (`<prefix>_subscribe`,
 * `<prefix>_subscribed`, `<prefix>_bell`), because a UI test that finds
 * two "subscribe" nodes on one screen cannot tell which it tapped.
 */
@Composable
@Suppress("LongParameterList") // One callback per control; a holder would hide, not help.
fun SubscribeControl(
    channelName: String,
    subscription: ChannelSubscription?,
    offersSubscribe: Boolean,
    busy: Boolean,
    onSubscribe: () -> Unit,
    onUnsubscribe: () -> Unit,
    onToggleNotify: () -> Unit,
    tagPrefix: String,
    modifier: Modifier = Modifier,
) {
    when {
        offersSubscribe -> UsFollowButton(
            text = "Subscribe",
            onClick = onSubscribe,
            busy = busy,
            modifier = modifier.testTag("${tagPrefix}_subscribe"),
        )
        subscription?.subscribed == true -> Row(
            modifier = modifier,
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            UsPillButton(
                text = "Subscribed",
                onClick = onUnsubscribe,
                filled = false,
                busy = busy,
                modifier = Modifier.testTag("${tagPrefix}_subscribed"),
            )
            NotifyBell(
                channelName = channelName,
                on = subscription.notifyOn == NotifyOn.ALL,
                enabled = !busy,
                onToggle = onToggleNotify,
                tag = "${tagPrefix}_bell",
            )
        }
    }
}

/**
 * The bell beside "Subscribed": on means every upload from this channel
 * notifies, off means none. The description names the channel and the
 * state, and the state is also a stateDescription, so a screen reader
 * says which way the bell is BEFORE the tap flips it.
 */
@Composable
private fun NotifyBell(
    channelName: String,
    on: Boolean,
    enabled: Boolean,
    onToggle: () -> Unit,
    tag: String,
) {
    val state = if (on) "Notifications on" else "Notifications off"
    IconButton(
        onClick = onToggle,
        enabled = enabled,
        modifier = Modifier
            .semantics { stateDescription = state }
            .testTag(tag),
    ) {
        Icon(
            imageVector = if (on) UsIcons.Notifications else UsIcons.NotificationsOff,
            contentDescription = "$state for $channelName",
            tint = UsTheme.extended.textPrimary,
        )
    }
}
