// MatchingDeclarationName: this file's primary export is the UsAvatar
// composable; UsAvatarSize is the value type it consumes. The rule assumes a
// file with one classlike declaration is *about* that declaration, which does
// not hold for a component plus its parameter type.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.theme.UsTheme
import kotlin.math.absoluteValue

/** The sizes the product actually uses. A free-form Dp invites one-off sizes. */
enum class UsAvatarSize(val diameter: Dp, val initialsSize: TextUnit) {
    Small(32.dp, 13.sp),
    Medium(44.dp, 17.sp),
    Large(88.dp, 32.sp),
}

/**
 * A user avatar.
 *
 * Renders initials on a colour derived from the user id — NOT a remote image.
 * That is a contract decision, not a placeholder: neither captured profile
 * payload (`/v1/profiles/:userId` or `/me`) contains an avatar field at all.
 * `avatar_media_id` exists in the database and is not serialized. Adding an
 * image-loading dependency now would be building for a response the server
 * does not send.
 *
 * When the backend starts returning an avatar, add an image slot here and
 * every call site gains it at once. Resolve it through a media URL resolver —
 * the capture (§3) warns specifically against constructing URLs from the
 * storage keys the public metadata exposes.
 *
 * Accessibility: the initials are decorative. A screen reader announcing "A C"
 * next to a name it is about to read anyway is noise, so semantics are cleared
 * and replaced with a caller-supplied description, or nothing when the name is
 * already adjacent in the reading order.
 */
@Composable
fun UsAvatar(
    name: String,
    modifier: Modifier = Modifier,
    size: UsAvatarSize = UsAvatarSize.Medium,
    /** Stable colour seed. Use the user id so a person keeps one colour. */
    seed: String = name,
    contentDescription: String? = null,
) {
    val background = avatarColor(seed)
    Box(
        modifier = modifier
            .size(size.diameter)
            .clip(CircleShape)
            .background(background)
            .clearAndSetSemantics {
                if (contentDescription != null) this.contentDescription = contentDescription
            },
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = initialsOf(name),
            color = Color.White,
            fontSize = size.initialsSize,
            fontWeight = FontWeight.SemiBold,
            textAlign = TextAlign.Center,
        )
    }
}

/**
 * Up to two initials.
 *
 * Works on code points rather than chars so a name beginning with an emoji or
 * a character outside the BMP does not get half a surrogate pair rendered as a
 * replacement glyph. Names in this product are already proven to include
 * scripts the naive path mishandles — the server had to be fixed to accept
 * Indic combining marks — so the client should not reintroduce the assumption.
 */
internal fun initialsOf(name: String): String {
    val words = name.trim().split(' ', '\t', '\n').filter { it.isNotBlank() }
    if (words.isEmpty()) return "?"
    val first = words.first().firstCodePoint()
    if (words.size == 1) return first
    return first + words.last().firstCodePoint()
}

private fun String.firstCodePoint(): String {
    if (isEmpty()) return ""
    val end = offsetByCodePoints(0, 1)
    return substring(0, end).uppercase()
}

/**
 * Deterministic colour from a seed, chosen from a fixed palette rather than
 * generated, so every avatar keeps a guaranteed contrast ratio against the
 * white initials. A freely generated colour cannot promise that.
 */
internal fun avatarColor(seed: String): Color {
    if (seed.isEmpty()) return AvatarPalette[0]
    return AvatarPalette[(seed.hashCode().absoluteValue) % AvatarPalette.size]
}

private val AvatarPalette = listOf(
    Color(0xFF1A73E8),
    Color(0xFF7B1FA2),
    Color(0xFFC2185B),
    Color(0xFF00796B),
    Color(0xFFE65100),
    Color(0xFF455A64),
    Color(0xFF5D4037),
    Color(0xFF283593),
)

@Preview(name = "Avatar sizes", showBackground = true)
@Composable
private fun UsAvatarPreview() {
    UsTheme {
        Row(verticalAlignment = Alignment.CenterVertically) {
            UsAvatar("Ada Lovelace", size = UsAvatarSize.Small, seed = "a")
            UsAvatar("Grace Hopper", size = UsAvatarSize.Medium, seed = "b")
            UsAvatar("Alan Turing", size = UsAvatarSize.Large, seed = "c")
        }
    }
}

@Preview(name = "Avatar edge cases", showBackground = true)
@Composable
private fun UsAvatarEdgeCasePreview() {
    UsTheme {
        Row(verticalAlignment = Alignment.CenterVertically) {
            // A cleared display_name is reachable: PUT /me with {} blanks it.
            UsAvatar("", seed = "empty")
            UsAvatar("प्रिया शर्मा", seed = "indic")
            UsAvatar("Madonna", seed = "single")
        }
    }
}
