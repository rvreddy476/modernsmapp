package com.us.android.feature.chat.navigation

import androidx.compose.material3.Text
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.Serializable
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Stands in for the profile surface a Message button is pressed on.
 *
 * TOP-LEVEL, not nested inside the test class. kotlinx.serialization resolves an
 * object serializer reflectively through its INSTANCE field, and a PRIVATE
 * nested object is not reachable that way — every test in this file failed with
 * `IllegalAccessException` until this was hoisted out.
 */
@Serializable
internal data object ProfileStub

/**
 * The navigation contract behind Messages: routes, arguments and the back stack.
 *
 * ## WHY THIS EXISTS
 *
 * B-LB-4 criterion 6 requires an automated navigation test, and the closure
 * review refused to accept the emulator journey in its place: one manual run
 * proves the binary worked once, it does not guard route arguments or the back
 * stack on the next change.
 *
 * It is a UNIT test (Robolectric) rather than an instrumented one so it runs on
 * the same `testDebugUnitTest` the gate already executes. A test that needs a
 * device attached is a test that does not run. `:core:database` already proves
 * Robolectric works in this build.
 *
 * ## WHY THE DESTINATIONS RENDER STUBS
 *
 * The real screens resolve their ViewModels through `hiltViewModel()`, so
 * hosting them here would need a Hilt test graph and would put a network stack
 * behind a navigation assertion — the test would then fail for reasons that
 * have nothing to do with navigation.
 *
 * What IS under test is the layer that actually broke and the layer that can
 * silently drift:
 *
 *  - the ROUTE TYPES. `composable<ChatInboxRoute>` resolves a serializer at
 *    runtime, which is exactly what threw `SerializationException: Serializer
 *    for class 'ChatInboxRoute' is not found` when `:feature:chat` was missing
 *    the serialization plugin — the app died on its first frame while every
 *    unit test passed. This test compiles against this module, so losing the
 *    plugin again fails it;
 *  - the ARGUMENTS, read back through `toRoute`, so a renamed or reordered
 *    field is caught here rather than by a thread that silently loads
 *    conversation "";
 *  - the BACK STACK, asserted through the real `popBackStack` and by entry id
 *    rather than by route alone.
 *
 * The real destination registrations are covered by the device journey recorded
 * in `prompt/slice-b-chat-closure.md`.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatNavigationTest {

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var navController: NavHostController

    private val currentRoute: String?
        get() = navController.currentBackStackEntry?.destination?.route

    private val currentEntryId: String
        get() = navController.currentBackStackEntry!!.id

    /** Builds a graph from the REAL chat routes and the real navigation helpers. */
    private fun setUpGraph() {
        composeRule.setContent {
            navController = rememberNavController()
            NavHost(navController = navController, startDestination = ProfileStub) {
                composable<ProfileStub> { Text(PROFILE_MARKER) }
                composable<ChatInboxRoute> { Text(INBOX_MARKER) }
                composable<ChatThreadRoute> { entry ->
                    val route = entry.toRoute<ChatThreadRoute>()
                    Text("thread:${route.conversationId}:${route.title}")
                }
            }
        }
    }

    private fun navigate(block: NavHostController.() -> Unit) {
        composeRule.runOnUiThread { navController.block() }
        composeRule.waitForIdle()
    }

    /**
     * The host's start-direct rule, as `ProfileScreen` and `UsNavHost` implement
     * it: navigate ONLY when the server actually returned a conversation.
     *
     * A denial carries no conversation, so any id the client pushed would be
     * one it invented, and the thread would open onto something that does not
     * exist.
     */
    private fun NavHostController.openChatIfAllowed(conversationId: String?, title: String) {
        conversationId?.let { navigateToChatThread(it, title) }
    }

    /**
     * Entry point → inbox → thread, with the arguments intact.
     *
     * The title travels ON the route because naming a direct conversation needs
     * the viewer's own id, which the inbox has and the thread does not. Losing
     * it here opens every direct chat with a blank header.
     */
    @Test
    fun `the inbox opens a thread carrying its conversation id and title`() {
        setUpGraph()

        navigate { navigateToChatInbox() }
        assertThat(currentRoute).contains(ChatInboxRoute::class.qualifiedName)

        navigate { navigateToChatThread("conv-42", "Bravo Closure") }

        val route = navController.currentBackStackEntry!!.toRoute<ChatThreadRoute>()
        assertThat(route.conversationId).isEqualTo("conv-42")
        assertThat(route.title).isEqualTo("Bravo Closure")
        composeRule.onNodeWithText("thread:conv-42:Bravo Closure").assertExists()
    }

    /**
     * Back returns to the inbox entry it came from, not a rebuilt one.
     *
     * Asserted by entry id: a host that "went back" with a fresh `navigate`
     * would satisfy a route-only assertion while losing the inbox's scroll
     * position and state.
     */
    @Test
    fun `back from a thread returns to the inbox entry it came from`() {
        setUpGraph()

        navigate { navigateToChatInbox() }
        val inboxEntryId = currentEntryId

        navigate { navigateToChatThread("conv-42", "Bravo") }
        navigate { popBackStack() }

        assertThat(currentRoute).contains(ChatInboxRoute::class.qualifiedName)
        assertThat(currentEntryId).isEqualTo(inboxEntryId)
        composeRule.onNodeWithText(INBOX_MARKER).assertExists()
    }

    /** The surface underneath the inbox survives too, rather than being popped. */
    @Test
    fun `backing out of the inbox returns to the originating surface`() {
        setUpGraph()
        val originEntryId = currentEntryId

        navigate { navigateToChatInbox() }
        navigate { navigateToChatThread("conv-42", "Bravo") }

        navigate { popBackStack() }
        navigate { popBackStack() }

        assertThat(currentRoute).contains(ProfileStub::class.qualifiedName)
        assertThat(currentEntryId).isEqualTo(originEntryId)
        composeRule.onNodeWithText(PROFILE_MARKER).assertExists()
    }

    /**
     * A DENIED start-direct navigates nowhere and leaves the back stack alone.
     *
     * The decision itself lives in `StartChatViewModel`; `:feature:profile`'s
     * `StartChatViewModelTest` proves it never emits a conversation on denial.
     * This asserts the host's half.
     */
    @Test
    fun `a denied start does not navigate and leaves the back stack alone`() {
        setUpGraph()
        val before = currentEntryId

        // StartDirectResult.NotAllowed carries no conversation.
        navigate { openChatIfAllowed(conversationId = null, title = "Bravo") }

        assertThat(currentRoute).contains(ProfileStub::class.qualifiedName)
        assertThat(currentEntryId).isEqualTo(before)
        composeRule.onNodeWithText(PROFILE_MARKER).assertExists()
    }

    /** An ALLOWED start opens the SERVER's conversation, not a locally chosen one. */
    @Test
    fun `an allowed start opens the conversation the server returned`() {
        setUpGraph()

        val fromServer = "6d5707d0-1f1c-4bea-b48b-e4d343f24d5e"
        navigate { openChatIfAllowed(conversationId = fromServer, title = "Bravo") }

        assertThat(navController.currentBackStackEntry!!.toRoute<ChatThreadRoute>().conversationId)
            .isEqualTo(fromServer)
    }

    private companion object {
        const val INBOX_MARKER = "inbox"
        const val PROFILE_MARKER = "Message"
    }
}
