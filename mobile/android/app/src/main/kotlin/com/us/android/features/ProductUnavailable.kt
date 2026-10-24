package com.us.android.features

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.State
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.Product
import com.us.android.core.model.productOpen
import com.us.android.explore.LauncherTile
import com.us.android.explore.launcherTiles
import kotlinx.serialization.Serializable

/**
 * Where a notification tap or app link into a closed product lands (founder
 * decision C, 24 Oct 2026): a plain "Not available" instead of the product's
 * screens, whose calls the gateway would refuse. [product] is the
 * `GET /v1/features` key.
 */
@Serializable
data class ProductUnavailableRoute(val product: String)

/** What the screen says. Pure, so the words are a test. */
const val PRODUCT_UNAVAILABLE_TITLE = "Not available"
const val PRODUCT_UNAVAILABLE_DETAIL = "This part of the app is not open right now. Please check back later."

/**
 * The shell's view of `GET /v1/features`: the last good answer, read through
 * a [State] so the graph is built once and only the screens that read it
 * recompose when it changes.
 */
@Stable
class ProductGateState(private val open: State<Set<Product>?>) {
    fun isOpen(product: Product): Boolean = productOpen(product, open.value)

    /** The launcher's tiles for the current answer: a closed product has none. */
    fun launcher(): List<LauncherTile> = launcherTiles(open.value)
}

fun NavHostController.navigateToProductUnavailable(product: Product) =
    navigate(ProductUnavailableRoute(product.wire)) { launchSingleTop = true }

fun NavGraphBuilder.productUnavailableScreen(onBack: () -> Unit) {
    composable<ProductUnavailableRoute> { ProductUnavailableScreen(onBack = onBack) }
}

@Composable
fun ProductUnavailableScreen(onBack: () -> Unit) {
    UsScaffold(topBar = { UsTopBar(title = "", onBack = onBack) }) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .padding(UsTheme.spacing.pageHorizontal)
                .testTag("product_unavailable"),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m, Alignment.CenterVertically),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = PRODUCT_UNAVAILABLE_TITLE,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                textAlign = TextAlign.Center,
            )
            Text(
                text = PRODUCT_UNAVAILABLE_DETAIL,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                textAlign = TextAlign.Center,
            )
            UsPillButton(text = "Go back", onClick = onBack, modifier = Modifier.testTag("product_unavailable_back"))
        }
    }
}
