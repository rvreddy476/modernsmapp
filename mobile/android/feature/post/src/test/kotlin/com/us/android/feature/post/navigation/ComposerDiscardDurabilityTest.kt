package com.us.android.feature.post.navigation

import androidx.compose.material3.Text
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.google.common.truth.Truth.assertThat
import com.us.android.core.database.ComposerDraftDao
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.testing.HiltTestActivity
import dagger.hilt.android.testing.HiltAndroidRule
import dagger.hilt.android.testing.HiltAndroidTest
import dagger.hilt.android.testing.HiltTestApplication
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.Serializable
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import javax.inject.Inject

/** Stands in for the feed the composer is opened from. */
@Serializable
internal data object DiscardFeedStub

/**
 * Discarding a draft is DURABLE before the screen leaves — Slice C, C-CLB-2.
 *
 * ## WHY THIS TEST EXISTS AND WHY THE EXISTING JOURNEY TEST COULD NOT CATCH IT
 *
 * The confirm button used to call `onDiscardConfirmed()` and `onClose()` on the
 * same tap. Popping the destination clears the navigation-owned ViewModel and
 * cancels `viewModelScope`, so the Room delete raced the pop and could lose.
 * Content the user explicitly threw away came back the next time they opened
 * the composer.
 *
 * `ComposerJourneyTest` cannot see that, and the review said so: it constructs
 * the ViewModel itself and holds a reference to it, so its scope survives the
 * pop and the delete always completes. The ownership is the defect. Reproducing
 * it requires the ViewModel to be created and owned by NAVIGATION, which means
 * the real `composerScreen()` registration, `hiltViewModel()`, and a real Hilt
 * graph — hence `@HiltAndroidTest` and a `@AndroidEntryPoint` host activity.
 *
 * ## WHAT IS REAL HERE
 *
 *  - the real `composerScreen()` builder, not a hand-rolled `composable` block;
 *  - the real `ComposerRoute` and real `PostRoute` types, whose serializers are
 *    resolved at runtime — a missing serialization plugin killed `:feature:chat`
 *    on its first frame while every unit test stayed green;
 *  - the real Hilt-owned `ComposerViewModel`, destroyed by the real pop;
 *  - the real Room database, read back through the injected DAO. A fake DAO
 *    would prove the call was made; the real one proves the row is gone.
 */
@HiltAndroidTest
@RunWith(RobolectricTestRunner::class)
// A real phone viewport: Robolectric's default window puts the composer's lower
// controls off-screen, where a click resolves to nothing and the test fails for
// a reason unrelated to what it asserts.
@Config(
    application = HiltTestApplication::class,
    sdk = [34],
    qualifiers = "w411dp-h891dp",
)
class ComposerDiscardDurabilityTest {

    @get:Rule(order = 0)
    val hiltRule = HiltAndroidRule(this)

    @get:Rule(order = 1)
    val composeRule = createAndroidComposeRule<HiltTestActivity>()

    /**
     * The REAL draft DAO from the real database.
     *
     * Injected rather than faked because the claim is "the row is gone", not
     * "clear() was called". Those differ exactly when the delete is cancelled
     * mid-flight, which is the defect.
     */
    @Inject
    lateinit var drafts: ComposerDraftDao

    private lateinit var navController: NavHostController

    private val currentRoute: String?
        get() = navController.currentBackStackEntry?.destination?.route

    private fun storedDraft() = runBlocking { drafts.load() }

    @Before
    fun setUp() {
        hiltRule.inject()
        // A previous test in the same Robolectric application may have left a
        // row behind; the assertions here are about presence and absence.
        runBlocking { drafts.clear() }
    }

    /** Builds the graph from the REAL registration helpers. */
    private fun launch() {
        composeRule.setContent {
            navController = rememberNavController()
            UsTheme {
                NavHost(navController = navController, startDestination = DiscardFeedStub) {
                    composable<DiscardFeedStub> { Text(FEED_MARKER) }
                    // The real post route. Registered so the composer's success
                    // target is a genuine destination whose serializer must
                    // resolve, rather than a stand-in that cannot fail the way
                    // production does.
                    composable<PostRoute> { entry ->
                        Text("post:" + entry.toRoute<PostRoute>().postId)
                    }
                    // THE REAL REGISTRATION. Its ViewModel comes from
                    // hiltViewModel() and is owned by this back-stack entry.
                    composerScreen(
                        onClose = { navController.popBackStack() },
                        onPublished = { postId ->
                            navController.navigate(PostRoute(postId)) {
                                popUpTo<ComposerRoute> { inclusive = true }
                            }
                        },
                    )
                }
            }
        }
        openComposer()
    }

    private fun openComposer() {
        composeRule.runOnUiThread { navController.navigateToComposer() }
        composeRule.waitForIdle()
    }

