package com.us.android.feature.commerce.ui

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.theme.OutfitFontFamily
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The two commerce mini-apps' names, in ONE place.
 *
 * The founder said "M shop", then corrected it to "M store", and the buyer
 * app shipped as MStore. Keeping both names here — and drawing both through
 * [CommerceWordmark] — means a rename is one line rather than a sweep through
 * every top bar, empty state and piece of copy that says the product's name.
 */
object CommerceBrand {
    /** The buyer app. */
    const val Buyer = "MStore"

    /** The seller app. One person can be both; this is a switch, not an account. */
    const val Seller = "MSeller"
}

/**
 * Splits a wordmark into its stylised initial and its tail — "MStore" becomes
 * "M" and "Store".
 *
 * Pure, so the rule is a plain unit test rather than something only a
 * screenshot can check. A one-character name has no tail, and an empty name
 * has neither; both are rendered rather than crashed, because a wordmark is
 * not worth an exception.
 */
fun wordmarkParts(name: String): Pair<String, String> =
    if (name.isEmpty()) "" to "" else name.take(1) to name.drop(1)

/**
 * A commerce mini-app's wordmark: the capital M set in Outfit Black under the
 * ember gradient, joined without a space to the rest of the name in the text
 * ramp's own colour (founder, 2026-09-05).
 *
 * Ember is otherwise reserved for primary actions. The exception is deliberate
 * and named: this is the brand mark, the one place in MStore and MSeller the
 * accent is allowed to be identity rather than instruction. Everything else on
 * these screens — hearts, bag glyphs, chips — stays in the navy ramp.
 *
 * It sits at the LEFT of the app bar on every screen of both apps, so a person
 * always knows which of the two they are in.
 */
@Composable
fun CommerceWordmark(
    name: String,
    modifier: Modifier = Modifier,
    size: TextUnit = CommerceWordmarkSize.TopBar,
) {
    val (initial, tail) = wordmarkParts(name)
    val ember = UsTheme.extended.ctaGradient
    val ink = UsTheme.extended.textPrimary
    Text(
        text = buildAnnotatedString {
            withStyle(SpanStyle(brush = ember, fontWeight = FontWeight.Black)) { append(initial) }
            withStyle(SpanStyle(color = ink, fontWeight = FontWeight.SemiBold)) { append(tail) }
        },
        style = TextStyle(fontFamily = OutfitFontFamily, fontSize = size),
        maxLines = 1,
        modifier = modifier
            .semantics(mergeDescendants = true) {
                heading()
                contentDescription = name
            }
            .testTag("commerce_wordmark:$name"),
    )
}

/** MStore's mark. */
@Composable
fun MStoreWordmark(modifier: Modifier = Modifier, size: TextUnit = CommerceWordmarkSize.TopBar) =
    CommerceWordmark(name = CommerceBrand.Buyer, modifier = modifier, size = size)

/** MSeller's mark — the same treatment, so the two read as one family. */
@Composable
fun MSellerWordmark(modifier: Modifier = Modifier, size: TextUnit = CommerceWordmarkSize.TopBar) =
    CommerceWordmark(name = CommerceBrand.Seller, modifier = modifier, size = size)

/** The two sizes the commerce wordmark ships at. */
object CommerceWordmarkSize {
    /** Every MStore and MSeller top bar. */
    val TopBar = 22.sp

    /** An empty state or a first-run panel that introduces the app. */
    val Hero = 34.sp
}
