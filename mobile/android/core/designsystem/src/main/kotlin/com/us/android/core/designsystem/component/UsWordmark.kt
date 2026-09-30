package com.us.android.core.designsystem.component

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The Momentum wordmark: Bodoni Moda Black, set at whatever size the call
 * site needs. The single entry point for the brand name in type, so a
 * rename or a typeface swap only ever happens here.
 *
 * [size] defaults to the home top bar's 34sp; the login and splash screens
 * pass 44sp — see [UsWordmarkSize] for both.
 */
@Composable
fun UsWordmark(
    modifier: Modifier = Modifier,
    size: TextUnit = UsWordmarkSize.TopBar,
) {
    Text(
        text = "Momentum",
        style = TextStyle(
            fontFamily = MomentumWordmarkFontFamily,
            fontWeight = FontWeight.Black,
            fontSize = size,
        ),
        color = UsTheme.extended.textPrimary,
        modifier = modifier,
    )
}

/**
 * The sizes a wordmark ships at — the ONE place they are written (founder's
 * rule: colour and font come from one file).
 */
object UsWordmarkSize {
    /** The home top bar. */
    val TopBar = 34.sp

    /** Login and the splash screen. */
    val Hero = 44.sp

    /**
     * A mini-app's mark (MStore, MSeller) in its own top bar (2026-09-30):
     * smaller than the Momentum mark, because it sits beside a back
     * control on every screen rather than alone on the home bar.
     */
    val MiniAppTopBar = 22.sp

    /** A mini-app's mark on an empty state or a first-run panel. */
    val MiniAppHero = 34.sp
}

@Preview(name = "Wordmark — top bar", showBackground = true)
@Composable
private fun UsWordmarkTopBarPreview() {
    UsTheme { UsWordmark() }
}

@Preview(name = "Wordmark — hero", showBackground = true)
@Composable
private fun UsWordmarkHeroPreview() {
    UsTheme { UsWordmark(size = UsWordmarkSize.Hero) }
}
