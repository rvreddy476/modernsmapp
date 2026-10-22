package com.us.android.feature.chat.ui

import android.content.Context
import android.content.SharedPreferences
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.DeliveredAcker
import com.us.android.core.chat.data.Tick
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsExtendedColors
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.core.notifications.ChatPushDeliveredHook
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/**
 * Message ticks (founder, 2026-10-22): one grey tick Sent, two grey
 * Delivered, two in the read colour Read. The read colour is ONE design
 * token ([UsExtendedColors.receiptSeen]) unless the viewer picked another
 * in chat settings ("Read tick colour").
 */
@Composable
internal fun MessageTicks(tick: Tick, modifier: Modifier = Modifier) {
    val muted = UsTheme.extended.textMuted
    val (icon, tint, label) = when (tick) {
        Tick.NONE, Tick.PENDING -> return
        Tick.SENT -> Triple(UsIcons.Check, muted, "Sent")
        Tick.DELIVERED -> Triple(UsIcons.CheckCheck, muted, "Delivered")
        Tick.READ -> Triple(UsIcons.CheckCheck, rememberReadTickColour(), "Read")
    }
    Icon(
        imageVector = icon,
        contentDescription = label,
        tint = tint,
        modifier = modifier.size(TICK_SIZE).testTag("message-tick:${tick.name.lowercase()}"),
    )
}

private val TICK_SIZE = 16.dp

/** The five read-tick colours a person may pick; each maps to an existing theme token. */
enum class ReadTickColour(val key: String, val label: String) {
    DEFAULT("default", "Blue"),
    GREEN("green", "Green"),
    VIOLET("violet", "Violet"),
    AMBER("amber", "Amber"),
    ROSE("rose", "Rose"),
    ;

    /** The token this choice draws with, in the current theme. */
    fun colour(colours: UsExtendedColors): Color = when (this) {
        DEFAULT -> colours.receiptSeen
        GREEN -> colours.statusSuccess
        VIOLET -> colours.chatSenders.firstOrNull() ?: colours.receiptSeen
        AMBER -> colours.statusWarning
        ROSE -> colours.statusDanger
    }

    companion object {
        /** A stored key → its choice; anything unknown is the default. */
        fun fromKey(key: String?): ReadTickColour = entries.firstOrNull { it.key == key?.trim()?.lowercase() } ?: DEFAULT
    }
}

/**
 * A LOCAL viewer preference (no server field holds it): one key in the
 * chat display preferences file, the same on every chat.
 */
object ReadTickColourPrefs {
    internal const val FILE = "chat_display"
    internal const val KEY = "read_tick_colour"

    private fun prefs(context: Context): SharedPreferences =
        context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    fun get(context: Context): ReadTickColour = ReadTickColour.fromKey(prefs(context).getString(KEY, null))

    fun set(context: Context, colour: ReadTickColour) {
        prefs(context).edit().putString(KEY, colour.key).apply()
    }

    /** The choice, kept current while the composable is on screen. */
    @Composable
    fun current(): ReadTickColour {
        val context = LocalContext.current
        var choice by remember { mutableStateOf(get(context)) }
        DisposableEffect(context) {
            val store = prefs(context)
            val listener = SharedPreferences.OnSharedPreferenceChangeListener { _, key ->
                if (key == KEY) choice = get(context)
            }
            store.registerOnSharedPreferenceChangeListener(listener)
            onDispose { store.unregisterOnSharedPreferenceChangeListener(listener) }
        }
        return choice
    }
}

@Composable
private fun rememberReadTickColour(): Color = ReadTickColourPrefs.current().colour(UsTheme.extended)

/** "Read tick colour": the five swatches with a live preview of two read ticks, then Save. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ReadTickColourSheet(current: ReadTickColour, onSave: (ReadTickColour) -> Unit, onDismiss: () -> Unit) {
    var picked by remember { mutableStateOf(current) }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("read_tick_colour_sheet"),
        ) {
            Text("Read tick colour", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                "The colour of the two ticks once someone has read your message. Only you see it.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                Icon(UsIcons.CheckCheck, contentDescription = null, tint = picked.colour(UsTheme.extended), modifier = Modifier.size(20.dp))
                Text("Read", style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textMuted)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                ReadTickColour.entries.forEach { choice ->
                    val selected = choice == picked
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        androidx.compose.foundation.layout.Box(
                            modifier = Modifier
                                .size(36.dp)
                                .background(choice.colour(UsTheme.extended), CircleShape)
                                .border(
                                    if (selected) 2.dp else 1.dp,
                                    if (selected) UsTheme.extended.textPrimary else UsTheme.extended.borderSubtle,
                                    CircleShape,
                                )
                                .clickable { picked = choice }
                                .semantics { contentDescription = choice.label + if (selected) ", selected" else "" }
                                .testTag("read_tick_swatch:${choice.key}"),
                        )
                        Text(choice.label, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
                    }
                }
            }
            UsButton(text = "Save", onClick = { onSave(picked) }, modifier = Modifier.fillMaxWidth().testTag("read_tick_save"))
        }
    }
}

/**
 * Binds the push seam to chat's delivered acker: a chat push to a signed-in
 * device acks delivery (batched; the acker sends on its own scope). Signed
 * out, nothing is sent.
 */
@Module
@InstallIn(SingletonComponent::class)
object ChatPushDeliveredModule {
    @Provides
    fun provideChatPushDeliveredHook(acker: DeliveredAcker, session: SessionStateProvider): ChatPushDeliveredHook =
        ChatPushDeliveredHook { conversationId, messageId ->
            if (session.sessionState.value is SessionState.Authenticated) acker.offer(conversationId, messageId, null)
        }
}
