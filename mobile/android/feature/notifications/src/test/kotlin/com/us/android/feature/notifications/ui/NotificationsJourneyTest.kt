package com.us.android.feature.notifications.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationAddress
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.network.ErrorMapper
import com.us.android.core.notifications.data.MarkReadRequest
import com.us.android.core.notifications.data.NotificationPage
import com.us.android.core.notifications.data.NotificationsApi
import com.us.android.core.notifications.data.NotificationsRepository
import com.us.android.core.notifications.data.UnreadBadge
import com.us.android.feature.notifications.navigation.NotificationsRoute
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** Stands in for the destinations `:app` maps notification targets to. */
@Serializable
internal data object PostStub

@Serializable
internal data object ProfileStub

/**
 * The inbox, rendered and tapped — Slice D.
 *
 * ## WHAT THIS PROVES THAT THE OTHER TESTS CANNOT
 *
 *  - THE ROUTE TYPE. `composable<NotificationsRoute>` resolves a serializer at
 *    runtime. That is exactly what threw `SerializationException` and killed
 *    `:feature:chat` on its first frame while every unit test passed, so the
 *    real route object is registered here rather than a stand-in;
 *  - THE TAP CONTRACT. A row hands back a resolved [NotificationTarget], never
 *    a URL. This asserts the value `:app` actually receives;
 *  - THE SEMANTICS. That a row is unread is a state fact; that a screen reader
 *    is TOLD it is unread is a rendering fact, and only the semantics tree
 *    shows it.
 *
 * Robolectric on the unit source set so it runs on the same `testDebugUnitTest`
 * the gate already executes.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w411dp-h891dp")
class NotificationsJourneyTest {

    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private lateinit var navController: NavHostController
    private val opened = mutableListOf<NotificationTarget>()
    private var preferencesOpened = 0

    private fun row(
        id: String,
        kind: NotificationKind = NotificationKind.Comment,
        target: NotificationTarget = NotificationTarget.PostComment("post-1", "c-1"),
        isRead: Boolean = false,
    ) = Notification(
        id = id,
        bucket = 202608,
        ts = "ts-$id",
        kind = kind,
        actorUserId = "actor",
        entityType = "post",
        entityId = "post-1",
        target = target,
        isRead = isRead,
        createdAt = "2026-08-22T17:10:21.526Z",
    )

    private class FakeRepository(
        private val rows: List<Notification>,
        private val count: Int,
    ) : NotificationsRepository(UnusedApi(), ErrorMapper(Json { ignoreUnknownKeys = true })) {
        val markedRead = mutableListOf<NotificationAddress>()
        var markAllCalls = 0

        override suspend fun page(limit: Int, cursor: String?) =
            AppResult.Success(NotificationPage(rows, null))

        override suspend fun unreadCount() = AppResult.Success(count)

        override suspend fun markRead(address: NotificationAddress): AppResult<Unit> {
            markedRead += address
            return AppResult.Success(Unit)
        }

        override suspend fun markAllRead(): AppResult<Unit> {
            markAllCalls++
            return AppResult.Success(Unit)
        }
    }

    private class UnusedApi : NotificationsApi {
        override suspend fun list(limit: Int, cursor: String?) = error("not used")
        override suspend fun unreadCount() = error("not used")
        override suspend fun markRead(body: MarkReadRequest) = error("not used")
        override suspend fun markAllRead() = error("not used")
    }

    private fun launch(repository: FakeRepository) {
        val viewModel = NotificationsViewModel(repository, UnreadBadge(repository))

        composeRule.setContent {
            navController = rememberNavController()
            UsTheme {
                NavHost(navController = navController, startDestination = NotificationsRoute) {
                    // The REAL route object, so its serializer must resolve.
                    composable<NotificationsRoute> {
                        NotificationsScreen(
                            onBack = { },
                            onOpenTarget = { target ->
                                opened += target
                                // Mirrors UsNavHost: :app maps the target to a
                                // destination; the feature never learns which.
                                when (target) {
                                    is NotificationTarget.Post,
                                    is NotificationTarget.PostComment,
                                    -> navController.navigate(PostStub)

                                    is NotificationTarget.Profile -> navController.navigate(ProfileStub)
                                    NotificationTarget.None -> Unit
                                }
                            },
                            onOpenPreferences = { preferencesOpened++ },
                            // No permission prompt: it resolves a Hilt
                            // ViewModel and is covered by its own tests. This
                            // test is about the inbox itself.
                            permissionPrompt = { },
                            viewModel = viewModel,
                        )
                    }
                    composable<PostStub> { Text(POST_MARKER) }
                    composable<ProfileStub> { Text(PROFILE_MARKER) }
                }
            }
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `the inbox renders a row for each notification`() {
        launch(FakeRepository(listOf(row("n1"), row("n2", kind = NotificationKind.Follow)), 2))

        composeRule.onNodeWithContentDescription("Unread. Someone commented on your post").assertExists()
        composeRule.onNodeWithContentDescription("Unread. Someone started following you").assertExists()
    }

    /**
     * An unread row announces that it is unread.
     *
     * The dot is a visual signal only. Without the merged description a screen
     * reader gets the sentence and no indication of whether it has been seen.
     */
    @Test
    fun `an unread row tells a screen reader it is unread`() {
        launch(FakeRepository(listOf(row("n1")), 1))

        composeRule
            .onNodeWithContentDescription("Unread. Someone commented on your post")
            .assertExists()
    }

    @Test
    fun `a read row does not announce itself as unread`() {
        launch(FakeRepository(listOf(row("n1", isRead = true)), 0))

        composeRule.onNodeWithContentDescription("Someone commented on your post").assertExists()
        composeRule
            .onNodeWithContentDescription("Unread. Someone commented on your post")
            .assertDoesNotExist()
    }

    /**
     * Tapping hands back the RESOLVED target, and navigates.
     *
     * The value asserted is the parsed target, not the server's string: that is
     * the contract `:app` consumes, and the reason a malformed deep link can
     * never become a navigation instruction.
     */
    @Test
    fun `tapping a comment notification opens the post`() {
        val repo = FakeRepository(listOf(row("n1")), 1)
        launch(repo)

        composeRule.onNodeWithContentDescription("Unread. Someone commented on your post").performClick()
        composeRule.waitForIdle()

        assertThat(opened).containsExactly(NotificationTarget.PostComment("post-1", "c-1"))
        composeRule.onNodeWithText(POST_MARKER).assertExists()
    }

    @Test
    fun `tapping a follow notification opens the profile`() {
        launch(
            FakeRepository(
                listOf(
                    row(
                        "n1",
                        kind = NotificationKind.Follow,
                        target = NotificationTarget.Profile("user-9"),
                    ),
                ),
                1,
            ),
        )

        composeRule.onNodeWithContentDescription("Unread. Someone started following you").performClick()
        composeRule.waitForIdle()

        assertThat(opened).containsExactly(NotificationTarget.Profile("user-9"))
        composeRule.onNodeWithText(PROFILE_MARKER).assertExists()
    }

    /**
     * Tapping also marks the row read, and the row updates in place.
     *
     * Read-state is optimistic and NOT gated on the request: the user sees the
     * change immediately rather than watching a spinner confirm something they
     * already did.
     */
    @Test
    fun `tapping marks the row read without waiting for the server`() {
        val repo = FakeRepository(listOf(row("n1")), 1)
        launch(repo)

        composeRule.onNodeWithContentDescription("Unread. Someone commented on your post").performClick()
        composeRule.waitForIdle()

        assertThat(repo.markedRead).containsExactly(NotificationAddress(202608, "ts-n1"))
        composeRule
            .onNodeWithContentDescription("Unread. Someone commented on your post")
            .assertDoesNotExist()
    }

    /**
     * A row whose target cannot be resolved renders and does nothing.
     *
     * This is the commerce/dating/live case: one notification service serves
     * every vertical, so this build receives rows it has no screen for. They
     * must be visible and inert, never crashing and never navigating somewhere
     * approximate.
     */
    @Test
    fun `a row with no resolvable target renders but does not navigate`() {
        val repo = FakeRepository(
            listOf(
                row(
                    "n1",
                    kind = NotificationKind.Unknown("commerce.order.shipped"),
                    target = NotificationTarget.None,
                ),
            ),
            1,
        )
        launch(repo)

        composeRule.onNodeWithContentDescription("Unread. You have a new notification").assertExists()
        composeRule.onNodeWithContentDescription("Unread. You have a new notification").performClick()
        composeRule.waitForIdle()

        assertThat(opened).isEmpty()
        composeRule.onNodeWithText(POST_MARKER).assertDoesNotExist()
        // Still marked read: the user has seen it even if it leads nowhere.
        assertThat(repo.markedRead).hasSize(1)
    }

    @Test
    fun `mark all read clears every row and hides the control`() {
        val repo = FakeRepository(listOf(row("n1"), row("n2")), 2)
        launch(repo)

        composeRule.onNodeWithText("Mark all read").performClick()
        composeRule.waitForIdle()

        assertThat(repo.markAllCalls).isEqualTo(1)
        // The control is gone because nothing is unread any more.
        composeRule.onNodeWithText("Mark all read").assertDoesNotExist()
        // Both rows are now READ, so their descriptions lose the "Unread." prefix.
        assertThat(
            composeRule.onAllNodesWithContentDescription("Someone commented on your post")
                .fetchSemanticsNodes(),
        ).hasSize(2)
    }

    /** With nothing unread the control is never offered. */
    @Test
    fun `mark all read is absent when everything is read`() {
        launch(FakeRepository(listOf(row("n1", isRead = true)), 0))

        composeRule.onNodeWithText("Mark all read").assertDoesNotExist()
    }

    @Test
    fun `an empty inbox shows the empty state rather than an error`() {
        launch(FakeRepository(emptyList(), 0))

        composeRule.onNodeWithText("Nothing yet").assertExists()
    }

    private companion object {
        const val POST_MARKER = "post-destination"
        const val PROFILE_MARKER = "profile-destination"
    }

    /**
     * The inbox can reach notification preferences — Slice D, D-D7.
     *
     * Preferences live in `:feature:profile` and were previously reachable only
     * from Settings. The inbox is where a user actually forms the thought
     * "this is too many notifications", so it is where the control belongs.
     *
     * The screen hands back an intent to open preferences; `:app` decides which
     * destination that is. This asserts the callback, which is the contract —
     * the feature never learns the route.
     */
    @Test
    fun `the inbox offers a route to notification preferences`() {
        launch(FakeRepository(listOf(row("n1")), 1))

        composeRule.onNodeWithContentDescription("Notification settings").performClick()
        composeRule.waitForIdle()

        assertThat(preferencesOpened).isEqualTo(1)
    }
}