    private fun typeDraft(text: String) {
        // The description sits on the WRAPPER; only the inner node has
        // RequestFocus, so matching the ancestor alone finds a node that
        // cannot be typed into.
        composeRule
            // The canvas is a BasicTextField, so ONE node carries both the
            // description and the text action. It used to be a wrapped
            // UsTextField where only a descendant was editable, hence the
            // former ancestor match.
            .onNode(hasSetTextAction() and hasContentDescription("Post text"))
            .performTextInput(text)
        composeRule.waitForIdle()
        // The draft is written on a launched coroutine; wait for the row rather
        // than assuming the write already landed.
        composeRule.waitUntil(TIMEOUT_MILLIS) { storedDraft() != null }
    }

    private fun pressSystemBack() {
        composeRule.runOnUiThread { composeRule.activity.onBackPressedDispatcher.onBackPressed() }
        composeRule.waitForIdle()
    }

    /**
     * The whole defect, end to end.
     *
     * Type, discard, and reopen. If the Room delete were still racing the pop,
     * the reopened composer would restore the discarded text.
     */
    @Test
    fun `a discarded draft does not come back when the composer is reopened`() {
        launch()
        typeDraft("something I changed my mind about")

        pressSystemBack()
        composeRule.onNodeWithText("Discard this post?").assertExists()
        composeRule.onNodeWithText("Discard").performClick()
        composeRule.waitForIdle()

        // The pop happened, so the navigation-owned ViewModel was destroyed and
        // its scope cancelled — the exact condition the old ordering lost under.
        composeRule.waitUntil(TIMEOUT_MILLIS) {
            currentRoute?.contains(ComposerRoute::class.qualifiedName!!) == false
        }
        composeRule.onNodeWithText(FEED_MARKER).assertExists()

        openComposer()

        assertThat(storedDraft()).isNull()
        composeRule.onNodeWithText("something I changed my mind about").assertDoesNotExist()
    }

    /**
     * The ordering itself: the row is gone AT the moment the route changes.
     *
     * The test above could in principle pass on a slow delete that happened to
     * finish before the reopen. This one pins the actual contract — navigation
     * is a CONSEQUENCE of the durable delete, so by the time the composer is off
     * the back stack there is nothing left to restore.
     */
    @Test
    fun `the draft is already gone the moment the composer leaves the back stack`() {
        launch()
        typeDraft("ordering matters")

        pressSystemBack()
        composeRule.onNodeWithText("Discard").performClick()
        // waitForIdle first: the discard runs on a coroutine and the
        // navigation LaunchedEffect only fires on the recomposition that
        // follows it. The ordering claim is unaffected — the route cannot
        // change until `discarded` is set, and `discarded` is set only after
        // the delete returns. What is asserted is that at THAT moment the row
        // is already gone.
        composeRule.waitForIdle()
        composeRule.waitUntil(TIMEOUT_MILLIS) {
            currentRoute?.contains(ComposerRoute::class.qualifiedName!!) == false
        }

        assertThat(storedDraft()).isNull()
    }

    /**
     * Keep editing keeps the draft.
     *
     * The mirror image, and the one that stops a fix for the above from
     * becoming "clear the draft whenever the dialog opens".
     */
    @Test
    fun `cancelling the discard keeps the draft and stays on the composer`() {
        launch()
        typeDraft("still writing this")

        pressSystemBack()
        composeRule.onNodeWithText("Keep editing").performClick()
        composeRule.waitForIdle()

        assertThat(currentRoute).contains(ComposerRoute::class.qualifiedName)
        assertThat(storedDraft()?.text).isEqualTo("still writing this")
        composeRule.onNodeWithText("still writing this").assertExists()
    }

    /**
     * The real success destination resolves.
     *
     * `PostRoute` carries an argument and its serializer is resolved at runtime.
     * Navigating to it here proves the composer's `onPublished` target is a
     * destination that actually exists in a real graph — the failure mode that
     * killed `:feature:chat` on its first frame.
     */
    @Test
    fun `the real post route the composer publishes into resolves with its argument`() {
        launch()

        composeRule.runOnUiThread {
            navController.navigate(PostRoute("post-42")) {
                popUpTo<ComposerRoute> { inclusive = true }
            }
        }
        composeRule.waitForIdle()

        assertThat(navController.currentBackStackEntry!!.toRoute<PostRoute>().postId)
            .isEqualTo("post-42")
        composeRule.onNodeWithText("post:post-42").assertExists()
        // popUpTo(inclusive) removed the composer, so Back reaches the feed
        // rather than a composer whose content is already published.
        composeRule.runOnUiThread { navController.popBackStack() }
        composeRule.waitForIdle()
        composeRule.onNodeWithText(FEED_MARKER).assertExists()
    }

    private companion object {
        const val FEED_MARKER = "feed"
        const val TIMEOUT_MILLIS = 5_000L
    }
}
